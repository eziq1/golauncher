package ring

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeClock fires its timers when told to.
type fakeClock struct {
	mu     sync.Mutex
	timers []*fakeTimer
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{d: d, f: f}
	c.timers = append(c.timers, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		was := !t.stopped
		t.stopped = true
		return was
	}
}

// fire runs the timers not stopped, as if their time had come.
func (c *fakeClock) fire() {
	c.mu.Lock()
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.stopped {
			t.stopped = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	for _, t := range due {
		t.f()
	}
}

func (c *fakeClock) running() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.timers {
		if !t.stopped {
			n++
		}
	}
	return n
}

// recorder records the machine's calls.
type recorder struct {
	mu      sync.Mutex
	calls   []string
	allowed bool
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

func (r *recorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.calls
	r.calls = nil
	return c
}

func newTestMachine() (*Machine, *fakeClock, *recorder) {
	clk := &fakeClock{}
	rec := &recorder{allowed: true}
	m := NewMachine(Options{
		Hold:      300 * time.Millisecond,
		Threshold: 6,
		Clock:     clk,
		Callbacks: Callbacks{
			Allowed: func(Point) bool { return rec.allowed },
			Replay:  func(down, up bool) { rec.add(fmt.Sprintf("replay %v %v", down, up)) },
			Open:    func(p Point) { rec.add(fmt.Sprintf("open %v,%v", p.X, p.Y)) },
			Track:   func(p Point) { rec.add(fmt.Sprintf("track %v,%v", p.X, p.Y)) },
			Release: func(p Point, moved bool) { rec.add(fmt.Sprintf("release %v,%v %v", p.X, p.Y, moved)) },
			Press:   func(p Point, middle bool) { rec.add(fmt.Sprintf("press %v,%v %v", p.X, p.Y, middle)) },
		},
	})
	return m, clk, rec
}

func ev(k EventKind, x, y float64) Event { return Event{Kind: k, Pt: Point{x, y}} }

func want(t *testing.T, rec *recorder, calls ...string) {
	t.Helper()
	if got := rec.take(); !slices.Equal(got, calls) {
		t.Fatalf("calls %q, want %q", got, calls)
	}
}

func handle(t *testing.T, m *Machine, e Event, swallow bool) {
	t.Helper()
	if got := m.Handle(e); got != swallow {
		t.Fatalf("Handle(%+v) = %v, want %v", e, got, swallow)
	}
}

// TestShortPress: a press let go before the hold time is given back to the
// app as a click, down and up, and the timer is stopped.
func TestShortPress(t *testing.T) {
	m, clk, rec := newTestMachine()
	handle(t, m, ev(MiddleDown, 100, 100), true)
	if !m.WantsMoves() || !m.WantsButtons() {
		t.Error("a pending press wants the moves and the buttons")
	}
	handle(t, m, ev(Move, 102, 101), false) // a tremble, below the threshold
	handle(t, m, ev(MiddleUp, 102, 101), true)
	want(t, rec, "replay true true")
	if clk.running() != 0 {
		t.Error("the timer still runs")
	}
	clk.fire()
	want(t, rec)
	if m.WantsMoves() || m.WantsButtons() || m.Open() {
		t.Error("idle again, the machine still wants events")
	}
	// The replay comes back marked as ours: it passes, nothing happens.
	handle(t, m, Event{Kind: MiddleDown, Own: true}, false)
	handle(t, m, Event{Kind: MiddleUp, Own: true}, false)
	want(t, rec)
}

// TestHoldOpens: held for the hold time, the ring opens where the button
// went down; the down and the up never reach the app.
func TestHoldOpens(t *testing.T) {
	m, clk, rec := newTestMachine()
	handle(t, m, ev(MiddleDown, 50, 60), true)
	clk.fire()
	want(t, rec, "open 50,60")
	if !m.Open() || !m.WantsMoves() {
		t.Fatal("open, the machine follows the pointer")
	}
	// Let go without moving: the ring stays.
	handle(t, m, ev(MiddleUp, 51, 61), true)
	want(t, rec, "release 51,61 false")
	if !m.Open() || m.WantsMoves() || !m.WantsButtons() {
		t.Fatal("the ring stays open and watches the buttons, not the moves")
	}
	// A click of another button while it stays: reported, never kept.
	handle(t, m, ev(OtherDown, 500, 500), false)
	want(t, rec, "press 500,500 false")
	// A middle click while it stays: reported and kept, with its up.
	handle(t, m, ev(MiddleDown, 70, 60), true)
	handle(t, m, ev(MiddleUp, 70, 60), true)
	want(t, rec, "press 70,60 true")
	m.SetOpen(false)
	if m.WantsButtons() {
		t.Error("closed, the machine still wants the buttons")
	}
	handle(t, m, ev(OtherDown, 1, 1), false)
	want(t, rec)
}

// TestOpenAt: a trackpad's tap opens the ring through Open, staying open
// as after a press let go without moving; not while it shows, nor while a
// press is in progress, nor over an exception.
func TestOpenAt(t *testing.T) {
	m, clk, rec := newTestMachine()
	if !m.OpenAt(Point{10, 20}) {
		t.Fatal("not opened")
	}
	want(t, rec, "open 10,20")
	if !m.Open() || m.WantsMoves() || !m.WantsButtons() || m.ButtonDown() {
		t.Fatal("the ring stays open and watches the buttons, not the moves")
	}
	if m.OpenAt(Point{30, 30}) {
		t.Error("opened again while it shows")
	}
	// A click outside is reported; a middle click chooses, kept with its up.
	handle(t, m, ev(OtherDown, 500, 500), false)
	handle(t, m, ev(MiddleDown, 12, 20), true)
	handle(t, m, ev(MiddleUp, 12, 20), true)
	want(t, rec, "press 500,500 false", "press 12,20 true")
	m.SetOpen(false)

	handle(t, m, ev(MiddleDown, 1, 1), true)
	if m.OpenAt(Point{1, 1}) {
		t.Error("opened while a press is pending")
	}
	handle(t, m, ev(MiddleUp, 1, 1), true)
	want(t, rec, "replay true true")
	if clk.running() != 0 {
		t.Error("a timer runs")
	}

	rec.allowed = false
	if m.OpenAt(Point{1, 1}) || m.Open() {
		t.Error("opened over an exception")
	}
	want(t, rec)
}

// TestDragSelect: held open and dragged, the pointer is tracked and the
// release reports a move.
func TestDragSelect(t *testing.T) {
	m, clk, rec := newTestMachine()
	handle(t, m, ev(MiddleDown, 100, 100), true)
	clk.fire()
	handle(t, m, ev(Move, 100, 80), false) // moves always pass
	handle(t, m, ev(Move, 100, 40), false)
	handle(t, m, ev(MiddleUp, 100, 40), true)
	want(t, rec, "open 100,100", "track 100,80", "track 100,40", "release 100,40 true")
	m.SetOpen(false) // the shell ran the slot and closed the ring
	// Back near the start before letting go still counts as a drag.
	handle(t, m, ev(MiddleDown, 100, 100), true)
	clk.fire()
	handle(t, m, ev(Move, 150, 100), false)
	handle(t, m, ev(Move, 101, 100), false)
	handle(t, m, ev(MiddleUp, 101, 100), true)
	want(t, rec, "open 100,100", "track 150,100", "track 101,100", "release 101,100 true")
}

// TestMovePassesThrough: a press that moves farther than the threshold
// before the hold time goes to the app at once (autoscroll, panning), with
// everything after it, and no ring opens.
func TestMovePassesThrough(t *testing.T) {
	m, clk, rec := newTestMachine()
	handle(t, m, ev(MiddleDown, 100, 100), true)
	handle(t, m, ev(Move, 110, 100), false)
	want(t, rec, "replay true false")
	if clk.running() != 0 || m.WantsMoves() {
		t.Error("passing through, the machine still waits")
	}
	clk.fire()
	handle(t, m, ev(Move, 200, 100), false)
	handle(t, m, ev(MiddleUp, 200, 100), false)
	want(t, rec)
	if m.Open() {
		t.Error("a ring opened")
	}
}

// TestChordPassesThrough: another button joining a pending press gives the
// press to the app at once.
func TestChordPassesThrough(t *testing.T) {
	m, _, rec := newTestMachine()
	handle(t, m, ev(MiddleDown, 1, 1), true)
	handle(t, m, ev(OtherDown, 1, 1), false)
	handle(t, m, ev(MiddleUp, 1, 1), false)
	want(t, rec, "replay true false")
}

// TestException: over an app the ring leaves alone, nothing is kept.
func TestException(t *testing.T) {
	m, clk, rec := newTestMachine()
	rec.allowed = false
	handle(t, m, ev(MiddleDown, 1, 1), false)
	handle(t, m, ev(Move, 50, 1), false)
	handle(t, m, ev(MiddleUp, 50, 1), false)
	if clk.running() != 0 {
		t.Error("a timer for an exception")
	}
	want(t, rec)
}

// TestInjectedIgnored: middle buttons another program injected pass and
// change nothing, also while a press of the user's is pending.
func TestInjectedIgnored(t *testing.T) {
	m, clk, rec := newTestMachine()
	handle(t, m, Event{Kind: MiddleDown, Injected: true}, false)
	handle(t, m, Event{Kind: MiddleUp, Injected: true}, false)
	if clk.running() != 0 {
		t.Fatal("an injected press started a timer")
	}
	handle(t, m, ev(MiddleDown, 1, 1), true)
	handle(t, m, Event{Kind: MiddleUp, Injected: true}, false)
	handle(t, m, Event{Kind: Move, Pt: Point{90, 90}, Injected: true}, false)
	clk.fire()
	want(t, rec, "open 1,1")
}

// TestClosedWhileHeld: the ring closed (Escape) while the button holds it:
// the button's up is still kept from the app, and nothing is chosen.
func TestClosedWhileHeld(t *testing.T) {
	m, clk, rec := newTestMachine()
	handle(t, m, ev(MiddleDown, 1, 1), true)
	clk.fire()
	m.SetOpen(false)
	handle(t, m, ev(Move, 90, 90), false)
	handle(t, m, ev(MiddleUp, 90, 90), true)
	want(t, rec, "open 1,1")
	// The next press starts afresh.
	handle(t, m, ev(MiddleDown, 1, 1), true)
	handle(t, m, ev(MiddleUp, 1, 1), true)
	want(t, rec, "replay true true")
}

// TestLateTimer: a timer that fires after its press ended does nothing.
func TestLateTimer(t *testing.T) {
	m, clk, rec := newTestMachine()
	handle(t, m, ev(MiddleDown, 1, 1), true)
	var stale func()
	clk.mu.Lock()
	stale = clk.timers[0].f
	clk.mu.Unlock()
	handle(t, m, ev(MiddleUp, 1, 1), true)
	stale() // the timer was already firing as the button went up
	want(t, rec, "replay true true")
	if m.Open() {
		t.Error("a stale timer opened the ring")
	}
}

// TestReset: the hook stops with a press pending: the press goes back to
// the app.
func TestReset(t *testing.T) {
	m, clk, rec := newTestMachine()
	handle(t, m, ev(MiddleDown, 1, 1), true)
	m.Reset()
	clk.fire()
	want(t, rec, "replay true false")
	handle(t, m, ev(MiddleUp, 1, 1), false)
}

// TestHoldTime: the timer uses the hold time, which can change.
func TestHoldTime(t *testing.T) {
	m, clk, _ := newTestMachine()
	m.SetHold(700 * time.Millisecond)
	handle(t, m, ev(MiddleDown, 1, 1), true)
	clk.mu.Lock()
	d := clk.timers[0].d
	clk.mu.Unlock()
	if d != 700*time.Millisecond {
		t.Errorf("timer %v", d)
	}
}

// TestRealClock: the real clock's timers fire and stop.
func TestRealClock(t *testing.T) {
	done := make(chan struct{})
	RealClock{}.AfterFunc(time.Millisecond, func() { close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the timer did not fire")
	}
	if stop := (RealClock{}).AfterFunc(time.Hour, func() {}); !stop() {
		t.Error("stop reported the timer already done")
	}
}

// TestConcurrent: events and timers from several goroutines (go test -race).
func TestConcurrent(t *testing.T) {
	m := NewMachine(Options{Hold: time.Microsecond, Threshold: 6, Callbacks: Callbacks{Replay: func(bool, bool) {}}})
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range 200 {
				m.Handle(ev(MiddleDown, float64(g), float64(i)))
				m.Handle(ev(Move, float64(i), 0))
				m.Handle(ev(MiddleUp, 0, 0))
				m.SetOpen(false)
			}
		})
	}
	wg.Wait()
}
