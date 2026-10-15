// Package core is the UI-independent launcher engine: it runs plugins,
// ranks root search results, executes actions and keeps the navigation
// state (Controller). Both the GUI and glctl are thin front ends over it.
package core

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// System performs the side effects of the built-in action kinds. glctl
// substitutes a recorder so actions can be observed without running them.
type System interface {
	Open(target string, opts plugin.OpenOptions) error
	CopyText(text string) error
	CopyImage(path string) error
	CopyFiles(paths []string) error
	// Paste puts content on the clipboard, hides the launcher, and pastes it
	// into the window that was active before the launcher was shown.
	Paste(text string, content plugin.PasteContent) error
}

// Options configure an Engine.
type Options struct {
	Plugins  []plugin.Plugin
	Services *host.Services
	Config   *config.Store
	System   System
	// RootTimeout bounds each plugin's RootItems call (default 150ms).
	RootTimeout time.Duration
}

// Engine runs plugins and actions. It is safe for concurrent use.
type Engine struct {
	svc         *host.Services
	cfg         *config.Store
	sys         System
	log         *slog.Logger
	core        *i18n.Localizer
	rootTimeout time.Duration

	plugins     []*loaded
	byID        map[string]*loaded
	unavailable []unavailable // plugins whose Init failed
}

// unavailable is a plugin that could not be initialized.
type unavailable struct {
	p      plugin.Plugin
	m      plugin.Manifest
	loc    *i18n.Localizer
	reason string
}

// unavailableReason strips the ErrUnavailable suffix from a wrapped error:
// "Everything is not installed: plugin unavailable" -> "Everything is not installed".
func unavailableReason(err error) string {
	s := err.Error()
	if r, ok := strings.CutSuffix(s, ": "+plugin.ErrUnavailable.Error()); ok {
		return r
	}
	return s
}

// loaded is an initialized plugin.
type loaded struct {
	p    plugin.Plugin
	m    plugin.Manifest
	host *host.Host
	loc  *i18n.Localizer
}

// New registers the plugins' catalogs and initializes them. A plugin whose
// Init fails is left out and reported by Plugins as unavailable.
func New(ctx context.Context, o Options) (*Engine, error) {
	e := &Engine{
		svc:         o.Services,
		cfg:         o.Config,
		sys:         o.System,
		log:         o.Services.Log,
		core:        o.Services.I18n.Localizer(i18n.CoreNamespace),
		rootTimeout: cmp.Or(o.RootTimeout, 150*time.Millisecond),
		byID:        map[string]*loaded{},
	}
	cfg := o.Config.Get()
	for _, p := range o.Plugins {
		m := p.Manifest()
		if _, dup := e.byID[m.ID]; dup {
			return nil, fmt.Errorf("core: duplicate plugin id %q", m.ID)
		}
		if lp, ok := p.(plugin.LocaleProvider); ok {
			if err := o.Services.I18n.Register(m.ID, lp.Locales()); err != nil {
				return nil, err
			}
		}
		h, err := o.Services.NewHost(m, cfg.Plugin(m.ID).Preferences)
		if err != nil {
			return nil, err
		}
		if _, err := guard(e, m.ID, func() (struct{}, error) { return struct{}{}, p.Init(ctx, h) }); err != nil {
			loc := o.Services.I18n.Localizer(m.ID)
			if errors.Is(err, plugin.ErrUnavailable) {
				e.log.Info("plugin unavailable", "plugin", m.ID, "reason", err)
				e.unavailable = append(e.unavailable, unavailable{p: p, m: m, loc: loc, reason: unavailableReason(err)})
			} else {
				e.log.Error("plugin init failed", "plugin", m.ID, "err", err)
				e.unavailable = append(e.unavailable, unavailable{p: p, m: m, loc: loc, reason: err.Error()})
			}
			continue
		}
		l := &loaded{p: p, m: m, host: h, loc: o.Services.I18n.Localizer(m.ID)}
		e.plugins = append(e.plugins, l)
		e.byID[m.ID] = l
	}
	o.Config.OnChange(func(c config.Config) {
		for _, l := range e.plugins {
			l.host.Prefs().SetValues(c.Plugin(l.m.ID).Preferences)
		}
		e.svc.Bus.Publish(plugin.EventConfigChanged)
	})
	return e, nil
}

// Close closes all plugins.
func (e *Engine) Close() error {
	var errs []error
	for _, l := range e.plugins {
		_, err := guard(e, l.m.ID, func() (struct{}, error) { return struct{}{}, l.p.Close() })
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Core returns the localizer of the host's own messages.
func (e *Engine) Core() *i18n.Localizer { return e.core }

// Now returns the engine's clock.
func (e *Engine) Now() time.Time { return e.svc.Now() }

// enabled returns the plugins not disabled in the configuration.
func (e *Engine) enabled(cfg config.Config) []*loaded {
	return slices.DeleteFunc(slices.Clone(e.plugins), func(l *loaded) bool {
		return cfg.Plugin(l.m.ID).Disabled
	})
}

// Entry is an item together with the plugin it came from.
type Entry struct {
	plugin.Item
	Plugin string `json:"plugin"`
	// Key identifies the entry across queries: "<plugin>/<item id>".
	Key string `json:"key"`
	// Rank is the final ranking score in the root search.
	Rank float64 `json:"rank,omitzero"`
}

// Section is a titled group of entries.
type Section struct {
	Title string  `json:"title,omitempty"`
	Items []Entry `json:"items"`
}

func newEntry(pluginID string, it plugin.Item) Entry {
	return Entry{Item: it, Plugin: pluginID, Key: pluginID + "/" + it.ID}
}

// Nav identifies a pushed view.
type Nav struct {
	Plugin  string             `json:"plugin"`
	Request plugin.ViewRequest `json:"request"`
}

// View renders a plugin view.
func (e *Engine) View(ctx context.Context, nav Nav) (plugin.View, error) {
	l, ok := e.byID[nav.Plugin]
	if !ok {
		return plugin.View{}, fmt.Errorf("core: unknown plugin %q", nav.Plugin)
	}
	vp, ok := l.p.(plugin.ViewProvider)
	if !ok {
		return plugin.View{}, fmt.Errorf("core: plugin %q has no views", nav.Plugin)
	}
	return callPlugin(ctx, e, nav.Plugin, viewTimeout, func(ctx context.Context) (plugin.View, error) { return vp.View(ctx, nav.Request) })
}

// Outcome tells the front end what to do after an action.
type Outcome struct {
	Hide      bool          `json:"hide,omitzero"`
	Push      *Nav          `json:"push,omitempty"`
	PopToRoot bool          `json:"popToRoot,omitzero"`
	Refresh   bool          `json:"refresh,omitzero"`
	Toast     *plugin.Toast `json:"toast,omitempty"`
}

// Run executes action a of entry. fromRoot records the use for ranking.
func (e *Engine) Run(ctx context.Context, entry Entry, a plugin.Action, fromRoot bool) (Outcome, error) {
	if fromRoot {
		if b, err := json.Marshal(entry); err == nil {
			if err := e.svc.DB.RecordUse(entry.Key, b, e.svc.Now()); err != nil {
				e.log.Warn("record use", "err", err)
			}
		}
	}
	do := a.Do
	copied := &plugin.Toast{Style: plugin.ToastSuccess, Title: e.core.T("toast.copied")}
	switch do.Kind {
	case plugin.ActionOpen:
		var opts plugin.OpenOptions
		if len(do.Payload) > 0 {
			if err := json.Unmarshal(do.Payload, &opts); err != nil {
				return Outcome{}, err
			}
		}
		return Outcome{Hide: true}, e.sys.Open(do.Target, opts)
	case plugin.ActionCopyText:
		return Outcome{Hide: true, Toast: copied}, e.sys.CopyText(do.Target)
	case plugin.ActionCopyImage:
		return Outcome{Hide: true, Toast: copied}, e.sys.CopyImage(do.Target)
	case plugin.ActionCopyFiles:
		var files []string
		if err := json.Unmarshal(do.Payload, &files); err != nil {
			return Outcome{}, err
		}
		return Outcome{Hide: true, Toast: copied}, e.sys.CopyFiles(files)
	case plugin.ActionPaste:
		var content plugin.PasteContent
		if do.Target == "" && len(do.Payload) > 0 {
			if err := json.Unmarshal(do.Payload, &content); err != nil {
				return Outcome{}, err
			}
		}
		return Outcome{Hide: true}, e.sys.Paste(do.Target, content)
	case plugin.ActionPush:
		return Outcome{Push: &Nav{Plugin: entry.Plugin, Request: plugin.ViewRequest{ID: do.Target, Payload: do.Payload}}}, nil
	case plugin.ActionRunCommand, plugin.ActionCallback:
		return e.handle(ctx, entry.Plugin, do)
	default:
		return Outcome{}, fmt.Errorf("core: unknown action kind %q", do.Kind)
	}
}

func (e *Engine) handle(ctx context.Context, pluginID string, do plugin.ActionDo) (Outcome, error) {
	l, ok := e.byID[pluginID]
	if !ok {
		return Outcome{}, fmt.Errorf("core: unknown plugin %q", pluginID)
	}
	h, ok := l.p.(plugin.ActionHandler)
	if !ok {
		return Outcome{}, fmt.Errorf("core: plugin %q cannot handle actions", pluginID)
	}
	r, err := callPlugin(ctx, e, pluginID, actionTimeout, func(ctx context.Context) (plugin.Result, error) { return h.HandleAction(ctx, do) })
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Hide: r.Hide, Push: navOf(pluginID, r.Push), PopToRoot: r.PopToRoot, Refresh: r.Refresh, Toast: r.Toast}, nil
}

func navOf(pluginID string, req *plugin.ViewRequest) *Nav {
	if req == nil {
		return nil
	}
	return &Nav{Plugin: pluginID, Request: *req}
}

// RunCommand runs a plugin command directly, as its hotkey does.
func (e *Engine) RunCommand(ctx context.Context, pluginID, name string) (Outcome, error) {
	entry, ok := e.commandEntry(e.cfg.Get(), pluginID, name)
	if !ok {
		return Outcome{}, fmt.Errorf("core: unknown command %s/%s", pluginID, name)
	}
	return e.Run(ctx, entry, entry.Actions[0], true)
}

// FindEntry looks an entry up by key among the given sections.
func FindEntry(sections []Section, key string) (Entry, bool) {
	for _, s := range sections {
		for _, it := range s.Items {
			if it.Key == key {
				return it, true
			}
		}
	}
	return Entry{}, false
}

// FindAction returns the action of entry with id, or the primary action
// when id is "".
func FindAction(entry Entry, id string) (plugin.Action, bool) {
	if id == "" && len(entry.Actions) > 0 {
		return entry.Actions[0], true
	}
	i := slices.IndexFunc(entry.Actions, func(a plugin.Action) bool { return a.ID == id })
	if i < 0 {
		return plugin.Action{}, false
	}
	return entry.Actions[i], true
}

// normalizeShortcut makes shortcut strings comparable: "ctrl + shift+c" -> "Ctrl+Shift+C".
func normalizeShortcut(s string) string {
	parts := strings.Split(s, "+")
	var mods []string
	key := ""
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch strings.ToLower(p) {
		case "ctrl", "control", "cmdorctrl", "commandorcontrol":
			mods = append(mods, "Ctrl")
		case "alt", "option":
			mods = append(mods, "Alt")
		case "shift":
			mods = append(mods, "Shift")
		case "win", "super", "meta", "cmd", "command":
			mods = append(mods, "Win")
		case "esc":
			key = "Escape"
		case "return":
			key = "Enter"
		default:
			if len(p) == 1 {
				p = strings.ToUpper(p)
			} else if p != "" {
				p = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
			}
			key = p
		}
	}
	order := map[string]int{"Ctrl": 0, "Alt": 1, "Shift": 2, "Win": 3}
	slices.SortFunc(mods, func(a, b string) int { return order[a] - order[b] })
	mods = slices.Compact(mods)
	return strings.Join(append(mods, key), "+")
}

// guard calls into a plugin, turning a panic into an error so a faulty
// plugin cannot take the launcher down. The stack is logged.
func guard[T any](e *Engine, pluginID string, fn func() (T, error)) (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			e.log.Error("plugin panicked", "plugin", pluginID, "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("core: plugin %s crashed: %v", pluginID, r)
		}
	}()
	return fn()
}

// Time limits for plugin calls that the user waits on. A plugin that does not
// answer in time is abandoned (its goroutine keeps running until it returns)
// so the launcher stays responsive.
var (
	viewTimeout   = 10 * time.Second
	actionTimeout = 30 * time.Second
)

// ErrPluginTimeout is returned when a plugin does not answer in time.
var ErrPluginTimeout = errors.New("core: plugin did not respond in time")

// callPlugin runs fn with panics contained and gives up after d.
func callPlugin[T any](ctx context.Context, e *Engine, pluginID string, d time.Duration, fn func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := guard(e, pluginID, func() (T, error) { return fn(ctx) })
		done <- result{v, err}
	}()
	select {
	case r := <-done:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			e.log.Error("plugin timed out", "plugin", pluginID, "after", d)
			return zero, fmt.Errorf("%w: %s (%v)", ErrPluginTimeout, pluginID, d)
		}
		return zero, ctx.Err()
	}
}
