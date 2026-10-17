package core_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

// The plugin lifecycle (docs/plugin-guide.md "Lifecycle", R88): a plugin
// turned off in the settings does not run, one turned off while the launcher
// runs is closed, and one turned on again is initialized again.

const lifeEvent = "life-test-event"

// lifePlugin counts its Init and Close calls and the events it receives
// through the Host it was given.
type lifePlugin struct {
	id      string
	initErr error // returned by Init after subscribing
	log     *[]string

	mu     sync.Mutex
	h      plugin.Host
	unsub  func()
	inits  int
	closes int
	events atomic.Int32
	calls  atomic.Int32 // RootItems, View and HandleAction
}

func (p *lifePlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: p.id, Name: "Life " + p.id, Version: "1",
		Commands: []plugin.Command{
			{Name: "show", Title: "Life Show " + p.id, Mode: plugin.ModeView},
			{Name: "poke", Title: "Life Poke " + p.id, Mode: plugin.ModeNoView},
		}}
}

func (p *lifePlugin) Init(_ context.Context, h plugin.Host) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inits++
	p.h = h
	// Subscribed before failing: the host must not keep calling a plugin
	// whose Init failed.
	p.unsub = h.Events().Subscribe(lifeEvent, func() { p.events.Add(1) })
	if p.log != nil {
		*p.log = append(*p.log, "init "+p.id)
	}
	return p.initErr
}

func (p *lifePlugin) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closes++
	if p.unsub != nil && p.initErr == nil { // a failed Init leaves cleaning up to the host
		p.unsub()
		p.unsub = nil
	}
	if p.log != nil {
		*p.log = append(*p.log, "close "+p.id)
	}
	return nil
}

func (p *lifePlugin) counts() (inits, closes int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inits, p.closes
}

func (p *lifePlugin) host() plugin.Host {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.h
}

func (p *lifePlugin) RootItems(context.Context, plugin.Query) ([]plugin.Item, error) {
	p.calls.Add(1)
	return []plugin.Item{{ID: "row", Title: "Lifeline Row " + p.id, Actions: []plugin.Action{
		{ID: "open", Title: "Open", Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: "x"}}}}}, nil
}

func (p *lifePlugin) View(context.Context, plugin.ViewRequest) (plugin.View, error) {
	p.calls.Add(1)
	return plugin.View{Kind: plugin.ViewList, Title: "Life", Sections: []plugin.Section{{Items: []plugin.Item{{ID: "a", Title: "A"}}}}}, nil
}

func (p *lifePlugin) HandleAction(context.Context, plugin.ActionDo) (plugin.Result, error) {
	p.calls.Add(1)
	return plugin.Result{}, nil
}

// buildConfigured builds a launcher environment whose configuration is cfg
// from the start (the fake environment always starts from the defaults).
func buildConfigured(t *testing.T, cfg config.Config, plugins ...plugin.Plugin) *app.Env {
	t.Helper()
	dir := t.TempDir()
	if err := config.Save(filepath.Join(dir, "config.json"), cfg); err != nil {
		t.Fatal(err)
	}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: plugins, ConfigDir: dir, DataDir: filepath.Join(dir, "data"), CacheDir: filepath.Join(dir, "cache"),
		Secrets: host.NewMemorySecrets(), TaskRunner: &tasks.FakeRunner{}, Locale: "en",
		System: &app.Recorder{}, Clipboard: &hostfake.Clipboard{}, Now: func() time.Time { return hostfake.Fixed },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	return env
}

func disabled(ids ...string) config.Config {
	cfg := config.Default()
	cfg.Plugins = map[string]config.PluginConfig{}
	for _, id := range ids {
		cfg.Plugins[id] = config.PluginConfig{Disabled: true}
	}
	return cfg
}

func setDisabled(t *testing.T, env *app.Env, id string, off bool) {
	t.Helper()
	if err := env.Config.Update(func(c *config.Config) {
		if c.Plugins == nil {
			c.Plugins = map[string]config.PluginConfig{}
		}
		pc := c.Plugins[id]
		pc.Disabled = off
		c.Plugins[id] = pc
	}); err != nil {
		t.Fatal(err)
	}
	env.Controller.Wait() // the engine applies the change in the background
}

func TestDisabledAtStartupIsNotInitialized(t *testing.T) {
	p := &lifePlugin{id: "life"}
	env := buildConfigured(t, disabled("life"), p)
	if inits, _ := p.counts(); inits != 0 {
		t.Fatalf("a plugin disabled in the configuration was initialized %d times", inits)
	}
	env.Services.Bus.PublishSync(lifeEvent)
	if n := p.events.Load(); n != 0 {
		t.Fatalf("a disabled plugin received %d events", n)
	}
	// Settings still list it, with its manifest, so it can be turned on.
	infos := withoutTasks(env.Engine.Plugins())
	i := slices.IndexFunc(infos, func(info core.PluginInfo) bool { return info.ID == "life" })
	if i < 0 || infos[i].Enabled || infos[i].Unavailable != "" || len(infos[i].Commands) != 2 || infos[i].Name != "Life life" {
		t.Fatalf("plugins = %+v", infos)
	}
	// Its command is not run by a hotkey (Engine.RunCommand) either.
	if _, err := env.Engine.RunCommand(t.Context(), "life", "poke"); err == nil || p.calls.Load() != 0 {
		t.Fatalf("RunCommand of a disabled plugin: err %v, %d calls", err, p.calls.Load())
	}

	// Turned on: initialized, listed, and receiving events.
	setDisabled(t, env, "life", false)
	if inits, _ := p.counts(); inits != 1 {
		t.Fatalf("turned on: %d inits", inits)
	}
	env.Services.Bus.PublishSync(lifeEvent)
	if n := p.events.Load(); n != 1 {
		t.Fatalf("turned on: %d events", n)
	}
	env.Controller.SetText(t.Context(), "lifeline")
	env.Controller.Wait()
	if got := titles(env.Controller.Snapshot()); !slices.Contains(got, "Lifeline Row life") {
		t.Fatalf("root after turning on = %v", got)
	}
}

func TestDisabledAtRunTimeIsClosed(t *testing.T) {
	p := &lifePlugin{id: "life"}
	other := &lifePlugin{id: "other"}
	env := buildConfigured(t, config.Default(), p, other)
	invalidated := atomic.Int32{}
	remove := env.Engine.OnInvalidate(func(pluginID, _ string) {
		if pluginID == "life" {
			invalidated.Add(1)
		}
	})
	defer remove()
	toasts := atomic.Int32{}
	env.Engine.SetNotifier(func(n core.Notice) {
		if n.Plugin == "life" {
			toasts.Add(1)
		}
	})
	// The root shows the plugin and a view of it is open.
	if _, err := env.Controller.OpenCommand(t.Context(), "life", "show"); err != nil {
		t.Fatal(err)
	}
	env.Controller.Wait()
	if s := env.Controller.Snapshot(); s.Depth != 1 {
		t.Fatalf("view not open: %+v", s)
	}
	old := p.host()

	setDisabled(t, env, "life", true)
	if inits, closes := p.counts(); inits != 1 || closes != 1 {
		t.Fatalf("disabled at run time: %d inits, %d closes", inits, closes)
	}
	if inits, closes := other.counts(); inits != 1 || closes != 0 {
		t.Fatalf("the other plugin: %d inits, %d closes", inits, closes)
	}
	if s := env.Controller.Snapshot(); s.Depth != 0 {
		t.Fatalf("the disabled plugin's view is still open: %+v", s)
	}
	// Nothing reaches it any more, and what its old Host does is dropped.
	calls := p.calls.Load()
	env.Services.Bus.PublishSync(lifeEvent)
	old.Invalidate("")
	old.Toast(plugin.Toast{Title: "late"})
	if p.events.Load() != 0 || invalidated.Load() != 0 || toasts.Load() != 0 {
		t.Fatalf("after Close: %d events, %d invalidations, %d toasts", p.events.Load(), invalidated.Load(), toasts.Load())
	}
	env.Controller.SetText(t.Context(), "lifeline")
	env.Controller.Wait()
	if got := titles(env.Controller.Snapshot()); slices.Contains(got, "Lifeline Row life") || !slices.Contains(got, "Lifeline Row other") {
		t.Fatalf("root = %v", got)
	}
	if _, err := env.Engine.View(t.Context(), core.Nav{Plugin: "life", Request: plugin.ViewRequest{ID: "show"}}); err == nil {
		t.Fatal("View of a disabled plugin succeeded")
	}
	if _, err := env.Engine.RunCommand(t.Context(), "life", "poke"); err == nil {
		t.Fatal("RunCommand of a disabled plugin succeeded")
	}
	if p.calls.Load() != calls {
		t.Fatalf("a disabled plugin was called %d times", p.calls.Load()-calls)
	}

	// Turned on again: a new Init with a new Host, and events again.
	setDisabled(t, env, "life", false)
	if inits, closes := p.counts(); inits != 2 || closes != 1 {
		t.Fatalf("enabled again: %d inits, %d closes", inits, closes)
	}
	if p.host() == old {
		t.Fatal("re-initialized with the Host of the closed run")
	}
	env.Services.Bus.PublishSync(lifeEvent)
	if n := p.events.Load(); n != 1 {
		t.Fatalf("enabled again: %d events", n)
	}
	p.host().Invalidate("")
	if invalidated.Load() != 1 {
		t.Fatal("the new Host's invalidation was dropped")
	}
	env.Controller.SetText(t.Context(), "lifeline ")
	env.Controller.Wait()
	if got := titles(env.Controller.Snapshot()); !slices.Contains(got, "Lifeline Row life") {
		t.Fatalf("root after enabling again = %v", got)
	}
}

// TestFailedInitIsCleanedUp: a plugin whose Init fails after subscribing is
// closed, and the host drops its subscriptions (R88, review 2.3).
func TestFailedInitIsCleanedUp(t *testing.T) {
	bad := &lifePlugin{id: "bad", initErr: errors.New("broken")}
	gone := &lifePlugin{id: "gone", initErr: fmt.Errorf("Gone is not installed: %w", plugin.ErrUnavailable)}
	env := buildWith(t, bad, gone)
	for _, p := range []*lifePlugin{bad, gone} {
		if inits, closes := p.counts(); inits != 1 || closes != 1 {
			t.Errorf("%s: %d inits, %d closes after a failed Init", p.id, inits, closes)
		}
	}
	env.Services.Bus.PublishSync(lifeEvent)
	if n := bad.events.Load() + gone.events.Load(); n != 0 {
		t.Fatalf("plugins whose Init failed received %d events", n)
	}
	infos := withoutTasks(env.Engine.Plugins())
	if len(infos) != 2 || !strings.Contains(infos[0].Unavailable, "broken") || infos[0].Enabled {
		t.Fatalf("plugins = %+v", infos)
	}
}

// TestNewRollsBack: when New fails after initializing some plugins, they are
// closed again, the last first (R88, review 2.3).
func TestNewRollsBack(t *testing.T) {
	var log []string
	a := &lifePlugin{id: "a", log: &log}
	b := &lifePlugin{id: "b", log: &log}
	// A plugin ID that cannot be a directory name: its Host cannot be made.
	bad := &lifePlugin{id: "bad\x00id", log: &log}
	_, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{a, b, bad}, Fake: true, Locale: "en", System: &app.Recorder{}, Clipboard: &hostfake.Clipboard{},
	})
	if err == nil {
		t.Fatal("Build succeeded")
	}
	for _, p := range []*lifePlugin{a, b} {
		if inits, closes := p.counts(); inits != closes {
			t.Errorf("%s: %d inits, %d closes", p.id, inits, closes)
		}
	}
	if want := []string{"init a", "init b", "close b", "close a"}; !slices.Equal(log, want) {
		t.Errorf("lifecycle = %q, want %q", log, want)
	}

	// A duplicate ID: nothing stays initialized either.
	log = nil
	c := &lifePlugin{id: "c", log: &log}
	dup := &lifePlugin{id: "c", log: &log}
	if _, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{c, dup}, Fake: true, Locale: "en", System: &app.Recorder{}, Clipboard: &hostfake.Clipboard{},
	}); err == nil {
		t.Fatal("Build with a duplicate ID succeeded")
	}
	for _, p := range []*lifePlugin{c, dup} {
		if inits, closes := p.counts(); inits != closes {
			t.Errorf("duplicate %s: %d inits, %d closes", p.id, inits, closes)
		}
	}
}

// TestCloseReverseOrder: Engine.Close closes the plugins in the reverse
// order of their Init, so a plugin initialized later (which may rely on an
// earlier one) is closed first.
func TestCloseReverseOrder(t *testing.T) {
	var log []string
	a := &lifePlugin{id: "a", log: &log}
	b := &lifePlugin{id: "b", log: &log}
	c := &lifePlugin{id: "c", log: &log}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{a, b, c}, Fake: true, Locale: "en", System: &app.Recorder{}, Clipboard: &hostfake.Clipboard{},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Turned off and on again, a is now the last initialized.
	setDisabled(t, env, "a", true)
	setDisabled(t, env, "a", false)
	log = nil
	if err := env.Close(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"close a", "close c", "close b"}; !slices.Equal(log, want) {
		t.Errorf("close order = %q, want %q", log, want)
	}
}
