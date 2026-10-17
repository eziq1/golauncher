package terminal

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestStandardJSON(t *testing.T) {
	in := "\xef\xbb\xbf" + `// header
{
  "a": "http://x//y", /* block
  comment */ "b": [1, 2, ],
  "c": "quote \" // still a string",
  "d": { "e": true, }, // trailing
  "f": "a\\", "g": "/*not*/",
}`
	var got map[string]any
	if err := json.Unmarshal(StandardJSON([]byte(in)), &got); err != nil {
		t.Fatalf("%v\n%s", err, StandardJSON([]byte(in)))
	}
	want := map[string]any{"a": "http://x//y", "b": []any{1.0, 2.0}, "c": `quote " // still a string`,
		"d": map[string]any{"e": true}, "f": `a\`, "g": "/*not*/"}
	gb, _ := json.Marshal(got, json.Deterministic(true))
	wb, _ := json.Marshal(want, json.Deterministic(true))
	if string(gb) != string(wb) {
		t.Errorf("got %s\nwant %s", gb, wb)
	}
	// An unterminated block comment does not panic.
	StandardJSON([]byte(`{"a": 1 /* open`))
}

const fixtureRoot = "../../testdata/fixtures/terminal"

func names(ps []Profile) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func profile(t *testing.T, ps []Profile, name string) Profile {
	t.Helper()
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no profile %q in %q", name, names(ps))
	return Profile{}
}

// TestLoadFixture reads the glctl fixture: comments, trailing commas, a
// hidden profile, a fragment completing a stub, a new fragment profile, a
// hidden fragment profile.
func TestLoadFixture(t *testing.T) {
	la := filepath.Join(fixtureRoot, "LocalAppData")
	pd := filepath.Join(fixtureRoot, "ProgramData")
	settings := findSettings(la)
	if !strings.Contains(settings, "Microsoft.WindowsTerminal_8wekyb3d8bbwe") {
		t.Fatalf("settings = %q", settings)
	}
	frags := fragmentFiles(fragmentRoots(la, pd))
	if len(frags) != 2 {
		t.Fatalf("fragments = %q", frags)
	}
	term, err := loadTerminal(settings, frags, func(string) string { return "" }, nil)
	if err != nil || len(term.Problems) > 0 {
		t.Fatal(err, term.Problems)
	}
	want := []string{"Windows PowerShell", "命令提示符", "PowerShell", "Ubuntu-22.04", "Git Bash", `Dev; Tools "Nightly"`, "Developer PowerShell for VS 2022"}
	if got := names(term.Profiles); !slices.Equal(got, want) {
		t.Errorf("profiles = %q\nwant %q", got, want)
	}
	git := profile(t, term.Profiles, "Git Bash")
	if git.Commandline != `C:\Program Files\Git\bin\bash.exe -i -l` || git.Source != "Git" {
		t.Errorf("Git Bash = %+v", git)
	}
	if ps := profile(t, term.Profiles, "PowerShell"); !ps.Default || ps.GUID != "{574e775e-4f2a-5b96-ac1e-a2962a402336}" {
		t.Errorf("PowerShell = %+v", ps)
	}
	if p := profile(t, term.Profiles, `Dev; Tools "Nightly"`); p.Commandline != "pwsh.exe -NoLogo // not a comment" {
		t.Errorf("commandline = %q", p.Commandline)
	}
	if vs := profile(t, term.Profiles, "Developer PowerShell for VS 2022"); vs.Source != "VisualStudio" {
		t.Errorf("fragment source = %q", vs.Source)
	}
}

// writeFile writes data to path, creating its folder.
func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSettingsLocations finds settings.json in each of the three places,
// in order of precedence.
func TestSettingsLocations(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Terminal is Windows-only (Windows paths)")
	}
	la := t.TempDir()
	locs := settingsLocations(la)
	if len(locs) != 3 {
		t.Fatal(locs)
	}
	if findSettings(la) != "" {
		t.Fatal("found settings in an empty folder")
	}
	for i := len(locs) - 1; i >= 0; i-- {
		writeFile(t, locs[i], `{"profiles": [{"name": "P`+string(rune('0'+i))+`"}]}`)
		got := findSettings(la)
		if got != locs[i] {
			t.Fatalf("with %d files: %q, want %q", len(locs)-i, got, locs[i])
		}
		term, err := loadTerminal(got, nil, nil, nil)
		if err != nil || len(term.Profiles) != 1 || term.Profiles[0].Name != "P"+string(rune('0'+i)) {
			t.Errorf("%s: %v %+v", got, err, term.Profiles)
		}
	}
	for _, want := range []string{`Packages\Microsoft.WindowsTerminal_8wekyb3d8bbwe\LocalState`, `Packages\Microsoft.WindowsTerminalPreview_8wekyb3d8bbwe\LocalState`, `Microsoft\Windows Terminal\settings.json`} {
		if !slices.ContainsFunc(locs, func(l string) bool { return strings.Contains(filepath.ToSlash(l), filepath.ToSlash(want)) }) {
			t.Errorf("no location like %s in %q", want, locs)
		}
	}
	if settingsLocations("") != nil {
		t.Error("locations without LOCALAPPDATA")
	}
}

// TestLayering checks profiles.defaults, fragment updates, hidden
// fragments and icons.
func TestLayering(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Terminal is Windows-only (%VAR%\\ icon paths)")
	}
	la := t.TempDir()
	settings := settingsLocations(la)[2]
	fragDir := filepath.Join(la, "Microsoft", "Windows Terminal", "Fragments", "Tool")
	writeFile(t, filepath.Join(fragDir, "tool.png"), "png")
	writeFile(t, filepath.Join(la, "icons", "shell.svg"), "<svg/>")
	writeFile(t, settings, `{
		"defaultProfile": "2C4DE342-38B7-51CF-B940-2309A097F518",
		"profiles": {
			"defaults": { "icon": "%ICONS%\\shell.svg" },
			"list": [
				{ "guid": "{2c4de342-38b7-51cf-b940-2309a097f518}", "name": "With Default Icon" },
				{ "name": "Emoji", "icon": "🐧" },
				{ "name": "Appx", "icon": "ms-appx:///ProfileIcons/pwsh.png" },
				{ "name": "Missing", "icon": "C:\\nope\\none.png" },
				{ "guid": "{aaaaaaaa-0000-0000-0000-000000000000}", "name": "Updated" },
				{ "name": "Hidden By Me", "hidden": true },
				{ "name": "Tool Shell", "source": "Tool" },
				{ "name": "Gone Tool", "source": "Tool", "hidden": true }
			]
		}
	}`)
	writeFile(t, filepath.Join(fragDir, "tool.json"), `{
		"profiles": [
			{ "name": "Tool Shell", "commandline": "tool.exe", "icon": "tool.png" },
			{ "name": "Gone Tool", "commandline": "gone.exe" },
			{ "name": "New Tool", "commandline": "new.exe", "icon": "tool.png" },
			{ "updates": "{AAAAAAAA-0000-0000-0000-000000000000}", "commandline": "updated.exe" }
		]
	}`)
	writeFile(t, filepath.Join(la, "Microsoft", "Windows Terminal", "Fragments", "Broken", "x.json"), `{"profiles": [`)
	getenv := func(name string) string {
		if name == "ICONS" {
			return filepath.Join(la, "icons")
		}
		return ""
	}
	term, err := loadTerminal(settings, fragmentFiles(fragmentRoots(la, "")), getenv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(term.Problems) != 1 || !strings.Contains(term.Problems[0].Error(), "Broken") {
		t.Errorf("problems = %v", term.Problems)
	}
	want := []string{"With Default Icon", "Emoji", "Appx", "Missing", "Updated", "Tool Shell", "New Tool"}
	if got := names(term.Profiles); !slices.Equal(got, want) {
		t.Fatalf("profiles = %q, want %q", got, want)
	}
	shell := filepath.Join(la, "icons", "shell.svg")
	toolPNG := filepath.Join(fragDir, "tool.png")
	for name, icon := range map[string]string{"With Default Icon": shell, "Emoji": "", "Appx": "", "Missing": "", "Updated": shell, "Tool Shell": toolPNG, "New Tool": toolPNG} {
		if got := profile(t, term.Profiles, name).Icon; got != icon {
			t.Errorf("%s: icon %q, want %q", name, got, icon)
		}
	}
	if !profile(t, term.Profiles, "With Default Icon").Default {
		t.Error("the default profile (GUID without braces, upper case) is not marked")
	}
	if c := profile(t, term.Profiles, "Updated").Commandline; c != "updated.exe" {
		t.Errorf("updates: commandline %q", c)
	}
	if c := profile(t, term.Profiles, "Tool Shell").Commandline; c != "tool.exe" {
		t.Errorf("stub: commandline %q", c)
	}

	// A settings.json being saved (invalid) is an error.
	writeFile(t, settings, `{"profiles": {"list": [ {"name": `)
	if _, err := loadTerminal(settings, nil, nil, nil); err == nil {
		t.Error("no error for a broken settings.json")
	}
}

// TestSignature notices edits by time and size and new fragments.
func TestSignature(t *testing.T) {
	la := t.TempDir()
	settings := settingsLocations(la)[0]
	writeFile(t, settings, `{}`)
	roots := fragmentRoots(la, "")
	s1 := signature(settings, fragmentFiles(roots))
	if s1 != signature(settings, fragmentFiles(roots)) {
		t.Fatal("signature not stable")
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(settings, old, old)
	s2 := signature(settings, fragmentFiles(roots))
	if s2 == s1 {
		t.Error("a new time is not noticed")
	}
	writeFile(t, filepath.Join(roots[0], "App", "a.json"), `{}`)
	if signature(settings, fragmentFiles(roots)) == s2 {
		t.Error("a new fragment is not noticed")
	}
}

func TestExpandEnv(t *testing.T) {
	getenv := func(n string) string { return map[string]string{"A": "x", "B": "y"}[n] }
	for in, want := range map[string]string{"%A%\\%B%": `x\y`, "%NOPE%\\a": `%NOPE%\a`, "100%": "100%", "%%A%": "%%A%", "plain": "plain"} {
		if got := expandEnv(in, getenv); got != want {
			t.Errorf("expandEnv(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBuiltinProfiles completes the stubs settings.json keeps for the
// profiles Windows Terminal creates itself, as seen on a real machine.
func TestBuiltinProfiles(t *testing.T) {
	la := t.TempDir()
	settings := settingsLocations(la)[0]
	writeFile(t, settings, `{"profiles": {"list": [
		{"guid": "{61c54bbd-c2c6-5271-96e7-009a87ff44bf}", "name": "Windows PowerShell", "hidden": false},
		{"guid": "{0caa0dad-35be-5f56-a8ff-afceeeaa6101}", "name": "命令提示符", "hidden": false},
		{"guid": "{0caa0dad-35be-5f56-a8ff-afceeeaa6101}", "name": "Mine", "commandline": "mine.exe"}
	]}}`)
	term, err := loadTerminal(settings, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"Windows PowerShell": `%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe`,
		"命令提示符":              `%SystemRoot%\System32\cmd.exe`,
		"Mine":               "mine.exe",
	} {
		if got := profile(t, term.Profiles, name).Commandline; got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}
