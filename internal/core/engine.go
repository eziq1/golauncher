// Package core is the UI-independent launcher engine: it runs plugins,
// ranks root search results, executes actions and keeps the navigation
// state (Controller). Both the GUI and glctl are thin front ends over it.
package core

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
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
	// into the window that was active before the launcher was shown. When
	// the content is on the clipboard but pasting is not allowed (macOS
	// without Accessibility), it returns an error wrapping ErrNotPasted.
	Paste(text string, content plugin.PasteContent) error
}

// ErrNotPasted is wrapped by System.Paste when the content was copied but
// could not be pasted for lack of a permission (macOS: Accessibility, which
// sending ⌘V needs). The action then succeeds: the user is told to paste
// themselves (toast.pasteManual) and where to grant the permission.
var ErrNotPasted = errors.New("core: copied, but not allowed to paste")

// SettingsPermissions is the settings page a Notice about a missing
// permission opens (ui.PagePermissions).
const SettingsPermissions = "permissions"

// Options configure an Engine.
type Options struct {
	Plugins  []plugin.Plugin
	Services *host.Services
	Config   *config.Store
	System   System
	// RootTimeout bounds each plugin's RootItems call (default 150ms).
	RootTimeout time.Duration
	// SelectionBudget bounds reading the selected text (default 250ms,
	// docs/selected-text.md §3.4).
	SelectionBudget time.Duration
	// Tasks is the background task manager (nil: no background tasks).
	Tasks TaskService
}

// TaskService is the host's background task manager (internal/tasks), as
// the engine sees it.
type TaskService interface {
	// RunProcess queues a process task of plugin pluginID (ActionRunProcess).
	RunProcess(pluginID string, spec plugin.TaskSpec, cmd plugin.ProcessSpec) string
	// Indicator returns the number of queued and running tasks and of
	// failed tasks the user has not seen yet.
	Indicator() (active, failed int)
	// View returns the view showing task id, or all tasks for "".
	View(id string) (pluginID string, req plugin.ViewRequest)
}

// Notice is a message for the user that is not the outcome of an action:
// a plugin's Host.Toast, or a finished background task (TaskID).
type Notice struct {
	Toast  plugin.Toast `json:"toast"`
	Plugin string       `json:"plugin,omitempty"`
	TaskID string       `json:"taskId,omitempty"`
	// Settings is the settings page clicking the notification opens
	// (SettingsPermissions), "" for none.
	Settings string `json:"settings,omitempty"`
}

// Engine runs plugins and actions. It is safe for concurrent use.
type Engine struct {
	svc         *host.Services
	cfg         *config.Store
	sys         System
	log         *slog.Logger
	core        *i18n.Localizer
	rootTimeout time.Duration
	selBudget   time.Duration // reading the selected text
	tasks       TaskService

	notifyMu sync.Mutex
	notifier func(Notice)

	// plugins are the registered plugins in their order, whatever their
	// state; neither it nor byID changes after New.
	plugins []*loaded
	byID    map[string]*loaded

	// The plugin lifecycle (lifecycle.go). lifeMu serializes starting and
	// stopping plugins; stateMu guards the loaded.state fields, initOrder and
	// closed.
	lifeMu    sync.Mutex
	stateMu   sync.RWMutex
	initOrder []*loaded // the plugins that are on, in the order of their Init
	closed    bool      // Close was called: no plugin starts any more
	syncs     syncState // the lifecycle changes waiting or running

	// Controllers showing this engine's plugins, told about Host.Invalidate
	// and Host.Render calls, and the hooks told when a view instance closes.
	subMu       sync.Mutex
	controllers []*Controller
	closedHooks []func(pluginID, instance string)
	invHooks    invalidateHooks

	// ctx is the context of the current show (context.go).
	ctx contextState

	// chooser shows the open dialog of ActionChooseFiles (files.go).
	chooserMu sync.Mutex
	chooser   FileChooser
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

// loaded is a registered plugin: one that runs on this system, on or off.
// p, m and loc never change; the other fields are guarded by Engine.stateMu
// (closing by Engine.lifeMu).
type loaded struct {
	p   plugin.Plugin
	m   plugin.Manifest
	loc *i18n.Localizer

	state  pluginState
	host   *host.Host // the Host of the current run while on
	reason string     // why the last Init failed (stateFailed)
	// closing is closed when the last run of the plugin has fully ended (its
	// Close returned, and its event handlers and the engine's calls into it
	// are done); nil before the first.
	closing chan struct{}

	calls callCount // calls into the plugin running (guard)
}

// callCount counts the calls into a plugin that are running, so that it is
// initialized again only once the calls of its last run have returned (a
// call the engine gave up on may still run).
type callCount struct {
	mu   sync.Mutex
	n    int
	idle *sync.Cond
}

func (c *callCount) enter() {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
}

func (c *callCount) leave() {
	c.mu.Lock()
	if c.n--; c.n == 0 {
		c.idle.Broadcast()
	}
	c.mu.Unlock()
}

// wait blocks until no call runs.
func (c *callCount) wait() {
	c.mu.Lock()
	for c.n > 0 {
		c.idle.Wait()
	}
	c.mu.Unlock()
}

// New registers the plugins and their catalogs and initializes those not
// turned off in the configuration, in their order. A plugin whose Init fails
// is closed and reported by Plugins as unavailable. When New fails, the
// plugins it initialized are closed again, the last first.
func New(ctx context.Context, o Options) (*Engine, error) {
	e := &Engine{
		svc:         o.Services,
		cfg:         o.Config,
		sys:         o.System,
		log:         o.Services.Log,
		core:        o.Services.I18n.Localizer(i18n.CoreNamespace),
		rootTimeout: cmp.Or(o.RootTimeout, 150*time.Millisecond),
		selBudget:   cmp.Or(o.SelectionBudget, defaultSelectionBudget),
		tasks:       o.Tasks,
		byID:        map[string]*loaded{},
	}
	// Before any plugin is initialized: Init may already start background
	// work that invalidates.
	o.Services.OnInvalidate = e.invalidate
	o.Services.OnRender = e.render
	o.Services.OnToast = func(pluginID string, t plugin.Toast) { e.Notify(Notice{Toast: t, Plugin: pluginID}) }
	e.syncs.idle = sync.NewCond(&e.syncs.mu)
	// Every plugin is registered before the first Init, so a duplicate ID or
	// a bad catalog fails New before any plugin runs.
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
		l := &loaded{p: p, m: m, loc: o.Services.I18n.Localizer(m.ID)}
		l.calls.idle = sync.NewCond(&l.calls.mu)
		e.plugins = append(e.plugins, l)
		e.byID[m.ID] = l
	}
	cfg := o.Config.Get()
	e.lifeMu.Lock()
	for _, l := range e.plugins {
		if cfg.Plugin(l.m.ID).Disabled {
			continue // off: not initialized until it is turned on
		}
		if err := e.start(ctx, l, cfg); err != nil {
			e.lifeMu.Unlock()
			// Roll back: close what was initialized, the last first.
			if cerr := e.Close(); cerr != nil {
				e.log.Warn("closing plugins after a failed start", "err", cerr)
			}
			return nil, err
		}
	}
	e.lifeMu.Unlock()
	// Tell plugins that track open views (plugin.ViewCloser: external plugin
	// proxies, timers of in-process ones) when a view closes, so they can
	// free its state and an idle process can stop.
	e.OnViewClosed(func(pluginID, instance string) {
		if l, ok := e.lookup(pluginID); ok {
			if vc, ok := l.p.(plugin.ViewCloser); ok {
				vc.ViewClosed(instance)
			}
		}
	})
	o.Config.OnChange(func(c config.Config) {
		for _, l := range e.plugins {
			if h := e.hostOf(l); h != nil {
				h.Prefs().SetValues(c.Plugin(l.m.ID).Preferences)
			}
		}
		e.svc.Bus.Publish(plugin.EventConfigChanged)
		e.clearContextIfOff(c)
		// Turning plugins on and off runs Init and Close, which may take a
		// while: not on the goroutine that changed the settings (the UI's).
		e.syncLater()
	})
	return e, nil
}

// subscribe makes c receive the plugins' invalidations and pushed views.
func (e *Engine) subscribe(c *Controller) {
	e.subMu.Lock()
	e.controllers = append(e.controllers, c)
	e.subMu.Unlock()
}

func (e *Engine) subscribers() []*Controller {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	return slices.Clone(e.controllers)
}

// invalidate receives Host.Invalidate (design m5 §2).
func (e *Engine) invalidate(pluginID, viewID string) {
	for _, c := range e.subscribers() {
		c.invalidate(pluginID, viewID)
	}
	e.invHooks.call(pluginID, viewID)
}

// render receives Host.Render (design m5 §5.4).
func (e *Engine) render(pluginID, instance string, v plugin.View) {
	for _, c := range e.subscribers() {
		c.push(pluginID, instance, v)
	}
}

// SetNotifier sets fn to show notices instead of the default, which shows
// them as a toast in the controllers. The GUI shows them in the launcher
// when it is visible and as Windows notifications otherwise.
func (e *Engine) SetNotifier(fn func(Notice)) {
	e.notifyMu.Lock()
	e.notifier = fn
	e.notifyMu.Unlock()
}

// Notify shows a notice to the user. It is safe from any goroutine.
func (e *Engine) Notify(n Notice) {
	e.notifyMu.Lock()
	fn := e.notifier
	e.notifyMu.Unlock()
	if fn != nil {
		fn(n)
		return
	}
	for _, c := range e.subscribers() {
		c.ShowToast(n.Toast)
	}
}

// Tasks returns the background task manager, or nil.
func (e *Engine) Tasks() TaskService { return e.tasks }

// OnViewClosed registers fn to be called (on a background goroutine) when a
// view instance (plugin.ViewRequest.Instance) is closed, so that a plugin
// keeping state per instance can release it. External plugins forward it as
// the viewClosed notification.
func (e *Engine) OnViewClosed(fn func(pluginID, instance string)) {
	e.subMu.Lock()
	e.closedHooks = append(e.closedHooks, fn)
	e.subMu.Unlock()
}

// viewsClosed calls the OnViewClosed hooks for closed frames.
func (e *Engine) viewsClosed(closed []*Nav) {
	if len(closed) == 0 {
		return
	}
	e.subMu.Lock()
	hooks := slices.Clone(e.closedHooks)
	e.subMu.Unlock()
	if len(hooks) == 0 {
		return
	}
	host.Go(e.log, "view closed hooks", func() {
		for _, n := range closed {
			for _, fn := range hooks {
				fn(n.Plugin, n.Request.Instance)
			}
		}
	})
}

// isRootProvider reports whether plugin id contributes to the root search.
func (e *Engine) isRootProvider(id string) bool {
	l, ok := e.lookup(id)
	if !ok {
		return false
	}
	_, ok = l.p.(plugin.RootProvider)
	return ok
}

// Core returns the localizer of the host's own messages.
func (e *Engine) Core() *i18n.Localizer { return e.core }

// Now returns the engine's clock.
func (e *Engine) Now() time.Time { return e.svc.Now() }

// enabled returns the plugins that are on and not turned off in cfg (which
// the engine may not have applied yet).
func (e *Engine) enabled(cfg config.Config) []*loaded {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	var out []*loaded
	for _, l := range e.plugins {
		if l.state == stateOn && !cfg.Plugin(l.m.ID).Disabled {
			out = append(out, l)
		}
	}
	return out
}

// Entry is an item together with the plugin it came from.
type Entry struct {
	plugin.Item
	Plugin string `json:"plugin"`
	// Key identifies the entry across queries: "<plugin>/<item id>".
	Key string `json:"key"`
	// Rank is the final ranking score in the root search.
	Rank float64 `json:"rank,omitzero"`
	// Context marks an item of a plugin.ContextProvider: it belongs to this
	// show's context and is never recorded as recently used.
	Context bool `json:"context,omitzero"`
	// Selection marks an item of a plugin.SelectionProvider (with Context):
	// it acts on the text selected when the launcher opened.
	Selection bool `json:"selection,omitzero"`
	// Fallback marks an item of a plugin.FallbackProvider (or the Tab
	// action): it acts on this search's text and is never recorded as
	// recently used.
	Fallback bool `json:"fallback,omitzero"`
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
	l, err := e.running(nav.Plugin)
	if err != nil {
		return plugin.View{}, err
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
	// FieldErrors are the plugin's messages for the fields of the submitted
	// form, by field ID; the form stays open.
	FieldErrors map[string]string `json:"fieldErrors,omitempty"`
	// ClearText empties the search field of the current view.
	ClearText bool `json:"clearText,omitzero"`
}

// Run executes action a of entry. fromRoot records the use for ranking.
func (e *Engine) Run(ctx context.Context, entry Entry, a plugin.Action, fromRoot bool) (Outcome, error) {
	if fromRoot && !entry.Context && !entry.Fallback && !entry.Transient {
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
		return e.paste(do.Target, content)
	case plugin.ActionPush:
		req := plugin.ViewRequest{ID: do.Target, Payload: do.Payload, Arguments: stringValues(do.Values)}
		return Outcome{Push: &Nav{Plugin: entry.Plugin, Request: req}}, nil
	case plugin.ActionRunCommand, plugin.ActionCallback:
		return e.handle(ctx, entry.Plugin, do)
	case plugin.ActionRunProcess:
		if e.tasks == nil {
			return Outcome{}, errors.New("core: background tasks are not available")
		}
		if entry.Plugin != "" {
			// A row of a plugin turned off since it was listed.
			if _, err := e.running(entry.Plugin); err != nil {
				return Outcome{}, err
			}
		}
		var p plugin.RunProcessPayload
		if err := json.Unmarshal(do.Payload, &p); err != nil {
			return Outcome{}, err
		}
		e.tasks.RunProcess(entry.Plugin, p.Spec, p.Cmd)
		return Outcome{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: e.core.T("tasks.toast.started", "title", p.Spec.Title),
			Message: e.core.T("tasks.toast.startedHint")}}, nil
	case plugin.ActionShowTask:
		if e.tasks == nil {
			return Outcome{}, errors.New("core: background tasks are not available")
		}
		id, req := e.tasks.View(do.Target)
		return Outcome{Push: &Nav{Plugin: id, Request: req}}, nil
	case plugin.ActionChooseFiles:
		// Only the Controller runs it, for the user, in a view that accepts
		// files (files.go): never an open dialog for anybody else (AI tools,
		// a Result.Do).
		return Outcome{}, ErrChooseFilesHere
	default:
		return Outcome{}, fmt.Errorf("core: unknown action kind %q", do.Kind)
	}
}

// paste carries out ActionPaste. When pasting is not allowed (ErrNotPasted)
// the content is still on the clipboard: the launcher hides as usual and a
// notice, which shows while the launcher is hidden, says to press ⌘V.
func (e *Engine) paste(text string, content plugin.PasteContent) (Outcome, error) {
	err := e.sys.Paste(text, content)
	if errors.Is(err, ErrNotPasted) {
		e.log.Info("paste: copied only", "err", err)
		e.Notify(Notice{
			Toast: plugin.Toast{Style: plugin.ToastInfo, Title: e.core.T("toast.pasteManual"),
				Message: e.core.T("toast.pasteManual.message")},
			Settings: SettingsPermissions,
		})
		return Outcome{Hide: true}, nil
	}
	return Outcome{Hide: true}, err
}

func (e *Engine) handle(ctx context.Context, pluginID string, do plugin.ActionDo) (Outcome, error) {
	l, err := e.running(pluginID)
	if err != nil {
		return Outcome{}, err
	}
	h, ok := l.p.(plugin.ActionHandler)
	if !ok {
		return Outcome{}, fmt.Errorf("core: plugin %q cannot handle actions", pluginID)
	}
	r, err := callPlugin(ctx, e, pluginID, actionTimeout, func(ctx context.Context) (plugin.Result, error) { return h.HandleAction(ctx, do) })
	if err != nil {
		return Outcome{}, err
	}
	return e.result(ctx, l, r)
}

// result turns the Result of a call into plugin l (an action's callback,
// files received) into an Outcome, storing the preferences it sets and
// running its follow-up action.
func (e *Engine) result(ctx context.Context, l *loaded, r plugin.Result) (Outcome, error) {
	pluginID := l.m.ID
	out := Outcome{Hide: r.Hide, Push: navOf(pluginID, r.Push), PopToRoot: r.PopToRoot, Refresh: r.Refresh, Toast: r.Toast,
		FieldErrors: r.FieldErrors, ClearText: r.ClearText}
	if len(r.Preferences) > 0 {
		if err := e.setPreferences(l, r.Preferences); err != nil {
			return out, err
		}
	}
	if r.Do != nil && len(r.FieldErrors) == 0 {
		// The follow-up action of the result (m5 §14): only the host's own
		// kinds, so a plugin cannot loop through its own callbacks.
		if k := r.Do.Kind; k == plugin.ActionCallback || k == plugin.ActionRunCommand {
			return out, fmt.Errorf("core: plugin %q returned a %s action in Result.Do", pluginID, k)
		}
		more, err := e.Run(ctx, Entry{Plugin: pluginID}, plugin.Action{ID: "result", Do: *r.Do}, false)
		if err != nil {
			return out, err
		}
		out.Hide = out.Hide || more.Hide
		out.Push = cmp.Or(more.Push, out.Push)
		out.Toast = cmp.Or(out.Toast, more.Toast)
	}
	return out, nil
}

// setPreferences stores values of a plugin's own preferences
// (Result.Preferences), as the settings window does. Only declared,
// non-password preferences can be set.
func (e *Engine) setPreferences(l *loaded, values map[string]jsontext.Value) error {
	for name := range values {
		i := slices.IndexFunc(l.m.Preferences, func(p plugin.Preference) bool { return p.Name == name })
		if i < 0 || l.m.Preferences[i].Type == plugin.PrefPassword || l.m.Preferences[i].Type == plugin.PrefButton {
			return fmt.Errorf("core: plugin %q cannot set preference %q", l.m.ID, name)
		}
	}
	return e.cfg.Update(func(c *config.Config) {
		if c.Plugins == nil {
			c.Plugins = map[string]config.PluginConfig{}
		}
		pc := c.Plugins[l.m.ID]
		if pc.Preferences == nil {
			pc.Preferences = map[string]jsontext.Value{}
		}
		for k, v := range values {
			pc.Preferences[k] = slices.Clone(v)
		}
		c.Plugins[l.m.ID] = pc
	})
}

// stringValues turns ActionDo.Values into ViewRequest.Arguments: JSON
// strings are decoded, other values kept as JSON text.
func stringValues(values map[string]jsontext.Value) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			s = string(v)
		}
		out[k] = s
	}
	return out
}

// stringValue is the JSON string value of s.
func stringValue(s string) jsontext.Value {
	b, _ := json.Marshal(s)
	return b
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

// guard calls into a plugin, turning a panic into an error so a faulty
// plugin cannot take the launcher down. The stack is logged.
func guard[T any](e *Engine, pluginID string, fn func() (T, error)) (v T, err error) {
	if l, ok := e.byID[pluginID]; ok {
		l.calls.enter()
		defer l.calls.leave()
	}
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
