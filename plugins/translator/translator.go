// Package translator is the built-in translator plugin, builtin.translator
// (docs/translator.md): "tr hello", "fy …" or "翻译 …" translates as you
// type, with every enabled engine on its own row (Google's web endpoint,
// MyMemory, DeepL with a key), copies, pastes or replaces the selected text
// with the translation, and opens it in a web translator.
//
// Privacy: text is sent only after a prefix (or, with the preference on, the
// selected text); rows are Transient; logs have lengths and timings only.
package translator

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/tts"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/translator/engines"
)

// ID is the plugin's manifest ID.
const ID = "builtin.translator"

// Command (and view) names.
const viewTranslate = "translate"

// Preference names.
const (
	prefTarget    = "target"
	prefEngines   = "engines"
	prefFallback  = "fallback"
	prefSelection = "selection"
	prefDeepLKey  = "deeplKey"
	prefEmail     = "mymemoryEmail"
	prefAzureKey  = "azureKey"
	prefAzureRgn  = "azureRegion"
	prefYoudaoApp = "youdaoAppKey"
	prefYoudaoKey = "youdaoSecret"
	prefBaiduApp  = "baiduAppID"
	prefBaiduKey  = "baiduSecret"
	// Phase 3.
	prefHistory      = "history"
	prefHistoryDays  = "historyDays"
	prefClearHistory = "clearHistory"
	prefCustom       = "customEngines"
	prefOllamaURL    = "ollamaURL"
	prefOllamaModel  = "ollamaModel"
)

// Engine IDs.
const (
	engGoogle   = "google"
	engMyMemory = "mymemory"
	engDeepL    = "deepl"
	engAzure    = "azure"
	engYoudao   = "youdao"
	engBaidu    = "baidu"
	engOllama   = "ollama"
	// customPrefix starts the IDs of the user's engines ("custom:Name").
	customPrefix = "custom:"
)

// knownEngines are the IDs of the engines.
var knownEngines = []string{engGoogle, engMyMemory, engDeepL, engAzure, engYoudao, engBaidu, engOllama}

// defaultEngines is the default of prefEngines: engines that need a key are
// skipped while it is not set, so DeepL and Azure come first once theirs
// is (docs/translator.md §3), then Youdao and Baidu; a local model (Ollama)
// once its model is chosen.
var defaultEngines = []string{engDeepL, engAzure, engYoudao, engBaidu, engGoogle, engOllama}

// keyPrefs are the preferences an engine needs, by engine ID.
var keyPrefs = map[string][]string{
	engDeepL:  {prefDeepLKey},
	engAzure:  {prefAzureKey},
	engYoudao: {prefYoudaoApp, prefYoudaoKey},
	engBaidu:  {prefBaiduApp, prefBaiduKey},
	engOllama: {prefOllamaModel},
}

const (
	defaultDebounce = 300 * time.Millisecond
	defaultTimeout  = 5 * time.Second
	// maxSelectionRunes is the longest selected text translated at once.
	maxSelectionRunes = 2000
)

//go:embed locales/*.json
var locales embed.FS

// Options change how the plugin reaches the network; tests and the fake
// environment replace them.
type Options struct {
	// Client makes the requests (default: a client with Timeout).
	Client *http.Client
	// Offline makes every request fail (no network at all).
	Offline bool
	// After waits for the debounce delay (default time.After).
	After func(time.Duration) <-chan time.Time
	// Debounce is how long typing must pause before a request (300ms);
	// Timeout limits one engine's answer (5s).
	Debounce, Timeout time.Duration
	// BaseURLs replace the engines' addresses, by engine ID (tests).
	BaseURLs map[string]string
	// Engine, when set, makes the engines instead of the real ones (glctl's
	// fake environment uses engines.Fake). Engines needing a key are only
	// made while their key is set.
	Engine func(id string) engines.Engine
	// Sync translates before RootItems, View and SelectionItems return, so
	// that results are deterministic (glctl, tests).
	Sync bool
	// Speaker reads text aloud (default: the system's voices, tts.New);
	// tests and the fake environment never speak.
	Speaker tts.Speaker
}

// Plugin is the translator plugin.
type Plugin struct {
	opts Options
	h    plugin.Host
	// typing serves the root search and the view (a new text cancels the
	// last request); sel the selected text.
	typing, sel *runner

	mu     sync.Mutex
	cfg    settings
	seen   map[string]bool // view instances rendered once
	unsubs []func()
	// speaking stops the speech going on; speech waits for it.
	speaking context.CancelFunc
	speech   sync.WaitGroup
	// histMu serializes the history's reads and writes.
	histMu sync.Mutex
}

// settings is a snapshot of the preferences, read when they change (keys
// come from the Credential Manager, too slow for every keystroke).
type settings struct {
	engines  []engines.Engine
	fallback engines.Engine // nil when off or already listed
	target   string         // the preference ("" automatic)
}

var (
	_ plugin.Plugin                    = (*Plugin)(nil)
	_ plugin.RootProvider              = (*Plugin)(nil)
	_ plugin.ViewProvider              = (*Plugin)(nil)
	_ plugin.ViewCloser                = (*Plugin)(nil)
	_ plugin.SelectionProvider         = (*Plugin)(nil)
	_ plugin.LocaleProvider            = (*Plugin)(nil)
	_ plugin.ActionHandler             = (*Plugin)(nil)
	_ plugin.PreferenceOptionsProvider = (*Plugin)(nil)
)

// New returns the plugin for the real network.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewWithOptions returns the plugin with o.
func NewWithOptions(o Options) *Plugin {
	if o.Debounce <= 0 {
		o.Debounce = defaultDebounce
	}
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	if o.Offline {
		o.Client = &http.Client{Transport: offlineTransport{}}
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: o.Timeout}
	}
	if o.After == nil {
		o.After = time.After
	}
	if o.Speaker == nil {
		o.Speaker = tts.New()
	}
	p := &Plugin{opts: o, seen: map[string]bool{}}
	p.typing = newRunner(p, "typing")
	p.sel = newRunner(p, "selection")
	return p
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "languages"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermNetwork, plugin.PermSelection, plugin.PermStorage},
		Commands: []plugin.Command{
			{Name: viewTranslate, Title: "%command.translate.title%", Mode: plugin.ModeView, Icon: plugin.Icon{Name: "languages"},
				Keywords: []string{"%command.translate.keywords%"}},
			{Name: viewHistory, Title: "%command.history.title%", Mode: plugin.ModeView, Icon: plugin.Icon{Name: "history"},
				Keywords: []string{"%command.history.keywords%"}},
		},
		Preferences: []plugin.Preference{
			{Name: prefTarget, Type: plugin.PrefSelect, Title: "%pref.target.title%", Description: "%pref.target.description%",
				Default: plugin.MustPayload(""), Options: targetOptions()},
			{Name: prefEngines, Type: plugin.PrefStringList, Title: "%pref.engines.title%", Description: "%pref.engines.description%",
				Default: plugin.MustPayload(defaultEngines)},
			{Name: prefFallback, Type: plugin.PrefBool, Title: "%pref.fallback.title%", Description: "%pref.fallback.description%",
				Default: plugin.MustPayload(true)},
			{Name: prefSelection, Type: plugin.PrefBool, Title: "%pref.selection.title%", Description: "%pref.selection.description%",
				Default: plugin.MustPayload(false)},
			{Name: prefDeepLKey, Type: plugin.PrefPassword, Title: "%pref.deeplKey.title%", Description: "%pref.deeplKey.description%",
				Group: "%group.keys%"},
			{Name: prefEmail, Type: plugin.PrefText, Title: "%pref.mymemoryEmail.title%", Description: "%pref.mymemoryEmail.description%",
				Group: "%group.keys%"},
			{Name: prefAzureKey, Type: plugin.PrefPassword, Title: "%pref.azureKey.title%", Description: "%pref.azureKey.description%",
				Group: "%group.keys%"},
			{Name: prefAzureRgn, Type: plugin.PrefText, Title: "%pref.azureRegion.title%", Description: "%pref.azureRegion.description%",
				Group: "%group.keys%"},
			{Name: prefYoudaoApp, Type: plugin.PrefText, Title: "%pref.youdaoAppKey.title%", Description: "%pref.youdaoAppKey.description%",
				Group: "%group.keys%"},
			{Name: prefYoudaoKey, Type: plugin.PrefPassword, Title: "%pref.youdaoSecret.title%", Description: "%pref.youdaoSecret.description%",
				Group: "%group.keys%"},
			{Name: prefBaiduApp, Type: plugin.PrefText, Title: "%pref.baiduAppID.title%", Description: "%pref.baiduAppID.description%",
				Group: "%group.keys%"},
			{Name: prefBaiduKey, Type: plugin.PrefPassword, Title: "%pref.baiduSecret.title%", Description: "%pref.baiduSecret.description%",
				Group: "%group.keys%"},
			{Name: prefOllamaModel, Type: plugin.PrefSelect, Title: "%pref.ollamaModel.title%", Description: "%pref.ollamaModel.description%",
				Default: plugin.MustPayload(""), DynamicOptions: true, Group: "%group.local%"},
			{Name: prefOllamaURL, Type: plugin.PrefText, Title: "%pref.ollamaURL.title%", Description: "%pref.ollamaURL.description%",
				Default: plugin.MustPayload(engines.OllamaBase), Group: "%group.local%"},
			{Name: prefCustom, Type: plugin.PrefStringList, Title: "%pref.customEngines.title%", Description: "%pref.customEngines.description%",
				Group: "%group.custom%"},
			{Name: prefHistory, Type: plugin.PrefBool, Title: "%pref.history.title%", Description: "%pref.history.description%",
				Default: plugin.MustPayload(false), Group: "%group.history%"},
			{Name: prefHistoryDays, Type: plugin.PrefSelect, Title: "%pref.historyDays.title%", Description: "%pref.historyDays.description%",
				Default: plugin.MustPayload(strconv.Itoa(defaultHistoryDays)), Group: "%group.history%",
				VisibleWhen: &plugin.Condition{Pref: prefHistory, Equals: plugin.MustPayload(true)},
				Options: []plugin.Option{{Value: "1", Title: "%pref.historyDays.1%"}, {Value: "7", Title: "%pref.historyDays.7%"},
					{Value: "30", Title: "%pref.historyDays.30%"}, {Value: "90", Title: "%pref.historyDays.90%"}}},
			{Name: prefClearHistory, Type: plugin.PrefButton, Title: "%pref.clearHistory.title%", Description: "%pref.clearHistory.description%",
				Group: "%group.history%", Action: &plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbClearHistory}},
		},
	}
}

// targetOptions are the choices of prefTarget.
func targetOptions() []plugin.Option {
	opts := []plugin.Option{{Value: "", Title: "%pref.target.auto%"}}
	for _, l := range languages {
		opts = append(opts, plugin.Option{Value: l.code, Title: "%lang." + l.code + "%"})
	}
	return opts
}

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, err := fs.Sub(locales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}

// Init implements plugin.Plugin. After Close it starts again
// (docs/plugin-guide.md "Lifecycle").
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	p.typing.reopen()
	p.sel.reopen()
	p.mu.Lock()
	p.unsubs, p.seen = nil, map[string]bool{}
	p.mu.Unlock()
	p.reload()
	p.pruneHistory()
	ev := h.Events()
	unsubs := []func(){
		ev.Subscribe(plugin.EventConfigChanged, p.configChanged),
		ev.Subscribe(plugin.EventWindowHidden, p.windowHidden),
		// Keys set in the settings live in the Credential Manager (or the
		// Keychain): read them again when the launcher shows.
		ev.Subscribe(plugin.EventWindowShown, p.reload),
	}
	p.mu.Lock()
	p.unsubs = unsubs
	p.mu.Unlock()
	return nil
}

// Close implements plugin.Plugin: requests being made are cancelled.
func (p *Plugin) Close() error {
	p.mu.Lock()
	unsubs := p.unsubs
	p.unsubs = nil
	p.mu.Unlock()
	for _, u := range unsubs {
		u()
	}
	p.typing.close()
	p.sel.close()
	p.stopSpeaking()
	p.speech.Wait()
	return nil
}

// configChanged reads the preferences again; cached translations may come
// from engines no longer used.
func (p *Plugin) configChanged() {
	p.reload()
	p.pruneHistory() // turning the history off deletes it
	p.typing.clear()
	p.sel.clear()
}

// windowHidden stops the requests and forgets the translations (the
// selected text, too: it is kept for one show only).
func (p *Plugin) windowHidden() {
	p.typing.stop()
	p.sel.stop()
	p.sel.clear()
}

// reload reads the preferences into p.cfg.
func (p *Plugin) reload() {
	prefs := p.h.Preferences()
	env := engines.Env{Client: p.opts.Client, Go: p.h.Go}
	var list []engines.Engine
	ids := prefs.Strings(prefEngines)
	if len(ids) == 0 {
		ids = defaultEngines
	}
	customs := p.customEngines(env)
	var seen []string
	for _, raw := range ids {
		id := strings.ToLower(strings.TrimSpace(raw))
		if id == "" || slices.Contains(seen, id) {
			continue
		}
		seen = append(seen, id)
		if strings.HasPrefix(id, customPrefix) {
			// A custom engine named in the list goes there.
			if i := slices.IndexFunc(customs, func(e engines.Engine) bool { return strings.EqualFold(e.ID(), id) }); i >= 0 {
				list = append(list, customs[i])
				customs = slices.Delete(customs, i, i+1)
			}
			continue
		}
		if e := p.newEngine(id, env); e != nil {
			list = append(list, e)
		}
	}
	list = append(list, customs...) // the others after the listed engines
	var fallback engines.Engine
	if prefs.Bool(prefFallback) && !slices.Contains(seen, engMyMemory) {
		fallback = p.newEngine(engMyMemory, env)
	}
	p.mu.Lock()
	p.cfg = settings{engines: list, fallback: fallback, target: prefs.String(prefTarget)}
	p.mu.Unlock()
}

// newEngine makes the engine with id, nil when it is unknown or its key is
// not set.
func (p *Plugin) newEngine(id string, env engines.Env) engines.Engine {
	prefs := p.h.Preferences()
	for _, k := range keyPrefs[id] {
		if strings.TrimSpace(prefs.String(k)) == "" {
			return nil
		}
	}
	if !slices.Contains(knownEngines, id) {
		return nil
	}
	if p.opts.Engine != nil {
		return p.opts.Engine(id)
	}
	base := p.opts.BaseURLs[id]
	switch id {
	case engGoogle:
		return &engines.Google{Env: env, BaseURL: base}
	case engMyMemory:
		return &engines.MyMemory{Env: env, Email: prefs.String(prefEmail), BaseURL: base}
	case engDeepL:
		return &engines.DeepL{Env: env, Key: prefs.String(prefDeepLKey), BaseURL: base}
	case engAzure:
		return &engines.Azure{Env: env, Key: prefs.String(prefAzureKey), Region: prefs.String(prefAzureRgn), BaseURL: base}
	case engYoudao:
		return &engines.Youdao{Env: env, AppKey: prefs.String(prefYoudaoApp), Secret: prefs.String(prefYoudaoKey), BaseURL: base}
	case engBaidu:
		return &engines.Baidu{Env: env, AppID: prefs.String(prefBaiduApp), Secret: prefs.String(prefBaiduKey), BaseURL: base}
	case engOllama:
		return &engines.Ollama{Env: env, BaseURL: firstOf(base, prefs.String(prefOllamaURL)), Model: prefs.String(prefOllamaModel)}
	}
	return nil
}

func (p *Plugin) settings() settings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cfg
}

// RootItems implements plugin.RootProvider: only text after a prefix is
// translated (and sent anywhere).
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	rest, ok := cutPrefix(strings.TrimSpace(q.Text))
	if !ok || rest == "" {
		p.typing.stop()
		return nil, nil
	}
	pq := parseQuery(rest)
	if strings.TrimSpace(pq.Text) == "" {
		p.typing.stop()
		return []plugin.Item{p.hintItem(pq)}, nil
	}
	j := p.newJob(pq)
	rows := p.typing.get(j, true, p.opts.Sync)
	items := p.rowItems(j, rows, origin{root: true})
	return items, ctx.Err()
}

// ViewClosed implements plugin.ViewCloser.
func (p *Plugin) ViewClosed(instance string) {
	p.mu.Lock()
	delete(p.seen, instance)
	p.mu.Unlock()
}

// offlineTransport fails every request (Options.Offline).
type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errOffline
}
