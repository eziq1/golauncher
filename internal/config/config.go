// Package config loads, saves and watches the user configuration
// (%APPDATA%\golauncher\config.json).
package config

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// SchemaVersion is the current config schema version.
const SchemaVersion = 1

// DefaultHotkey shows and hides the launcher.
const DefaultHotkey = "Alt+Space"

// HotkeyNone disables a command's default hotkey.
const HotkeyNone = "none"

// Config is the user configuration.
type Config struct {
	SchemaVersion int    `json:"schemaVersion"`
	Hotkey        string `json:"hotkey"`
	Theme         string `json:"theme"`    // "system", "light" or "dark"
	Language      string `json:"language"` // "system" or a BCP 47 tag
	HideOnBlur    bool   `json:"hideOnBlur"`
	LaunchAtLogin bool   `json:"launchAtLogin"`
	LogLevel      string `json:"logLevel,omitempty"` // "debug", "info" (default), "warn", "error"
	// Plugins holds per-plugin settings keyed by plugin ID.
	Plugins map[string]PluginConfig `json:"plugins,omitempty"`
}

// PluginConfig is the configuration of one plugin.
type PluginConfig struct {
	Disabled    bool                      `json:"disabled,omitzero"`
	Preferences map[string]jsontext.Value `json:"preferences,omitempty"`
	Commands    map[string]CommandConfig  `json:"commands,omitempty"`
}

// CommandConfig is the configuration of one command.
type CommandConfig struct {
	Alias string `json:"alias,omitempty"`
	// Hotkey overrides the command's default hotkey; HotkeyNone disables it.
	Hotkey       string `json:"hotkey,omitempty"`
	HideFromRoot bool   `json:"hideFromRoot,omitzero"`
}

// Default returns the default configuration.
func Default() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Hotkey:        DefaultHotkey,
		Theme:         "system",
		Language:      "system",
		HideOnBlur:    true,
	}
}

// Clone returns a deep copy of c.
func (c Config) Clone() Config {
	out := c
	out.Plugins = make(map[string]PluginConfig, len(c.Plugins))
	for id, p := range c.Plugins {
		p.Preferences = maps.Clone(p.Preferences)
		for k, v := range p.Preferences {
			p.Preferences[k] = slices.Clone(v)
		}
		p.Commands = maps.Clone(p.Commands)
		out.Plugins[id] = p
	}
	return out
}

// Plugin returns the configuration of plugin id (zero if absent).
func (c Config) Plugin(id string) PluginConfig { return c.Plugins[id] }

// Load reads the configuration at path. A missing file yields Default().
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return migrate(cfg)
}

// migrate upgrades cfg to SchemaVersion.
func migrate(cfg Config) (Config, error) {
	switch {
	case cfg.SchemaVersion > SchemaVersion:
		return Config{}, fmt.Errorf("config: schema version %d is newer than supported %d", cfg.SchemaVersion, SchemaVersion)
	case cfg.SchemaVersion == 0:
		cfg.SchemaVersion = SchemaVersion
	}
	if cfg.Hotkey == "" {
		cfg.Hotkey = DefaultHotkey
	}
	return cfg, nil
}

// Save writes cfg to path atomically.
func Save(path string, cfg Config) error {
	data, err := json.Marshal(cfg, jsontext.WithIndent("  "), json.Deterministic(true))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Store holds the live configuration and notifies listeners of changes.
// It is safe for concurrent use.
type Store struct {
	mu        sync.Mutex
	path      string // "" keeps the configuration in memory only
	cfg       Config
	listeners []func(Config)
}

// Open loads the configuration at path into a Store.
func Open(path string) (*Store, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &Store{path: path, cfg: cfg}, nil
}

// NewMemory returns a Store that is never written to disk.
func NewMemory(cfg Config) *Store { return &Store{cfg: cfg} }

// Path returns the file backing the store, or "".
func (s *Store) Path() string { return s.path }

// Get returns a copy of the current configuration.
func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Clone()
}

// Update applies fn to a copy of the configuration, saves it and notifies
// listeners.
func (s *Store) Update(fn func(*Config)) error {
	s.mu.Lock()
	next := s.cfg.Clone()
	fn(&next)
	if s.path != "" {
		if err := Save(s.path, next); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.cfg = next
	listeners := slices.Clone(s.listeners)
	s.mu.Unlock()
	for _, l := range listeners {
		l(next.Clone())
	}
	return nil
}

// OnChange registers fn to run after every Update.
func (s *Store) OnChange(fn func(Config)) {
	s.mu.Lock()
	s.listeners = append(s.listeners, fn)
	s.mu.Unlock()
}
