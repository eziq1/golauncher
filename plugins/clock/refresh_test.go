package clock

import (
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func (e *env) running() int { return e.p.loops.Running() }

func (e *env) waitStopped(t *testing.T) {
	t.Helper()
	eventually(t, "all refresh loops stopped", func() bool { return e.running() == 0 })
}

func (e *env) invalidations(t *testing.T, n int) {
	t.Helper()
	eventually(t, "invalidated", func() bool { return len(e.h.Invalidated()) >= n })
	if got := len(e.h.Invalidated()); got != n {
		t.Fatalf("%d invalidations, want %d", got, n)
	}
}

// TestRootRefreshTicks checks that while a prefixed search shows, the plugin
// invalidates once a second at whole seconds, and the re-pulled items show
// the new time.
func TestRootRefreshTicks(t *testing.T) {
	e := newEnv(t)
	if got := e.root(t, "time bj")[0].Title; got != "Beijing  18:00:00" {
		t.Fatalf("title = %q", got)
	}
	if e.running() != 1 {
		t.Fatalf("%d loops running", e.running())
	}
	eventually(t, "the loop waits", func() bool { return e.ticks.lastWait() > 0 })
	// Started a quarter second past 10:00:00: the first tick is at 10:00:01.
	if got := e.ticks.lastWait(); got != 750*time.Millisecond+tickSlack {
		t.Errorf("first wait = %v", got)
	}
	for i := 1; i <= 3; i++ {
		e.ticks.tick(t)
		e.invalidations(t, i)
		if got := e.h.Invalidated()[i-1]; got != "" {
			t.Errorf("invalidated %q, want the root (\"\")", got)
		}
		// The host pulls again.
		want := []string{"Beijing  18:00:01", "Beijing  18:00:02", "Beijing  18:00:03"}[i-1]
		if got := e.root(t, "time bj")[0].Title; got != want {
			t.Errorf("tick %d: title %q, want %q", i, got, want)
		}
		// Aligned: after a tick at x.010 the next one waits 990ms.
		eventually(t, "the loop waits again", func() bool { return e.ticks.lastWait() == time.Second })
	}
	if e.running() != 1 {
		t.Errorf("%d loops running, want 1 (pulls must not start more)", e.running())
	}
}

func TestRootRefreshStopsWithoutPrefix(t *testing.T) {
	e := newEnv(t)
	e.root(t, "time")
	e.ticks.tick(t)
	e.invalidations(t, 1)
	if items := e.root(t, "vsc"); len(items) != 0 {
		t.Fatal(items)
	}
	e.waitStopped(t)
	// A new prefixed search starts it again.
	e.root(t, "tz tokyo")
	e.ticks.tick(t)
	e.invalidations(t, 2)
}

func TestRootRefreshStopsWhenHidden(t *testing.T) {
	e := newEnv(t)
	e.root(t, "time")
	e.ticks.tick(t)
	e.invalidations(t, 1)
	e.h.Bus.PublishSync(plugin.EventWindowHidden)
	e.waitStopped(t)
	if got := len(e.h.Invalidated()); got != 1 {
		t.Errorf("%d invalidations after hiding", got)
	}
}

// TestRootRefreshStopsWhenNotPulled checks the fallback: after two ticks
// without a pull (the root search is no longer shown) the loop ends.
func TestRootRefreshStopsWhenNotPulled(t *testing.T) {
	e := newEnv(t)
	e.root(t, "time")
	e.ticks.tick(t) // pulled since the start: invalidate
	e.invalidations(t, 1)
	e.ticks.tick(t) // one miss: still invalidate, the pull may be late
	e.invalidations(t, 2)
	e.ticks.tick(t) // second miss: stop
	e.waitStopped(t)
	if got := len(e.h.Invalidated()); got != 2 {
		t.Errorf("%d invalidations, want 2", got)
	}
}

func TestRootRefreshStopsOnClose(t *testing.T) {
	e := newEnv(t)
	e.root(t, "time")
	if err := e.p.Close(); err != nil {
		t.Fatal(err)
	}
	if e.running() != 0 {
		t.Errorf("%d loops running after Close", e.running())
	}
	e.root(t, "time") // no new loop after Close
	if e.running() != 0 {
		t.Errorf("a loop started after Close")
	}
}

func (e *env) view(t *testing.T, instance, text string) plugin.View {
	t.Helper()
	v, err := e.p.View(t.Context(), plugin.ViewRequest{ID: cmdWorld, Instance: instance, Query: plugin.Query{Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (e *env) renders(t *testing.T, n int) {
	t.Helper()
	eventually(t, "rendered", func() bool { return len(e.h.Renders()) >= n })
	if got := len(e.h.Renders()); got != n {
		t.Fatalf("%d renders, want %d", got, n)
	}
}

func TestViewPushesEverySecond(t *testing.T) {
	e := newEnv(t)
	v := e.view(t, "v1", "")
	if len(v.Sections) != 2 || v.Sections[0].Items[0].ID != localID || len(v.Sections[1].Items) != len(defaultCities) || !v.ShowDetail {
		t.Fatalf("view = %+v", v)
	}
	if got := v.Sections[1].Items[0].Title; got != "Beijing  18:00:00" {
		t.Errorf("title = %q", got)
	}
	for i := 1; i <= 3; i++ {
		e.ticks.tick(t)
		e.renders(t, i)
		r := e.h.Renders()[i-1]
		want := []string{"Beijing  18:00:01", "Beijing  18:00:02", "Beijing  18:00:03"}[i-1]
		if r.Instance != "v1" || r.View.Sections[1].Items[0].Title != want {
			t.Errorf("push %d: %s %q, want %q", i, r.Instance, r.View.Sections[1].Items[0].Title, want)
		}
	}
	// Typing filters; later pushes keep the search text.
	// Kathmandu first, then the other cities of its zone.
	if got := e.view(t, "v1", "kathmandu").Sections[0].Items; len(got) == 0 || got[0].ID != "city:kathmandu" {
		t.Fatalf("search = %v", ids(got))
	}
	e.ticks.tick(t)
	e.renders(t, 4)
	if got := e.h.Renders()[3].View.Sections[0].Items; len(got) == 0 || got[0].Title != "Kathmandu  15:45:04" {
		t.Errorf("push after search = %v", got)
	}
	if len(e.h.Invalidated()) != 0 {
		t.Errorf("the view invalidated: %v", e.h.Invalidated())
	}
	if e.running() != 1 {
		t.Errorf("%d loops", e.running())
	}
	e.p.ViewClosed("v1")
	e.waitStopped(t)
	e.p.ViewClosed("v1") // twice is fine
}

func TestViewPushesStop(t *testing.T) {
	t.Run("hidden", func(t *testing.T) {
		e := newEnv(t)
		e.view(t, "v1", "")
		e.view(t, "v2", "")
		if e.running() != 2 {
			t.Fatalf("%d loops", e.running())
		}
		e.h.Bus.PublishSync(plugin.EventWindowHidden)
		e.waitStopped(t)
		// Pulled again (shown again): pushes resume.
		e.view(t, "v2", "")
		e.ticks.tick(t)
		e.renders(t, 1)
	})
	t.Run("close", func(t *testing.T) {
		e := newEnv(t)
		e.view(t, "v1", "")
		e.ticks.tick(t)
		e.renders(t, 1)
		if err := e.p.Close(); err != nil {
			t.Fatal(err)
		}
		if e.running() != 0 {
			t.Errorf("%d loops after Close", e.running())
		}
	})
}

func TestViewFavorites(t *testing.T) {
	e := newEnv(t)
	if _, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackFavorite,
		Payload: plugin.MustPayload(favoritePayload{City: "tokyo"})}); err != nil {
		t.Fatal(err)
	}
	v := e.view(t, "", "")
	if len(v.Sections) != 3 || v.Sections[1].Title != "Favorites" || v.Sections[1].Items[0].ID != "city:tokyo" {
		t.Fatalf("sections = %+v", v.Sections)
	}
	for _, it := range v.Sections[2].Items {
		if it.ID == "city:tokyo" {
			t.Error("a favorite is listed twice")
		}
	}
	for _, s := range v.Sections {
		for _, it := range s.Items {
			for _, a := range it.Actions {
				if a.Do.Kind == plugin.ActionPush {
					t.Errorf("%s offers to open the view it is in", it.ID)
				}
			}
		}
	}
	if e.running() != 0 {
		t.Errorf("a view without an instance started a loop")
	}
	if _, err := e.p.View(t.Context(), plugin.ViewRequest{ID: "nope"}); err == nil {
		t.Error("unknown view accepted")
	}
}
