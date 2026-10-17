package timer

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestDisabledSendsNoNotifications runs the plugin in the real engine: once
// it is turned off in the settings it is closed, and a timer that ends
// meanwhile is not notified (review finding 2.1, R88). Turned on again, the
// plugin starts again and announces the timer that ended while it was off.
func TestDisabledSendsNoNotifications(t *testing.T) {
	clock := &fakeClock{t: testNow}
	waits := &fakeWaits{}
	p := NewWithOptions(Options{After: (&fakeTicks{ch: make(chan time.Time)}).after, WaitAfter: waits.after, StartupDelay: -1})
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{p}, Fake: true, Locale: "en", System: &app.Recorder{},
		Clipboard: &hostfake.Clipboard{}, Now: clock.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	var (
		mu      sync.Mutex
		notices []string
	)
	env.Engine.SetNotifier(func(n core.Notice) {
		mu.Lock()
		notices = append(notices, n.Toast.Title)
		mu.Unlock()
	})
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(notices)
	}
	setOff := func(off bool) {
		t.Helper()
		if err := env.Config.Update(func(c *config.Config) {
			c.Plugins = map[string]config.PluginConfig{ID: {Disabled: off}}
		}); err != nil {
			t.Fatal(err)
		}
		env.Controller.Wait()
	}

	items, err := p.RootItems(t.Context(), plugin.Query{Text: "timer 25m tea"})
	if err != nil || len(items) == 0 {
		t.Fatalf("items %v, err %v", items, err)
	}
	if _, err := p.HandleAction(t.Context(), items[0].Actions[0].Do); err != nil {
		t.Fatal(err)
	}

	setOff(true)
	clock.advance(26 * time.Minute)
	checks := p.checks.Load()
	select {
	case p.wake <- struct{}{}: // a running scheduler would look now
	default:
	}
	waits.fire()
	time.Sleep(100 * time.Millisecond)
	if n := count(); n != 0 || p.checks.Load() != checks {
		t.Fatalf("turned off: %d notifications, %d checks", n, p.checks.Load()-checks)
	}

	setOff(false)
	eventually(t, "the timer that ended while off is announced", func() bool { return count() == 1 })
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(notices[0], "tea") {
		t.Fatalf("notices = %q", notices)
	}
}
