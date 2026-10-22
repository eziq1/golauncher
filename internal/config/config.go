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
	"time"
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
	Appearance    Appearance `json:"appearance"`
	Language      string     `json:"language"` // "system" or a BCP 47 tag
	HideOnBlur    bool       `json:"hideOnBlur"`
	LaunchAtLogin bool       `json:"launchAtLogin"`
	LogLevel      string     `json:"logLevel,omitempty"` // "debug", "info" (default), "warn", "error"
	// ContextAware lets the launcher look at the window that was active when
	// it opened (docs/context-awareness.md). Off, no other window is read.
	ContextAware bool `json:"contextAware"`
	// Selection configures reading the selected text, part of context
	// awareness (docs/selected-text.md §8, §12).
	Selection SelectionConfig `json:"selection"`
	// Plugins holds per-plugin settings keyed by plugin ID.
	Plugins map[string]PluginConfig `json:"plugins,omitempty"`
	// Agent configures what AI clients may do with the plugins (docs/ai.md §4).
	Agent AgentConfig `json:"agent,omitzero"`
	// Updates configures automatic updates (docs/auto-update.md §7.1).
	Updates UpdatesConfig `json:"updates,omitzero"`
	// ReturnToRoot is when a view that asks to be kept across a hide
	// (plugin.View.KeepOnHide) gives way to the root search on the next show
	// (docs/keep-view.md): ReturnToRootImmediately, ReturnToRootAfter
	// (KeepViewsFor; the default, also for "") or ReturnToRootNever.
	ReturnToRoot string `json:"returnToRoot,omitempty"`
	// Ring configures the Action Ring, opened by holding the middle mouse
	// button (docs/action-ring.md).
	Ring RingConfig `json:"ring"`
}

// Hold durations of the Action Ring (RingConfig.HoldMS), in milliseconds.
const (
	DefaultRingHoldMS = 300
	MinRingHoldMS     = 150
	MaxRingHoldMS     = 1000
)

// MaxRingFixed is the number of fixed slots of the Action Ring: the top,
// the right, the bottom and the left (docs/action-ring-folders.md); the
// diagonals are the context actions'.
const MaxRingFixed = 4

// MaxRingFolder is the most actions a folder of the Action Ring holds.
const MaxRingFolder = 6

// RingAction is a fixed action of the Action Ring: one of the ring's own
// (RingLauncher, …), or a root entry of the launcher ("<plugin>/<item id>":
// a command, "builtin.ai/cmd:chat", or a root item, "builtin.system/system:lock").
type RingAction struct {
	Key string `json:"key"`
	// Title and Icon (a built-in icon name) are shown until the entry is
	// found again; Title is also the search text that finds a root item.
	Title string `json:"title,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

// RingSlot is a fixed slot of the Action Ring: an action (Key, Title,
// Icon, as a RingAction), a folder (Actions, at least two and at most
// MaxRingFolder, with its Name; folders hold actions only), or empty
// (neither). An action is stored as a RingAction was before folders, so
// that a list of actions of an older version reads as a list of slots.
type RingSlot struct {
	Key   string `json:"key,omitempty"`
	Title string `json:"title,omitempty"`
	Icon  string `json:"icon,omitempty"`
	// Name is a folder's name; "" shows the default one ("Folder").
	Name    string       `json:"name,omitempty"`
	Actions []RingAction `json:"actions,omitempty"`
}

// ActionSlot returns the slot holding action a.
func ActionSlot(a RingAction) RingSlot { return RingSlot{Key: a.Key, Title: a.Title, Icon: a.Icon} }

// FolderSlot returns a folder named name holding actions.
func FolderSlot(name string, actions ...RingAction) RingSlot {
	return RingSlot{Name: name, Actions: actions}
}

// IsFolder reports whether s is a folder.
func (s RingSlot) IsFolder() bool { return len(s.Actions) > 0 }

// IsEmpty reports whether s holds nothing.
func (s RingSlot) IsEmpty() bool { return s.Key == "" && len(s.Actions) == 0 }

// Action returns the action of an action slot.
func (s RingSlot) Action() RingAction { return RingAction{Key: s.Key, Title: s.Title, Icon: s.Icon} }

// Clone returns a deep copy of s.
func (s RingSlot) Clone() RingSlot {
	s.Actions = slices.Clone(s.Actions)
	return s
}

// NormalizeRingFixed returns slots as they are kept: at most MaxRingFixed
// slots, without the empty ones at the end. Slots past MaxRingFixed (a list
// of up to 8 actions written before folders) go into a folder in the last
// slot, with the action there, so that none is lost while there is room. A
// folder keeps at most MaxRingFolder actions; one left with a single action
// becomes that action, an empty one an empty slot. An action without a key
// and the second of two equal keys are left out. Actions this version does
// not know stay: the ring leaves them out when it shows (ring.FixedSlots).
func NormalizeRingFixed(slots []RingSlot) []RingSlot {
	out := make([]RingSlot, 0, MaxRingFixed)
	for i, s := range slots {
		if i < MaxRingFixed {
			out = append(out, s.Clone())
			continue
		}
		// Past the last slot: into a folder there.
		last := &out[MaxRingFixed-1]
		var more []RingAction
		if s.IsFolder() {
			more = s.Actions
		} else if s.Key != "" {
			more = []RingAction{s.Action()}
		}
		if len(more) == 0 {
			continue
		}
		switch {
		case last.IsFolder():
		case last.Key != "":
			*last = FolderSlot("", last.Action())
		default:
			*last = RingSlot{}
		}
		last.Actions = append(last.Actions, more...)
	}
	seen := map[string]bool{}
	keep := func(a RingAction) bool {
		if a.Key == "" || seen[a.Key] {
			return false
		}
		seen[a.Key] = true
		return true
	}
	for i := range out {
		s := &out[i]
		if !s.IsFolder() {
			if s.Key == "" || !keep(s.Action()) {
				*s = RingSlot{}
			} else {
				s.Name = ""
			}
			continue
		}
		var acts []RingAction
		for _, a := range s.Actions {
			if len(acts) < MaxRingFolder && keep(a) {
				acts = append(acts, a)
			}
		}
		switch len(acts) {
		case 0:
			*s = RingSlot{}
		case 1:
			*s = ActionSlot(acts[0])
		default:
			*s = RingSlot{Name: s.Name, Actions: acts}
		}
	}
	for len(out) > 0 && out[len(out)-1].IsEmpty() {
		out = out[:len(out)-1]
	}
	return out
}

// The Action Ring's own actions (RingAction.Key).
const (
	RingLauncher   = "ring:launcher"   // show golauncher
	RingAskAI      = "ring:askai"      // an AI chat, about the selected text if any
	RingScreenshot = "ring:screenshot" // the system's screenshot tool
	// RingLock is the system plugin's Lock Screen item.
	RingLock = "builtin.system/system:lock"
)

// DefaultRingFixed returns the fixed slots of a new configuration: four
// actions, no folder.
func DefaultRingFixed() []RingSlot {
	// The lock's title comes from the system plugin, in the UI language.
	return []RingSlot{{Key: RingLauncher}, {Key: RingAskAI}, {Key: RingScreenshot}, {Key: RingLock, Icon: "lock"}}
}

// DefaultRingExceptions are the apps where the middle button is never taken:
// remote desktops, virtual machines, Blender and other apps where it is the
// main way to move around (Windows executable names and macOS bundle
// identifiers in one list; each system matches its own).
var DefaultRingExceptions = []string{
	// Remote desktops.
	"mstsc.exe", "msrdc.exe", "msrdcw.exe", "Windows365.exe", "vncviewer.exe", "TeamViewer.exe", "AnyDesk.exe", "parsecd.exe",
	"com.microsoft.rdc.macos", "com.microsoft.WindowsApp", "com.realvnc.vncviewer", "com.teamviewer.TeamViewer",
	"com.philandro.anydesk", "tv.parsec.www",
	// Virtual machines.
	"vmware.exe", "vmplayer.exe", "vmware-vmx.exe", "vmconnect.exe", "VirtualBoxVM.exe",
	"com.vmware.fusion", "org.virtualbox.app.VirtualBoxVM", "com.parallels.desktop.console", "com.utmapp.UTM",
	// 3D and CAD.
	"blender.exe", "org.blenderfoundation.blender",
}

// RingConfig configures the Action Ring (docs/action-ring.md). Enabled and
// Context are on by default.
type RingConfig struct {
	Enabled bool `json:"enabled"`
	// HoldMS is how long the middle button is held before the ring opens; 0
	// is DefaultRingHoldMS.
	HoldMS int `json:"holdMs,omitzero"`
	// Fixed are the slots always shown: the top, the right, the bottom and
	// the left (at most MaxRingFixed; NormalizeRingFixed), each an action,
	// a folder of actions or empty.
	Fixed []RingSlot `json:"fixed"`
	// Context fills the other slots with actions for the selected text and
	// the folder under the pointer.
	Context bool `json:"context"`
	// Exceptions are the apps (executable names, macOS bundle identifiers
	// or app names) where the middle button is left alone.
	Exceptions []string `json:"exceptions"`
}

// DefaultRing returns the Action Ring settings of a new configuration.
func DefaultRing() RingConfig {
	return RingConfig{Enabled: true, Fixed: DefaultRingFixed(), Context: true, Exceptions: slices.Clone(DefaultRingExceptions)}
}

// EffectiveHoldMS returns HoldMS within its bounds, or its default.
func (r RingConfig) EffectiveHoldMS() int {
	if r.HoldMS == 0 {
		return DefaultRingHoldMS
	}
	return min(max(r.HoldMS, MinRingHoldMS), MaxRingHoldMS)
}

// Clone returns a deep copy of r.
func (r RingConfig) Clone() RingConfig {
	if r.Fixed != nil {
		fixed := make([]RingSlot, len(r.Fixed))
		for i, s := range r.Fixed {
			fixed[i] = s.Clone()
		}
		r.Fixed = fixed
	}
	r.Exceptions = slices.Clone(r.Exceptions)
	return r
}

// Values of Config.ReturnToRoot.
const (
	ReturnToRootImmediately = "immediately"
	ReturnToRootAfter       = "10m" // after KeepViewsFor (default)
	ReturnToRootNever       = "never"
)

// KeepViewsFor is how long a kept view is restored after the window hid,
// with ReturnToRootAfter.
const KeepViewsFor = 10 * time.Minute

// EffectiveReturnToRoot returns ReturnToRoot, or its default.
func (c Config) EffectiveReturnToRoot() string {
	switch c.ReturnToRoot {
	case ReturnToRootImmediately, ReturnToRootNever:
		return c.ReturnToRoot
	}
	return ReturnToRootAfter
}

// Values of UpdatesConfig. Empty values take the defaults.
const (
	UpdatesAuto   = "auto"   // download and install automatically (default)
	UpdatesNotify = "notify" // only tell about a new version
	UpdatesOff    = "off"    // no automatic checks
	UpdatesDaily  = "daily"  // check every day (default)
	UpdatesWeekly = "weekly" // check every week
)

// UpdatesConfig configures automatic updates. Empty fields take the
// defaults (UpdatesAuto, UpdatesDaily), so that the field needs no schema
// version (docs/storage.md §2.2).
type UpdatesConfig struct {
	Mode      string `json:"mode,omitempty"`
	Frequency string `json:"frequency,omitempty"`
}

// EffectiveMode returns Mode, or its default.
func (u UpdatesConfig) EffectiveMode() string {
	switch u.Mode {
	case UpdatesNotify, UpdatesOff:
		return u.Mode
	}
	return UpdatesAuto
}

// EffectiveFrequency returns Frequency, or its default.
func (u UpdatesConfig) EffectiveFrequency() string {
	if u.Frequency == UpdatesWeekly {
		return UpdatesWeekly
	}
	return UpdatesDaily
}

// Built-in themes the appearance falls back to.
const (
	DefaultLightTheme = "golauncher-light"
	DefaultDarkTheme  = "golauncher-dark"
)

// renamedThemes maps the old IDs of renamed built-in themes to their new
// ones (R56: "Raycast-style" became "Crimson").
var renamedThemes = map[string]string{
	"raycast-light": "crimson-light",
	"raycast-dark":  "crimson-dark",
}

// RenamedTheme returns the current ID of the built-in theme once called
// old, and whether old is such an ID.
func RenamedTheme(old string) (string, bool) {
	id, ok := renamedThemes[old]
	return id, ok
}

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
	// very translucent (below 40%). Off by default: the backdrop is a box
	// behind each line, which looks wrong over the material. (Stored as
	// textBackdrop: the first builds stored textShadow true by default.)
	TextShadow bool `json:"textBackdrop"`
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
	for _, id := range []*string{&a.Theme, &a.LightTheme, &a.DarkTheme} {
		if n, ok := RenamedTheme(*id); ok {
			*id = n
		}
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

// Selection length bounds (SelectionConfig.MaxChars).
const (
	DefaultSelectionMaxChars = 10_000
	MinSelectionMaxChars     = 1_000
	MaxSelectionMaxChars     = 100_000
)

// DefaultSelectionExcludeApps are the apps whose selected text is never
// read: password managers (the defaults of the clipboard history's ignored
// apps).
var DefaultSelectionExcludeApps = []string{"KeePassXC.exe", "KeePass.exe", "1Password.exe", "Bitwarden.exe"}

// SelectionConfig configures reading the text selected in the window that
// was active when the launcher opened. It follows ContextAware: with that
// off, nothing is read (docs/selected-text.md §12).
type SelectionConfig struct {
	// Clipboard falls back to copying (a simulated Ctrl+C, the clipboard
	// restored afterwards) when UI Automation cannot read the selection of
	// the app. Off by default.
	Clipboard bool `json:"clipboard"`
	// ClipboardApps are executables ("Weixin.exe") where the copying method
	// is used at once, without trying UI Automation first (with Clipboard on).
	ClipboardApps []string `json:"clipboardApps"`
	// Hotkey opens the launcher for the selected text: it reads the
	// selection before showing, copying it (with Clipboard on) when UI
	// Automation cannot read it. "" for none.
	Hotkey string `json:"hotkey"`
	// ExcludeApps are executables whose selection is never read.
	ExcludeApps []string `json:"excludeApps"`
	// MaxChars is the most characters read; longer selections are
	// truncated. 0 is DefaultSelectionMaxChars.
	MaxChars int `json:"maxChars"`
}

// DefaultSelection returns the selection settings of a new configuration.
func DefaultSelection() SelectionConfig {
	return SelectionConfig{ExcludeApps: slices.Clone(DefaultSelectionExcludeApps), MaxChars: DefaultSelectionMaxChars}
}

// EffectiveMaxChars returns MaxChars within its bounds, or its default.
func (s SelectionConfig) EffectiveMaxChars() int {
	if s.MaxChars == 0 {
		return DefaultSelectionMaxChars
	}
	return min(max(s.MaxChars, MinSelectionMaxChars), MaxSelectionMaxChars)
}

// Clone returns a deep copy of s.
func (s SelectionConfig) Clone() SelectionConfig {
	s.ClipboardApps = slices.Clone(s.ClipboardApps)
	s.ExcludeApps = slices.Clone(s.ExcludeApps)
	return s
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
		Selection:     DefaultSelection(),
		Ring:          DefaultRing(),
	}
}

// Clone returns a deep copy of c.
func (c Config) Clone() Config {
	out := c
	out.Appearance = c.Appearance.Clone()
	out.Selection = c.Selection.Clone()
	out.Ring = c.Ring.Clone()
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
	if cfg.Ring.Fixed != nil {
		// Folders came after lists of up to 8 actions (R118).
		cfg.Ring.Fixed = NormalizeRingFixed(cfg.Ring.Fixed)
	}
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
//
// Listeners are told in commit order (R88, review 2.2): one goroutine at a
// time delivers, always the newest configuration, until the listeners have
// seen the last committed one. Two concurrent Updates committing A then B
// therefore never end with the listeners on A. Changes committed while the
// listeners run (by another goroutine, or by a listener itself calling
// Update) are delivered by the goroutine already delivering, after the
// current round; several of them may be coalesced into one notification of
// the newest configuration. Listeners may call Get and Update.
type Store struct {
	mu        sync.Mutex
	path      string // "" keeps the configuration in memory only
	cfg       Config
	listeners []func(Config)
	recovered error // why the file on disk could not be used, see Open

	version    uint64 // incremented by every committed Update
	delivered  uint64 // the version the listeners were last told about
	delivering bool   // a goroutine is telling the listeners
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
// listeners (see Store for the order). It returns after the listeners were
// told, unless another goroutine, or the listener calling it, is telling
// them already: that one tells them about this change too before it
// returns.
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
	s.version++
	if s.delivering {
		s.mu.Unlock()
		return nil
	}
	s.delivering = true
	s.mu.Unlock()
	s.deliver()
	return nil
}

// deliver tells the listeners about the newest configuration until they
// have seen the last committed version. Only the goroutine that set
// s.delivering runs it.
func (s *Store) deliver() {
	defer func() {
		// A panicking listener must not leave the store unable to notify.
		if r := recover(); r != nil {
			s.mu.Lock()
			s.delivering = false
			s.mu.Unlock()
			panic(r)
		}
	}()
	for {
		s.mu.Lock()
		if s.delivered == s.version {
			s.delivering = false
			s.mu.Unlock()
			return
		}
		cfg, v := s.cfg.Clone(), s.version
		listeners := slices.Clone(s.listeners)
		s.mu.Unlock()
		for _, l := range listeners {
			l(cfg.Clone())
		}
		s.mu.Lock()
		s.delivered = v
		s.mu.Unlock()
	}
}

// OnChange registers fn to run after every Update.
func (s *Store) OnChange(fn func(Config)) {
	s.mu.Lock()
	s.listeners = append(s.listeners, fn)
	s.mu.Unlock()
}
