package ui_test

import (
	"encoding/json/jsontext"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// TestRingAskAIFileChip (R123): the Action Ring's Ask AI on a file runs
// while the launcher is hidden; once it shows, the chat shows the file's
// chip at once, before anything is typed.
func TestRingAskAIFileChip(t *testing.T) {
	fake := &backend.Fake{}
	fake.Go = func(fn func()) { go fn() }
	clip := &hostfake.Clipboard{}
	rec := &app.Recorder{Clipboard: clip}
	p := ai.NewWithOptions(ai.Options{Backend: fake, HasProtocol: func(string) bool { return false }})
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{testPlugin{}, p}, Fake: true, Locale: "en", System: rec, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	if err := env.Config.Update(func(c *config.Config) {
		c.Plugins = map[string]config.PluginConfig{ai.ID: {Preferences: map[string]jsontext.Value{"enabled": jsontext.Value("true")}}}
	}); err != nil {
		t.Fatal(err)
	}
	lt := &launcherTest{Env: env, t: t, rec: rec, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	lt.l.Hidden()
	lt.step()

	file := filepath.Join(t.TempDir(), "notes.png")
	if err := os.WriteFile(file, solidPNG(t, 64, 48, color.NRGBA{G: 200, A: 255}), 0o644); err != nil {
		t.Fatal(err)
	}
	item := plugin.Item{ID: "ring-ask", Transient: true, Actions: []plugin.Action{{ID: "ask",
		Do: plugin.ActionDo{Kind: plugin.ActionAskAI, Payload: plugin.MustPayload(plugin.AskAI{Files: []string{file}})}}}}
	done := make(chan bool, 1)
	lt.l.RunEntry(core.Entry{Item: item, Plugin: ai.ID, Key: ai.ID + "/ring-ask", Context: true},
		func(_ core.Outcome, _ error, show bool) { done <- show })
	if !<-done {
		t.Fatal("the chat has nothing to show")
	}
	// The show pulls the chat again while the image is being prepared (the
	// race of R123): the chip must come without typing.
	lt.l.ShownWith(core.ShowOptions{Current: true})
	lt.waitFor("the file's chip", func(string) bool { return lt.tt.HasText("notes.png") })
	if s := lt.Controller.Snapshot(); s.Text != "" || len(s.Chips) != 1 {
		t.Fatalf("text %q chips %+v", s.Text, s.Chips)
	}
}
