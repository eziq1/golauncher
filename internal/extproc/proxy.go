package extproc

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os/exec"
	"sync"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/jsonrpc"
	"github.com/eziq1/golauncher/pkg/plugin/protocol"
)

var lookPath = exec.LookPath

// Options tune the process lifecycle. Zero fields take the defaults of
// docs/m5-plugin-platform.md §5.3.
type Options struct {
	// InitTimeout bounds starting the process and initialize (5s).
	InitTimeout time.Duration
	// IdleTimeout stops a process that had no calls and no open views for
	// this long (5m).
	IdleTimeout time.Duration
	// ShutdownGrace is how long a stopping process may take before it is
	// killed (2s).
	ShutdownGrace time.Duration
	// BackoffMin and BackoffMax bound the delay before restarting a
	// crashed process; it doubles with every consecutive crash (1s, 30s).
	BackoffMin, BackoffMax time.Duration
	// BreakerCrashes crashes within BreakerWindow turn the plugin off until
	// Reset (5 in 2m).
	BreakerCrashes int
	BreakerWindow  time.Duration
	// HungTimeouts consecutive timed-out calls make the process count as
	// hung: it is killed, which counts as a crash (3).
	HungTimeouts int
	// Eager starts the process in Init and waits for it, instead of on the
	// first call. glctl uses it so results do not depend on start-up time.
	Eager bool
	// HostVersion is sent in initialize.
	HostVersion string
}

func (o Options) withDefaults() Options {
	o.InitTimeout = cmp.Or(o.InitTimeout, 5*time.Second)
	o.IdleTimeout = cmp.Or(o.IdleTimeout, 5*time.Minute)
	o.ShutdownGrace = cmp.Or(o.ShutdownGrace, 2*time.Second)
	o.BackoffMin = cmp.Or(o.BackoffMin, time.Second)
	o.BackoffMax = cmp.Or(o.BackoffMax, 30*time.Second)
	o.BreakerCrashes = cmp.Or(o.BreakerCrashes, 5)
	o.BreakerWindow = cmp.Or(o.BreakerWindow, 2*time.Minute)
	o.HungTimeouts = cmp.Or(o.HungTimeouts, 3)
	o.HostVersion = cmp.Or(o.HostVersion, "dev")
	return o
}

// Process states reported by ProcessState.
const (
	StateStopped  = "stopped"  // not running; starts on the next call
	StateRunning  = "running"  // running and initialized
	StateCrashed  = "crashed"  // exited unexpectedly; restarts after a backoff
	StateDisabled = "disabled" // crashed too often; off until Reset
	// StateUnavailable: the plugin cannot run here (missing program,
	// unsupported protocol, or its Init reported it unavailable).
	StateUnavailable = "unavailable"
)

// Errors returned by calls, wrapped in *CallError with a localized message.
var (
	ErrCrashed     = errors.New("extproc: plugin crashed")
	ErrDisabled    = errors.New("extproc: plugin disabled after repeated crashes")
	ErrUnavailable = errors.New("extproc: plugin unavailable")
	ErrClosed      = errors.New("extproc: plugin closed")
)

// CallError is a localized error for the user; errors.Is matches its Kind.
type CallError struct {
	Kind error
	Msg  string
}

func (e *CallError) Error() string { return e.Msg }
func (e *CallError) Unwrap() error { return e.Kind }

// Proxy is an external plugin as seen by the engine. It implements
// plugin.Plugin and all optional plugin interfaces, forwarding them over
// JSON-RPC to the plugin process. It is safe for concurrent use.
type Proxy struct {
	dir     string
	man     Manifest
	opts    Options
	locales fs.FS
	log     *slog.Logger

	h        plugin.Host
	hostSubs []func()

	mu        sync.Mutex
	proc      *process
	starting  chan struct{} // closed when the current start attempt ends
	caps      protocol.Capabilities
	capsKnown bool
	state     string
	reason    string
	crashes   []time.Time // within the breaker window
	crashRun  int         // consecutive crashes, for the backoff
	nextStart time.Time
	timeouts  int // consecutive timed-out calls
	inflight  int
	lastUse   time.Time
	views     map[string]bool  // open view instances
	renderVer map[string]int64 // last accepted ui/render version per instance
	lastPrefs string
	closed    bool
	// fixed is set when Init found the plugin unavailable (manifest
	// problems); Reset does not clear that.
	fixed bool
}

var (
	_ plugin.Plugin                    = (*Proxy)(nil)
	_ plugin.RootProvider              = (*Proxy)(nil)
	_ plugin.ViewProvider              = (*Proxy)(nil)
	_ plugin.ActionHandler             = (*Proxy)(nil)
	_ plugin.LocaleProvider            = (*Proxy)(nil)
	_ plugin.PreferenceOptionsProvider = (*Proxy)(nil)
)

// New returns a proxy for the plugin in dir described by m.
func New(dir string, m Manifest, o Options, log *slog.Logger) *Proxy {
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{
		dir: dir, man: m, opts: o.withDefaults(),
		locales: loadLocales(dir, m.ID, log),
		log:     log.With("plugin", m.ID),
		state:   StateStopped,
		views:   map[string]bool{}, renderVer: map[string]int64{},
	}
}

// Dir returns the plugin directory.
func (p *Proxy) Dir() string { return p.dir }

// Manifest implements plugin.Plugin.
func (p *Proxy) Manifest() plugin.Manifest { return p.man.Manifest }

// Locales implements plugin.LocaleProvider with the catalogs in
// <dir>/locales.
func (p *Proxy) Locales() fs.FS { return p.locales }

// t translates a core message.
func (p *Proxy) t(id string, kv ...any) string {
	if p.h == nil {
		return id
	}
	return p.h.I18n().T(id, kv...)
}

// Init implements plugin.Plugin. It does not start the process (unless
// Options.Eager); it reports a plugin that cannot run here as unavailable.
func (p *Proxy) Init(ctx context.Context, h plugin.Host) error {
	p.h = h
	p.log = h.Logger()
	if p.man.Protocol != protocol.Version {
		reason := p.t("extproc.protocol", "version", p.man.Protocol, "supported", protocol.Version)
		p.setUnavailable(reason)
		return fmt.Errorf("%s: %w", reason, plugin.ErrUnavailable)
	}
	if _, err := resolveCommand(p.dir, p.man.Run.Command); err != nil {
		reason := p.t("extproc.commandNotFound", "command", p.man.Run.Command)
		p.setUnavailable(reason)
		return fmt.Errorf("%s: %w", reason, plugin.ErrUnavailable)
	}
	p.mu.Lock()
	p.lastPrefs = p.prefsKey(p.preferences())
	p.mu.Unlock()
	ev := h.Events()
	p.hostSubs = append(p.hostSubs,
		ev.Subscribe(plugin.EventLocaleChanged, func() {
			p.notify(protocol.NotifyLocaleChanged, protocol.LocaleChangedParams{Locale: h.I18n().Locale()})
		}),
		ev.Subscribe(plugin.EventConfigChanged, p.preferencesChanged),
	)
	if p.opts.Eager {
		ctx, cancel := context.WithTimeout(ctx, p.opts.InitTimeout+time.Second)
		defer cancel()
		if _, err := p.ensure(ctx); err != nil {
			p.log.Warn("external plugin did not start", "err", err)
		}
	}
	return nil
}

func (p *Proxy) setUnavailable(reason string) {
	p.mu.Lock()
	p.state, p.reason, p.fixed = StateUnavailable, reason, true
	p.mu.Unlock()
}

// Close implements plugin.Plugin: it stops the process.
func (p *Proxy) Close() error {
	p.mu.Lock()
	p.closed = true
	proc := p.proc
	starting := p.starting
	subs := p.hostSubs
	p.hostSubs = nil
	p.mu.Unlock()
	for _, unsub := range subs {
		unsub()
	}
	if starting != nil {
		<-starting
		p.mu.Lock()
		proc = p.proc
		p.mu.Unlock()
	}
	if proc != nil {
		p.stop(proc)
	}
	return nil
}

// ProcessState returns the process state (one of the State constants) and,
// for crashed, disabled and unavailable plugins, a localized reason.
func (p *Proxy) ProcessState() (state, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state, p.reason
}

// Reset turns a plugin disabled by the crash breaker (or unavailable
// because of what its process reported) back on and forgets past crashes;
// the next call starts it. The settings page's "Turn on again" and the
// engine's ResetPlugin call it.
func (p *Proxy) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.crashes, p.crashRun, p.timeouts = nil, 0, 0
	p.nextStart = time.Time{}
	switch p.state {
	case StateCrashed, StateDisabled:
		p.state, p.reason = StateStopped, ""
	case StateUnavailable:
		if !p.fixed {
			// Reported at start-up by the process, which may now succeed.
			p.state, p.reason = StateStopped, ""
			p.capsKnown = false
		}
	}
}

// ViewClosed tells the plugin that the view instance (ViewRequest.Instance)
// was closed, so it can release its state, and lets an idle process stop.
// The engine calls it when a view frame is popped.
func (p *Proxy) ViewClosed(instance string) {
	if instance == "" {
		return
	}
	p.mu.Lock()
	delete(p.views, instance)
	delete(p.renderVer, instance)
	p.mu.Unlock()
	p.notify(protocol.NotifyViewClosed, protocol.ViewClosedParams{Instance: instance})
}

// RootItems implements plugin.RootProvider.
func (p *Proxy) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	if !p.mayHave(func(c protocol.Capabilities) bool { return c.RootItems }) {
		return nil, nil
	}
	var items []plugin.Item
	err := p.call(ctx, protocol.MethodRootItems, protocol.RootItemsParams{Query: q.Text}, &items)
	return items, err
}

// View implements plugin.ViewProvider.
func (p *Proxy) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	if req.Instance != "" {
		p.mu.Lock()
		p.views[req.Instance] = true
		p.mu.Unlock()
	}
	var v plugin.View
	err := p.call(ctx, protocol.MethodView, protocol.ViewParams{Request: req}, &v)
	return v, err
}

// HandleAction implements plugin.ActionHandler.
func (p *Proxy) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	var r plugin.Result
	err := p.call(ctx, protocol.MethodHandleAction, protocol.HandleActionParams{Action: a}, &r)
	return r, err
}

// PreferenceOptions implements plugin.PreferenceOptionsProvider.
func (p *Proxy) PreferenceOptions(ctx context.Context, name string) ([]plugin.Option, error) {
	var opts []plugin.Option
	err := p.call(ctx, protocol.MethodPreferenceOptions, protocol.PreferenceOptionsParams{Name: name}, &opts)
	return opts, err
}

// mayHave reports whether the plugin may have a capability: true until the
// first initialize told otherwise. It keeps a plugin without root items
// from being started by every keystroke.
func (p *Proxy) mayHave(c func(protocol.Capabilities) bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.capsKnown || c(p.caps)
}

// preferences returns the effective values of the declared preferences.
func (p *Proxy) preferences() map[string]jsontext.Value {
	out := map[string]jsontext.Value{}
	if p.h == nil {
		return out
	}
	prefs := p.h.Preferences()
	for _, d := range p.man.Preferences {
		if d.Type == plugin.PrefButton {
			continue
		}
		if v, ok := prefs.Raw(d.Name); ok {
			out[d.Name] = v
		}
	}
	return out
}

func (p *Proxy) prefsKey(m map[string]jsontext.Value) string {
	b, _ := json.Marshal(m, json.Deterministic(true))
	return string(b)
}

func (p *Proxy) preferencesChanged() {
	prefs := p.preferences()
	key := p.prefsKey(prefs)
	p.mu.Lock()
	changed := key != p.lastPrefs
	p.lastPrefs = key
	p.mu.Unlock()
	if changed {
		p.notify(protocol.NotifyPreferencesChanged, protocol.PreferencesChangedParams{Preferences: prefs})
	}
}

// notify sends a notification to the running process, if any.
func (p *Proxy) notify(method string, params any) {
	p.mu.Lock()
	proc := p.proc
	p.mu.Unlock()
	if proc != nil {
		if err := proc.conn.Notify(method, params); err != nil {
			p.log.Debug("notify", "method", method, "err", err)
		}
	}
}

// ensure returns the running process, starting it if needed and allowed.
func (p *Proxy) ensure(ctx context.Context) (*process, error) {
	for {
		p.mu.Lock()
		switch {
		case p.closed:
			p.mu.Unlock()
			return nil, &CallError{ErrClosed, p.t("extproc.closed")}
		case p.state == StateDisabled:
			defer p.mu.Unlock()
			return nil, &CallError{ErrDisabled, p.reason}
		case p.state == StateUnavailable:
			defer p.mu.Unlock()
			return nil, &CallError{ErrUnavailable, p.reason}
		case p.proc != nil:
			proc := p.proc
			p.mu.Unlock()
			return proc, nil
		case p.starting != nil:
			ch := p.starting
			p.mu.Unlock()
			select {
			case <-ch:
				p.mu.Lock()
				ok := p.proc != nil || p.state == StateDisabled || p.state == StateUnavailable || p.closed
				p.mu.Unlock()
				if !ok {
					return nil, p.crashedErr()
				}
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case time.Now().Before(p.nextStart):
			p.mu.Unlock()
			return nil, p.crashedErr()
		}
		p.starting = make(chan struct{})
		go p.start(p.starting)
		p.mu.Unlock()
	}
}

func (p *Proxy) crashedErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	wait := time.Until(p.nextStart).Round(time.Second)
	if wait > 0 {
		return &CallError{ErrCrashed, p.t("extproc.restarting", "seconds", int(wait/time.Second))}
	}
	return &CallError{ErrCrashed, p.t("extproc.crashed")}
}

// start launches the process; it runs on its own goroutine so a caller
// giving up does not abort the start.
func (p *Proxy) start(done chan struct{}) {
	proc, caps, err := p.launch()
	p.mu.Lock()
	defer func() {
		p.starting = nil
		close(done)
		p.mu.Unlock()
	}()
	if err != nil {
		if ue, ok := errors.AsType[*CallError](err); ok && errors.Is(ue, ErrUnavailable) {
			p.state, p.reason = StateUnavailable, ue.Msg
			p.capsKnown = true
			p.log.Warn("external plugin unavailable", "reason", ue.Msg)
			return
		}
		p.log.Error("external plugin failed to start", "err", err)
		p.recordCrashLocked()
		return
	}
	if p.closed {
		go p.stop(proc)
		return
	}
	p.proc = proc
	p.caps, p.capsKnown = caps, true
	p.state, p.reason = StateRunning, ""
	p.lastUse = time.Now()
	go p.watchIdle(proc)
}

// recordCrashLocked counts a crash and schedules the restart or trips the
// breaker. p.mu is held.
func (p *Proxy) recordCrashLocked() {
	now := time.Now()
	p.crashes = append(p.crashes, now)
	for len(p.crashes) > 0 && now.Sub(p.crashes[0]) > p.opts.BreakerWindow {
		p.crashes = p.crashes[1:]
	}
	p.crashRun++
	p.timeouts = 0
	if len(p.crashes) >= p.opts.BreakerCrashes {
		p.state, p.reason = StateDisabled, p.t("extproc.breaker")
		p.log.Error("external plugin turned off after repeated crashes", "crashes", len(p.crashes))
		return
	}
	backoff := p.opts.BackoffMin << min(p.crashRun-1, 16)
	backoff = min(backoff, p.opts.BackoffMax)
	p.nextStart = now.Add(backoff)
	p.state, p.reason = StateCrashed, p.t("extproc.crashed")
}

// call sends a request to the process, starting it if needed, and keeps
// the hang detection up to date.
func (p *Proxy) call(ctx context.Context, method string, params, result any) error {
	proc, err := p.ensure(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.inflight++
	p.lastUse = time.Now()
	p.mu.Unlock()

	err = proc.conn.Call(ctx, method, params, result)

	p.mu.Lock()
	p.inflight--
	p.lastUse = time.Now()
	hung := false
	_, rpcErr := errors.AsType[*jsonrpc.Error](err)
	switch {
	case err == nil || rpcErr:
		p.timeouts = 0
		p.crashRun = 0
	case errors.Is(err, context.DeadlineExceeded) && p.proc == proc:
		p.timeouts++
		if p.timeouts >= p.opts.HungTimeouts {
			hung = true
			p.timeouts = 0
		}
	case errors.Is(err, jsonrpc.ErrClosed):
		err = &CallError{ErrCrashed, p.t("extproc.crashed")}
	}
	p.mu.Unlock()
	if hung {
		p.log.Error("external plugin hung; killing it", "method", method)
		proc.kill()
	}
	return err
}

// watchIdle stops proc after IdleTimeout without calls or open views.
func (p *Proxy) watchIdle(proc *process) {
	tick := time.NewTicker(min(max(p.opts.IdleTimeout/5, 10*time.Millisecond), 30*time.Second))
	defer tick.Stop()
	for {
		select {
		case <-proc.done:
			return
		case <-tick.C:
			p.mu.Lock()
			idle := p.proc == proc && p.inflight == 0 && len(p.views) == 0 && time.Since(p.lastUse) >= p.opts.IdleTimeout
			p.mu.Unlock()
			if idle {
				p.log.Info("stopping idle external plugin")
				p.stop(proc)
				return
			}
		}
	}
}

// stop shuts proc down gracefully (shutdown, then closing stdin) and kills
// it if it does not exit within ShutdownGrace. It returns after the process
// exited.
func (p *Proxy) stop(proc *process) {
	proc.expected.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), p.opts.ShutdownGrace)
	defer cancel()
	_ = proc.conn.Call(ctx, protocol.MethodShutdown, struct{}{}, nil)
	_ = proc.stdin.Close()
	select {
	case <-proc.done:
	case <-ctx.Done():
		proc.kill()
		<-proc.done
	}
}

// onExit runs after a process exited.
func (p *Proxy) onExit(proc *process, waitErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, unsub := range proc.subs {
		unsub()
	}
	proc.subs = nil
	if p.proc != proc {
		return
	}
	p.proc = nil
	if proc.expected.Load() {
		if p.state == StateRunning {
			p.state, p.reason = StateStopped, ""
		}
		return
	}
	p.log.Error("external plugin exited unexpectedly", "err", waitErr)
	p.recordCrashLocked()
}

// openViews returns the open view instances (for tests).
func (p *Proxy) openViews() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.views)
}
