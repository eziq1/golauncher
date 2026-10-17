package config

import (
	"encoding/json/jsontext"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingIsDefault(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hotkey != DefaultHotkey || cfg.SchemaVersion != SchemaVersion || !cfg.HideOnBlur {
		t.Fatalf("unexpected default: %+v", cfg)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var seen Config
	s.OnChange(func(c Config) { seen = c })
	err = s.Update(func(c *Config) {
		c.Theme = "dark"
		c.Plugins = map[string]PluginConfig{"x": {Commands: map[string]CommandConfig{"a": {Alias: "aa"}}}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen.Theme != "dark" {
		t.Fatal("listener not called")
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Theme != "dark" || again.Plugins["x"].Commands["a"].Alias != "aa" {
		t.Fatalf("reloaded config = %+v", again)
	}
}

func TestNewerSchemaRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"schemaVersion": 99}`), 0o644)
	if _, err := Load(path); err == nil {
		t.Fatal("want error for newer schema")
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	cfg := Default()
	cfg.Theme, cfg.Hotkey, cfg.LogLevel = "dark", "Ctrl+Space", "debug"
	cfg.Plugins = map[string]PluginConfig{"x": {
		Disabled:    true,
		Preferences: map[string]jsontext.Value{"n": jsontext.Value("3")},
		Commands:    map[string]CommandConfig{"a": {Alias: "aa", Hotkey: HotkeyNone}},
	}}
	data, err := Export(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\n  \"") {
		t.Fatalf("export is not indented: %s", data)
	}
	got, err := Import(data)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Export(got)
	if string(again) != string(data) {
		t.Fatalf("round trip changed the config:\n%s\n%s", data, again)
	}
}

func TestImportFillsDefaults(t *testing.T) {
	got, err := Import([]byte(`{"schemaVersion": 1, "theme": "light"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.Theme = "light"
	if got.Theme != "light" || got.Hotkey != DefaultHotkey || !got.HideOnBlur || got.Language != want.Language {
		t.Fatalf("imported = %+v", got)
	}
	if got, err := Import([]byte(`{}`)); err != nil || got.SchemaVersion != SchemaVersion {
		t.Fatalf("import of {} = %+v, %v", got, err)
	}
}

func TestImportRejects(t *testing.T) {
	for name, data := range map[string]string{
		"newer schema":   `{"schemaVersion": 99}`,
		"unknown field":  `{"schemaVersion": 1, "colour": "red"}`,
		"unknown nested": `{"schemaVersion": 1, "plugins": {"x": {"bogus": true}}}`,
		"invalid json":   `{"schemaVersion": 1,`,
		"not an object":  `[1, 2]`,
		"empty":          ``,
		"wrong type":     `{"hideOnBlur": "yes"}`,
	} {
		if _, err := Import([]byte(data)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestGetIsACopy(t *testing.T) {
	s := NewMemory(Default())
	s.Update(func(c *Config) { c.Plugins = map[string]PluginConfig{"x": {}} })
	c := s.Get()
	c.Plugins["y"] = PluginConfig{}
	if _, ok := s.Get().Plugins["y"]; ok {
		t.Fatal("Get leaked internal map")
	}
}

func TestOpenRecoversFromBadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"theme": `), 0o644)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Recovered() == nil || s.Get().Hotkey != DefaultHotkey {
		t.Fatalf("recovered = %v, cfg = %+v", s.Recovered(), s.Get())
	}
	if b, err := os.ReadFile(path + ".bad"); err != nil || string(b) != `{"theme": ` {
		t.Fatalf("bad file not kept: %q %v", b, err)
	}
	if err := s.Update(func(c *Config) { c.Theme = "dark" }); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(path); err != nil || c.Theme != "dark" {
		t.Fatalf("reload = %+v %v", c, err)
	}
}

func TestMigrateThemeToAppearance(t *testing.T) {
	for _, tc := range []struct {
		theme        string
		followSystem bool
		fixed        string
	}{
		{"system", true, DefaultLightTheme},
		{"", true, DefaultLightTheme},
		{"light", false, DefaultLightTheme},
		{"dark", false, DefaultDarkTheme},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(path, []byte(`{"schemaVersion": 1, "theme": "`+tc.theme+`"}`), 0o644)
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		a := cfg.Appearance
		if a.FollowSystem != tc.followSystem || a.Theme != tc.fixed || a.LightTheme != DefaultLightTheme ||
			a.DarkTheme != DefaultDarkTheme || a.Accent != AccentTheme || a.WindowOpacity != 1 || a.TextShadow || a.Opacity != nil {
			t.Errorf("theme %q: appearance = %+v", tc.theme, a)
		}
	}
}

func TestAppearanceKeptAndNormalized(t *testing.T) {
	// An appearance in the file wins over the old setting; what it leaves
	// out gets its default, and numbers are kept in range.
	got, err := Import([]byte(`{"schemaVersion": 1, "theme": "light",
		"appearance": {"theme": "nord", "opacity": 1.5, "windowOpacity": 0.2, "radius": 10, "material": "mica"}}`))
	if err != nil {
		t.Fatal(err)
	}
	a := got.Appearance
	if a.Theme != "nord" || a.FollowSystem || *a.Opacity != 1 || a.WindowOpacity != MinWindowOpacity || *a.Radius != 10 ||
		*a.Material != "mica" || a.DarkTheme != DefaultDarkTheme || a.Accent != AccentTheme || a.Density != nil {
		t.Fatalf("appearance = %+v", a)
	}
	// null means the theme's value, and survives a round trip.
	data, _ := Export(Default())
	if !strings.Contains(string(data), `"opacity": null`) {
		t.Fatalf("export = %s", data)
	}
	back, err := Import(data)
	if err != nil || back.Appearance.Opacity != nil || !back.Appearance.FollowSystem {
		t.Fatalf("round trip = %+v, %v", back.Appearance, err)
	}
	a.ResetToTheme()
	if a.Opacity != nil || a.Material != nil || a.Radius != nil || a.Theme != "nord" {
		t.Fatalf("reset = %+v", a)
	}
}

// TestRenamedThemes: a configuration that chose a built-in theme under its
// old ID gets the new one (R56).
func TestRenamedThemes(t *testing.T) {
	got, err := Import([]byte(`{"schemaVersion": 1, "appearance": {"theme": "raycast-dark",
		"lightTheme": "raycast-light", "darkTheme": "raycast-dark"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if a := got.Appearance; a.Theme != "crimson-dark" || a.LightTheme != "crimson-light" || a.DarkTheme != "crimson-dark" {
		t.Fatalf("appearance = %+v", a)
	}
}

func TestCloneCopiesAppearance(t *testing.T) {
	cfg := Default()
	v := 0.5
	cfg.Appearance.Opacity = &v
	c := cfg.Clone()
	*c.Appearance.Opacity = 0.9
	if *cfg.Appearance.Opacity != 0.5 {
		t.Fatal("Clone shares the appearance")
	}
}

func TestLegacyTheme(t *testing.T) {
	a := DefaultAppearance()
	if LegacyTheme(a, true) != "system" {
		t.Fatal("following the system")
	}
	a.FollowSystem = false
	if LegacyTheme(a, true) != "dark" || LegacyTheme(a, false) != "light" {
		t.Fatal("fixed theme")
	}
}

func TestUpdatesDefaultsAndRoundTrip(t *testing.T) {
	var u UpdatesConfig
	if u.EffectiveMode() != UpdatesAuto || u.EffectiveFrequency() != UpdatesDaily {
		t.Fatalf("defaults = %s/%s", u.EffectiveMode(), u.EffectiveFrequency())
	}
	if (UpdatesConfig{Mode: "bogus", Frequency: "hourly"}).EffectiveMode() != UpdatesAuto {
		t.Fatal("an unknown mode is not the default")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	// A file of an older version has no "updates": it loads with the defaults
	// and is written without the field until it is set.
	os.WriteFile(path, []byte(`{"schemaVersion": 1, "hotkey": "Alt+Space"}`), 0o644)
	cfg, err := Load(path)
	if err != nil || cfg.Updates != (UpdatesConfig{}) {
		t.Fatalf("Load = %+v, %v", cfg.Updates, err)
	}
	cfg.Updates = UpdatesConfig{Mode: UpdatesNotify, Frequency: UpdatesWeekly}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil || again.Updates.EffectiveMode() != UpdatesNotify || again.Updates.EffectiveFrequency() != UpdatesWeekly {
		t.Fatalf("reloaded = %+v, %v", again.Updates, err)
	}
	data, _ := Export(again)
	if _, err := Import(data); err != nil {
		t.Fatalf("import of an export with updates: %v", err)
	}
}

// TestReturnToRoot: the field is optional (an older file has none, an older
// version ignores it); unknown values take the default (docs/keep-view.md).
func TestReturnToRoot(t *testing.T) {
	if got := Default().EffectiveReturnToRoot(); got != ReturnToRootAfter {
		t.Fatalf("default = %q", got)
	}
	dir := t.TempDir()
	old := filepath.Join(dir, "old.json")
	if err := os.WriteFile(old, []byte(`{"schemaVersion": 1, "hotkey": "Alt+Space"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(old)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReturnToRoot != "" || cfg.EffectiveReturnToRoot() != ReturnToRootAfter {
		t.Fatalf("old file: %q", cfg.ReturnToRoot)
	}
	// The default is not written, so the file stays as an older version wrote it.
	if data, _ := marshal(Default()); strings.Contains(string(data), "returnToRoot") {
		t.Fatal("the default value is written")
	}
	for v, want := range map[string]string{
		ReturnToRootNever: ReturnToRootNever, ReturnToRootImmediately: ReturnToRootImmediately,
		ReturnToRootAfter: ReturnToRootAfter, "5m": ReturnToRootAfter,
	} {
		cfg, err := Import([]byte(`{"returnToRoot": "` + v + `"}`))
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.EffectiveReturnToRoot(); got != want {
			t.Errorf("%q -> %q, want %q", v, got, want)
		}
	}
}
