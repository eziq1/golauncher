// Package quicklinks is the built-in quicklinks and web search plugin
// (docs/quicklinks.md).
//
// "g golang generics" searches Google, "bd 天气" Baidu (about twenty
// built-in engines with keywords, and engines of the user's own); when the
// root search finds nothing good it offers "Search Google for …" (a
// plugin.FallbackProvider); a typed URL ("github.com/foo", "localhost:3000")
// is opened directly; and quicklinks are the user's named links to URLs,
// files, folders and programs, with placeholders such as {query},
// {clipboard} and {date}. Search suggestions are off by default.
package quicklinks

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin's manifest ID.
const ID = "builtin.quicklinks"

// Command names (also the IDs of their views) and other view IDs.
const (
	cmdLinks      = "quicklinks"
	cmdCreate     = "create"
	cmdEngines    = "engines"
	viewEdit      = "edit"
	viewEngine    = "engine"
	viewImport    = "import"
	viewExport    = "export"
	prefDefault   = "defaultEngine"
	prefSecond    = "secondEngine"
	prefFallback  = "fallback"
	prefOpenURLs  = "openURLs"
	prefSuggest   = "suggestions"
	maxBrowsers   = 5
	argQuery      = "query"
	debounceDelay = 250 * time.Millisecond
	suggestLimit  = 2 * time.Second
)

//go:embed locales/*.json
var locales embed.FS

// Options change how the plugin reaches the system; tests and the fake
// environment replace them.
type Options struct {
	// Client makes the suggestion requests (default: http.DefaultClient's
	// transport with SuggestTimeout).
	Client *http.Client
	// Offline replaces Client by one that fails every request (glctl's fake
	// environment).
	Offline bool
	// After waits for the debounce delay (default time.After).
	After func(time.Duration) <-chan time.Time
	// Debounce is how long typing must pause before suggestions are asked
	// for (default 250ms); SuggestTimeout limits a request (default 2s).
	Debounce, SuggestTimeout time.Duration
	// Browsers lists the installed browsers (default shell.Browsers).
	Browsers func() []shell.Browser
	// Sync reads the browsers in Init instead of in the background, so
	// that results are deterministic (glctl, tests).
	Sync bool
}

// Plugin is the quicklinks and web search plugin.
type Plugin struct {
	opts Options
	h    plugin.Host
	sug  *suggester

	mu       sync.Mutex
	links    linksData
	engines  enginesData
	browsers []shell.Browser

	wg     sync.WaitGroup
	unsubs []func()
}

var (
	_ plugin.Plugin                    = (*Plugin)(nil)
	_ plugin.RootProvider              = (*Plugin)(nil)
	_ plugin.FallbackProvider          = (*Plugin)(nil)
	_ plugin.FallbackOrderer           = (*Plugin)(nil)
	_ plugin.ViewProvider              = (*Plugin)(nil)
	_ plugin.ActionHandler             = (*Plugin)(nil)
	_ plugin.LocaleProvider            = (*Plugin)(nil)
	_ plugin.PreferenceOptionsProvider = (*Plugin)(nil)
)

// New returns the plugin for the real system.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewWithOptions returns the plugin with o.
func NewWithOptions(o Options) *Plugin {
	if o.Debounce <= 0 {
		o.Debounce = debounceDelay
	}
	if o.SuggestTimeout <= 0 {
		o.SuggestTimeout = suggestLimit
	}
	if o.Offline {
		o.Client = &http.Client{Transport: offlineTransport{}}
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: o.SuggestTimeout}
	}
	if o.After == nil {
		o.After = defaultAfter
	}
	if o.Browsers == nil {
		o.Browsers = shell.Browsers
	}
	p := &Plugin{opts: o}
	p.sug = newSuggester(p)
	return p
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "globe"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermStorage, plugin.PermClipboard, plugin.PermNetwork},
		Commands: []plugin.Command{
			{Name: cmdLinks, Title: "%command.quicklinks.title%", Mode: plugin.ModeView, Icon: plugin.Icon{Name: "globe"},
				Keywords: []string{"%command.quicklinks.keywords%"}},
			{Name: cmdCreate, Title: "%command.create.title%", Mode: plugin.ModeView, Icon: plugin.Icon{Name: "file-plus"},
				Keywords: []string{"%command.create.keywords%"}},
			{Name: cmdEngines, Title: "%command.engines.title%", Mode: plugin.ModeView, Icon: plugin.Icon{Name: "search"},
				Keywords: []string{"%command.engines.keywords%"}},
		},
		Preferences: []plugin.Preference{
			{Name: prefDefault, Type: plugin.PrefSelect, Title: "%pref.defaultEngine.title%", Description: "%pref.defaultEngine.description%",
				Default: plugin.MustPayload(""), DynamicOptions: true},
			{Name: prefSecond, Type: plugin.PrefSelect, Title: "%pref.secondEngine.title%", Description: "%pref.secondEngine.description%",
				Default: plugin.MustPayload(""), DynamicOptions: true},
			{Name: prefFallback, Type: plugin.PrefBool, Title: "%pref.fallback.title%", Description: "%pref.fallback.description%",
				Default: plugin.MustPayload(true)},
			{Name: prefOpenURLs, Type: plugin.PrefBool, Title: "%pref.openURLs.title%", Description: "%pref.openURLs.description%",
				Default: plugin.MustPayload(true)},
			{Name: prefSuggest, Type: plugin.PrefBool, Title: "%pref.suggestions.title%", Description: "%pref.suggestions.description%",
				Default: plugin.MustPayload(false)},
		},
	}
}

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, err := fs.Sub(locales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	p.mu.Lock()
	p.load()
	p.mu.Unlock()
	ev := h.Events()
	p.unsubs = append(p.unsubs,
		ev.Subscribe(plugin.EventWindowHidden, p.sug.stop),
		ev.Subscribe(plugin.EventWindowShown, p.refreshBrowsers),
	)
	if p.opts.Sync {
		p.readBrowsers()
	} else {
		p.refreshBrowsers()
	}
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close() error {
	for _, u := range p.unsubs {
		u()
	}
	p.sug.close()
	p.wg.Wait()
	return nil
}

// refreshBrowsers reads the installed browsers in the background (or at
// once in Sync mode).
func (p *Plugin) refreshBrowsers() {
	if p.opts.Sync {
		p.readBrowsers()
		return
	}
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		p.readBrowsers()
	})
}

func (p *Plugin) readBrowsers() {
	bs := p.opts.Browsers()
	if len(bs) > maxBrowsers {
		bs = bs[:maxBrowsers]
	}
	p.mu.Lock()
	p.browsers = bs
	p.mu.Unlock()
}

func (p *Plugin) browserList() []shell.Browser {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.browsers)
}

// defaultEngines are the engines of the fallback items: the default one
// (or the automatic choice) and the second one, if chosen; disabled
// engines are left out.
func (p *Plugin) defaultEngines() []Engine {
	engines := p.engineList()
	find := func(id string) (Engine, bool) {
		i := slices.IndexFunc(engines, func(e Engine) bool { return e.ID == id && !e.Disabled })
		if i < 0 {
			return Engine{}, false
		}
		return engines[i], true
	}
	prefs := p.h.Preferences()
	var out []Engine
	first, ok := find(prefs.String(prefDefault))
	if !ok {
		first, ok = find(autoEngine(p.h.I18n().Locale()))
	}
	if !ok {
		i := slices.IndexFunc(engines, func(e Engine) bool { return !e.Disabled })
		if i >= 0 {
			first, ok = engines[i], true
		}
	}
	if ok {
		out = append(out, first)
	}
	if second, ok := find(prefs.String(prefSecond)); ok && (len(out) == 0 || second.ID != out[0].ID) {
		out = append(out, second)
	}
	return out
}

// RootItems implements plugin.RootProvider (docs/quicklinks.md §2, §4,
// §5.3).
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return nil, nil
	}
	b := p.newBuilder()
	prefs := p.h.Preferences()
	var items []plugin.Item
	if prefs.Bool(prefOpenURLs) {
		if u, ok := DetectURL(text); ok {
			items = append(items, b.urlItem(text, u))
		}
	}
	suggesting := false
	if m, ok := findKeyword(text, b.links, b.engines); ok {
		switch {
		case m.link != nil && m.rest != "":
			if hasQuery, _ := placeholders(m.link.Link); hasQuery {
				items = append(items, b.linkQueryItem(*m.link, m.rest))
			}
		case m.engine != nil && m.rest != "":
			items = append(items, b.searchItem(*m.engine, m.rest))
			if prefs.Bool(prefSuggest) && m.engine.Suggest != "" {
				suggesting = true
				if res, ok := p.sug.get(*m.engine, m.rest); ok {
					items = append(items, b.suggestionItems(*m.engine, res)...)
				}
			}
		}
	}
	if !suggesting {
		p.sug.stop()
	}
	low := strings.ToLower(text)
	for _, e := range b.engines {
		if e.Disabled {
			continue
		}
		byName := len([]rune(low)) >= 3 && strings.HasPrefix(strings.ToLower(e.Name), low)
		if byName || slices.ContainsFunc(e.Keywords, func(k string) bool { return normKeyword(k) == low }) {
			items = append(items, b.engineItem(e))
		}
	}
	for _, l := range b.links {
		items = append(items, b.linkItem(l, true))
	}
	return items, ctx.Err()
}

// FallbackOrder implements plugin.FallbackOrderer: the web search is the
// catch-all, after every other fallback ("Ask AI: …" comes first, R56).
func (p *Plugin) FallbackOrder() int { return plugin.FallbackOrderLast }

// FallbackItems implements plugin.FallbackProvider: "Search Google for …"
// with the default engine, and the second engine if chosen
// (docs/quicklinks.md §3).
func (p *Plugin) FallbackItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	text := strings.TrimSpace(q.Text)
	if text == "" || !p.h.Preferences().Bool(prefFallback) {
		return nil, nil
	}
	b := p.newBuilder()
	var items []plugin.Item
	for _, e := range p.defaultEngines() {
		it := b.searchItem(e, text)
		it.ID = "fallback:" + e.ID
		it.Score = 0
		it.Group = ""
		items = append(items, it)
	}
	return items, nil
}

// PreferenceOptions implements plugin.PreferenceOptionsProvider: the
// enabled engines, after "Automatic" (default engine) or "None" (second).
func (p *Plugin) PreferenceOptions(_ context.Context, name string) ([]plugin.Option, error) {
	l := p.h.I18n()
	var opts []plugin.Option
	switch name {
	case prefDefault:
		auto, _ := builtinByID(autoEngine(l.Locale()))
		opts = append(opts, plugin.Option{Value: "", Title: l.T("pref.defaultEngine.auto", "engine", auto.localName(l))})
	case prefSecond:
		opts = append(opts, plugin.Option{Value: "", Title: l.T("pref.secondEngine.none")})
	default:
		return nil, nil
	}
	for _, e := range p.engineList() {
		if !e.Disabled {
			opts = append(opts, plugin.Option{Value: e.ID, Title: e.Name})
		}
	}
	return opts, nil
}
