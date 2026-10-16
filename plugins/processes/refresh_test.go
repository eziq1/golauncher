package processes

import (
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func (e *env) running() int { return int(e.p.running.Load()) }

func (e *env) stopped(t *testing.T) {
	t.Helper()
	eventually(t, "sampling stopped", func() bool { return e.running() == 0 })
}

func (e *env) sampled() int {
	e.b.mu.Lock()
	defer e.b.mu.Unlock()
	return e.b.samples
}

// TestRootRefresh: a prefixed search samples and invalidates once per tick,
// and stops when the prefix is gone or the search is no longer pulled.
func TestRootRefresh(t *testing.T) {
	e := newEnv(t, false)
	if e.running() != 0 || e.sampled() != 0 {
		t.Fatal("sampling before anything shows")
	}
	items := e.root(t, "ps ")
	if e.sampled() != 1 || e.running() != 1 {
		t.Fatalf("after the first search: %d samples, %d loops", e.sampled(), e.running())
	}
	// The first sample has no CPU percentages yet.
	if items[0].Accessories[0].Text != "CPU —" {
		t.Errorf("first summary CPU = %q", items[0].Accessories[0].Text)
	}
	for i := 1; i <= 3; i++ {
		e.ticks.tick(t)
		eventually(t, "invalidated", func() bool { return len(e.h.Invalidated()) == i })
		if e.sampled() != i+1 {
			t.Errorf("tick %d: %d samples", i, e.sampled())
		}
		items = e.root(t, "ps ") // the host pulls again
	}
	if items[0].Accessories[0].Text != "CPU 23.0%" {
		t.Errorf("summary CPU = %q", items[0].Accessories[0].Text)
	}
	if e.running() != 1 {
		t.Errorf("%d loops", e.running())
	}
	// Text without the prefix: the root no longer uses the loop.
	e.root(t, "notepad")
	e.ticks.tick(t)
	e.stopped(t)
	// Not pulled for two ticks: stops too.
	e.root(t, "kill fab")
	e.ticks.tick(t) // pulled since the start
	e.ticks.tick(t) // first miss
	e.ticks.tick(t) // second miss: stop
	e.stopped(t)
}

func TestViewRefresh(t *testing.T) {
	e := newEnv(t, false)
	e.view(t, cmdProcesses, "v1", "", nil)
	e.view(t, cmdHung, "v2", "", nil)
	if e.running() != 1 {
		t.Fatalf("%d loops for two views", e.running())
	}
	e.ticks.tick(t)
	eventually(t, "rendered", func() bool { return len(e.h.Renders()) == 2 })
	if len(e.h.Invalidated()) != 0 {
		t.Errorf("views invalidated the root: %v", e.h.Invalidated())
	}
	// The search text is kept for the pushes.
	e.view(t, cmdProcesses, "v1", "writer", nil)
	e.p.ViewClosed("v2")
	e.ticks.tick(t)
	eventually(t, "rendered again", func() bool { return len(e.h.Renders()) == 3 })
	r := e.h.Renders()[2]
	if r.Instance != "v1" || len(r.View.Sections) != 1 || r.View.Sections[0].Items[0].ID != idWriter {
		t.Errorf("push = %s %+v", r.Instance, r.View.Sections)
	}
	e.p.ViewClosed("v1")
	e.ticks.tick(t)
	e.stopped(t)
}

// TestRefreshStopsWhenHidden: hiding the launcher stops sampling at once,
// and the cache is released 30 seconds later.
func TestRefreshStopsWhenHidden(t *testing.T) {
	e := newEnv(t, false)
	e.root(t, "ps ")
	e.view(t, cmdProcesses, "v1", "", nil)
	e.h.Bus.PublishSync(plugin.EventWindowHidden)
	e.stopped(t)
	e.p.mu.Lock()
	cached := e.p.st.cur != nil
	e.p.mu.Unlock()
	if !cached {
		t.Fatal("the cache was dropped at once")
	}
	e.ticks.tick(t) // the release timer
	eventually(t, "the cache released", func() bool {
		e.p.mu.Lock()
		defer e.p.mu.Unlock()
		return e.p.st.cur == nil && e.p.st.info == nil
	})
	// Shown again: a new search samples and runs again.
	e.h.Bus.PublishSync(plugin.EventWindowShown)
	e.root(t, "ps ")
	if e.running() != 1 {
		t.Errorf("%d loops after showing again", e.running())
	}
}

func TestRefreshStopsOnClose(t *testing.T) {
	e := newEnv(t, false)
	e.root(t, "ps ")
	if err := e.p.Close(); err != nil {
		t.Fatal(err)
	}
	if e.running() != 0 {
		t.Errorf("%d loops after Close", e.running())
	}
	e.root(t, "ps ")
	if e.running() != 0 {
		t.Error("a loop started after Close")
	}
}
