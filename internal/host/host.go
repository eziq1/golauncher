package host

import (
	"encoding/json/jsontext"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/store"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Services are the shared host services that per-plugin Hosts are built on.
type Services struct {
	Log       *slog.Logger
	Now       func() time.Time
	DB        *store.DB
	Bus       *Bus
	I18n      *i18n.Manager
	Clipboard plugin.Clipboard
	// Secrets holds the values of password preferences; nil keeps them in
	// memory only.
	Secrets Secrets
	// OnInvalidate and OnRender receive Host.Invalidate and Host.Render calls
	// (set by the engine; nil drops them).
	OnInvalidate func(pluginID, viewID string)
	OnRender     func(pluginID, instance string, v plugin.View)
	// OnToast receives Host.Toast calls (set by the engine; nil drops them).
	OnToast func(pluginID string, t plugin.Toast)
	// Tasks runs Host.RunProcess and Host.RunTask (internal/tasks); nil
	// fails them.
	Tasks TaskRunner
	// DataRoot and CacheRoot contain one subdirectory per plugin.
	DataRoot  string
	CacheRoot string
}

// Host is the plugin.Host given to one Init of one plugin. The engine
// revokes it (Revoke) when that run of the plugin ends: after Close, or
// after a failed Init.
type Host struct {
	s     *Services
	id    string
	prefs *Prefs
	data  string
	cache string

	revoked  atomic.Bool
	subMu    sync.Mutex
	nextSub  int
	subs     map[int]func() // the Bus subscriptions made through Events
	handling int            // event handlers running
	idle     *sync.Cond     // on subMu: handling dropped to 0
}

var _ plugin.Host = (*Host)(nil)

// NewHost returns the Host for the plugin described by m, creating its data
// and cache directories.
func (s *Services) NewHost(m plugin.Manifest, prefs map[string]jsontext.Value) (*Host, error) {
	h := &Host{
		s:     s,
		id:    m.ID,
		prefs: NewPrefs(m.Preferences, prefs),
		data:  filepath.Join(s.DataRoot, m.ID),
		cache: filepath.Join(s.CacheRoot, m.ID),
		subs:  map[int]func(){},
	}
	h.idle = sync.NewCond(&h.subMu)
	if s.Secrets != nil {
		h.prefs = newSecretPrefs(m.ID, m.Preferences, prefs, s.Secrets, s.Log)
	}
	for _, d := range []string{h.data, h.cache} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// Prefs returns the plugin's preferences, so the host can update them when
// the configuration changes.
func (h *Host) Prefs() *Prefs { return h.prefs }

func (h *Host) Logger() *slog.Logger            { return h.s.Log.With("plugin", h.id) }
func (h *Host) Now() time.Time                  { return h.s.Now() }
func (h *Host) Storage() plugin.KV              { return h.s.DB.KV(h.id) }
func (h *Host) Preferences() plugin.Preferences { return h.prefs }
func (h *Host) Clipboard() plugin.Clipboard     { return h.s.Clipboard }
func (h *Host) Events() plugin.Events           { return hostEvents{h} }
func (h *Host) I18n() plugin.Localizer          { return h.s.I18n.Localizer(h.id) }
func (h *Host) DataDir() string                 { return h.data }
func (h *Host) CacheDir() string                { return h.cache }

// Revoke ends this run of the plugin (docs/plugin-guide.md "Lifecycle"):
// the event subscriptions made through Events are removed, and Invalidate,
// Render, Toast, RunProcess and RunTask are dropped from now on. Go still
// runs functions, so that a plugin waiting for its own goroutines in Close
// cannot hang. Revoke is idempotent.
func (h *Host) Revoke() {
	h.subMu.Lock()
	if h.revoked.Swap(true) {
		h.subMu.Unlock()
		return
	}
	subs := h.subs
	h.subs = map[int]func(){}
	h.subMu.Unlock()
	for _, unsub := range subs {
		unsub()
	}
}

// Revoked reports whether Revoke was called.
func (h *Host) Revoked() bool { return h.revoked.Load() }

// Drain waits until no event handler subscribed through this Host runs.
// After Revoke none starts any more, so the plugin can be initialized again
// without a handler of the last run still touching it.
func (h *Host) Drain() {
	h.subMu.Lock()
	for h.handling > 0 {
		h.idle.Wait()
	}
	h.subMu.Unlock()
}

// handle runs fn, an event handler, unless the Host is revoked.
func (h *Host) handle(fn func()) {
	h.subMu.Lock()
	if h.revoked.Load() {
		h.subMu.Unlock()
		return
	}
	h.handling++
	h.subMu.Unlock()
	defer func() {
		h.subMu.Lock()
		if h.handling--; h.handling == 0 {
			h.idle.Broadcast()
		}
		h.subMu.Unlock()
	}()
	fn()
}

// hostEvents is the plugin.Events of one Host: it records the subscriptions
// so that Revoke can remove those the plugin left behind, and drops events
// once the Host is revoked.
type hostEvents struct{ h *Host }

func (e hostEvents) Subscribe(name string, fn func()) (unsubscribe func()) {
	h := e.h
	h.subMu.Lock()
	defer h.subMu.Unlock()
	if h.revoked.Load() {
		h.Logger().Warn("event subscription after the plugin was closed", "event", name)
		return func() {}
	}
	unsub := h.s.Bus.Subscribe(name, func() { h.handle(fn) })
	id := h.nextSub
	h.nextSub++
	h.subs[id] = unsub
	return func() {
		h.subMu.Lock()
		delete(h.subs, id)
		h.subMu.Unlock()
		unsub()
	}
}

// Go implements plugin.Host: fn runs on a new goroutine with panics contained.
func (h *Host) Go(fn func()) { Go(h.Logger(), "plugin "+h.id, fn) }

// Invalidate implements plugin.Host.
func (h *Host) Invalidate(viewID string) {
	if fn := h.s.OnInvalidate; fn != nil && !h.revoked.Load() {
		fn(h.id, viewID)
	}
}

// Render implements plugin.Host.
func (h *Host) Render(instance string, v plugin.View) {
	if fn := h.s.OnRender; fn != nil && !h.revoked.Load() {
		fn(h.id, instance, v)
	}
}

// TaskRunner runs background tasks on behalf of plugins
// (internal/tasks.Manager).
type TaskRunner interface {
	RunProcess(pluginID string, spec plugin.TaskSpec, cmd plugin.ProcessSpec) string
	RunTask(pluginID string, spec plugin.TaskSpec, fn plugin.TaskFunc) string
}

// RunProcess implements plugin.Host.
func (h *Host) RunProcess(spec plugin.TaskSpec, cmd plugin.ProcessSpec) string {
	if h.revoked.Load() {
		h.Logger().Warn("RunProcess after the plugin was closed", "title", spec.Title)
		return ""
	}
	if h.s.Tasks == nil {
		h.Logger().Warn("RunProcess: background tasks are not available")
		return ""
	}
	return h.s.Tasks.RunProcess(h.id, spec, cmd)
}

// RunTask implements plugin.Host.
func (h *Host) RunTask(spec plugin.TaskSpec, fn plugin.TaskFunc) string {
	if h.revoked.Load() {
		h.Logger().Warn("RunTask after the plugin was closed", "title", spec.Title)
		return ""
	}
	if h.s.Tasks == nil {
		h.Logger().Warn("RunTask: background tasks are not available")
		return ""
	}
	return h.s.Tasks.RunTask(h.id, spec, fn)
}

// Toast implements plugin.Host.
func (h *Host) Toast(t plugin.Toast) {
	if fn := h.s.OnToast; fn != nil && !h.revoked.Load() {
		fn(h.id, t)
	}
}
