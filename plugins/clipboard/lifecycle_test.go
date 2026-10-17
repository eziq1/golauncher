package clipboard

import (
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/tasks"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Clipboard history in the real engine with the plugin turned off (review
// finding 2.1, R88): nothing copied while it is off is recorded.

type hostEnv struct {
	*app.Env
	clip *hostfake.Clipboard
}

// buildHost starts the launcher with the clipboard plugin, turned off in the
// configuration when off is set.
func buildHost(t *testing.T, off bool) *hostEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	if off {
		cfg.Plugins = map[string]config.PluginConfig{ID: {Disabled: true}}
	}
	if err := config.Save(filepath.Join(dir, "config.json"), cfg); err != nil {
		t.Fatal(err)
	}
	clip := &hostfake.Clipboard{}
	var ticks atomic.Int64 // every reading of the clock is a second later
	now := func() time.Time { return hostfake.Fixed.Add(time.Duration(ticks.Add(1)) * time.Second) }
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{NewWithOptions(Options{})}, ConfigDir: dir,
		DataDir: filepath.Join(dir, "data"), CacheDir: filepath.Join(dir, "cache"),
		Secrets: host.NewMemorySecrets(), TaskRunner: &tasks.FakeRunner{}, Locale: "en",
		System: &app.Recorder{}, Clipboard: clip, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	return &hostEnv{env, clip}
}

// copyText is another application copying s.
func (e *hostEnv) copyText(s string) {
	e.clip.Set(plugin.ClipboardContent{Text: s})
	e.Services.Bus.PublishSync(plugin.EventClipboardChanged)
}

func (e *hostEnv) setOff(t *testing.T, off bool) {
	t.Helper()
	if err := e.Config.Update(func(c *config.Config) {
		if c.Plugins == nil {
			c.Plugins = map[string]config.PluginConfig{}
		}
		c.Plugins[ID] = config.PluginConfig{Disabled: off}
	}); err != nil {
		t.Fatal(err)
	}
	e.Controller.Wait() // the engine closes or starts the plugin in the background
}

// history returns the titles of the recorded clips.
func (e *hostEnv) history(t *testing.T) []string {
	t.Helper()
	v, err := e.Engine.View(t.Context(), core.Nav{Plugin: ID, Request: plugin.ViewRequest{ID: viewHistory}})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range items(v) {
		out = append(out, it.Title)
	}
	return out
}

func TestDisabledAtStartupRecordsNothing(t *testing.T) {
	e := buildHost(t, true)
	e.copyText("secret while off")
	e.setOff(t, false)
	e.copyText("copied while on")
	if got := e.history(t); !slices.Equal(got, []string{"copied while on"}) {
		t.Fatalf("history = %q", got)
	}
}

func TestDisabledAtRunTimeRecordsNothing(t *testing.T) {
	e := buildHost(t, false)
	e.copyText("first")
	e.setOff(t, true)
	e.copyText("secret while off")
	e.setOff(t, false)
	e.copyText("again")
	if got := e.history(t); !slices.Equal(got, []string{"again", "first"}) {
		t.Fatalf("history = %q", got)
	}
}
