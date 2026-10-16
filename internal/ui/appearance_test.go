package ui_test

import (
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/theme"
	"github.com/eziq1/golauncher/internal/ui"
)

// pixel returns the color at x, y (in DIPs) of the tester's image, with its
// alpha undone.
func pixel(tt *mui.Tester, x, y int) color.NRGBA {
	return color.NRGBAModel.Convert(tt.Image().At(x, y)).(color.NRGBA)
}

func near(a, b uint8) bool { d := int(a) - int(b); return d >= -3 && d <= 3 }

func sameColor(got color.NRGBA, want mui.Color) bool {
	return near(got.R, want.R) && near(got.G, want.G) && near(got.B, want.B) && near(got.A, want.A)
}

// emptyPoint is a point of the launcher where only the background shows:
// the padding left of the results.
const emptyX, emptyY = 2, ui.LauncherHeight / 2

func useTheme(env *app.Env, id string, fn func(a *config.Appearance)) {
	env.Config.Update(func(c *config.Config) {
		c.Appearance.FollowSystem, c.Appearance.Theme = false, id
		if fn != nil {
			fn(&c.Appearance)
		}
	})
}

// selectedRowColor samples the row of title, the first and selected one.
func selectedRowColor(t *testing.T, lt *launcherTest, title string) color.NRGBA {
	t.Helper()
	r, ok := lt.tt.Find(title)
	if !ok {
		t.Fatalf("no %q in %q", title, lt.tt.Texts())
	}
	return pixel(lt.tt, int(r.X)+4, int(r.Y+r.H/2)) // in the padding, left of the icon
}

func TestLauncherDrawsTheTheme(t *testing.T) {
	lt := newLauncher(t, "en")
	golauncher := mui.LightTheme()
	// Without a material (the tester's default), the background is solid.
	if got := pixel(lt.tt, emptyX, emptyY); !sameColor(got, golauncher.Background) {
		t.Fatalf("golauncher background = %v", got)
	}
	if got := selectedRowColor(t, lt, "AI Activity"); !sameColor(got, golauncher.Accent.Alpha(0.9).Over(golauncher.Background)) {
		t.Fatalf("golauncher selected row = %v", got)
	}

	useTheme(lt.Env, "nord", nil)
	lt.step()
	nord, _ := lt.Themes.Library().Get("nord")
	colors := nord.Colors()
	if got := pixel(lt.tt, emptyX, emptyY); !sameColor(got, colors["background"]) {
		t.Fatalf("nord background = %v", got)
	}
	if got := selectedRowColor(t, lt, "AI Activity"); !sameColor(got, colors["rowSelected"]) {
		t.Fatalf("nord selected row = %v, want %v", got, colors["rowSelected"])
	}

	// With the material, the background has the theme's opacity, and the
	// selected row is solid at it.
	lt.tt.SetVibrancy(true)
	lt.step()
	if got := pixel(lt.tt, emptyX, emptyY); !sameColor(got, colors["background"].Alpha(0.85)) {
		t.Fatalf("nord translucent background = %v", got)
	}
	if got := selectedRowColor(t, lt, "AI Activity"); !sameColor(got, colors["rowSelected"]) {
		t.Fatalf("nord translucent selected row = %v", got)
	}

	// Lower, the row follows the background.
	useTheme(lt.Env, "nord", func(a *config.Appearance) { v := 0.3; a.Opacity = &v })
	lt.step()
	if got := pixel(lt.tt, emptyX, emptyY); !near(got.A, 77) {
		t.Fatalf("background at 30%% = %v", got)
	}
	if got := selectedRowColor(t, lt, "AI Activity"); got.A == 255 || got.A < 150 {
		t.Fatalf("row at 30%% = %v", got)
	}

	// Transparency effects off, or a contrast theme: solid again.
	lt.Themes.SetReduceTransparency(true)
	lt.step()
	if got := pixel(lt.tt, emptyX, emptyY); got.A != 255 {
		t.Fatalf("transparency off: %v", got)
	}
	lt.Themes.SetReduceTransparency(false)
	lt.tt.SetPreferences(mui.Preferences{HighContrast: true})
	lt.step()
	if got := pixel(lt.tt, emptyX, emptyY); got.A != 255 {
		t.Fatalf("high contrast: %v", got)
	}
}

func TestLauncherFollowsSystemAndAccent(t *testing.T) {
	lt := newLauncher(t, "en")
	lt.Config.Update(func(c *config.Config) {
		c.Appearance.LightTheme, c.Appearance.DarkTheme = "solarized-light", "dracula"
	})
	lt.step()
	sol, _ := lt.Themes.Library().Get("solarized-light")
	if got := pixel(lt.tt, emptyX, emptyY); !sameColor(got, sol.Colors()["background"]) {
		t.Fatalf("light: %v", got)
	}
	lt.tt.SetDark(true)
	lt.step()
	dra, _ := lt.Themes.Library().Get("dracula")
	if got := pixel(lt.tt, emptyX, emptyY); !sameColor(got, dra.Colors()["background"]) {
		t.Fatalf("dark: %v", got)
	}

	// golauncher's themes take the Windows accent, as before themes.
	lt.Config.Update(func(c *config.Config) { c.Appearance = config.DefaultAppearance() })
	lt.tt.SetDark(false)
	red := mui.RGB(200, 30, 30)
	lt.tt.SetPreferences(mui.Preferences{Accent: red})
	lt.step()
	if got := selectedRowColor(t, lt, "AI Activity"); !sameColor(got, red.Alpha(0.9).Over(mui.LightTheme().Background)) {
		t.Fatalf("windows accent row = %v", got)
	}
}

// TestIconTintsFromTheme: plugins name a tint; the theme has its color.
func TestIconTintsFromTheme(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tinted.json"), []byte(`{"extends": "nord", "colors": {"tint.muted": "#ff00ff"}}`), 0o644)
	env, _ := newEnvThemes(t, "en", dir)
	r := env.Themes.Resolve(func() config.Appearance {
		a := config.DefaultAppearance()
		a.FollowSystem, a.Theme = false, "tinted"
		return a
	}(), theme.System{})
	if r.Tokens.Tint("muted", mui.Color{}) != mui.Hex("#ff00ff") || r.Tokens.Tint("unknown", mui.Hex("#010203")) != mui.Hex("#010203") {
		t.Fatal("tints")
	}
}

func newAppearanceSettings(t *testing.T, dir string) (*app.Env, *ui.Settings, *mui.Tester, *fakeHost) {
	t.Helper()
	env, _ := newEnvThemes(t, "en", dir)
	host := &fakeHost{}
	s := ui.NewSettings(env, host, "1.0.0-test")
	tt := mui.NewTester(s.View, ui.SettingsWidth, 2400) // the whole page
	s.ShowPage(ui.PageAppearance)
	tt.Frame()
	return env, s, tt, host
}

func TestAppearanceGallery(t *testing.T) {
	env, _, tt, host := newAppearanceSettings(t, "")
	for _, id := range theme.BuiltinOrder {
		th, _ := env.Themes.Library().Get(id)
		if !tt.HasText(th.Name) {
			t.Fatalf("no thumbnail of %s in %q", id, tt.Texts())
		}
	}
	// Following the system, a dark theme becomes the dark one.
	if err := tt.Click("Nord"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a := env.Config.Get().Appearance
	if a.DarkTheme != "nord" || a.LightTheme != config.DefaultLightTheme || !a.FollowSystem || host.previews == 0 {
		t.Fatalf("appearance %+v, previews %d", a, host.previews)
	}
	// Not following, it is the theme.
	if err := tt.Click("Follow Windows light and dark mode"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a := env.Config.Get(); a.Appearance.FollowSystem || a.Appearance.Theme != config.DefaultLightTheme || a.Theme != "light" {
		t.Fatalf("not following: %+v, legacy %q", a.Appearance, a.Theme)
	}
	if err := tt.Click("Paper"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a := env.Config.Get(); a.Appearance.Theme != "paper" || a.Theme != "light" {
		t.Fatalf("paper: %+v", a.Appearance)
	}
	if err := tt.Click("Dracula"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a := env.Config.Get(); a.Appearance.Theme != "dracula" || a.Theme != "dark" {
		t.Fatalf("dracula: %+v, legacy %q", a.Appearance, a.Theme)
	}
}

func TestAppearanceSliders(t *testing.T) {
	env, _, tt, host := newAppearanceSettings(t, "")
	useTheme(env, "nord", nil)
	tt.Frame()
	if !tt.HasText("85%") {
		t.Fatalf("the theme's opacity: %q", tt.Texts())
	}
	// Pressing the middle of the slider previews 50% in the launcher;
	// letting go saves it. The slider is 200 DIPs wide, 10 left of its
	// value.
	v, ok := tt.Find("85%")
	if !ok {
		t.Fatal("no slider value")
	}
	sl := mui.Rect{X: v.X - 10 - 200, Y: v.Y, W: 200, H: v.H}
	tt.Press(sl.X+sl.W/2, sl.Y+sl.H/2)
	tt.Frame()
	if !env.Themes.Previewing() || host.previews == 0 || env.Config.Get().Appearance.Opacity != nil {
		t.Fatalf("while pressed: previewing %v, opacity %v", env.Themes.Previewing(), env.Config.Get().Appearance.Opacity)
	}
	if got := env.Themes.Resolve(env.Config.Get().Appearance, theme.System{}).ThemeOpacity; got < 0.4 || got > 0.6 {
		t.Fatalf("previewed opacity %v", got)
	}
	tt.Release(sl.X+sl.W/2, sl.Y+sl.H/2)
	tt.Frame()
	tt.Frame()
	if o := env.Config.Get().Appearance.Opacity; o == nil || *o < 0.4 || *o > 0.6 || env.Themes.Previewing() {
		t.Fatalf("after release: %v", o)
	}

	// Transparency effects off: the slider says why it is off.
	env.Themes.SetReduceTransparency(true)
	tt.Frame()
	if !tt.HasText("Transparency effects are off in Windows settings, so the background is solid.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	env.Themes.SetReduceTransparency(false)
	tt.Frame()

	// Reset to the theme's own values.
	if err := tt.Click("Reset to theme defaults"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if env.Config.Get().Appearance.Opacity != nil {
		t.Fatal("reset")
	}
	// Density and accent.
	for _, label := range []string{"Compact", "Custom"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	a := env.Config.Get().Appearance
	if a.Density == nil || *a.Density != "compact" || !strings.HasPrefix(a.Accent, "#") {
		t.Fatalf("density %v, accent %q", a.Density, a.Accent)
	}
}

func TestAppearanceCustomThemes(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"extends": "nope"}`), 0o644)
	env, _, tt, host := newAppearanceSettings(t, dir)
	if !tt.HasText(`broken: extends: no theme "nope"`) {
		t.Fatalf("problems: %q", tt.Texts())
	}

	// Paste a theme.
	if err := tt.Click("Paste JSON…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Theme JSON"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type(`{"name": "Midnight Teal", "extends": "golauncher-dark", "colors": {"accent": "#2dd4bf", "accentText": "#0f1a1f"}}`)
	tt.Frame()
	if err := tt.Click("Import"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Theme “Midnight Teal” imported.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if _, err := os.Stat(filepath.Join(dir, "midnight-teal.json")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Midnight Teal") {
		t.Fatal("the new theme is in the gallery")
	}

	// Import a file; a bad one says why.
	src := filepath.Join(t.TempDir(), "my-nord.json")
	os.WriteFile(src, []byte(`{"name": "My Nord", "extends": "nord", "colors": {"accent": "#ff0000"}}`), 0o644)
	host.themeOpenPath = src
	if err := tt.Click("Import file…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := env.Themes.Library().Get("my-nord"); !ok {
		t.Fatalf("not imported: %q", tt.Texts())
	}
	os.WriteFile(src, []byte(`{"name": `), 0o644)
	if err := tt.Click("Import file…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "Could not import the theme:") }) {
		t.Fatalf("texts %q", tt.Texts())
	}

	// Export the theme in use with its adjustments.
	useTheme(env, "my-nord", func(a *config.Appearance) { v := 0.5; a.Opacity = &v })
	tt.Frame()
	host.themeSavePath = filepath.Join(t.TempDir(), "out.json")
	if err := tt.Click("Export current…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	data, err := os.ReadFile(host.themeSavePath)
	if err != nil || !strings.Contains(string(data), `"opacity": 0.5`) || !strings.Contains(string(data), `"accent": "#ff0000"`) || host.themeSaveName != "my-nord.json" {
		t.Fatalf("export %q (%s): %s %v", host.themeSaveName, tt.Texts(), data, err)
	}

	// Duplicate it.
	if err := tt.Click("Duplicate as theme"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	cp, ok := env.Themes.Library().Get("my-nord-copy")
	if !ok || cp.Name != "My Nord (copy)" {
		t.Fatalf("duplicate: %+v %q", cp, tt.Texts())
	}
}

// TestAppearanceHotReloadRedraws: editing a theme file shows in the
// launcher without anything else happening.
func TestAppearanceHotReloadRedraws(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "live.json"), []byte(`{"extends": "nord", "colors": {"background": "#102030"}}`), 0o644)
	env, _ := newEnvThemes(t, "en", dir)
	lt := &launcherTest{Env: env, t: t, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	useTheme(env, "live", nil)
	lt.step()
	if got := pixel(lt.tt, emptyX, emptyY); !sameColor(got, mui.Hex("#102030")) {
		t.Fatalf("before: %v", got)
	}
	go env.Themes.Library().Watch(t.Context(), 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond) // a later modification time
	os.WriteFile(filepath.Join(dir, "live.json"), []byte(`{"extends": "nord", "colors": {"background": "#302010"}}`), 0o644)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		lt.step()
		if got := pixel(lt.tt, emptyX, emptyY); sameColor(got, mui.Hex("#302010")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the launcher did not follow the file")
		}
	}
}

// BenchmarkAppearancePage draws the Appearance page with its gallery of
// thumbnails (docs/themes.md §8.3).
func BenchmarkAppearancePage(b *testing.B) {
	clip := &hostfake.Clipboard{}
	env, err := app.Build(b.Context(), app.Options{Fake: true, Locale: "en", System: &app.Recorder{Clipboard: clip}, Clipboard: clip})
	if err != nil {
		b.Fatal(err)
	}
	defer env.Close()
	s := ui.NewSettings(env, &fakeHost{}, "1")
	s.ShowPage(ui.PageAppearance)
	tt := mui.NewTester(s.View, ui.SettingsWidth, 2400)
	for b.Loop() {
		tt.Frame()
	}
}
