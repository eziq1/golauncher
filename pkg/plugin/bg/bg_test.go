package bg

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// host implements the Go and Now of plugin.Host.
type host struct {
	plugin.Host
	started atomic.Int32
	mu      sync.Mutex
	now     time.Time
}

func (h *host) Go(fn func()) {
	h.started.Add(1)
	go fn()
}

func (h *host) Now() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGroup(t *testing.T) {
	h := &host{}
	var g Group
	release := make(chan struct{})
	var ran atomic.Int32
	if !g.Go(h, func() { <-release; ran.Add(1) }) {
		t.Fatal("Go before Close did not start")
	}
	if g.Closed() {
		t.Fatal("closed before Close")
	}
	if !g.Close() || g.Close() {
		t.Error("Close should report only the first call")
	}
	if g.Go(h, func() { ran.Add(1) }) {
		t.Error("Go after Close started")
	}
	waited := make(chan struct{})
	go func() { g.Wait(); close(waited) }()
	select {
	case <-waited:
		t.Fatal("Wait returned while work runs")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-waited
	if ran.Load() != 1 || h.started.Load() != 1 {
		t.Errorf("ran %d, started %d", ran.Load(), h.started.Load())
	}
}

// TestRefresher: requests during a run cause exactly one more run; requests
// after it start a new one; a closed group stops the reruns.
func TestRefresher(t *testing.T) {
	h := &host{}
	var g Group
	var r Refresher
	gate := make(chan struct{})
	var runs atomic.Int32
	fn := func() { runs.Add(1); <-gate }
	r.Request(&g, h, fn)
	eventually(t, "the first run", func() bool { return runs.Load() == 1 })
	for range 5 {
		r.Request(&g, h, func() { t.Error("the fn of a later request ran") })
	}
	gate <- struct{}{} // end run 1: one rerun follows
	eventually(t, "the rerun", func() bool { return runs.Load() == 2 })
	gate <- struct{}{} // end run 2: nothing was requested since
	eventually(t, "the end", func() bool { return !r.Running() })
	if runs.Load() != 2 || h.started.Load() != 1 {
		t.Fatalf("runs %d, goroutines %d", runs.Load(), h.started.Load())
	}

	r.Request(&g, h, fn)
	eventually(t, "a new run", func() bool { return runs.Load() == 3 })
	r.Request(&g, h, fn)
	g.Close()
	gate <- struct{}{} // closed: no rerun
	eventually(t, "the end after Close", func() bool { return !r.Running() })
	if runs.Load() != 3 {
		t.Errorf("runs %d after Close", runs.Load())
	}
	r.Request(&g, h, fn) // closed: nothing starts
	if r.Running() {
		t.Error("a request after Close runs")
	}
	g.Wait()
}

func TestReady(t *testing.T) {
	var r Ready
	if r.IsDone() {
		t.Fatal("done")
	}
	start := time.Now()
	if r.Wait(t.Context(), 30*time.Millisecond) {
		t.Error("Wait reported done")
	}
	if d := time.Since(start); d < 30*time.Millisecond {
		t.Errorf("Wait returned after %v", d)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if r.Wait(ctx, time.Hour) {
		t.Error("Wait with an ended context reported done")
	}
	go func() { time.Sleep(10 * time.Millisecond); r.Done() }()
	if !r.Wait(t.Context(), time.Hour) {
		t.Error("Wait did not see Done")
	}
	r.Done() // again: no panic
	if !r.IsDone() || !r.Wait(t.Context(), 0) {
		t.Error("not done after Done")
	}
}

func TestUntilNextSecond(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, tt := range []struct {
		off  time.Duration
		want time.Duration
	}{
		{0, time.Second + TickSlack},
		{250 * time.Millisecond, 750*time.Millisecond + TickSlack},
		{999 * time.Millisecond, time.Millisecond + TickSlack},
	} {
		if got := UntilNextSecond(base.Add(tt.off)); got != tt.want {
			t.Errorf("UntilNextSecond(+%v) = %v, want %v", tt.off, got, tt.want)
		}
	}
}

// ticks delivers a tick only when the test sends one, and records waits.
type ticks struct {
	mu    sync.Mutex
	ch    chan time.Time
	waits []time.Duration
}

func (k *ticks) after(d time.Duration) <-chan time.Time {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.waits = append(k.waits, d)
	return k.ch
}

func (k *ticks) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.waits)
}

func TestSecondLoops(t *testing.T) {
	h := &host{now: time.Date(2026, 1, 2, 3, 4, 5, 400*int(time.Millisecond), time.UTC)}
	k := &ticks{ch: make(chan time.Time)}
	var wg sync.WaitGroup
	s := &SecondLoops{Host: h, After: k.after, WG: &wg}
	stop := make(chan struct{})
	var n atomic.Int32
	s.Start(stop, func() bool { return n.Add(1) < 3 })
	if s.Running() != 1 {
		t.Fatalf("%d loops", s.Running())
	}
	eventually(t, "the first wait", func() bool { return k.count() == 1 })
	if k.waits[0] != 600*time.Millisecond+TickSlack {
		t.Errorf("waited %v", k.waits[0])
	}
	for range 3 {
		k.ch <- time.Time{}
	}
	wg.Wait() // the tick returned false the third time
	if n.Load() != 3 || s.Running() != 0 {
		t.Errorf("ticks %d, running %d", n.Load(), s.Running())
	}

	// Closing stop ends a loop waiting for its tick.
	stop2 := make(chan struct{})
	s.Start(stop2, func() bool { t.Error("ticked after stop"); return true })
	close(stop2)
	wg.Wait()
	if s.Running() != 0 {
		t.Errorf("running %d after stop", s.Running())
	}
}
