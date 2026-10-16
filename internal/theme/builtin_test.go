package theme

import (
	"testing"

	mui "github.com/egoist/mygo/ui"
)

// TestBuiltinThemes guards every built-in theme, including ones added
// later: it parses, has every color, and its text is readable. There are no
// exceptions: golauncher's own themes were darkened to pass (R56).
func TestBuiltinThemes(t *testing.T) {
	if len(Builtin()) != len(BuiltinOrder) || len(BuiltinOrder) < 18 {
		t.Fatalf("%d built-in themes", len(Builtin()))
	}
	for _, th := range Builtin() {
		t.Run(th.ID, func(t *testing.T) {
			data, ok := BuiltinSource(th.ID)
			if !ok {
				t.Fatal("no source")
			}
			f, warnings, err := ParseFile(data)
			if err != nil || len(warnings) > 0 || len(th.Warnings) > 0 {
				t.Fatalf("parse: %v %q %q", err, warnings, th.Warnings)
			}
			if f.Extends == "" {
				if missing := th.missingColors(); len(missing) > 0 {
					t.Fatalf("missing colors %v", missing)
				}
			}
			if th.Name == "" || th.Source == "" || th.License == "" {
				t.Fatalf("name %q, source %q, license %q", th.Name, th.Source, th.License)
			}
			colors := th.Colors()
			for _, name := range ColorNames() {
				if _, ok := colors[name]; !ok {
					t.Errorf("no color %s", name)
				}
			}
			if th.HighContrast && (th.Material != MaterialNone || th.Opacity != 1) {
				t.Error("a high contrast theme must be opaque")
			}
			for _, r := range CheckContrast(th) {
				if r.OK {
					continue
				}
				t.Errorf("%s: %.2f:1 < %.1f:1 (over %s)", r.Pair, r.Ratio, r.Min, r.Over)
			}
		})
	}
}

// TestGolauncherThemesAreMyGos keeps the default themes looking as before
// themes, MyGo's own light and dark themes, but for the colors darkened to
// pass the contrast checks (R56, docs/themes.md §15): the accent of both,
// the light theme's secondary text.
func TestGolauncherThemesAreMyGos(t *testing.T) {
	lib := NewLibrary("")
	for _, dark := range []bool{false, true} {
		want := *mui.LightTheme()
		want.Accent, want.TextMuted = mui.Hex("#2461e6"), mui.Hex("#68686f") // MyGo's #2563eb, #71717a
		if dark {
			want = *mui.DarkTheme()
			want.Accent = mui.Hex("#3472d8") // MyGo's #3b82f6
		}
		want.Inverse, want.InverseText = want.Text, want.Background // what MyGo uses when they are zero
		r := Resolve(lib, defaultAppearance(), System{Dark: dark, NoMaterial: true})
		if got := r.UI; got != want {
			t.Errorf("dark %v:\n got %+v\nwant %+v", dark, got, want)
		}
	}
}

// TestSystemAccentReadable: whatever the Windows accent, light or dark, text
// on it and on the selected row of golauncher's themes stays readable at
// any background opacity (R56).
func TestSystemAccentReadable(t *testing.T) {
	lib := NewLibrary("")
	accents := []string{"#0078d4", "#ffb900", "#ffd700", "#fff100", "#00b7c3", "#99ebff", "#4cc2ff", "#00cc6a",
		"#a4c400", "#e81123", "#ff4343", "#107c10", "#744da9", "#4a148c", "#000000", "#ffffff", "#767676", "#3b82f6"}
	for _, hex := range accents {
		for _, dark := range []bool{false, true} {
			for _, opacity := range []float64{0, 0.3, 0.7, 1} {
				a := defaultAppearance()
				a.Opacity = &opacity
				sys := System{Dark: dark, Accent: mui.Hex(hex)}
				r := Resolve(lib, a, sys)
				for _, c := range r.Contrast {
					if !c.OK {
						t.Errorf("accent %s, dark %v, opacity %v: accent %s: %s %.2f:1 (over %s)",
							hex, dark, opacity, FormatColor(r.Colors["accent"]), c.Pair, c.Ratio, c.Over)
					}
				}
			}
		}
	}
	// An accent that is readable already is used as it is.
	r := Resolve(lib, defaultAppearance(), System{Dark: true, Accent: mui.Hex("#0078d4")})
	if got := FormatColor(r.Colors["accent"]); got != "#0078d4" {
		t.Errorf("readable accent changed to %s", got)
	}
	// A custom accent is used as chosen; only its text follows it.
	a := defaultAppearance()
	a.Accent = "#00b7c3"
	r = Resolve(lib, a, System{})
	if got := FormatColor(r.Colors["accent"]); got != "#00b7c3" || r.Colors["accentText"] != darkAccentText {
		t.Errorf("custom accent %s, text %s", got, FormatColor(r.Colors["accentText"]))
	}
}
