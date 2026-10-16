package theme

import (
	"fmt"
	"slices"
	"testing"

	mui "github.com/egoist/mygo/ui"
)

// legacyContrast lists the contrast checks that golauncher's own themes,
// which keep the look from before themes (MyGo's default colors), are
// known to miss: white on MyGo's blue accent. Every other built-in theme,
// and every one added later, must pass all of them.
var legacyContrast = map[string][]string{
	"golauncher-light": {"textMuted/background", "rowSelectedText/rowSelected"},
	"golauncher-dark":  {"accentText/accent", "rowSelectedText/rowSelected"},
}

// TestBuiltinThemes guards every built-in theme, including ones added
// later: it parses, has every color, and its text is readable.
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
				if r.OK || slices.Contains(legacyContrast[th.ID], r.Pair) {
					continue
				}
				t.Errorf("%s: %.2f:1 < %.1f:1 (over %s)", r.Pair, r.Ratio, r.Min, r.Over)
			}
		})
	}
}

// TestGolauncherThemesAreMyGos keeps the default themes looking exactly as
// before themes: MyGo's own light and dark themes.
func TestGolauncherThemesAreMyGos(t *testing.T) {
	lib := NewLibrary("")
	for _, dark := range []bool{false, true} {
		want := mui.LightTheme()
		if dark {
			want = mui.DarkTheme()
		}
		want.Inverse, want.InverseText = want.Text, want.Background // what MyGo uses when they are zero
		r := Resolve(lib, defaultAppearance(), System{Dark: dark, NoMaterial: true})
		if got := r.UI; got != *want {
			t.Errorf("dark %v:\n got %+v\nwant %+v", dark, got, *want)
		}
	}
}

func TestPrintContrast(t *testing.T) {
	if !testing.Verbose() {
		t.Skip()
	}
	for _, th := range Builtin() {
		for _, r := range CheckContrast(th) {
			fmt.Printf("%-20s %-28s %6.2f %v %s\n", th.ID, r.Pair, r.Ratio, r.OK, r.Over)
		}
	}
}
