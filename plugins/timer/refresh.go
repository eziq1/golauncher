package timer

import (
	"sync"

	"github.com/eziq1/golauncher/pkg/plugin/bg"
)

// Live updates of what is shown (docs/timers.md §1, as in the world clock):
// while a prefixed root search shows, the plugin calls Host.Invalidate once
// a second; an open Timers view gets a new snapshot pushed with Host.Render
// once a second. Both stop when the launcher hides; the root one also when
// the text no longer has a prefix or the root is not pulled for two ticks.

const (
	// maxMisses stops the root refresh after this many ticks in a row
	// without a pull.
	maxMisses = 2
	// tickSlack is waited past each whole second so that a timer firing a
	// little early does not show the previous second again.
	tickSlack = bg.TickSlack
)

type rootLoop struct {
	stop   chan struct{}
	pulled bool
	misses int
}

type viewLoop struct {
	stop chan struct{}

	mu    sync.Mutex // held while rendering
	query string
}

// rootPulled records a prefixed RootItems call and starts the root refresh
// if it is not running.
func (p *Plugin) rootPulled() {
	p.lm.Lock()
	defer p.lm.Unlock()
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

func (p *Plugin) rootTick(rl *rootLoop) bool {
	p.lm.Lock()
	if p.root != rl {
		p.lm.Unlock()
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
		p.lm.Unlock()
		return false
	}
	p.lm.Unlock()
	p.h.Invalidate("")
	return true
}

func (p *Plugin) stopRoot() {
	p.lm.Lock()
	defer p.lm.Unlock()
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
	p.lm.Lock()
	defer p.lm.Unlock()
	if p.closed {
		return nil
	}
	if vl, ok := p.views[instance]; ok {
		return vl
	}
	vl := &viewLoop{stop: make(chan struct{})}
	p.views[instance] = vl
	p.loop(vl.stop, func() bool { return p.viewTick(instance, vl) })
	return vl
}

func (p *Plugin) viewTick(instance string, vl *viewLoop) bool {
	p.lm.Lock()
	open := p.views[instance] == vl
	p.lm.Unlock()
	if !open {
		return false
	}
	vl.mu.Lock()
	defer vl.mu.Unlock()
	p.h.Render(instance, p.timersView(vl.query))
	return true
}

// ViewClosed implements plugin.ViewCloser: it stops the pushes to instance.
func (p *Plugin) ViewClosed(instance string) {
	p.lm.Lock()
	defer p.lm.Unlock()
	if vl, ok := p.views[instance]; ok {
		close(vl.stop)
		delete(p.views, instance)
	}
}

// stopAll stops every refresh loop (the launcher was hidden, or Close).
func (p *Plugin) stopAll() {
	p.lm.Lock()
	defer p.lm.Unlock()
	p.stopRootLocked()
	for id, vl := range p.views {
		close(vl.stop)
		delete(p.views, id)
	}
}

// loop runs tick on Host.Go just after every whole second until stop is
// closed or tick returns false. p.lm must be held.
func (p *Plugin) loop(stop <-chan struct{}, tick func() bool) {
	p.loops.Start(stop, tick)
}
