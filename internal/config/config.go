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
	// Theme is the appearance before themes ("system", "light" or "dark").
	// Appearance replaced it; it is still written, following Appearance
	// (LegacyTheme), so that an older version keeps light or dark, and a
	// file without Appearance is migrated from it.
	Theme string `json:"theme"`
	// Appearance chooses the theme and adjusts it (docs/themes.md §8.2).
	Appearance Appearance `json:"appearance"`
	Language   string     `json:"language"` // "system" or a BCP 47 tag
	HideOnBlur    bool   `json:"hideOnBlur"`
	LaunchAtLogin bool   `json:"launchAtLogin"`
	LogLevel      string `json:"logLevel,omitempty"` // "debug", "info" (default), "warn", "error"
	// ContextAware lets the launcher look at the window that was active when
	// it opened (docs/context-awareness.md). Off, no other window is read.
	ContextAware bool `json:"contextAware"`
	// Plugins holds per-plugin settings keyed by plugin ID.
	Plugins map[string]PluginConfig `json:"plugins,omitempty"`
	// Agent configures what AI clients may do with the plugins (docs/ai.md §4).
	Agent AgentConfig `json:"agent,omitzero"`
}

// Built-in themes the appearance falls back to.
const (
	DefaultLightTheme = "golauncher-light"
	DefaultDarkTheme  = "golauncher-dark"
)

// Values of Appearance.Accent other than a color.
const (
	AccentTheme   = "theme"   // the theme's own accent color
	AccentWindows = "windows" // the Windows accent color, following its changes
)

// Window opacity bounds (Appearance.WindowOpacity).
const (
	MinWindowOpacity = 0.7
	MaxWindowOpacity = 1
)

// Appearance is the look of the launcher. A nil field takes the value of
// the theme, so that choosing another theme brings its own; a value set
// here stays until "Reset to theme defaults" sets it to nil again.
type Appearance struct {
	// Theme is the theme used when FollowSystem is off.
	Theme string `json:"theme"`
	// FollowSystem uses LightTheme or DarkTheme, as Windows is light or
	// dark.
	FollowSystem bool   `json:"followSystem"`
	LightTheme   string `json:"lightTheme"`
	DarkTheme    string `json:"darkTheme"`
	// Material is "acrylic", "mica" or "none".
	Material *string `json:"material"`
	// Opacity is how much of the theme's background covers the material,
	// 0 to 1.
	Opacity *float64 `json:"opacity"`
	// Accent is AccentTheme, AccentWindows or a color ("#rrggbb").
	Accent string `json:"accent"`
	// Radius rounds corners, in DIPs.
	Radius *float64 `json:"radius"`
	// Density is "compact", "standard" or "comfortable".
	Density *string `json:"density"`
	// FontSize is the size of text in DIPs; nil is the system's.
	FontSize *float64 `json:"fontSize"`
	// WindowOpacity makes the whole window translucent, text included,
	// between MinWindowOpacity and 1.
	WindowOpacity float64 `json:"windowOpacity"`
	// TextShadow draws a faint backdrop under text when the background is
	// very translucent (below 40%).
	TextShadow bool `json:"textShadow"`
}

// DefaultAppearance returns the appearance of a new configuration: the
// golauncher themes, following Windows.
func DefaultAppearance() Appearance {
	return Appearance{
		Theme:         DefaultLightTheme,
		FollowSystem:  true,
		LightTheme:    DefaultLightTheme,
		DarkTheme:     DefaultDarkTheme,
		Accent:        AccentTheme,
		WindowOpacity: 1,
		TextShadow:    true,
	}
}

// Clone returns a deep copy of a.
func (a Appearance) Clone() Appearance {
	a.Material = clonePtr(a.Material)
	a.Opacity = clonePtr(a.Opacity)
	a.Radius = clonePtr(a.Radius)
	a.Density = clonePtr(a.Density)
	a.FontSize = clonePtr(a.FontSize)
	return a
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// ResetToTheme drops the values that override the theme's own: material,
// opacity, accent, radius, density and font size.
func (a *Appearance) ResetToTheme() {
	a.Material, a.Opacity, a.Radius, a.Density, a.FontSize = nil, nil, nil, nil, nil
	a.Accent = AccentTheme
}

// normalize fills in what a partial appearance leaves out and keeps the
// numbers in range.
func (a *Appearance) normalize() {
	d := DefaultAppearance()
	if a.Theme == "" {
		a.Theme = d.Theme
	}
	if a.LightTheme == "" {
		a.LightTheme = d.LightTheme
	}
	if a.DarkTheme == "" {
		a.DarkTheme = d.DarkTheme
	}
	if a.Accent == "" {
		a.Accent = d.Accent
	}
	if a.WindowOpacity == 0 {
		a.WindowOpacity = d.WindowOpacity
	}
	a.WindowOpacity = min(max(a.WindowOpacity, MinWindowOpacity), MaxWindowOpacity)
	if a.Opacity != nil {
		v := min(max(*a.Opacity, 0), 1)
		a.Opacity = &v
	}
}

// AppearanceFromTheme is the appearance an old "theme" setting
// ("system", "light" or "dark") stands for.
func AppearanceFromTheme(theme string) Appearance {
	a := DefaultAppearance()
	switch theme {
	case "light":
		a.FollowSystem, a.Theme = false, DefaultLightTheme
	case "dark":
		a.FollowSystem, a.Theme = false, DefaultDarkTheme
	}
	return a
}

// LegacyTheme is the old "theme" setting closest to an appearance whose
// fixed theme is dark or not.
func LegacyTheme(a Appearance, dark bool) string {
	switch {
	case a.FollowSystem:
		return "system"
	case dark:
		return "dark"
	}
	return "light"
}

// AgentConfig configures the AI tool layer (docs/ai.md §4.2–§4.4).
type AgentConfig struct {
	// External lets AI apps (Claude Desktop, Claude Code) use the plugins
	// through `golauncher mcp`. The launcher's own AI chat does not need it.
	External bool `json:"external,omitzero"`
	// AskOpen asks before "open" actions (opening an app, a URL, copying),
	// which run directly by default.
	AskOpen bool `json:"askOpen,omitzero"`
	// Plugins overrides which plugins AI may use, by plugin ID. By default
	// the built-in plugins are exposed except the clipboard history, and
	// plugins in their own process are not.
	Plugins map[string]bool `json:"plugins,omitempty"`
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
		Appearance:    DefaultAppearance(),
		Language:      "system",
		HideOnBlur:    true,
		ContextAware:  true,
	}
}

// Clone returns a deep copy of c.
func (c Config) Clone() Config {
	out := c
	out.Appearance = c.Appearance.Clone()
	out.Plugins = make(map[string]PluginConfig, len(c.Plugins))
	for id, p := range c.Plugins {
		p.Preferences = maps.Clone(p.Preferences)
		for k, v := range p.Preferences {
			p.Preferences[k] = slices.Clone(v)
		}
		p.Commands = maps.Clone(p.Commands)
		out.Plugins[id] = p
	}
	out.Agent.Plugins = maps.Clone(c.Agent.Plugins)
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
	cfg := decodeBase()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return migrate(cfg)
}

// decodeBase is what a file is decoded over: the defaults, but without an
// appearance, so that migrate can tell a file that has none.
func decodeBase() Config {
	cfg := Default()
	cfg.Appearance = Appearance{}
	return cfg
}

// migrate upgrades cfg to SchemaVersion.
//
// Fields added since version 1 (appearance) need no new version: a file
// without them is completed here, and an older version, which ignores
// fields it does not know, can still read a newer file (docs/storage.md).
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
	if cfg.Appearance == (Appearance{}) {
		// Written before themes: the old setting says light, dark or system.
		cfg.Appearance = AppearanceFromTheme(cfg.Theme)
	}
	cfg.Appearance.normalize()
	return cfg, nil
}

// marshal encodes cfg as indented JSON with sorted keys.
func marshal(cfg Config) ([]byte, error) {
	return json.Marshal(cfg, jsontext.WithIndent("  "), json.Deterministic(true))
}

// Export encodes cfg for a settings file the user keeps or moves to another
// computer. It holds no secrets: password preferences are never in Config.
func Export(cfg Config) ([]byte, error) {
	data, err := marshal(cfg)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Import decodes a settings file made by Export into a complete
// configuration: what it leaves out has its default value. Unknown fields
// and a schema version newer than SchemaVersion are rejected.
func Import(data []byte) (Config, error) {
	cfg := decodeBase()
	if err := json.Unmarshal(data, &cfg, json.RejectUnknownMembers(true)); err != nil {
		return Config{}, fmt.Errorf("config: import: %w", err)
	}
	return migrate(cfg)
}

// Save writes cfg to path atomically.
func Save(path string, cfg Config) error {
	data, err := marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	// Sync before the rename so a crash can't leave an empty config.
	err = errors.Join(err, f.Sync(), f.Close())
	if err != nil {
		os.Remove(tmp)
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
	recovered error // why the file on disk could not be used, see Open
}

// Open loads the configuration at path into a Store. A file that cannot be
// parsed (hand edits, a newer schema after a downgrade) does not stop the app:
// it is moved aside to "<path>.bad", the defaults are used, and Recovered
// reports what happened so the user can be told.
func Open(path string) (*Store, error) {
	cfg, err := Load(path)
	if err == nil {
		return &Store{path: path, cfg: cfg}, nil
	}
	if _, statErr := os.Stat(path); statErr != nil {
		return nil, err // unreadable for another reason, e.g. permissions
	}
	if mvErr := os.Rename(path, path+".bad"); mvErr != nil {
		return nil, errors.Join(err, mvErr)
	}
	return &Store{path: path, cfg: Default(), recovered: err}, nil
}

// Recovered returns the error that made Open fall back to the defaults, or nil.
func (s *Store) Recovered() error { return s.recovered }

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
