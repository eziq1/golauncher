package clock

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

func init() {
	// Tests create many plugins one after another; they share one index.
	keepOnClose = true
}

// idleTimer replaces time.After for the idle release.
type idleTimer struct {
	ch    chan time.Time
	waits atomic.Int32
}

func (f *idleTimer) after(d time.Duration) <-chan time.Time {
	if d != idleRelease {
		panic("unexpected idle wait")
	}
	f.waits.Add(1)
	return f.ch
}

// fire ends the running idle wait.
func (f *idleTimer) fire(t *testing.T) {
	t.Helper()
	select {
	case f.ch <- time.Time{}:
	case <-time.After(5 * time.Second):
		t.Fatal("no idle timer is waiting")
	}
}

type lazyEnv struct {
	p     *Plugin
	h     *hostfake.Host
	cache *indexCache
	loads *atomic.Int32
	idle  *idleTimer
}

// newLazyEnv returns a plugin with its own cache of the fixture cities.
func newLazyEnv(t *testing.T, o Options) *lazyEnv {
	t.Helper()
	var loads atomic.Int32
	cache := &indexCache{load: func() (*index, error) {
		loads.Add(1)
		return buildIndex([]byte(fixtureCurated), gz(t, fixtureGeonames))
	}}
	idle := &idleTimer{ch: make(chan time.Time)}
	o.After = func(time.Duration) <-chan time.Time { return nil }
	o.IdleAfter = idle.after
	p := NewWithOptions(o)
	p.cache, p.index = cache, cache.get
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return &lazyEnv{p, h, cache, &loads, idle}
}

func (e *lazyEnv) root(t *testing.T, text string) []plugin.Item {
	t.Helper()
	items, err := e.p.RootItems(t.Context(), plugin.Query{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// TestLazyLoad: the cities are loaded on the first prefixed search, not in
// Init or for other searches; a search during loading is asked for again.
func TestLazyLoad(t *testing.T) {
	e := newLazyEnv(t, Options{})
	e.root(t, "vsc")
	if n := e.loads.Load(); n != 0 || e.cache.loadedNow() {
		t.Fatalf("%d loads before the first search", n)
	}
	if got := e.root(t, "time maple"); len(got) != 2 {
		t.Fatalf("time maple: %v", ids(got))
	}
	eventually(t, "loaded", e.p.loaded.Load)
	eventually(t, "invalidated after loading", func() bool { return len(e.h.Invalidated()) > 0 })
	e.root(t, "time hc")
	if n := e.loads.Load(); n != 1 {
		t.Errorf("%d loads", n)
	}
}

// TestLazyLoadSync: with Options.Sync the search loads the cities itself
// and does not ask the host to pull again.
func TestLazyLoadSync(t *testing.T) {
	e := newLazyEnv(t, Options{Sync: true})
	if got := e.root(t, "time maple"); len(got) != 2 {
		t.Fatalf("time maple: %v", ids(got))
	}
	if e.p.missed.Load() || len(e.h.Invalidated()) != 0 {
		t.Errorf("invalidated: %v", e.h.Invalidated())
	}
	if n := e.loads.Load(); n != 1 {
		t.Errorf("%d loads", n)
	}
}

// TestIdleRelease: the index is released when the launcher has been hidden
// for idleRelease, and loaded again on the next search.
func TestIdleRelease(t *testing.T) {
	e := newLazyEnv(t, Options{})
	// Hidden before anything was loaded: nothing to release, no timer.
	e.h.Bus.PublishSync(plugin.EventWindowHidden)
	e.h.Bus.PublishSync(plugin.EventWindowShown)
	if n := e.idle.waits.Load(); n != 0 {
		t.Fatalf("%d idle waits without an index", n)
	}
	e.root(t, "time maple")
	eventually(t, "loaded", e.p.loaded.Load)
	e.h.Bus.PublishSync(plugin.EventWindowHidden)
	e.idle.fire(t)
	eventually(t, "released", func() bool { return !e.cache.loadedNow() && !e.p.loaded.Load() })
	// Shown again: the next search loads the cities again.
	e.h.Bus.PublishSync(plugin.EventWindowShown)
	if got := e.root(t, "time maple"); len(got) != 2 {
		t.Fatalf("time maple after release: %v", ids(got))
	}
	eventually(t, "loaded again", e.p.loaded.Load)
	if n := e.loads.Load(); n != 2 {
		t.Errorf("%d loads, want 2", n)
	}
}

// TestIdleReleaseCancelled: showing the launcher stops the idle timer.
func TestIdleReleaseCancelled(t *testing.T) {
	e := newLazyEnv(t, Options{})
	e.root(t, "time maple")
	eventually(t, "loaded", e.p.loaded.Load)
	e.h.Bus.PublishSync(plugin.EventWindowHidden)
	eventually(t, "the idle timer waits", func() bool { return e.idle.waits.Load() == 1 })
	e.h.Bus.PublishSync(plugin.EventWindowShown)
	select {
	case e.idle.ch <- time.Time{}:
		t.Fatal("the idle timer still waits after the launcher was shown")
	case <-time.After(50 * time.Millisecond):
	}
	if !e.cache.loadedNow() || !e.p.loaded.Load() {
		t.Error("released while shown")
	}
}

// TestIdleReleaseWithOpenView: the index is not released while a World
// Clock view is open; closing the view starts the idle timer again.
func TestIdleReleaseWithOpenView(t *testing.T) {
	e := newLazyEnv(t, Options{})
	if _, err := e.p.View(t.Context(), plugin.ViewRequest{ID: cmdWorld, Instance: "v1"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "loaded", e.p.loaded.Load)
	e.h.Bus.PublishSync(plugin.EventWindowHidden)
	e.idle.fire(t)
	time.Sleep(20 * time.Millisecond)
	if !e.cache.loadedNow() {
		t.Fatal("released with a view open")
	}
	e.p.ViewClosed("v1")
	e.idle.fire(t)
	eventually(t, "released", func() bool { return !e.cache.loadedNow() })
}

// TestCloseReleases: closing the last plugin drops the index, unless
// keepOnClose or Options.Sync keep it for the next plugin.
func TestCloseReleases(t *testing.T) {
	for _, tt := range []struct {
		keep, sync bool
		dropped    bool
	}{{false, false, true}, {true, false, false}, {false, true, false}} {
		keepOnClose = tt.keep
		e := newLazyEnv(t, Options{Sync: tt.sync})
		e.root(t, "time maple")
		if err := e.p.Close(); err != nil {
			t.Fatal(err)
		}
		if got := !e.cache.loadedNow(); got != tt.dropped {
			t.Errorf("keep %v, sync %v: dropped = %v", tt.keep, tt.sync, got)
		}
	}
	keepOnClose = true
}

// TestIndexCacheShared: concurrent users get one index from one load.
func TestIndexCacheShared(t *testing.T) {
	var loads atomic.Int32
	c := &indexCache{load: func() (*index, error) {
		loads.Add(1)
		time.Sleep(10 * time.Millisecond)
		return &index{}, nil
	}}
	var wg sync.WaitGroup
	got := make([]*index, 8)
	for i := range got {
		wg.Go(func() {
			idx, err := c.get()
			if err != nil {
				t.Error(err)
			}
			got[i] = idx
		})
	}
	wg.Wait()
	for _, idx := range got {
		if idx != got[0] {
			t.Fatal("different indexes")
		}
	}
	if n := loads.Load(); n != 1 {
		t.Errorf("%d loads", n)
	}
	// Two users: the index stays until both release it.
	c.acquire()
	c.acquire()
	c.release(true)
	if !c.loadedNow() {
		t.Error("dropped while in use")
	}
	c.release(true)
	if c.loadedNow() {
		t.Error("not dropped")
	}
}
