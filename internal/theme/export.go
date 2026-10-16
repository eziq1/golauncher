package theme

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"

	"github.com/eziq1/golauncher/internal/config"
)

// SchemaURL is the JSON Schema of theme files, for editors.
const SchemaURL = "https://raw.githubusercontent.com/eziq1/golauncher/main/docs/theme.schema.json"

// accentOverride is the accent the appearance puts in place of t's.
func accentOverride(t *Theme, accent string, sys System) overrides {
	var o overrides
	switch accent {
	case config.AccentTheme, "":
		if t.SystemAccent && sys.Accent.A > 0 {
			o.accent, o.hasAccent, o.readable = opaque(sys.Accent), true, true
		}
	case config.AccentWindows:
		if sys.Accent.A > 0 {
			o.accent, o.hasAccent, o.readable = opaque(sys.Accent), true, true
		}
	default:
		if c, err := ParseColor(accent); err == nil && c.A > 0 {
			o.accent, o.hasAccent = opaque(c), true
		}
	}
	return o
}

// fileOf writes t as a self-contained file (no extends) with the colors it
// sets itself under o; the others are derived again when it is loaded, so
// that changing its accent changes what follows from it.
func fileOf(t *Theme, o overrides) *File {
	colors, own := t.colorsWith(o)
	f := &File{
		Schema:  SchemaURL,
		Name:    t.Name,
		Names:   maps.Clone(t.Names),
		Author:  t.Author,
		Source:  t.Source,
		License: t.License,
		Dark:    ptr(t.Dark),
		Colors:  map[string]string{},
	}
	if t.HighContrast {
		f.HighContrast = ptr(true)
	}
	if t.SystemAccent && !o.hasAccent {
		f.SystemAccent = ptr(true)
	}
	for name := range own {
		if l, ok := t.colors[name]; ok && colors[name] == l.color {
			f.Colors[name] = l.raw
		} else {
			f.Colors[name] = FormatColor(colors[name])
		}
	}
	if o.hasAccent {
		f.Colors["accent"] = FormatColor(o.accent)
	}
	f.Material = ptr(string(t.Material))
	f.Opacity = ptr(t.Opacity)
	f.Radius = ptr(float64(t.Radius))
	f.Density = ptr(string(t.Density))
	if t.FontSize > 0 {
		f.FontSize = ptr(float64(t.FontSize))
	}
	return f
}

func ptr[T any](v T) *T { return &v }

// ExportTheme returns theme t as a file of its own, as it comes.
func ExportTheme(t *Theme) *File { return fileOf(t, overrides{}) }

// ExportAppearance returns the theme appearance a uses now, with a's
// adjustments (accent, material, opacity, radius, density, font size), as
// a file of its own named name.
func ExportAppearance(lib *Library, a config.Appearance, sys System, name string) *File {
	r := Resolve(lib, a, System{Dark: sys.Dark, Accent: sys.Accent})
	t := r.Theme
	f := fileOf(t, accentOverride(t, a.Accent, sys))
	f.Name, f.Names = name, nil
	f.Source, f.License = "", ""
	f.Material = ptr(string(r.Material))
	f.Opacity = ptr(r.ThemeOpacity)
	f.Radius = ptr(float64(r.UI.Radius))
	f.Density = ptr(string(densityOf(r.UI.Spacing)))
	if a.FontSize != nil {
		f.FontSize = ptr(float64(r.UI.FontSize))
	}
	return f
}

func densityOf(spacing float32) Density {
	switch spacing {
	case 3:
		return DensityCompact
	case 5:
		return DensityComfortable
	}
	return DensityStandard
}

// Encode writes f as indented JSON with sorted members.
func (f *File) Encode() ([]byte, error) {
	b, err := json.Marshal(f, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	v := jsontext.Value(b)
	if err := v.Indent(jsontext.WithIndent("  ")); err != nil {
		return nil, err
	}
	return append(v, '\n'), nil
}
