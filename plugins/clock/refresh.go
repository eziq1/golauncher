package clock

import (
	"sync"

	"github.com/eziq1/golauncher/pkg/plugin/bg"
)

// Refresh loops (docs/world-clock.md §3). The root search is refreshed by
// notifying the host (Host.Invalidate), which pulls RootItems again; an open
// World Clock view gets new snapshots pushed (Host.Render). Both tick at
// whole seconds of Host.Now and run on Host.Go.

const (
	// maxMisses stops the root refresh after this many ticks in a row
	// without a pull: the host no longer shows the root search (a view is
	// open, the text was cleared) or no longer asks.
	maxMisses = 2
	// tickSlack is waited past each whole second so a timer firing a little
	// early does not show the previous second again.
	tickSlack = bg.TickSlack
)

// rootLoop is the refresh of the root search.
type rootLoop struct {
	stop   chan struct{}
	pulled bool // RootItems ran since the last tick
	misses int  // ticks in a row without a pull
}

// viewLoop pushes snapshots to one open World Clock view.
type viewLoop struct {
	stop chan struct{}

	mu    sync.Mutex // held while rendering
	query string     // the view's search text
}

// rootPulled records a prefixed RootItems call and starts the root refresh
// if it is not running.
func (p *Plugin) rootPulled() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if p.root != nil {
		p.root.pulled = true
		return
	}
	rl := &rootLoop{stop: make(chan struct{}), pulled: true}
	p.root = rl
	p.loop(rl.stop, func() bool { return p.rootTick(rl) })
}

// rootTick asks the host to pull the root results again, unless they were
// not pulled for maxMisses ticks.
func (p *Plugin) rootTick(rl *rootLoop) bool {
	p.mu.Lock()
	if p.root != rl {
		p.mu.Unlock()
		return false
	}
	if rl.pulled {
		rl.misses = 0
	} else {
		rl.misses++
	}
	rl.pulled = false
	if rl.misses >= maxMisses {
		p.root = nil
		p.mu.Unlock()
		return false
	}
	p.mu.Unlock()
	p.h.Invalidate("")
	return true
}

// stopRoot stops the root refresh.
func (p *Plugin) stopRoot() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopRootLocked()
}

func (p *Plugin) stopRootLocked() {
	if p.root != nil {
		close(p.root.stop)
		p.root = nil
	}
}

// viewPulled returns the loop of view instance, starting it if needed, or
// nil when the plugin is closed.
func (p *Plugin) viewPulled(instance string) *viewLoop {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.open[instance] = true
	if vl, ok := p.views[instance]; ok {
		return vl
	}
	vl := &viewLoop{stop: make(chan struct{})}
	p.views[instance] = vl
	p.loop(vl.stop, func() bool { return p.viewTick(instance, vl) })
	return vl
}

// viewTick pushes a new snapshot of the view.
func (p *Plugin) viewTick(instance string, vl *viewLoop) bool {
	p.mu.Lock()
	open := p.views[instance] == vl
	p.mu.Unlock()
	if !open {
		return false
	}
	idx, err := p.index()
	if err != nil {
		return false
	}
	vl.mu.Lock()
	defer vl.mu.Unlock()
	p.h.Render(instance, p.worldView(idx, vl.query))
	return true
}

// ViewClosed implements plugin.ViewCloser: it stops the pushes to instance.
// When the launcher is hidden and no view is left open, the idle release of
// the city index can start.
func (p *Plugin) ViewClosed(instance string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if vl, ok := p.views[instance]; ok {
		close(vl.stop)
		delete(p.views, instance)
	}
	delete(p.open, instance)
	if p.hidden && p.held && p.idle == nil && len(p.open) == 0 {
		p.armIdleLocked()
	}
}

// stopAll stops every refresh loop: the launcher was hidden or the plugin
// closes. Views still open when the launcher is shown again are pulled
// again (which restarts their loop) or closed.
func (p *Plugin) stopAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopRootLocked()
	for id, vl := range p.views {
		close(vl.stop)
		delete(p.views, id)
	}
}

// loop runs tick on a background goroutine at every whole second until stop
// is closed or tick returns false. p.mu must be held.
func (p *Plugin) loop(stop <-chan struct{}, tick func() bool) {
	p.loops.Start(stop, tick)
}
