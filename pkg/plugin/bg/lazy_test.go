package bg

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func (h *host) set(t time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.now = t
}

// lazyEnv is a Lazy[int] whose loads count up, with a fake clock and ticks.
type lazyEnv struct {
	h     *host
	g     *Group
	k     *ticks
	l     *Lazy[int]
	loads atomic.Int32
	late  atomic.Int32
	gate  chan struct{} // if set, each load waits for it
}

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newLazy(t *testing.T) *lazyEnv {
	e := &lazyEnv{h: &host{now: t0}, g: &Group{}, k: &ticks{ch: make(chan time.Time)}}
	e.l = &Lazy[int]{Host: e.h, Group: e.g, After: e.k.after, Idle: 10 * time.Minute,
		Load: func() (int, error) {
			if e.gate != nil {
				<-e.gate
			}
			return int(e.loads.Add(1)), nil
		},
		OnLate: func() { e.late.Add(1) },
	}
	t.Cleanup(func() { e.g.Close(); e.g.Wait() })
	return e
}

func TestLazyLoadsOnce(t *testing.T) {
	e := newLazy(t)
	if e.l.Loaded() || e.loads.Load() != 0 {
		t.Fatal("loaded before use")
	}
	if _, ok := e.l.Peek(); ok {
		t.Fatal("Peek loaded")
	}
	if e.l.Set(5, nil) || e.l.Loaded() {
		t.Fatal("Set stored a value that was not loaded")
	}
	e.gate = make(chan struct{})
	var wg sync.WaitGroup
	got := make([]int, 5)
	for i := range got {
		wg.Go(func() { got[i], _ = e.l.Get(t.Context()) })
	}
	eventually(t, "the load", func() bool { return e.l.loadingNow() })
	close(e.gate)
	wg.Wait()
	for i, v := range got {
		if v != 1 {
			t.Errorf("caller %d got %d", i, v)
		}
	}
	if v, err := e.l.Get(t.Context()); v != 1 || err != nil || e.loads.Load() != 1 {
		t.Errorf("Get = %d, %v after %d loads", v, err, e.loads.Load())
	}
	if e.late.Load() != 0 {
		t.Error("OnLate ran although every caller waited")
	}

	// Set replaces a loaded value and does not count as a use.
	e.h.set(t0.Add(time.Minute))
	if !e.l.Set(7, errors.New("partial")) {
		t.Fatal("Set refused a loaded value")
	}
	if v, ok := e.l.Peek(); !ok || v != 7 {
		t.Errorf("Peek = %d, %v", v, ok)
	}
	if !e.l.used.Equal(t0) {
		t.Errorf("Set or Peek counted as a use: %v", e.l.used)
	}
	if _, err := e.l.Get(t.Context()); err == nil || err.Error() != "partial" {
		t.Errorf("error %v", err)
	}
}

// loadingNow reports whether a load runs.
func (l *Lazy[T]) loadingNow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loading != nil
}

func TestLazyTryGet(t *testing.T) {
	e := newLazy(t)
	if _, ok := e.l.TryGet(); ok {
		t.Fatal("TryGet before a load")
	}
	eventually(t, "the background load", e.l.Loaded)
	eventually(t, "OnLate", func() bool { return e.late.Load() == 1 })
	if v, ok := e.l.TryGet(); !ok || v != 1 {
		t.Errorf("TryGet = %d, %v", v, ok)
	}

	// A Get that gives up also gets OnLate.
	e.l.Drop()
	e.gate = make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error)
	go func() { _, err := e.l.Get(ctx); done <- err }()
	eventually(t, "the load", e.l.loadingNow)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Get = %v", err)
	}
	close(e.gate)
	eventually(t, "OnLate", func() bool { return e.late.Load() == 2 })
	if v, _ := e.l.Peek(); v != 2 {
		t.Errorf("value %d", v)
	}
}

// TestLazyIdleRelease: the value is dropped once unused for Idle by the
// host's clock, and loaded again by the next use.
func TestLazyIdleRelease(t *testing.T) {
	e := newLazy(t)
	if v, _ := e.l.Get(t.Context()); v != 1 {
		t.Fatalf("Get = %d", v)
	}
	eventually(t, "the idle timer", func() bool { return e.k.count() == 1 })
	e.h.set(t0.Add(4 * time.Minute))
	e.l.Get(t.Context()) // a use at +4m
	e.h.set(t0.Add(10 * time.Minute))
	e.k.ch <- time.Time{}
	eventually(t, "the second wait", func() bool { return e.k.count() == 2 })
	if !e.l.Loaded() {
		t.Fatal("dropped although used 6 minutes ago")
	}
	e.k.mu.Lock()
	waits := append([]time.Duration(nil), e.k.waits...)
	e.k.mu.Unlock()
	if waits[0] != 10*time.Minute || waits[1] != 4*time.Minute {
		t.Errorf("waits %v", waits)
	}
	// A background Set is no use.
	e.h.set(t0.Add(13 * time.Minute))
	e.l.Set(9, nil)
	e.h.set(t0.Add(14 * time.Minute))
	e.k.ch <- time.Time{}
	eventually(t, "the drop", func() bool { return !e.l.Loaded() })
	if e.l.Set(3, nil) {
		t.Error("Set stored into a dropped value")
	}
	if v, _ := e.l.Get(t.Context()); v != 2 || e.loads.Load() != 2 {
		t.Errorf("reload = %d after %d loads", v, e.loads.Load())
	}
	eventually(t, "a new idle timer", func() bool { return e.k.count() == 3 })
}

// TestLazyClose: closing the group ends the idle timer, drops the value and
// refuses later loads.
func TestLazyClose(t *testing.T) {
	e := newLazy(t)
	e.l.Get(t.Context())
	eventually(t, "the idle timer", func() bool { return e.k.count() == 1 })
	e.g.Close()
	e.g.Wait() // the timer ended without a tick
	if e.l.Loaded() {
		t.Error("still loaded after Close")
	}
	if _, err := e.l.Get(t.Context()); !errors.Is(err, ErrClosed) {
		t.Errorf("Get after Close = %v", err)
	}
	if _, ok := e.l.TryGet(); ok || e.loads.Load() != 1 {
		t.Errorf("a load after Close (%d loads)", e.loads.Load())
	}

	// A load that ends after Close keeps nothing.
	e2 := newLazy(t)
	e2.gate = make(chan struct{})
	e2.l.TryGet()
	eventually(t, "the load", e2.l.loadingNow)
	e2.g.Close()
	close(e2.gate)
	e2.g.Wait()
	if e2.l.Loaded() || e2.late.Load() != 0 {
		t.Error("a load that ended after Close was kept")
	}
}
