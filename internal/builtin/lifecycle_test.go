package builtin_test

import (
	"slices"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/builtin"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/extproc"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

const fixtures = "../../testdata/fixtures"

// queries touch the root items of most built-in plugins.
var queries = []string{"1+1", "100 usd", "timer 25m", "time tokyo", "notepad", "terminal", "ping", "ip", "recycle",
	"g hello", "ask ai", "scoop", "winget", "processes", "clipboard", "files", "script", "world clock"}

func snapshot(t *testing.T, env *app.Env) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, q := range queries {
		secs, err := env.Engine.RootSearch(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range secs {
			for _, it := range s.Items {
				out[q] = append(out[q], it.Key+" "+it.Title)
			}
		}
	}
	return out
}

func states(env *app.Env) map[string]string {
	out := map[string]string{}
	for _, p := range env.Engine.Plugins() {
		out[p.ID] = p.Unavailable
	}
	return out
}

// TestPluginsStartAgain turns every built-in plugin off and on again in
// the running engine (Close, then Init on the same plugin value, docs/
// plugin-guide.md "Lifecycle"): each must come back as it was, with the
// same root results.
func TestPluginsStartAgain(t *testing.T) {
	t.Setenv(extproc.EnvPluginPath, "")
	env, err := app.Build(t.Context(), app.Options{
		Plugins: builtin.Fake(fixtures), Fake: true, Locale: "en", System: &app.Recorder{},
		Clipboard: &hostfake.Clipboard{}, Now: func() time.Time { return hostfake.Fixed },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	env.Controller.Wait()
	before, wantStates := snapshot(t, env), states(env)

	set := func(id string, off bool) {
		t.Helper()
		if err := env.Config.Update(func(c *config.Config) {
			c.Plugins = map[string]config.PluginConfig{id: {Disabled: off}}
		}); err != nil {
			t.Fatal(err)
		}
		env.Controller.Wait()
	}
	for _, p := range env.Engine.Plugins() {
		set(p.ID, true)
		set(p.ID, false)
		if got := states(env)[p.ID]; got != wantStates[p.ID] {
			t.Errorf("%s: unavailable %q after turning it on again, was %q", p.ID, got, wantStates[p.ID])
		}
	}
	after := snapshot(t, env)
	for _, q := range queries {
		if !slices.Equal(before[q], after[q]) {
			t.Errorf("%q:\nbefore %q\nafter  %q", q, before[q], after[q])
		}
	}
	// The engine still answers views of plugins started again.
	if _, err := env.Engine.View(t.Context(), core.Nav{Plugin: "builtin.timer", Request: plugin.ViewRequest{ID: "timers"}}); err != nil {
		t.Errorf("timers view: %v", err)
	}
}
