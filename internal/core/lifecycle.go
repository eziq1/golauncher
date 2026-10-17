package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// The plugin lifecycle (docs/plugin-guide.md "Lifecycle", docs/design.md,
// R88): a plugin turned off in the configuration does not run. The engine
// initializes only the plugins that are on; turning one off closes it, and
// turning it on again initializes it again, with a new Host. A plugin whose
// Init fails (or panics) is closed as well, and the Host of every run is
// revoked when the run ends, which removes the event subscriptions the
// plugin left behind and drops its late Invalidate, Render, Toast,
// RunProcess and RunTask calls.

// pluginState is where a registered plugin is in its lifecycle.
type pluginState int

const (
	stateOff    pluginState = iota // not initialized: turned off, or closed
	stateOn                        // initialized and not closed
	stateFailed                    // its last Init failed; reported as unavailable
)

// closeTimeout bounds a Close while the launcher runs (turning a plugin off,
// or after a failed Init). A plugin that takes longer is left to finish on
// its own: its Host is revoked meanwhile and it is not initialized again
// before its Close has returned. Engine.Close, at quit, waits.
var closeTimeout = 10 * time.Second

// lookup returns plugin id when it is on.
func (e *Engine) lookup(id string) (*loaded, bool) {
	l, ok := e.byID[id]
	if !ok {
		return nil, false
	}
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return l, l.state == stateOn
}

// running returns plugin id when it is on, else an error saying why not.
func (e *Engine) running(id string) (*loaded, error) {
	l, ok := e.byID[id]
	if !ok {
		return nil, fmt.Errorf("core: unknown plugin %q", id)
	}
	e.stateMu.RLock()
	state, reason := l.state, l.reason
	e.stateMu.RUnlock()
	switch state {
	case stateOn:
		return l, nil
	case stateFailed:
		return nil, fmt.Errorf("core: plugin %q is unavailable: %s", id, reason)
	default:
		return nil, fmt.Errorf("core: plugin %q is turned off", id)
	}
}

// registered returns plugin id unless it is unknown or unavailable: its
// manifest describes commands that can be on.
func (e *Engine) registered(id string) (*loaded, bool) {
	l, ok := e.byID[id]
	if !ok {
		return nil, false
	}
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return l, l.state != stateFailed
}

// stateOf returns l's state and, when it failed, the reason.
func (e *Engine) stateOf(l *loaded) (pluginState, string) {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return l.state, l.reason
}

// hostOf returns the Host of l's current run, nil when it is not on.
func (e *Engine) hostOf(l *loaded) *host.Host {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return l.host
}

// start initializes l with a new Host. A failed Init is not an error: the
// plugin is closed and marked unavailable. Only a Host that cannot be
// created is (New fails then). e.lifeMu must be held.
func (e *Engine) start(ctx context.Context, l *loaded, cfg config.Config) error {
	id := l.m.ID
	if l.closing != nil {
		select {
		case <-l.closing:
		case <-time.After(closeTimeout):
			// Its last run has not ended (a Close or a call that hangs):
			// Init now would race with it. It is started once that run
			// ends (closeRun).
			e.log.Warn("plugin not started: its last run has not ended", "plugin", id)
			return nil
		}
	}
	h, err := e.svc.NewHost(l.m, cfg.Plugin(id).Preferences)
	if err != nil {
		return err
	}
	if _, err := guard(e, id, func() (struct{}, error) { return struct{}{}, l.p.Init(ctx, h) }); err != nil {
		reason := err.Error()
		if errors.Is(err, plugin.ErrUnavailable) {
			e.log.Info("plugin unavailable", "plugin", id, "reason", err)
			reason = unavailableReason(err)
		} else {
			e.log.Error("plugin init failed", "plugin", id, "err", err)
		}
		// The contract: a failed Init is followed by Close, so that what Init
		// set up before failing is released; the revoked Host drops the rest.
		if err := e.closeRun(l, h, closeTimeout); err != nil {
			e.log.Warn("closing a plugin whose Init failed", "plugin", id, "err", err)
		}
		e.stateMu.Lock()
		l.state, l.reason = stateFailed, reason
		e.stateMu.Unlock()
		return nil
	}
	e.stateMu.Lock()
	l.state, l.host, l.reason = stateOn, h, ""
	e.initOrder = append(e.initOrder, l)
	e.stateMu.Unlock()
	return nil
}

// stop closes l, which is on, within d (0: no limit). From now on the
// engine calls nothing of it. e.lifeMu must be held.
func (e *Engine) stop(l *loaded, d time.Duration) error {
	e.stateMu.Lock()
	h := l.host
	l.state, l.host = stateOff, nil
	e.initOrder = slices.DeleteFunc(e.initOrder, func(o *loaded) bool { return o == l })
	e.stateMu.Unlock()
	return e.closeRun(l, h, d)
}

// closeRun calls l's Close, contained by guard, then revokes h, the Host of
// the run that ends. It gives up waiting after d (0: no limit); the Host is
// revoked then too. l.closing is closed once the run has fully ended: Close
// returned, and the event handlers of h and the engine's calls into l (one
// it gave up on may still run) are done. e.lifeMu must be held.
func (e *Engine) closeRun(l *loaded, h *host.Host, d time.Duration) error {
	id := l.m.ID
	closing := make(chan struct{})
	l.closing = closing
	result := make(chan error, 1)
	go func() {
		_, err := guard(e, id, func() (struct{}, error) { return struct{}{}, l.p.Close() })
		h.Revoke()
		result <- err
		h.Drain()
		l.calls.wait()
		close(closing)
		// A start skipped meanwhile (start) happens now.
		e.syncLater()
	}()
	if d <= 0 {
		return <-result
	}
	select {
	case err := <-result:
		return err
	case <-time.After(d):
		h.Revoke()
		e.log.Error("plugin did not close in time", "plugin", id, "after", d)
		return fmt.Errorf("%w: %s closing (%v)", ErrPluginTimeout, id, d)
	}
}

// Close closes the plugins that are on, in the reverse order of their Init,
// and keeps plugins from starting afterwards. It waits for each Close.
func (e *Engine) Close() error {
	e.lifeMu.Lock()
	defer e.lifeMu.Unlock()
	e.stateMu.Lock()
	e.closed = true
	order := slices.Clone(e.initOrder)
	e.stateMu.Unlock()
	var errs []error
	for _, l := range slices.Backward(order) {
		errs = append(errs, e.stop(l, 0))
	}
	// The pasted images of the root search go now (rootfiles.go).
	for _, c := range e.subscribers() {
		c.clearFiles()
	}
	e.removeTemps()
	return errors.Join(errs...)
}

// syncState counts the lifecycle syncs waiting or running, for Wait.
type syncState struct {
	mu      sync.Mutex
	idle    *sync.Cond
	pending int
	queued  bool // a sync is waiting to start: one more is not needed
}

// syncLater applies the configuration's plugin states in the background
// (SyncPlugins). Requests arriving while one waits to start are coalesced:
// it reads the configuration when it starts.
func (e *Engine) syncLater() {
	s := &e.syncs
	s.mu.Lock()
	if s.queued {
		s.mu.Unlock()
		return
	}
	s.queued, s.pending = true, s.pending+1
	s.mu.Unlock()
	host.Go(e.log, "plugin lifecycle", func() {
		defer func() {
			s.mu.Lock()
			s.pending--
			s.idle.Broadcast()
			s.mu.Unlock()
		}()
		e.syncPlugins(context.Background(), func() {
			s.mu.Lock()
			s.queued = false
			s.mu.Unlock()
		})
	})
}

// WaitPlugins blocks until the plugins have been turned on and off as the
// configuration says (the background work started by its changes).
func (e *Engine) WaitPlugins() {
	s := &e.syncs
	s.mu.Lock()
	for s.pending > 0 {
		s.idle.Wait()
	}
	s.mu.Unlock()
}

// SyncPlugins turns plugins on and off as the configuration says, now: a
// plugin turned off is closed, one turned on is initialized (again), and one
// that failed and is now off forgets the failure, so that turning it on
// tries again. The controllers leave the views of plugins turned off and
// refresh the root search. The engine does this by itself after every
// configuration change; SyncPlugins is for callers that need it done.
func (e *Engine) SyncPlugins(ctx context.Context) { e.syncPlugins(ctx, nil) }

// syncPlugins is SyncPlugins; started is called once e.lifeMu is held,
// before the configuration is read.
func (e *Engine) syncPlugins(ctx context.Context, started func()) {
	e.lifeMu.Lock()
	if started != nil {
		started()
	}
	e.stateMu.RLock()
	closed := e.closed
	e.stateMu.RUnlock()
	if closed {
		e.lifeMu.Unlock()
		return
	}
	cfg := e.cfg.Get()
	var off []string
	changed := false
	for _, l := range e.plugins {
		want := !cfg.Plugin(l.m.ID).Disabled
		state, _ := e.stateOf(l)
		switch {
		case want && state == stateOff:
			if err := e.start(ctx, l, cfg); err != nil {
				e.log.Error("plugin not started", "plugin", l.m.ID, "err", err)
				e.stateMu.Lock()
				l.state, l.reason = stateFailed, err.Error()
				e.stateMu.Unlock()
			}
			changed = true
		case !want && state == stateOn:
			if err := e.stop(l, closeTimeout); err != nil {
				e.log.Warn("closing a plugin turned off", "plugin", l.m.ID, "err", err)
			}
			off = append(off, l.m.ID)
			changed = true
		case !want && state == stateFailed:
			e.stateMu.Lock()
			l.state, l.reason = stateOff, ""
			e.stateMu.Unlock()
			changed = true
		case state == stateOn:
			// A run started while the configuration changed may have missed
			// the new preferences (the change listener sets those of the
			// plugins that were on).
			if h := e.hostOf(l); h != nil {
				h.Prefs().SetValues(cfg.Plugin(l.m.ID).Preferences)
			}
		}
	}
	e.lifeMu.Unlock()
	if changed {
		for _, c := range e.subscribers() {
			c.pluginsChanged(off)
		}
	}
}
