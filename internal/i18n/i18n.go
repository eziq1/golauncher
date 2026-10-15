// Package i18n holds message catalogs, resolves the UI locale, and formats
// values for it.
//
// Catalogs are JSON files named "<lang>.json". A value is either a string or
// an object of CLDR plural forms ("one", "other", ...). Messages are
// text/template strings: {{.name}} refers to an argument and {{.Count}} to
// the plural count.
//
// English ("en") is the source language and the fallback for every lookup.
// The pseudo-locale "en-XA" is generated from English for layout testing.
package i18n

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"text/template"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
)

// Source is the source and fallback locale.
const Source = "en"

// Pseudo is the generated pseudo-locale.
const Pseudo = "en-XA"

// CoreNamespace is the namespace of the host's own messages.
const CoreNamespace = "core"

// entry is one catalog message.
type entry struct {
	text   string            // set for plain messages
	plural map[string]string // set for plural messages, keyed by CLDR form
}

func (m *entry) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	if dec.PeekKind() == '"' {
		return json.UnmarshalDecode(dec, &m.text)
	}
	return json.UnmarshalDecode(dec, &m.plural)
}

// catalog maps locale -> message id -> message.
type catalog map[string]map[string]*entry

// Manager owns all catalogs and the current locale. It is safe for
// concurrent use.
type Manager struct {
	mu         sync.RWMutex
	locale     string
	namespaces map[string]catalog
	tmplCache  sync.Map // template text -> *template.Template
	listeners  []func()
	log        *slog.Logger
}

// NewManager returns a Manager using locale, which may be "system".
func NewManager(locale string, log *slog.Logger) *Manager {
	m := &Manager{namespaces: map[string]catalog{}, log: log}
	m.locale = m.resolve(locale)
	return m
}

// Register loads every "<lang>.json" file in fsys into namespace ns.
func (m *Manager) Register(ns string, fsys fs.FS) error {
	files, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return err
	}
	cat := catalog{}
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return err
		}
		var msgs map[string]*entry
		if err := json.Unmarshal(data, &msgs); err != nil {
			return fmt.Errorf("i18n: %s/%s: %w", ns, f, err)
		}
		cat[strings.TrimSuffix(path.Base(f), ".json")] = msgs
	}
	if _, ok := cat[Source]; !ok {
		return fmt.Errorf("i18n: namespace %q has no %s.json", ns, Source)
	}
	m.mu.Lock()
	m.namespaces[ns] = cat
	m.mu.Unlock()
	return nil
}

// Locale returns the current locale.
func (m *Manager) Locale() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.locale
}

// SetLocale changes the current locale ("system" picks the OS language) and
// notifies listeners if it changed.
func (m *Manager) SetLocale(locale string) {
	resolved := m.resolve(locale)
	m.mu.Lock()
	changed := resolved != m.locale
	m.locale = resolved
	listeners := slices.Clone(m.listeners)
	m.mu.Unlock()
	if changed {
		for _, fn := range listeners {
			fn()
		}
	}
}

// OnChange registers fn to run after the locale changes.
func (m *Manager) OnChange(fn func()) {
	m.mu.Lock()
	m.listeners = append(m.listeners, fn)
	m.mu.Unlock()
}

// Available lists the locales that have a core catalog, sorted, without the
// pseudo-locale.
func (m *Manager) Available() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Sorted(maps.Keys(m.namespaces[CoreNamespace]))
}

// resolve maps a configured locale to a supported one.
func (m *Manager) resolve(locale string) string {
	switch locale {
	case Pseudo:
		return Pseudo
	case "", "system":
		return m.match(systemLanguages())
	default:
		return m.match([]string{locale})
	}
}

func (m *Manager) match(prefs []string) string {
	m.mu.RLock()
	var supported []language.Tag
	for loc := range m.namespaces[CoreNamespace] {
		supported = append(supported, language.Make(loc))
	}
	m.mu.RUnlock()
	if len(supported) == 0 {
		return Source
	}
	// The source language goes first so it is the matcher's default.
	slices.SortStableFunc(supported, func(a, b language.Tag) int {
		switch {
		case a.String() == Source:
			return -1
		case b.String() == Source:
			return 1
		}
		return strings.Compare(a.String(), b.String())
	})
	var want []language.Tag
	for _, p := range prefs {
		if t, err := language.Parse(p); err == nil {
			want = append(want, t)
		}
	}
	_, idx, _ := language.NewMatcher(supported).Match(want...)
	return supported[idx].String()
}

// Localizer returns a Localizer for namespace ns that falls back to the core
// namespace.
func (m *Manager) Localizer(ns string) *Localizer {
	return &Localizer{m: m, ns: ns}
}

// lookup finds the message for id, trying the locale then the source
// language, first in ns and then in the core namespace.
func (m *Manager) lookup(ns, id string) (*entry, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	loc := m.locale
	pseudo := loc == Pseudo
	for _, n := range []string{ns, CoreNamespace} {
		cat := m.namespaces[n]
		if cat == nil {
			continue
		}
		if !pseudo {
			if msg, ok := cat[loc][id]; ok {
				return msg, loc, false
			}
		}
		if msg, ok := cat[Source][id]; ok {
			return msg, Source, pseudo
		}
	}
	return nil, "", false
}

// Keys returns the message ids of namespace ns in locale loc, sorted.
func (m *Manager) Keys(ns, loc string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Sorted(maps.Keys(m.namespaces[ns][loc]))
}

// Locales returns the locales that have a catalog in namespace ns, sorted.
func (m *Manager) Locales(ns string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Sorted(maps.Keys(m.namespaces[ns]))
}

// Texts returns the template texts of a message (one per plural form), or
// nil if it does not exist.
func (m *Manager) Texts(ns, loc, id string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.namespaces[ns][loc][id]
	if !ok {
		return nil
	}
	if e.plural == nil {
		return []string{e.text}
	}
	return slices.Collect(maps.Values(e.plural))
}

// Namespaces returns the registered namespaces, sorted.
func (m *Manager) Namespaces() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Sorted(maps.Keys(m.namespaces))
}

// render executes a message template.
func (m *Manager) render(text string, data map[string]any) string {
	if !strings.Contains(text, "{{") {
		return text
	}
	var t *template.Template
	if v, ok := m.tmplCache.Load(text); ok {
		t = v.(*template.Template)
	} else {
		var err error
		t, err = template.New("").Option("missingkey=zero").Parse(text)
		if err != nil {
			m.logf("i18n: bad template %q: %v", text, err)
			return text
		}
		m.tmplCache.Store(text, t)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		m.logf("i18n: render %q: %v", text, err)
		return text
	}
	return buf.String()
}

func (m *Manager) logf(format string, args ...any) {
	if m.log != nil {
		m.log.Warn(fmt.Sprintf(format, args...))
	}
}

// pluralForm returns the CLDR plural form name of count in loc.
func pluralForm(loc string, count int) string {
	switch plural.Cardinal.MatchPlural(language.Make(loc), count, 0, 0, 0, 0) {
	case plural.Zero:
		return "zero"
	case plural.One:
		return "one"
	case plural.Two:
		return "two"
	case plural.Few:
		return "few"
	case plural.Many:
		return "many"
	default:
		return "other"
	}
}
