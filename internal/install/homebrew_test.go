package install

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeCaskroom makes <prefix>/Caskroom/golauncher with the given folders
// and, unless config is "", .metadata/config.json.
func fakeCaskroom(t *testing.T, prefix string, dirs []string, config string) {
	t.Helper()
	room := filepath.Join(prefix, "Caskroom", CaskToken)
	for _, d := range append([]string{".metadata"}, dirs...) {
		if err := os.MkdirAll(filepath.Join(room, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if config != "" {
		if err := os.WriteFile(filepath.Join(room, ".metadata", "config.json"), []byte(config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// homebrewConfig is a config.json as Homebrew writes it (env is null
// without HOMEBREW_CASK_OPTS).
func homebrewConfig(defaultApps, explicitApps string) string {
	q := func(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }
	explicit := "{}"
	if explicitApps != "" {
		explicit = `{"appdir":"` + q(explicitApps) + `"}`
	}
	return `{"default":{"appdir":"` + q(defaultApps) + `","keyboard_layoutdir":"/Library/Keyboard Layouts","binarydir":"/opt/homebrew/bin","languages":[]},"env":null,"explicit":` + explicit + `}`
}

func TestHomebrewManaged(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "Users", "someone")
	apps := filepath.Join(root, "Applications")
	custom := filepath.Join(root, "Tools")
	exeIn := func(dir string) string { return BundleExe(BundleIn(dir)) }

	arm := filepath.Join(root, "opt", "homebrew")
	fakeCaskroom(t, arm, []string{"0.8.0"}, homebrewConfig(apps, ""))
	intelCustom := filepath.Join(root, "usr", "local")
	fakeCaskroom(t, intelCustom, []string{"0.9.0"}, homebrewConfig(apps, custom))
	noConfig := filepath.Join(root, "noconfig")
	fakeCaskroom(t, noConfig, []string{"0.8.0"}, "")
	badConfig := filepath.Join(root, "badconfig")
	fakeCaskroom(t, badConfig, []string{"0.8.0"}, "{not json")
	uninstalled := filepath.Join(root, "uninstalled")
	fakeCaskroom(t, uninstalled, nil, homebrewConfig(apps, "")) // only .metadata
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(filepath.Join(other, "Caskroom", "golauncher2", "0.8.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	none := filepath.Join(root, "nothing")

	for _, tc := range []struct {
		name     string
		exe      string
		prefixes []string
		want     bool
	}{
		{"default appdir", exeIn(apps), []string{arm}, true},
		{"glctl in the bundle", filepath.Join(BundleIn(apps), "Contents", "MacOS", "glctl"), []string{arm}, true},
		{"case", exeIn(strings.ToUpper(apps)), []string{arm}, true},
		{"second prefix", exeIn(apps), []string{none, "", arm}, true},
		{"--appdir", exeIn(custom), []string{intelCustom}, true},
		{"--appdir: not the default", exeIn(apps), []string{intelCustom}, false},
		{"no config: /Applications or ~/Applications", exeIn(filepath.Join(home, "Applications")), []string{noConfig}, true},
		{"no config: elsewhere", exeIn(custom), []string{noConfig}, false},
		{"bad config", exeIn(filepath.Join(home, "Applications")), []string{badConfig}, true},
		{"self-installed elsewhere", exeIn(custom), []string{arm}, false},
		{"downloaded copy", exeIn(filepath.Join(home, "Downloads")), []string{arm}, false},
		{"no version folder", exeIn(apps), []string{uninstalled}, false},
		{"another cask", exeIn(apps), []string{other}, false},
		{"no Homebrew", exeIn(apps), []string{none}, false},
		{"renamed bundle", BundleExe(filepath.Join(apps, "golauncher 2.app")), []string{arm}, false},
		{"not in a bundle", filepath.Join(apps, "golauncher"), []string{arm}, false},
		{"empty", "", []string{arm}, false},
	} {
		if got := HomebrewManaged(tc.exe, tc.prefixes, home); got != tc.want {
			t.Errorf("%s: HomebrewManaged(%q) = %v, want %v", tc.name, tc.exe, got, tc.want)
		}
	}
	if HomebrewManaged(exeIn(filepath.Join(home, "Applications")), []string{noConfig}, "") {
		t.Error("~/Applications counted without a home folder")
	}
}

func TestHomebrewPrefixes(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "HOMEBREW_PREFIX" {
				return v
			}
			return ""
		}
	}
	for v, want := range map[string][]string{
		"":                {"/opt/homebrew", "/usr/local"},
		"/opt/homebrew/":  {"/opt/homebrew", "/usr/local"},
		"/usr/local":      {"/usr/local", "/opt/homebrew"},
		"/Users/x/brew":   {"/Users/x/brew", "/opt/homebrew", "/usr/local"},
		"relative/prefix": {"/opt/homebrew", "/usr/local"},
	} {
		if got := HomebrewPrefixes(env(v)); !slices.Equal(got, want) {
			t.Errorf("HomebrewPrefixes(%q) = %q, want %q", v, got, want)
		}
	}
	if HomebrewUpgrade != "brew upgrade --cask golauncher" || CaskFullName != "eziq1/tap/golauncher" {
		t.Errorf("commands: %q, %q", HomebrewUpgrade, CaskFullName)
	}
}
