package theme

import (
	"sync"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
)

// System is the state of the desktop that Resolve follows. Its zero value
// is a light desktop with transparency effects on and no accent color.
type System struct {
	// Dark is the Windows app mode (or, in a window, MyGo's appearance).
	Dark bool
	// Accent is the Windows accent color; A is 0 where there is none.
	Accent mui.Color
	// ReduceTransparency is Windows's transparency effects turned off.
	ReduceTransparency bool
	// HighContrast is a Windows contrast theme on.
	HighContrast bool
	// TextScale is Windows's text size, 1 (or 0) for usual.
	TextScale float32
	// NoMaterial is a window that shows no material: Windows 10, or a
	// window created without one (mui.Context.Vibrancy is false).
	NoMaterial bool
}

// OpaqueReason is why a translucent background is drawn opaque.
type OpaqueReason string

// Reasons for an opaque background, by priority.
const (
	// OpaqueHighContrast: a Windows contrast theme is on.
	OpaqueHighContrast OpaqueReason = "highContrast"
	// OpaqueTransparencyOff: Windows's transparency effects are off.
	OpaqueTransparencyOff OpaqueReason = "transparencyOff"
	// OpaqueMaterialNone: the material is none (the theme's, or chosen).
	OpaqueMaterialNone OpaqueReason = "materialNone"
	// OpaqueUnsupported: the window shows no material on this system.
	OpaqueUnsupported OpaqueReason = "unsupported"
)

// textShadowBelow is the background opacity under which text gets a faint
// backdrop, when Appearance.TextShadow allows it (docs/themes.md §4).
const textShadowBelow = 0.4

// Tokens are the launcher's own colors (docs/themes.md §3.2), with the
// overlays already scaled to the background opacity.
type Tokens struct {
	RowSelected      mui.Color
	RowSelectedText  mui.Color
	RowSelectedMuted mui.Color // secondary text on the selected row
	// TintSelected tints icons on the selected row too: its text is the
	// usual text, not text on an accent.
	TintSelected      bool
	RowHover          mui.Color
	SearchText        mui.Color
	SearchPlaceholder mui.Color
	SectionHeader     mui.Color
	// SectionBackground is behind section headers: opaque backgrounds
	// only, translucent ones show the material.
	SectionBackground mui.Color
	Divider           mui.Color
	Footer            mui.Color
	Overlay           mui.Color
	Shadow            mui.Color
	DialogShadow      mui.Color
	Control           mui.Color
	ControlHover      mui.Color
	// TextShadow is the faint backdrop under text when the background is
	// very translucent; Transparent when there is none.
	TextShadow mui.Color
	Tints      map[string]mui.Color
}

// Tint returns the color of icons tinted name ("success", "danger",
// "accent", "muted"), or fg.
func (t *Tokens) Tint(name string, fg mui.Color) mui.Color {
	if c, ok := t.Tints[name]; ok {
		return c
	}
	return fg
}

// Resolved is what the launcher draws with at a moment: the theme the
// configuration and the system pick, with the configuration's adjustments.
// It must not be changed.
type Resolved struct {
	Theme *Theme
	// Requested is the ID the configuration asked for; Missing is set when
	// there is no such theme and Theme is a fallback.
	Requested string
	Missing   bool

	UI     mui.Theme // the palette and metrics for MyGo's widgets
	Tokens Tokens
	Colors Colors
	Dark   bool

	// Material is what the window should show behind the background.
	Material Material
	// ThemeOpacity is the opacity the theme and the configuration ask for;
	// Opacity the one in effect: 1 when Opaque says why it cannot be.
	ThemeOpacity float64
	Opacity      float64
	Opaque       OpaqueReason
	// Background fills the window's root.
	Background mui.Color
	// WindowOpacity is the opacity of the whole window.
	WindowOpacity float64
	// RadiusScale and SpaceScale scale the launcher's corners and room
	// against the defaults (radius 6, spacing 4).
	RadiusScale float32
	SpaceScale  float32

	// Contrast checks the text of these colors at the opacity in effect.
	Contrast []ContrastResult
}

// Translucent reports whether the background lets the material show.
func (r *Resolved) Translucent() bool { return r.Opaque == "" && r.Opacity < 1 }

// themeID is the theme the appearance asks for in the system's mode.
func themeID(a config.Appearance, sys System) string {
	switch {
	case !a.FollowSystem:
		return a.Theme
	case sys.Dark:
		return a.DarkTheme
	}
	return a.LightTheme
}

// Resolve picks the theme of appearance a for the system's state and
// applies a's adjustments: accent, material, opacity, radius, density,
// font size. A theme that does not exist falls back to golauncher's own of
// the system's brightness.
func Resolve(lib *Library, a config.Appearance, sys System) *Resolved {
	id := themeID(a, sys)
	r := &Resolved{Requested: id}
	t, ok := lib.Get(id)
	if !ok {
		fallback := config.DefaultLightTheme
		if sys.Dark {
			fallback = config.DefaultDarkTheme
		}
		t, _ = lib.Get(fallback)
		r.Missing = true
	}
	r.Theme, r.Dark = t, t.Dark

	// Colors, with the accent of the configuration.
	o := accentOverride(t, a.Accent, sys)
	o.highContrast = sys.HighContrast
	colors, _ := t.colorsWith(o)
	r.Colors = colors

	// Metrics.
	ui := colors.UITheme(t.Dark)
	radius := t.Radius
	if a.Radius != nil {
		radius = float32(min(max(*a.Radius, MinRadius), MaxRadius))
	}
	density := t.Density
	if a.Density != nil {
		density = Density(*a.Density)
	}
	fontSize := t.FontSize
	if a.FontSize != nil {
		fontSize = float32(min(max(*a.FontSize, MinFontSize), MaxFontSize))
	}
	if fontSize <= 0 {
		fontSize = mui.LightTheme().FontSize
	}
	if s := sys.TextScale; s > 0 && s != 1 {
		fontSize *= s
	}
	ui.Radius, ui.Spacing, ui.FontSize = radius, density.Spacing(), fontSize
	r.UI = ui
	r.RadiusScale, r.SpaceScale = radius/DefaultRadius, density.Spacing()/4

	// The background over the material.
	r.Material = t.Material
	if a.Material != nil {
		switch m := Material(*a.Material); m {
		case MaterialAcrylic, MaterialMica, MaterialNone:
			r.Material = m
		}
	}
	r.ThemeOpacity = t.Opacity
	if a.Opacity != nil {
		r.ThemeOpacity = min(max(*a.Opacity, 0), 1)
	}
	switch {
	case sys.HighContrast:
		r.Opaque = OpaqueHighContrast
	case sys.ReduceTransparency:
		r.Opaque = OpaqueTransparencyOff
	case r.Material == MaterialNone || t.HighContrast:
		r.Opaque = OpaqueMaterialNone
	case sys.NoMaterial:
		r.Opaque = OpaqueUnsupported
	}
	r.Opacity = r.ThemeOpacity
	if r.Opaque != "" {
		r.Opacity = 1
	}
	bg := colors["background"]
	switch {
	case r.Opaque != "":
		r.Background = opaque(bg)
	case r.Opacity <= 0:
		r.Background = mui.Transparent
	default:
		r.Background = bg.Alpha(float32(r.Opacity))
	}
	r.WindowOpacity = a.WindowOpacity
	if r.WindowOpacity == 0 {
		r.WindowOpacity = 1
	}
	r.WindowOpacity = min(max(r.WindowOpacity, config.MinWindowOpacity), config.MaxWindowOpacity)

	r.Tokens = tokens(colors, r, t, a.TextShadow)
	r.Contrast = checkContrast(colors, r.Dark, r.Material, r.Opaque != "", r.Opacity, t.HighContrast)
	return r
}

// opaque returns c without its alpha.
func opaque(c mui.Color) mui.Color {
	c.A = 255
	return c
}

// tokens returns the launcher's colors. The overlays (selected and hovered
// rows, the footer) are defined as they look at the theme's own opacity;
// at another one their alpha scales with the background's, so that a
// translucent background does not carry solid rows (docs/themes.md §4).
func tokens(c Colors, r *Resolved, t *Theme, textShadow bool) Tokens {
	k := float32(1)
	if r.Translucent() {
		k = float32((0.5 + 0.5*r.Opacity) / (0.5 + 0.5*t.Opacity))
	}
	scale := func(col mui.Color) mui.Color {
		if k == 1 {
			return col
		}
		return col.Alpha(k)
	}
	tk := Tokens{
		RowSelected:       scale(c["rowSelected"]),
		RowSelectedText:   c["rowSelectedText"],
		RowSelectedMuted:  c["rowSelectedText"].Alpha(0.75),
		TintSelected:      c["rowSelectedText"] == c["text"],
		RowHover:          scale(c["rowHover"]),
		SearchText:        c["searchText"],
		SearchPlaceholder: c["searchPlaceholder"],
		SectionHeader:     c["sectionHeader"],
		SectionBackground: mui.Transparent,
		Divider:           c["divider"],
		Footer:            scale(c["footer"]),
		Overlay:           c["overlay"],
		Shadow:            c["shadow"],
		DialogShadow:      c["shadow"].Alpha(1.2),
		Control:           c["control"],
		ControlHover:      c["controlHover"],
		TextShadow:        mui.Transparent,
		Tints:             map[string]mui.Color{},
	}
	if tk.TintSelected {
		tk.RowSelectedMuted = c["textMuted"]
	}
	if !r.Translucent() {
		tk.SectionBackground = c["background"].Alpha(0.9)
	}
	if textShadow && r.Translucent() && r.Opacity < textShadowBelow {
		tk.TextShadow = c["background"].Alpha(0.35)
	}
	for name, col := range c {
		if tint, ok := cutTint(name); ok {
			tk.Tints[tint] = col
		}
	}
	return tk
}

func cutTint(name string) (string, bool) {
	const prefix = "tint."
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		return name[len(prefix):], true
	}
	return "", false
}

// Manager is the live appearance of the app: the library, the cache of
// Resolve, the system state that windows cannot see (transparency effects)
// and the preview of a setting being adjusted. It is safe for concurrent
// use.
type Manager struct {
	lib *Library

	mu        sync.Mutex
	key       resolveKey
	last      *Resolved
	preview   *config.Appearance
	reduce    bool // transparency effects off
	listeners []func()
}

// resolveKey is everything Resolve depends on.
type resolveKey struct {
	gen                                      uint64
	sys                                      System
	theme, light, dark, accent, mat, dens    string
	follow, hasMat, hasOp, hasRad, hasDens   bool
	hasFont, textShadow                      bool
	opacity, radius, fontSize, windowOpacity float64
}

func keyOf(gen uint64, a config.Appearance, sys System) resolveKey {
	k := resolveKey{gen: gen, sys: sys, theme: a.Theme, light: a.LightTheme, dark: a.DarkTheme, accent: a.Accent,
		follow: a.FollowSystem, textShadow: a.TextShadow, windowOpacity: a.WindowOpacity}
	if a.Material != nil {
		k.mat, k.hasMat = *a.Material, true
	}
	if a.Opacity != nil {
		k.opacity, k.hasOp = *a.Opacity, true
	}
	if a.Radius != nil {
		k.radius, k.hasRad = *a.Radius, true
	}
	if a.Density != nil {
		k.dens, k.hasDens = *a.Density, true
	}
	if a.FontSize != nil {
		k.fontSize, k.hasFont = *a.FontSize, true
	}
	return k
}

// NewManager returns the manager of lib's themes.
func NewManager(lib *Library) *Manager {
	m := &Manager{lib: lib}
	lib.OnChange(m.changed)
	return m
}

// Library returns the themes.
func (m *Manager) Library() *Library { return m.lib }

// Resolve is Resolve with the library's themes, or the preview's
// appearance while there is one. The result is kept until the appearance,
// the system or the themes change, so a frame can call it every time.
func (m *Manager) Resolve(a config.Appearance, sys System) *Resolved {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.preview != nil {
		a = *m.preview
	}
	if m.reduce {
		sys.ReduceTransparency = true
	}
	k := keyOf(m.lib.Generation(), a, sys)
	if m.last != nil && k == m.key {
		return m.last
	}
	m.last, m.key = Resolve(m.lib, a, sys), k
	return m.last
}

// SetPreview makes Resolve use a instead of the configuration's appearance
// until it is called with nil: a slider being dragged.
func (m *Manager) SetPreview(a *config.Appearance) {
	m.mu.Lock()
	if a != nil {
		c := a.Clone()
		a = &c
	}
	m.preview = a
	m.mu.Unlock()
	m.changed()
}

// Previewing reports whether a preview is set.
func (m *Manager) Previewing() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.preview != nil
}

// SetReduceTransparency tells whether Windows's transparency effects are
// off, which windows cannot see themselves.
func (m *Manager) SetReduceTransparency(off bool) {
	m.mu.Lock()
	same := m.reduce == off
	m.reduce = off
	m.mu.Unlock()
	if !same {
		m.changed()
	}
}

// ReduceTransparency reports what SetReduceTransparency set.
func (m *Manager) ReduceTransparency() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reduce
}

// OnChange registers fn to run when what Resolve returns may change for
// reasons other than the configuration: the themes, the preview, the
// transparency effects.
func (m *Manager) OnChange(fn func()) {
	m.mu.Lock()
	m.listeners = append(m.listeners, fn)
	m.mu.Unlock()
}

func (m *Manager) changed() {
	m.mu.Lock()
	listeners := append([]func(){}, m.listeners...)
	m.mu.Unlock()
	for _, fn := range listeners {
		fn()
	}
}
