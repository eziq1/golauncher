// Package theme holds golauncher's themes (docs/themes.md): the built-in
// ones, the user's JSON files with extends and hot reload, their checks
// (colors, contrast) and Resolve, which turns the configuration and the
// state of the system into the colors and metrics the launcher draws with.
package theme

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	mui "github.com/egoist/mygo/ui"
)

// Material is what shows behind a translucent background.
type Material string

// Materials.
const (
	// MaterialAcrylic blurs what is behind the window.
	MaterialAcrylic Material = "acrylic"
	// MaterialMica tints the window with the desktop wallpaper.
	MaterialMica Material = "mica"
	// MaterialNone is a solid background.
	MaterialNone Material = "none"
)

// Density is how much room the controls leave.
type Density string

// Densities.
const (
	DensityCompact     Density = "compact"
	DensityStandard    Density = "standard"
	DensityComfortable Density = "comfortable"
)

// Spacing is the mui.Theme.Spacing of the density: 3, 4 or 5.
func (d Density) Spacing() float32 {
	switch d {
	case DensityCompact:
		return 3
	case DensityComfortable:
		return 5
	}
	return 4
}

// Defaults of the appearance values a theme leaves out.
const (
	DefaultRadius   = 6
	DefaultMaterial = MaterialAcrylic
	DefaultDensity  = DensityStandard
	// defaultOpacity is the background opacity of a translucent theme that
	// does not say.
	defaultOpacity = 0.8
)

// Ranges of the appearance values.
const (
	MinRadius   = 0
	MaxRadius   = 16
	MinFontSize = 10
	MaxFontSize = 24
)

// colorKey describes a color of a theme: the colors of mui.Theme
// (docs/themes.md §3.1), then the launcher's own (§3.2). A color with a
// base is derived from it when the theme does not set it, or set it in a
// theme further up the extends chain than the base: a theme that changes
// the accent also changes its hover color, unless it sets that too.
type colorKey struct {
	name     string
	base     string
	required bool // a theme without extends must set it
	launcher bool // a launcher color rather than a mui.Theme one
	help     string
}

// colorKeys lists every color in order. Tints ("tint.<name>") color the
// built-in icons of plugins by meaning (plugin.IconTint).
var colorKeys = []colorKey{
	{name: "background", required: true, help: "fills the window, at the background opacity over the material"},
	{name: "surface", required: true, help: "the face of buttons, inputs and other controls"},
	{name: "surfaceHover", base: "surface", help: "surface while hovered (default: 6% lighter or darker)"},
	{name: "surfacePressed", base: "surface", help: "surface while pressed (default: 12% lighter or darker)"},
	{name: "border", required: true, help: "outlines of controls"},
	{name: "text", required: true, help: "text"},
	{name: "textMuted", required: true, help: "secondary text and placeholders"},
	{name: "accent", required: true, help: "primary buttons, checked controls, the selected row"},
	{name: "accentHover", base: "accent", help: "accent while hovered"},
	{name: "accentPressed", base: "accent", help: "accent while pressed"},
	{name: "accentText", base: "accent", help: "text on the accent color"},
	{name: "danger", required: true, help: "destructive actions and errors"},
	{name: "warning", required: true, help: "what calls for attention"},
	{name: "success", required: true, help: "what went well"},
	{name: "selection", base: "accent", help: "selected text"},
	{name: "focus", base: "accent", help: "the ring around the focused control"},
	{name: "inverse", base: "text", help: "tooltips and toasts"},
	{name: "inverseText", base: "background", help: "text on inverse"},
	{name: "scrollbar", help: "scroll bar thumbs"},
	{name: "rowSelected", base: "accent", launcher: true, help: "the selected result row (default: accent)"},
	{name: "rowSelectedText", base: "accentText", launcher: true, help: "text on the selected row"},
	{name: "rowHover", base: "text", launcher: true, help: "the result row under the pointer"},
	{name: "searchText", base: "text", launcher: true, help: "text typed in the search field"},
	{name: "searchPlaceholder", base: "textMuted", launcher: true, help: "the search field's placeholder"},
	{name: "sectionHeader", base: "textMuted", launcher: true, help: "section titles (Applications, System…)"},
	{name: "divider", base: "border", launcher: true, help: "lines under the search field and above the footer"},
	{name: "footer", launcher: true, help: "the footer's background, over the material"},
	{name: "overlay", launcher: true, help: "dims the launcher behind a confirmation"},
	{name: "shadow", launcher: true, help: "shadow of the action panel and dialogs"},
	{name: "control", base: "text", launcher: true, help: "icon tiles, key caps and small buttons"},
	{name: "controlHover", base: "text", launcher: true, help: "small buttons while hovered"},
	{name: "tint.success", base: "success", launcher: true, help: "icons tinted success"},
	{name: "tint.danger", base: "danger", launcher: true, help: "icons tinted danger"},
	{name: "tint.accent", base: "accent", launcher: true, help: "icons tinted accent"},
	{name: "tint.muted", base: "textMuted", launcher: true, help: "icons tinted muted"},
}

var keyIndex = func() map[string]int {
	m := map[string]int{}
	for i, k := range colorKeys {
		m[k.name] = i
	}
	return m
}()

// ColorNames returns the names of all theme colors, in order.
func ColorNames() []string {
	out := make([]string, len(colorKeys))
	for i, k := range colorKeys {
		out[i] = k.name
	}
	return out
}

// RequiredColors returns the colors a theme without extends must set.
func RequiredColors() []string {
	var out []string
	for _, k := range colorKeys {
		if k.required {
			out = append(out, k.name)
		}
	}
	return out
}

// File is a theme file as written (docs/themes.md §6.1). Pointers are nil
// where the file says nothing, taking the value of the theme it extends.
type File struct {
	Schema string `json:"$schema,omitempty"`
	Name   string `json:"name,omitempty"`
	// Names are the localized names, from "name.<locale>" members.
	Names        map[string]string `json:"-"`
	Author       string            `json:"author,omitempty"`
	Source       string            `json:"source,omitempty"`
	License      string            `json:"license,omitempty"`
	Extends      string            `json:"extends,omitempty"`
	Dark         *bool             `json:"dark,omitempty"`
	HighContrast *bool             `json:"highContrast,omitempty"`
	SystemAccent *bool             `json:"systemAccent,omitempty"`
	Colors       map[string]string `json:"colors,omitempty"`
	Material     *string           `json:"material,omitempty"`
	Opacity      *float64          `json:"opacity,omitempty"`
	Radius       *float64          `json:"radius,omitempty"`
	Density      *string           `json:"density,omitempty"`
	FontSize     *float64          `json:"fontSize,omitempty"`
}

// fileMembers are the members of a theme file, besides "name.<locale>".
var fileMembers = []string{"$schema", "name", "author", "source", "license", "extends", "dark", "highContrast",
	"systemAccent", "colors", "material", "opacity", "radius", "density", "fontSize"}

// ParseFile decodes a theme file. It fails on what is not JSON or has
// members of the wrong type; unknown members are warnings.
func ParseFile(data []byte) (*File, []string, error) {
	var raw map[string]jsontext.Value
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("not a theme: %w", err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, nil, fmt.Errorf("not a theme: %w", err)
	}
	var warnings []string
	for _, k := range slices.Sorted(maps.Keys(raw)) {
		if loc, ok := strings.CutPrefix(k, "name."); ok && loc != "" {
			var name string
			if err := json.Unmarshal(raw[k], &name); err != nil {
				return nil, nil, fmt.Errorf("not a theme: %s: %w", k, err)
			}
			if f.Names == nil {
				f.Names = map[string]string{}
			}
			f.Names[loc] = name
			continue
		}
		if !slices.Contains(fileMembers, k) {
			warnings = append(warnings, fmt.Sprintf("unknown field %q", k))
		}
	}
	for _, k := range slices.Sorted(maps.Keys(f.Colors)) {
		if _, ok := keyIndex[k]; !ok && !strings.HasPrefix(k, "tint.") {
			warnings = append(warnings, fmt.Sprintf("unknown color %q", k))
		}
	}
	return &f, warnings, nil
}

// MarshalJSON writes the file with its localized names as "name.<locale>".
func (f *File) MarshalJSON() ([]byte, error) {
	type plain File
	b, err := json.Marshal((*plain)(f), json.Deterministic(true))
	if err != nil || len(f.Names) == 0 {
		return b, err
	}
	var m map[string]jsontext.Value
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for loc, name := range f.Names {
		v, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		m["name."+loc] = v
	}
	return json.Marshal(m, json.Deterministic(true))
}

// layered is a color a theme file set, and how far down the extends chain
// (0 is the theme without extends).
type layered struct {
	raw   string
	color mui.Color
	depth int
}

// Theme is a theme with its extends chain applied: everything it sets, or
// what it inherits. Colors derives the colors it leaves out.
type Theme struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Names   map[string]string `json:"names,omitempty"`
	Author  string            `json:"author,omitempty"`
	Source  string            `json:"source,omitempty"`
	License string            `json:"license,omitempty"`
	Extends string            `json:"extends,omitempty"`
	// Builtin themes are part of golauncher; the others are files, at Path.
	Builtin bool   `json:"builtin"`
	Path    string `json:"path,omitempty"`

	Dark bool `json:"dark"`
	// HighContrast themes are checked at 7:1 and are always opaque.
	HighContrast bool `json:"highContrast,omitzero"`
	// SystemAccent takes the Windows accent color when the configuration
	// asks for the theme's accent (golauncher's own themes, as before
	// themes).
	SystemAccent bool `json:"systemAccent,omitzero"`

	Material Material `json:"material"`
	Opacity  float64  `json:"opacity"`
	Radius   float32  `json:"radius"`
	Density  Density  `json:"density"`
	FontSize float32  `json:"fontSize,omitzero"` // 0 is the system's

	// Warnings are what was wrong in the file but did not stop it.
	Warnings []string `json:"warnings,omitempty"`

	colors map[string]layered
	depth  int // of this theme's own file in its chain
}

// DisplayName is the theme's name in locale, or its name.
func (t *Theme) DisplayName(locale string) string {
	for loc := locale; loc != ""; {
		if n := t.Names[loc]; n != "" {
			return n
		}
		i := strings.LastIndexAny(loc, "-_")
		if i < 0 {
			break
		}
		loc = loc[:i]
	}
	if t.Name != "" {
		return t.Name
	}
	return t.ID
}

// apply sets on t what f sets, at depth. It reports the problems that do
// not stop the theme: bad colors and values out of range keep the value
// t had (the base theme's).
func (t *Theme) apply(f *File, depth int) []string {
	var warnings []string
	warn := func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
	t.depth = depth
	// Names and credits are the file's own: the base's name another theme.
	t.Name, t.Names = f.Name, maps.Clone(f.Names)
	t.Author, t.Source, t.License = f.Author, f.Source, f.License
	if f.Dark != nil {
		t.Dark = *f.Dark
	}
	if f.HighContrast != nil {
		t.HighContrast = *f.HighContrast
	}
	if f.SystemAccent != nil {
		t.SystemAccent = *f.SystemAccent
	}
	if t.colors == nil {
		t.colors = map[string]layered{}
	}
	for _, k := range slices.Sorted(maps.Keys(f.Colors)) {
		raw := f.Colors[k]
		c, err := ParseColor(raw)
		if err != nil {
			warn("color %q: %v; keeping the base theme's", k, err)
			continue
		}
		t.colors[k] = layered{raw: raw, color: c, depth: depth}
	}
	if f.Material != nil {
		switch m := Material(*f.Material); m {
		case MaterialAcrylic, MaterialMica, MaterialNone:
			t.Material = m
		default:
			warn("material %q: want acrylic, mica or none", *f.Material)
		}
	}
	if f.Opacity != nil {
		if v := *f.Opacity; v >= 0 && v <= 1 {
			t.Opacity = v
		} else {
			warn("opacity %v: want 0 to 1", v)
		}
	}
	if f.Radius != nil {
		if v := *f.Radius; v >= MinRadius && v <= MaxRadius {
			t.Radius = float32(v)
		} else {
			warn("radius %v: want %d to %d", v, MinRadius, MaxRadius)
		}
	}
	if f.Density != nil {
		switch d := Density(*f.Density); d {
		case DensityCompact, DensityStandard, DensityComfortable:
			t.Density = d
		default:
			warn("density %q: want compact, standard or comfortable", *f.Density)
		}
	}
	if f.FontSize != nil {
		if v := *f.FontSize; v >= MinFontSize && v <= MaxFontSize {
			t.FontSize = float32(v)
		} else {
			warn("fontSize %v: want %d to %d", v, MinFontSize, MaxFontSize)
		}
	}
	return warnings
}

// clone returns a copy of t to extend.
func (t *Theme) clone() *Theme {
	c := *t
	c.colors = maps.Clone(t.colors)
	c.Names = maps.Clone(t.Names)
	c.Warnings = nil
	return &c
}

// newRoot is the theme a file without extends starts from.
func newRoot() *Theme {
	return &Theme{Material: DefaultMaterial, Opacity: defaultOpacity, Radius: DefaultRadius, Density: DefaultDensity, colors: map[string]layered{}}
}

// missingColors returns the required colors t does not set.
func (t *Theme) missingColors() []string {
	var out []string
	for _, k := range colorKeys {
		if _, ok := t.colors[k.name]; k.required && !ok {
			out = append(out, k.name)
		}
	}
	return out
}

// Colors are the colors of a theme, every one of colorKeys and the tints
// the theme sets, by name.
type Colors map[string]mui.Color

// overrides change the colors of a theme as it is used.
type overrides struct {
	// accent replaces the theme's accent (the Windows or a custom accent),
	// and what derives from it, whatever the theme sets.
	accent    mui.Color
	hasAccent bool
	// readable adjusts accent until text on it is readable
	// (readableAccent): the Windows accent, which the user did not pick
	// for this theme. A custom accent is used as chosen.
	readable     bool
	highContrast bool // the system asks for more contrast
}

// Colors returns all the theme's colors, deriving the ones it leaves out.
func (t *Theme) Colors() Colors {
	c, _ := t.colorsWith(overrides{})
	return c
}

// colorsWith derives the colors with o, and reports which ones are the
// theme's own (not derived).
func (t *Theme) colorsWith(o overrides) (Colors, map[string]bool) {
	out := Colors{}
	own := map[string]bool{}
	depth := map[string]int{}
	var resolve func(name string) (mui.Color, int)
	resolve = func(name string) (mui.Color, int) {
		if c, ok := out[name]; ok {
			return c, depth[name]
		}
		k := colorKeys[keyIndex[name]]
		explicit, has := t.colors[name]
		var c mui.Color
		d := -1
		switch {
		case name == "accent" && o.hasAccent:
			c, d = o.accent, math.MaxInt
			if o.readable {
				bg, _ := resolve("background")
				minText := float64(MinTextContrast)
				if t.HighContrast {
					minText = MinTextContrastHigh
				}
				c = readableAccent(c, bg, t.Dark, minText)
			}
		case k.base == "":
			if has {
				c, d = explicit.color, explicit.depth
				own[name] = true
			} else {
				c, d = t.fallback(name), 0
			}
		default:
			bc, bd := resolve(k.base)
			if bd == math.MaxInt && k.launcher {
				// An accent of the configuration replaces the palette's
				// accent colors, but a launcher color the theme sets (a
				// selected row of its own) stays.
				bd = t.colors["accent"].depth
			}
			if has && explicit.depth >= bd {
				c, d = explicit.color, explicit.depth
				own[name] = true
			} else {
				c, d = t.derive(name, bc, out), bd
			}
		}
		if o.highContrast {
			c = highContrast(name, c, out, resolve)
		}
		out[name], depth[name] = c, d
		return c, d
	}
	for _, k := range colorKeys {
		resolve(k.name)
	}
	for name, l := range t.colors {
		if _, ok := out[name]; !ok { // a tint of a name of its own
			out[name] = l.color
			own[name] = true
		}
	}
	return out, own
}

// fallback is a color without a base that a theme left out: what MyGo's
// own theme of its brightness has.
func (t *Theme) fallback(name string) mui.Color {
	base := mui.LightTheme()
	if t.Dark {
		base = mui.DarkTheme()
	}
	switch name {
	case "background":
		return base.Background
	case "surface":
		return base.Surface
	case "border":
		return base.Border
	case "text":
		return base.Text
	case "textMuted":
		return base.TextMuted
	case "accent":
		return base.Accent
	case "danger":
		return base.Danger
	case "warning":
		return base.Warning
	case "success":
		return base.Success
	case "scrollbar":
		return base.Scrollbar
	case "footer":
		return mui.Transparent
	case "overlay":
		return mui.RGBA(0, 0, 0, 0.35)
	case "shadow":
		return mui.RGBA(0, 0, 0, 0.25)
	}
	return mui.Transparent
}

// derive is the color name the theme left out, from its base b.
func (t *Theme) derive(name string, b mui.Color, done Colors) mui.Color {
	toward := black
	if t.Dark {
		toward = white
	}
	switch name {
	case "surfaceHover":
		return b.Mix(toward, 0.06)
	case "surfacePressed":
		return b.Mix(toward, 0.12)
	// The accent's are MyGo's, as when its theme follows the Windows
	// accent: golauncher's own themes look as they did before themes.
	case "accentHover":
		if t.Dark {
			return b.Mix(white, 0.2)
		}
		return b.Mix(black, 0.12)
	case "accentPressed":
		if t.Dark {
			return b.Mix(black, 0.15)
		}
		return b.Mix(black, 0.25)
	case "accentText":
		return textOnAccent(b)
	case "selection":
		if t.Dark {
			return b.Alpha(0.4)
		}
		return b.Alpha(0.25)
	case "focus":
		if t.Dark {
			return b.Mix(white, 0.2).Alpha(0.6)
		}
		return b.Alpha(0.55)
	case "rowSelected":
		return b.Alpha(rowSelectedAlpha)
	case "rowHover":
		return b.Alpha(0.05)
	case "control":
		return b.Alpha(0.08)
	case "controlHover":
		return b.Alpha(0.14)
	}
	return b // inverse, inverseText, rowSelectedText, searchText, …, tints
}

// highContrast strengthens a color as MyGo's own theme does when the system
// asks for more contrast.
func highContrast(name string, c mui.Color, _ Colors, resolve func(string) (mui.Color, int)) mui.Color {
	switch name {
	case "border":
		text, _ := resolve("text")
		return c.Mix(text, 0.45)
	case "textMuted":
		text, _ := resolve("text")
		return c.Mix(text, 0.4)
	case "focus":
		c.A = 255
	case "scrollbar":
		return c.Alpha(1.6)
	}
	return c
}

// UITheme returns the mui.Theme of colors c with the metrics of t.
func (c Colors) UITheme(dark bool) mui.Theme {
	return mui.Theme{
		Dark:           dark,
		Background:     c["background"],
		Surface:        c["surface"],
		SurfaceHover:   c["surfaceHover"],
		SurfacePressed: c["surfacePressed"],
		Border:         c["border"],
		Text:           c["text"],
		TextMuted:      c["textMuted"],
		Accent:         c["accent"],
		AccentHover:    c["accentHover"],
		AccentPressed:  c["accentPressed"],
		AccentText:     c["accentText"],
		Danger:         c["danger"],
		Warning:        c["warning"],
		Success:        c["success"],
		Selection:      c["selection"],
		Focus:          c["focus"],
		Inverse:        c["inverse"],
		InverseText:    c["inverseText"],
		Scrollbar:      c["scrollbar"],
	}
}
