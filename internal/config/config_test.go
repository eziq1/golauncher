package config

import (
	"os"
	"path/filepath"
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

func TestGetIsACopy(t *testing.T) {
	s := NewMemory(Default())
	s.Update(func(c *Config) { c.Plugins = map[string]PluginConfig{"x": {}} })
	c := s.Get()
	c.Plugins["y"] = PluginConfig{}
	if _, ok := s.Get().Plugins["y"]; ok {
		t.Fatal("Get leaked internal map")
	}
}
