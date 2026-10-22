// Package bg has small helpers for the background work of Go plugins, all
// started through plugin.Host.Go (which contains panics):
//
//   - Group starts work until the plugin closes, then waits for it.
//   - Refresher runs one refresh at a time; a request during a run causes
//     exactly one more run after it.
//   - Ready is closed once the first result is known, and waits for it with
//     a time limit.
//   - SecondLoops runs loops that tick just after every whole second of
//     Host.Now, for live clocks and countdowns.
//   - Lazy loads a value on first use and drops it when it has not been used
//     for a while, for large indexes that are read from a cache.
//
// They hold no plugin state: what is refreshed, when it is stale and what
// is invalidated stay in each plugin.
package bg

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Group tracks the background work of a plugin. The zero Group is ready.
type Group struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{} // closed by Close
	wg     sync.WaitGroup
}

// Done returns a channel that is closed when Close is called, for work that
// waits and must end with the plugin.
func (g *Group) Done() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.doneLocked()
}

func (g *Group) doneLocked() chan struct{} {
	if g.done == nil {
		g.done = make(chan struct{})
	}
	return g.done
}

// Go runs fn through h.Go unless the group is closed. It reports whether fn
// was started.
func (g *Group) Go(h plugin.Host, fn func()) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	h.Go(func() {
		defer g.wg.Done()
		fn()
	})
	return true
}

// Closed reports whether Close was called.
func (g *Group) Closed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closed
}

// Close stops Go from starting work. It reports whether this was the first
// call; it does not wait (see Wait), so the caller can first cancel what
// the work waits for.
func (g *Group) Close() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	first := !g.closed
	if first {
		g.closed = true
		close(g.doneLocked())
	}
	return first
}

// Wait waits for the work started with Go.
func (g *Group) Wait() { g.wg.Wait() }

// Refresher coalesces refreshes: one runs at a time, and requests while it
// runs cause one more run after it. The zero Refresher is ready.
type Refresher struct {
	mu      sync.Mutex
	running bool
	again   bool
}

// Request runs fn on g (through h.Go) unless a run is under way, in which
// case fn's run is followed by one more. Further runs stop once g is
// closed. The fn of the request that started the runs is the one run again.
func (r *Refresher) Request(g *Group, h plugin.Host, fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		r.again = true
		return
	}
	r.running = g.Go(h, func() {
		for {
			fn()
			r.mu.Lock()
			if !r.again || g.Closed() {
				r.running, r.again = false, false
				r.mu.Unlock()
				return
			}
			r.again = false
			r.mu.Unlock()
		}
	})
}

// Running reports whether a run is under way.
func (r *Refresher) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Ready marks that a first result is known. The zero Ready is not done.
type Ready struct {
	mu   sync.Mutex
	ch   chan struct{}
	done bool
}

func (r *Ready) chanLocked() chan struct{} {
	if r.ch == nil {
		r.ch = make(chan struct{})
	}
	return r.ch
}

// Done marks the result as known and wakes the waiters. Later calls do
// nothing.
func (r *Ready) Done() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.done {
		r.done = true
		close(r.chanLocked())
	}
}

// IsDone reports whether Done was called.
func (r *Ready) IsDone() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.done
}

// Wait waits until Done is called, ctx ends or d passes. It reports whether
// Done was called.
func (r *Ready) Wait(ctx context.Context, d time.Duration) bool {
	r.mu.Lock()
	ch := r.chanLocked()
	r.mu.Unlock()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-ctx.Done():
	case <-timer.C:
	}
	return r.IsDone()
}

// TickSlack is waited past each whole second so that a timer firing a
// little early does not show the previous second again.
const TickSlack = 10 * time.Millisecond

// UntilNextSecond is the time from now to just after the next whole second.
func UntilNextSecond(now time.Time) time.Duration {
	return now.Truncate(time.Second).Add(time.Second).Sub(now) + TickSlack
}

// SecondLoops runs loops that tick just after every whole second of
// Host.Now. Set the fields before the first Start.
type SecondLoops struct {
	Host plugin.Host
	// After waits for a duration (default time.After; tests deliver ticks).
	After func(time.Duration) <-chan time.Time
	// WG, if set, counts each running loop, so that the plugin's Close can
	// wait for them with its other work.
	WG *sync.WaitGroup

	running atomic.Int32
}

// Start runs tick on Host.Go just after every whole second until stop is
// closed or tick returns false. A stop closed while a tick is due wins.
func (s *SecondLoops) Start(stop <-chan struct{}, tick func() bool) {
	after := s.After
	if after == nil {
		after = time.After
	}
	if s.WG != nil {
		s.WG.Add(1)
	}
	s.running.Add(1)
	s.Host.Go(func() {
		if s.WG != nil {
			defer s.WG.Done()
		}
		defer s.running.Add(-1)
		for {
			select {
			case <-stop:
				return
			case <-after(UntilNextSecond(s.Host.Now())):
			}
			select {
			case <-stop:
				return
			default:
			}
			if !tick() {
				return
			}
		}
	})
}

// Running is the number of loops running.
func (s *SecondLoops) Running() int { return int(s.running.Load()) }
