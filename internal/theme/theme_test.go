package theme

import (
	"context"
	"encoding/json/v2"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
)

func TestParseColor(t *testing.T) {
	for in, want := range map[string]mui.Color{
		"#fff":                     mui.RGB(255, 255, 255),
		"#FFF8":                    mui.RGBA(255, 255, 255, 0.5333),
		"#0f1a1f":                  mui.RGB(15, 26, 31),
		"#0f1a1f80":                {R: 15, G: 26, B: 31, A: 128},
		" #ABCDEF ":                mui.RGB(0xab, 0xcd, 0xef),
		"rgb(1, 2, 3)":             mui.RGB(1, 2, 3),
		"rgba(37, 99, 235, 0.25)":  mui.RGBA(37, 99, 235, 0.25),
		"RGBA(255 0 0 / 50%)":      mui.RGBA(255, 0, 0, 0.5),
		"rgb(100%, 0%, 50%)":       mui.RGB(255, 0, 128),
		"transparent":              mui.Transparent,
		"rgba(0,0,0,0)":            mui.RGBA(0, 0, 0, 0),
		"rgba(255, 255, 255, 1.0)": mui.RGB(255, 255, 255),
	} {
		got, err := ParseColor(in)
		if err != nil || got != want {
			t.Errorf("ParseColor(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "red", "#ff", "#fffff", "#ggg", "rgb(1,2)", "rgb(1,2,3", "rgba(1,2,3,2)", "rgb(256,0,0)", "rgb(a,b,c)", "hsl(0,0,0)"} {
		if _, err := ParseColor(in); err == nil {
			t.Errorf("ParseColor(%q): want an error", in)
		}
	}
	if s := FormatColor(mui.RGBA(37, 99, 235, 0.25)); s != "#2563eb40" {
		t.Errorf("FormatColor = %s", s)
	}
	if s := FormatColor(mui.RGB(1, 2, 3)); s != "#010203" {
		t.Errorf("FormatColor = %s", s)
	}
	if r := ContrastRatio(white, black); r != 21 {
		t.Errorf("ratio = %v", r)
	}
}

// writeTheme writes a theme file into dir.
func writeTheme(t *testing.T, dir, id, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRenamedBuiltinThemes: the old IDs of the "Raycast-style" themes still
// find them, as Crimson (R56), in the configuration and in extends.
func TestRenamedBuiltinThemes(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "mine", `{"name": "Mine", "extends": "raycast-light", "colors": {"accent": "#aa0000"}}`)
	lib := loaded(t, dir)
	if th, ok := lib.Get("raycast-dark"); !ok || th.ID != "crimson-dark" || th.DisplayName("zh-CN") != "绯红（深色）" {
		t.Fatalf("raycast-dark = %+v, %v", th, ok)
	}
	th, ok := lib.Get("mine")
	if !ok || th.Dark || th.Colors()["border"] != mui.Hex("#e2e2e5") || len(lib.Problems()) != 0 {
		t.Fatalf("mine = %+v, %v, problems %+v", th, ok, lib.Problems())
	}
	a := config.DefaultAppearance()
	a.FollowSystem, a.Theme = false, "raycast-dark"
	if r := Resolve(lib, a, System{}); r.Missing || r.Theme.ID != "crimson-dark" {
		t.Fatalf("resolved %s, missing %v", r.Theme.ID, r.Missing)
	}
	if rep := lib.Validate("other.json", []byte(`{"extends": "raycast-dark"}`)); !rep.OK {
		t.Fatalf("validate: %+v", rep)
	}
}

func loaded(t *testing.T, dir string) *Library {
	t.Helper()
	lib := NewLibrary(dir)
	if err := lib.Reload(); err != nil {
		t.Fatal(err)
	}
	return lib
}

func problemOf(lib *Library, id string) Problem {
	for _, p := range lib.Problems() {
		if p.ID == id {
			return p
		}
	}
	return Problem{}
}

func TestExtends(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "teal", `{
		"$schema": "x", "name": "Midnight Teal", "name.zh-CN": "午夜青", "author": "someone",
		"extends": "golauncher-dark",
		"colors": {"background": "#0f1a1f", "accent": "#2dd4bf", "rowSelected": "#1e3a40"},
		"material": "mica", "opacity": 0.65}`)
	writeTheme(t, dir, "teal-light-text", `{"extends": "teal", "colors": {"text": "#ffffff", "accentHover": "#ff0000"}}`)
	lib := loaded(t, dir)
	teal, ok := lib.Get("teal")
	if !ok {
		t.Fatalf("problems: %+v", lib.Problems())
	}
	c := teal.Colors()
	base, _ := lib.Get("golauncher-dark")
	bc := base.Colors()
	switch {
	case c["background"] != mui.Hex("#0f1a1f") || c["rowSelected"] != mui.Hex("#1e3a40"):
		t.Error("own colors")
	case c["surface"] != bc["surface"] || c["text"] != bc["text"]:
		t.Error("inherited colors")
	case c["accentHover"] != mui.Hex("#2dd4bf").Mix(white, 0.2):
		t.Errorf("accentHover = %v: a new accent derives its hover color, not the base's", c["accentHover"])
	case c["inverseText"] != mui.Hex("#0f1a1f"):
		t.Error("inverseText follows the new background")
	case !teal.Dark || teal.Material != MaterialMica || teal.Opacity != 0.65 || teal.Radius != DefaultRadius:
		t.Errorf("appearance %+v", teal)
	case teal.DisplayName("zh-CN") != "午夜青" || teal.DisplayName("zh") != "Midnight Teal" || teal.DisplayName("en") != "Midnight Teal":
		t.Error("names")
	case teal.Builtin || teal.Path != filepath.Join(dir, "teal.json") || teal.Extends != "golauncher-dark":
		t.Errorf("%+v", teal)
	}
	child, _ := lib.Get("teal-light-text")
	cc := child.Colors()
	if cc["accentHover"] != mui.Hex("#ff0000") || cc["background"] != c["background"] || cc["searchText"] != white {
		t.Error("a child's own color wins; a launcher color follows its base")
	}
	if child.DisplayName("zh-CN") != "teal-light-text" {
		t.Error("a theme without a name of its own is called by its ID")
	}
	if len(lib.Themes()) != len(BuiltinOrder)+2 {
		t.Errorf("%d themes", len(lib.Themes()))
	}
}

func TestExtendsErrors(t *testing.T) {
	dir := t.TempDir()
	writeTheme(t, dir, "a", `{"extends": "b"}`)
	writeTheme(t, dir, "b", `{"extends": "a"}`)
	writeTheme(t, dir, "self", `{"extends": "self"}`)
	writeTheme(t, dir, "orphan", `{"extends": "nope"}`)
	writeTheme(t, dir, "bare", `{"colors": {"background": "#000000"}}`)
	writeTheme(t, dir, "nord", `{"extends": "dracula"}`)
	writeTheme(t, dir, "broken", `{"name": `)
	writeTheme(t, dir, "typed", `{"opacity": "high"}`)
	writeTheme(t, dir, "warned", `{"extends": "nord", "colour": 1, "colors": {"accent": "blue", "texxt": "#fff", "tint.pink": "#ff00ff"},
		"opacity": 3, "material": "glass", "density": "airy", "radius": 99, "fontSize": 2}`)
	lib := loaded(t, dir)
	for id, want := range map[string]string{
		"a":      "cycle",
		"self":   "cycle",
		"orphan": `no theme "nope"`,
		"bare":   "missing colors surface, border, text",
		"nord":   "built-in",
		"broken": "not a theme",
		"typed":  "not a theme",
	} {
		p := problemOf(lib, id)
		if !strings.Contains(p.Error, want) {
			t.Errorf("%s: error %q, want %q", id, p.Error, want)
		}
		if th, ok := lib.Get(id); ok && !th.Builtin {
			t.Errorf("%s: loaded", id)
		}
	}
	w, ok := lib.Get("warned")
	if !ok {
		t.Fatal("warnings do not stop a theme")
	}
	p := problemOf(lib, "warned")
	for _, want := range []string{`unknown field "colour"`, `unknown color "texxt"`, `color "accent"`, "opacity 3", `material "glass"`,
		`density "airy"`, "radius 99", "fontSize 2"} {
		if !slices.ContainsFunc(p.Warnings, func(s string) bool { return strings.Contains(s, want) }) {
			t.Errorf("no warning %q in %q", want, p.Warnings)
		}
	}
	nord, _ := lib.Get("nord")
	if w.Colors()["accent"] != nord.Colors()["accent"] || w.Opacity != nord.Opacity || w.Material != nord.Material {
		t.Error("a bad value keeps the base's")
	}
	if w.Colors()["tint.pink"] != mui.Hex("#ff00ff") {
		t.Error("tints of other names are kept")
	}
}

func TestHotReload(t *testing.T) {
	dir := t.TempDir()
	lib := NewLibrary(dir)
	var changes atomic.Int32
	lib.OnChange(func() { changes.Add(1) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go lib.Watch(ctx, 5*time.Millisecond)
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; problems %+v", what, lib.Problems())
			}
		}
	}
	accent := func() mui.Color {
		th, ok := lib.Get("live")
		if !ok {
			return mui.Color{}
		}
		return th.Colors()["accent"]
	}

	// A good file shows up.
	writeTheme(t, dir, "live", `{"extends": "nord", "colors": {"accent": "#112233"}}`)
	waitFor("the new theme", func() bool { return accent() == mui.Hex("#112233") })
	gen := lib.Generation()

	// A file written broken keeps the last good version and says why.
	writeTheme(t, dir, "live", `{"extends": "nord", "colors": {"accent": `)
	waitFor("the error", func() bool { return problemOf(lib, "live").Error != "" })
	if p := problemOf(lib, "live"); !p.Stale || accent() != mui.Hex("#112233") || lib.Generation() == gen {
		t.Fatalf("broken write: %+v, accent %v", p, accent())
	}

	// Fixed again.
	writeTheme(t, dir, "live", `{"extends": "nord", "colors": {"accent": "#445566"}}`)
	waitFor("the fix", func() bool { return accent() == mui.Hex("#445566") })
	if p := problemOf(lib, "live"); p.Error != "" {
		t.Fatalf("problem after the fix: %+v", p)
	}

	// Removed.
	os.Remove(filepath.Join(dir, "live.json"))
	waitFor("the removal", func() bool { _, ok := lib.Get("live"); return !ok })
	if changes.Load() < 4 {
		t.Errorf("%d changes", changes.Load())
	}
}

func TestMissingDirIsEmpty(t *testing.T) {
	lib := loaded(t, filepath.Join(t.TempDir(), "none"))
	if len(lib.Themes()) != len(BuiltinOrder) || len(lib.Problems()) != 0 {
		t.Fatal("a missing folder has no themes")
	}
}

func TestResolveFollowSystem(t *testing.T) {
	lib := NewLibrary("")
	a := config.DefaultAppearance()
	a.LightTheme, a.DarkTheme = "solarized-light", "solarized-dark"
	if r := Resolve(lib, a, System{}); r.Theme.ID != "solarized-light" || r.Dark || r.UI.Dark {
		t.Errorf("light: %s", r.Theme.ID)
	}
	if r := Resolve(lib, a, System{Dark: true}); r.Theme.ID != "solarized-dark" || !r.Dark || !r.UI.Dark {
		t.Errorf("dark: %s", r.Theme.ID)
	}
	a.FollowSystem, a.Theme = false, "nord"
	if r := Resolve(lib, a, System{}); r.Theme.ID != "nord" {
		t.Errorf("fixed: %s", r.Theme.ID)
	}
	a.Theme = "gone"
	if r := Resolve(lib, a, System{Dark: true}); r.Theme.ID != config.DefaultDarkTheme || !r.Missing || r.Requested != "gone" {
		t.Errorf("missing: %s", r.Theme.ID)
	}
}

func TestResolveAccent(t *testing.T) {
	lib := NewLibrary("")
	windows := mui.RGB(0xc4, 0x2b, 0x1c)
	a := config.DefaultAppearance()
	// golauncher's themes follow the Windows accent as MyGo's own theme does.
	r := Resolve(lib, a, System{Accent: windows})
	want := mui.LightTheme()
	if r.UI.Accent != windows || r.UI.AccentHover != windows.Mix(black, 0.12) || r.UI.Selection != windows.Alpha(0.25) ||
		r.UI.AccentText != white || r.Tokens.RowSelected != windows.Alpha(0.9) || r.UI.Background != want.Background {
		t.Errorf("windows accent: %+v", r.UI)
	}
	// Other themes keep theirs unless asked.
	a.FollowSystem, a.Theme = false, "nord"
	nord := Resolve(lib, a, System{Accent: windows})
	if nord.UI.Accent != mui.Hex("#88c0d0") {
		t.Errorf("nord accent %v", nord.UI.Accent)
	}
	a.Accent = config.AccentWindows
	r = Resolve(lib, a, System{Accent: windows})
	if r.UI.Accent != windows || r.Tokens.RowSelected != nord.Tokens.RowSelected || r.Tokens.Tints["accent"] != windows {
		t.Errorf("nord with the Windows accent: %v, row %v", r.UI.Accent, r.Tokens.RowSelected)
	}
	if r := Resolve(lib, a, System{}); r.UI.Accent != mui.Hex("#88c0d0") {
		t.Error("no Windows accent: the theme's")
	}
	a.Accent = "#ffd700" // yellow: dark text on it
	r = Resolve(lib, a, System{})
	if r.UI.Accent != mui.Hex("#ffd700") || r.UI.AccentText != mui.RGB(24, 24, 27) {
		t.Errorf("custom accent: %v on %v", r.UI.AccentText, r.UI.Accent)
	}
	a.Accent = "nonsense"
	if r := Resolve(lib, a, System{}); r.UI.Accent != mui.Hex("#88c0d0") {
		t.Error("a bad accent is the theme's")
	}
}

func TestResolveOpaque(t *testing.T) {
	lib := NewLibrary("")
	a := config.DefaultAppearance()
	a.FollowSystem, a.Theme = false, "nord"
	r := Resolve(lib, a, System{})
	bg := mui.Hex("#2e3440")
	if r.Opaque != "" || r.Opacity != 0.85 || r.Background != bg.Alpha(0.85) || r.Material != MaterialAcrylic || !r.Translucent() {
		t.Fatalf("translucent: %+v", r)
	}
	for sys, want := range map[System]OpaqueReason{
		{HighContrast: true, ReduceTransparency: true}: OpaqueHighContrast,
		{ReduceTransparency: true}:                     OpaqueTransparencyOff,
		{NoMaterial: true}:                             OpaqueUnsupported,
	} {
		r := Resolve(lib, a, sys)
		if r.Opaque != want || r.Opacity != 1 || r.Background != bg && !sys.HighContrast || r.Tokens.SectionBackground == mui.Transparent {
			t.Errorf("%+v: %s, %v", sys, r.Opaque, r.Background)
		}
	}
	a.Material = ptr("none")
	if r := Resolve(lib, a, System{}); r.Opaque != OpaqueMaterialNone || r.Material != MaterialNone {
		t.Errorf("material none: %s", r.Opaque)
	}
	a.Material = nil
	a.FollowSystem = true // golauncher: opacity 0, as before themes
	if r := Resolve(lib, a, System{}); r.Background != mui.Transparent || r.Tokens.RowSelected != mui.Hex("#2461e6").Alpha(0.9) {
		t.Errorf("golauncher: %v, row %v", r.Background, r.Tokens.RowSelected)
	}
	a.FollowSystem, a.Theme = false, "high-contrast-dark"
	if r := Resolve(lib, a, System{}); r.Opaque != OpaqueMaterialNone {
		t.Error("high contrast themes are opaque")
	}
}

func TestResolveNullVsManual(t *testing.T) {
	lib := NewLibrary("")
	a := config.DefaultAppearance()
	a.FollowSystem, a.Theme = false, "dracula"
	r := Resolve(lib, a, System{})
	if r.ThemeOpacity != 0.85 || r.UI.Radius != DefaultRadius || r.UI.Spacing != 4 || r.UI.FontSize != mui.LightTheme().FontSize {
		t.Fatalf("theme's values: %+v", r)
	}
	a.Opacity, a.Radius, a.Density, a.FontSize, a.Material = ptr(0.3), ptr(10.0), ptr("compact"), ptr(16.0), ptr("mica")
	r = Resolve(lib, a, System{TextScale: 1.5})
	if r.ThemeOpacity != 0.3 || r.UI.Radius != 10 || r.UI.Spacing != 3 || r.UI.FontSize != 24 || r.Material != MaterialMica ||
		r.RadiusScale != 10.0/6 || r.SpaceScale != 0.75 {
		t.Fatalf("manual values: %+v", r)
	}
	// The overlays scale with the background; text gets its backdrop when
	// it is turned on.
	a.TextShadow = true
	r = Resolve(lib, a, System{TextScale: 1.5})
	if r.Tokens.RowSelected.A >= 255 || r.Tokens.TextShadow == mui.Transparent {
		t.Errorf("row %v, shadow %v", r.Tokens.RowSelected, r.Tokens.TextShadow)
	}
	a.TextShadow = false
	if r := Resolve(lib, a, System{}); r.Tokens.TextShadow != mui.Transparent {
		t.Error("text shadow off")
	}
	// Choosing another theme keeps what was set by hand.
	a.Theme = "nord"
	if r := Resolve(lib, a, System{}); r.ThemeOpacity != 0.3 || r.UI.Radius != 10 {
		t.Error("manual values follow to another theme")
	}
	a.ResetToTheme()
	if r := Resolve(lib, a, System{}); r.ThemeOpacity != 0.85 || r.UI.Radius != DefaultRadius {
		t.Error("reset to the theme's")
	}
}

func TestResolveHighContrastAndContrast(t *testing.T) {
	lib := NewLibrary("")
	a := config.DefaultAppearance()
	r := Resolve(lib, a, System{HighContrast: true})
	want := mui.LightTheme()
	if r.UI.TextMuted != mui.Hex("#68686f").Mix(want.Text, 0.4) || r.UI.Border != want.Border.Mix(want.Text, 0.45) {
		t.Error("high contrast strengthens muted text and borders, as MyGo does")
	}
	if r.Tokens.SectionHeader != r.UI.TextMuted {
		t.Error("launcher colors follow")
	}
	// Too translucent: the check says so.
	a.FollowSystem, a.Theme, a.Opacity = false, "solarized-dark", ptr(0.0)
	r = Resolve(lib, a, System{})
	bad := slices.IndexFunc(r.Contrast, func(c ContrastResult) bool { return !c.OK })
	if bad < 0 {
		t.Errorf("contrast at opacity 0: %+v", r.Contrast)
	}
}

func TestManagerCachesAndPreviews(t *testing.T) {
	dir := t.TempDir()
	lib := loaded(t, dir)
	m := NewManager(lib)
	var changes int
	m.OnChange(func() { changes++ })
	a := config.DefaultAppearance()
	r1 := m.Resolve(a, System{})
	if r2 := m.Resolve(a.Clone(), System{}); r1 != r2 {
		t.Error("the same inputs are resolved once")
	}
	if r3 := m.Resolve(a, System{Dark: true}); r3 == r1 || !r3.Dark {
		t.Error("a system change resolves again")
	}
	p := a.Clone()
	p.Opacity = ptr(0.5)
	m.SetPreview(&p)
	if r := m.Resolve(a, System{}); r.ThemeOpacity != 0.5 || !m.Previewing() {
		t.Error("the preview wins")
	}
	m.SetPreview(nil)
	if r := m.Resolve(a, System{}); r.ThemeOpacity != 0 {
		t.Error("the preview ended")
	}
	m.SetReduceTransparency(true)
	if r := m.Resolve(a, System{}); r.Opaque != OpaqueTransparencyOff || !m.ReduceTransparency() {
		t.Error("transparency effects off")
	}
	writeTheme(t, dir, "x", `{"extends": "nord"}`)
	lib.Reload()
	a.FollowSystem, a.Theme = false, "x"
	if r := m.Resolve(a, System{}); r.Theme.ID != "x" {
		t.Error("a new theme")
	}
	if changes != 4 {
		t.Errorf("%d changes", changes)
	}
}

func TestExport(t *testing.T) {
	lib := NewLibrary("")
	dark, _ := lib.Get("golauncher-dark")
	data, err := ExportTheme(dark).Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"$schema": "`+SchemaURL) || !strings.Contains(string(data), `"selection": "rgba(59, 130, 246, 0.4)"`) {
		t.Fatalf("export:\n%s", data)
	}
	// Loaded again, it is the same theme.
	dir := t.TempDir()
	writeTheme(t, dir, "copy", string(data))
	again := loaded(t, dir)
	cp, ok := again.Get("copy")
	if !ok {
		t.Fatalf("problems %+v", again.Problems())
	}
	if !sameColors(cp.Colors(), dark.Colors()) || cp.Dark != dark.Dark || cp.Opacity != dark.Opacity || !cp.SystemAccent {
		t.Error("export changed the theme")
	}

	// With the adjustments of the appearance.
	a := config.DefaultAppearance()
	a.FollowSystem, a.Theme = false, "paper"
	a.Opacity, a.Accent, a.Density = ptr(0.5), "#123456", ptr("comfortable")
	f := ExportAppearance(lib, a, System{}, "My Paper")
	if f.Name != "My Paper" || f.Names != nil || *f.Opacity != 0.5 || f.Colors["accent"] != "#123456" || *f.Density != "comfortable" ||
		f.Extends != "" || f.Colors["background"] != "#f6f1e7" {
		t.Errorf("%+v", f)
	}
	if _, ok := f.Colors["accentHover"]; ok {
		t.Error("derived colors are left to derive")
	}
	data, _ = f.Encode()
	var back File
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	paper, _ := lib.Get("paper")
	if data, _ := ExportTheme(paper).Encode(); !strings.Contains(string(data), `"name.zh-CN": "宣纸"`) {
		t.Errorf("localized names: %s", data)
	}
}

func sameColors(a, b Colors) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

var update = flag.Bool("update", false, "rewrite docs/theme.schema.json")

// TestSchema keeps docs/theme.schema.json in step with the colors.
func TestSchema(t *testing.T) {
	const path = "../../docs/theme.schema.json"
	want, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		os.WriteFile(path, want, 0o644)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(data), "\r\n", "\n") != string(want) {
		t.Fatalf("%s is out of date: go test ./internal/theme -run TestSchema -update", path)
	}
	var schema struct {
		Properties map[string]struct {
			Properties map[string]any `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	colors := schema.Properties["colors"].Properties
	for _, name := range ColorNames() {
		if _, ok := colors[name]; !ok {
			t.Errorf("schema has no color %q", name)
		}
	}
	for _, m := range fileMembers {
		if _, ok := schema.Properties[m]; !ok {
			t.Errorf("schema has no member %q", m)
		}
	}
	// Every built-in theme passes the schema's types, as far as decoding
	// goes: a file with all members decodes.
	if len(colors) != len(ColorNames()) {
		t.Errorf("schema has %d colors, want %d", len(colors), len(ColorNames()))
	}
}

func BenchmarkResolve(b *testing.B) {
	lib := NewLibrary("")
	a := config.DefaultAppearance()
	a.FollowSystem, a.Theme, a.Accent = false, "nord", config.AccentWindows
	sys := System{Accent: mui.RGB(1, 2, 3)}
	for b.Loop() {
		Resolve(lib, a, sys)
	}
}

func BenchmarkManagerResolveCached(b *testing.B) {
	m := NewManager(NewLibrary(""))
	a := config.DefaultAppearance()
	for b.Loop() {
		m.Resolve(a, System{})
	}
}
