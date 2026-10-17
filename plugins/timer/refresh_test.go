package timer

import (
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func (e *env) loops() int { return e.p.loops.Running() }

func (e *env) waitStopped(t *testing.T) {
	t.Helper()
	eventually(t, "refresh loops stopped", func() bool { return e.loops() == 0 })
}

// countInvalidations counts the root refreshes (Invalidate also follows
// every change of the timers).
func (e *env) invalidations() int { return len(e.h.Invalidated()) }

func TestRootRefresh(t *testing.T) {
	e := newEnv(t)
	e.start(t, "timer 25m")
	base := e.invalidations()
	e.root(t, "timer")
	if e.loops() != 1 {
		t.Fatalf("%d loops", e.loops())
	}
	for i := 1; i <= 3; i++ {
		e.clock.advance(time.Second)
		e.ticks.tick(t)
		eventually(t, "invalidated", func() bool { return e.invalidations() == base+i })
		if got, want := e.item(t, "timer:1").Title, clock(25*time.Minute-time.Duration(i)*time.Second)+"  25 min timer"; got != want {
			t.Errorf("tick %d: %q, want %q", i, got, want)
		}
	}
	if e.loops() != 1 {
		t.Errorf("%d loops, want 1", e.loops())
	}
	// Text without a prefix stops it.
	e.root(t, "vsc")
	e.waitStopped(t)
	// So does hiding the launcher.
	e.root(t, "timer")
	e.ticks.tick(t)
	e.h.Bus.PublishSync(plugin.EventWindowHidden)
	e.waitStopped(t)
	// And two ticks without a pull.
	e.root(t, "timer")
	e.ticks.tick(t)
	e.ticks.tick(t)
	e.ticks.tick(t)
	e.waitStopped(t)
}

func TestViewRender(t *testing.T) {
	e := newEnv(t)
	e.start(t, "timer 25m tea")
	e.root(t, "vsc") // stop the root refresh that start began
	e.waitStopped(t)
	v := e.view(t, "v1", "")
	if got := section(t, v, "Running")[0].Title; got != "25:00  tea" {
		t.Fatalf("title = %q", got)
	}
	for i := 1; i <= 2; i++ {
		e.clock.advance(time.Second)
		e.ticks.tick(t)
		eventually(t, "rendered", func() bool { return len(e.h.Renders()) == i })
		r := e.h.Renders()[i-1]
		if r.Instance != "v1" {
			t.Errorf("instance %q", r.Instance)
		}
		if got, want := section(t, r.View, "Running")[0].Title, clock(25*time.Minute-time.Duration(i)*time.Second)+"  tea"; got != want {
			t.Errorf("render %d: %q, want %q", i, got, want)
		}
	}
	// The pushes use the search text of the last pull.
	e.view(t, "v1", "zzz")
	e.ticks.tick(t)
	eventually(t, "rendered", func() bool { return len(e.h.Renders()) == 3 })
	if v := e.h.Renders()[2].View; len(v.Sections) != 0 {
		t.Errorf("filtered push = %+v", v.Sections)
	}
	e.p.ViewClosed("v1")
	e.waitStopped(t)
	// Pulled again, it pushes again; hiding stops it.
	e.view(t, "v1", "")
	e.ticks.tick(t)
	eventually(t, "rendered", func() bool { return len(e.h.Renders()) == 4 })
	e.h.Bus.PublishSync(plugin.EventWindowHidden)
	e.waitStopped(t)
	// Close stops everything and starts nothing new.
	e.view(t, "v2", "")
	if err := e.p.Close(); err != nil {
		t.Fatal(err)
	}
	if e.loops() != 0 {
		t.Error("loops after Close")
	}
	e.view(t, "v3", "")
	if e.loops() != 0 {
		t.Error("a loop started after Close")
	}
}

// TestFiredRefreshes: a timer that ends asks the host to pull what is
// shown.
func TestFiredRefreshes(t *testing.T) {
	e := newEnv(t)
	e.start(t, "timer 1m")
	n := e.invalidations()
	e.clock.advance(time.Minute)
	e.check(t)
	if e.invalidations() <= n {
		t.Error("no Invalidate after the timer ended")
	}
}
