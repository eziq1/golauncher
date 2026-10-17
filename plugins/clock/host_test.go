package clock

import (
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestWithHost runs the plugin in the real engine and controller: the root
// list and the World Clock view tick, and the loops stop when the view is
// closed (Engine.OnViewClosed reaches plugin.ViewCloser) or the root search
// is no longer shown.
func TestWithHost(t *testing.T) {
	clock := &fakeClock{t: start}
	ticks := &fakeTicks{ch: make(chan time.Time), clock: clock}
	p := NewWithOptions(Options{After: ticks.after})
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{p}, Fake: true, Locale: "en", System: &app.Recorder{},
		Clipboard: &hostfake.Clipboard{}, Now: clock.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	ctrl := env.Controller
	first := func() core.Entry {
		t.Helper()
		ctrl.Wait()
		s := ctrl.Snapshot()
		if len(s.Sections) == 0 || len(s.Sections[0].Items) == 0 {
			t.Fatalf("empty snapshot: %+v", s)
		}
		return s.Sections[0].Items[0]
	}
	waitFirst := func(title string) {
		t.Helper()
		eventually(t, "the list shows "+title, func() bool { return first().Title == title })
	}

	// Root search: invalidated every second, pulled again by the controller.
	ctrl.SetText(t.Context(), "time bj")
	waitFirst("Beijing  18:00:00")
	ticks.tick(t)
	waitFirst("Beijing  18:00:01")
	ticks.tick(t)
	waitFirst("Beijing  18:00:02")
	// Cleared text: the root no longer asks the plugin, the loop ends.
	ctrl.SetText(t.Context(), "")
	ctrl.Wait()
	ticks.tick(t) // pulled since the last tick (before the text was cleared)
	ticks.tick(t) // first miss
	ticks.tick(t) // second miss: stop
	eventually(t, "the root loop stopped", func() bool { return p.loops.Running() == 0 })

	// The view gets pushes and stops when closed.
	if _, err := ctrl.OpenCommand(t.Context(), ID, cmdWorld); err != nil {
		t.Fatal(err)
	}
	ctrl.SetText(t.Context(), "tokyo")
	waitFirst("Tokyo  19:00:05")
	ticks.tick(t)
	waitFirst("Tokyo  19:00:06")
	for range 2 { // clear the text, close the view
		if _, err := ctrl.Key(t.Context(), "Escape"); err != nil {
			t.Fatal(err)
		}
		ctrl.Wait()
	}
	eventually(t, "the view loop stopped", func() bool { return p.loops.Running() == 0 })

	// Hiding the launcher stops the root refresh.
	ctrl.SetText(t.Context(), "time")
	ctrl.Wait()
	if p.loops.Running() != 1 {
		t.Fatalf("%d loops", p.loops.Running())
	}
	env.Services.Bus.Publish(plugin.EventWindowHidden)
	eventually(t, "the loops stopped", func() bool { return p.loops.Running() == 0 })
}
