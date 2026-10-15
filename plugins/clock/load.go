package clock

import (
	"sync"
	"time"
)

// Loading and releasing the city index (docs/world-clock.md §10.5). The
// index is built on first use: the first prefixed root search, the World
// Clock view or an action. It is released once the launcher has been hidden
// for idleRelease, unless a World Clock view is open; the next use builds it
// again.

// idleRelease is how long the launcher stays hidden before the index is
// released.
const idleRelease = 10 * time.Minute

// indexCache holds the index of the embedded cities for the process: it is
// read-only once built, so every plugin instance (tests and glctl create
// many) shares it. Plugins count as users while they hold it; it is dropped
// when the last user releases it after being idle.
type indexCache struct {
	load func() (*index, error)

	mu      sync.Mutex
	idx     *index
	err     error
	loading chan struct{} // closed when the running load ends
	gen     int           // incremented when the index is dropped
	users   int
}

var sharedCache = &indexCache{load: loadIndex}

// get returns the index, building it (or waiting for the running build)
// when needed.
func (c *indexCache) get() (*index, error) {
	c.mu.Lock()
	for c.idx == nil && c.err == nil && c.loading != nil {
		ch := c.loading
		c.mu.Unlock()
		<-ch
		c.mu.Lock()
	}
	if c.idx != nil || c.err != nil {
		idx, err := c.idx, c.err
		c.mu.Unlock()
		return idx, err
	}
	ch, gen := make(chan struct{}), c.gen
	c.loading = ch
	c.mu.Unlock()
	var (
		idx *index
		err error
	)
	defer func() { // also when load panics: the next get tries again
		c.mu.Lock()
		if c.gen == gen {
			c.idx, c.err = idx, err
		}
		c.loading = nil
		c.mu.Unlock()
		close(ch)
	}()
	idx, err = c.load()
	return idx, err
}

// acquire counts a user.
func (c *indexCache) acquire() {
	c.mu.Lock()
	c.users++
	c.mu.Unlock()
}

// release ends a use. When it was the last one and drop is set, the index
// is dropped: memory goes back once the searches using it are done.
func (c *indexCache) release(drop bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.users = max(c.users-1, 0)
	if c.users == 0 && drop {
		c.idx, c.err = nil, nil
		c.gen++
	}
}

// loadedNow reports whether the index is built (for tests).
func (c *indexCache) loadedNow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.idx != nil
}

// keepOnClose keeps the shared index when the last plugin closes; tests set
// it so that each test does not build the index again.
var keepOnClose = false

// use marks the index as used by p and, unless it is loaded or being
// loaded, starts loading it in the background (with Options.Sync the caller
// loads it). Used while the launcher is hidden, it is released again later.
func (p *Plugin) use() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if !p.held {
		p.held = true
		p.cache.acquire()
	}
	if p.hidden && p.idle == nil {
		p.armIdleLocked()
	}
	if p.loaded.Load() || p.loading || p.opts.Sync {
		return
	}
	p.loading = true
	gen := p.gen
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			p.mu.Lock()
			p.loading = false
			p.mu.Unlock()
			return
		}
		_, err := p.index()
		p.mu.Lock()
		p.loading = false
		current := p.gen == gen
		if err == nil && current {
			p.loaded.Store(true)
		}
		p.mu.Unlock()
		if err != nil {
			p.h.Logger().Error("clock: loading cities", "err", err)
			return
		}
		if current && p.missed.Swap(false) {
			// A root search made while the cities were loading may have
			// been dropped by the host's time limit: ask for it again.
			p.h.Invalidate("")
		}
	})
}

// windowHidden stops the refresh loops and starts the idle timer.
func (p *Plugin) windowHidden() {
	p.stopAll()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hidden = true
	if p.held && p.idle == nil {
		p.armIdleLocked()
	}
}

// windowShown stops the idle timer.
func (p *Plugin) windowShown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hidden = false
	p.disarmIdleLocked()
}

// armIdleLocked starts the idle timer. p.mu must be held.
func (p *Plugin) armIdleLocked() {
	if p.closed {
		return
	}
	stop := make(chan struct{})
	p.idle = stop
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		select {
		case <-stop:
			return
		case <-p.opts.IdleAfter(idleRelease):
		}
		p.idleTimeout(stop)
	})
}

// disarmIdleLocked stops the idle timer. p.mu must be held.
func (p *Plugin) disarmIdleLocked() {
	if p.idle != nil {
		close(p.idle)
		p.idle = nil
	}
}

// idleTimeout releases the index when the launcher is still hidden and no
// World Clock view is open (ViewClosed arms the timer again).
func (p *Plugin) idleTimeout(stop chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idle != stop {
		return
	}
	p.idle = nil
	if !p.hidden || len(p.open) > 0 || !p.held {
		return
	}
	p.held = false
	p.gen++
	p.loaded.Store(false)
	p.cache.release(true)
}

// releaseOnClose ends p's use of the index. p.mu must be held.
func (p *Plugin) releaseOnCloseLocked() {
	p.disarmIdleLocked()
	if p.held {
		p.held = false
		p.cache.release(!p.opts.Sync && !keepOnClose)
	}
}
