package ui_test

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// TestSettingsModelsAndEfforts: Codex's models and the efforts come from the
// backends (R109); choosing a model loads the efforts it takes, and
// Re-detect lists the models again.
func TestSettingsModelsAndEfforts(t *testing.T) {
	codex := &backend.FakeInstalls{Fake: &backend.Fake{}, BackendName: "Codex (ChatGPT)", Found: backend.Discovery{
		MinVersion: "0.130.0", Auto: 0, AutoReason: backend.ReasonOnly, Status: backend.StatusAvailable,
		Installs: []backend.Install{{Path: appCodex, Source: backend.SourceApp, Version: "0.163.0"}}},
		ModelList: backend.ModelList{Models: []backend.Model{
			{ID: "big", Name: "Big One", Efforts: []string{"low", "medium", "high", "xhigh"}},
			{ID: "small", Name: "Small One", Efforts: []string{"low", "medium", "high"}},
		}}}
	claude := &backend.FakeInstalls{Fake: &backend.Fake{}, EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}}
	clip := &hostfake.Clipboard{}
	p := ai.NewWithOptions(ai.Options{Sync: true, HasProtocol: func(string) bool { return false }, Backends: []ai.BackendOption{
		{ID: ai.DefaultBackendID, Backend: claude, Efforts: true},
		{ID: "codex", Backend: codex, DynamicModels: true, Efforts: true},
	}})
	env, err := app.Build(t.Context(), app.Options{Plugins: []plugin.Plugin{p}, Fake: true, Locale: "en",
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	// Chosen before: the selects show these (the others show "Default").
	env.Config.Update(func(c *config.Config) {
		c.Plugins = map[string]config.PluginConfig{ai.ID: {Preferences: map[string]jsontext.Value{
			"model.codex": jsontext.Value(`"small"`), "effort.codex": jsontext.Value(`"medium"`)}}}
	})
	s := ui.NewSettings(env, &fakeHost{}, "1.0.0-test")
	tt := mui.NewTester(s.View, ui.SettingsWidth, 3200)
	s.ShowPage(ui.PagePlugins)
	tt.Frame()
	if err := tt.Click("Configure"); err != nil {
		t.Fatal(err)
	}
	settle(s, tt)
	for _, want := range []string{"Model (Codex)", "Reasoning effort (Codex)", "Reasoning effort (Claude Code)"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	open := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatalf("%v in %q", err, tt.Texts())
		}
		tt.Frame()
	}
	choose := func(option string) {
		t.Helper()
		if err := tt.Click(option); err != nil {
			t.Fatalf("%v in %q", err, tt.Texts())
		}
		settle(s, tt)
	}
	pref := func(name string) string {
		return strings.Trim(string(env.Config.Get().Plugin(ai.ID).Preferences[name]), `"`)
	}

	// The levels Small One takes.
	open("Medium")
	if !tt.HasText("High") || tt.HasText("Extra high") {
		t.Fatalf("efforts of Small One: %q", tt.Texts())
	}
	choose("High")
	if pref("effort.codex") != "high" {
		t.Fatalf("effort %q", pref("effort.codex"))
	}
	// Choosing another model loads its levels.
	open("Small One")
	choose("Big One")
	if pref("model.codex") != "big" {
		t.Fatalf("model %q", pref("model.codex"))
	}
	open("High")
	if !tt.HasText("Extra high") {
		t.Fatalf("efforts of Big One: %q", tt.Texts())
	}
	choose("Extra high")

	listed, _ := codex.Listed()
	if err := tt.Click("Re-detect"); err != nil {
		t.Fatal(err)
	}
	settle(s, tt)
	settle(s, tt)
	if again, redetects := codex.Listed(); redetects != 1 || again <= listed {
		t.Fatalf("Re-detect: listed %d → %d, redetects %d", listed, again, redetects)
	}
}
