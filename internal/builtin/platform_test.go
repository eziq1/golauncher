package builtin

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/eziq1/golauncher/internal/extproc"
	"github.com/eziq1/golauncher/pkg/plugin"
)

func ids(ps []plugin.Plugin) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Manifest().ID)
	}
	return out
}

// TestFakePlatforms: the fake environment chooses plugins for Windows on
// every system unless told otherwise; Scoop and WinGet are Windows-only,
// and external plugins are filtered by the same platform.
func TestFakePlatforms(t *testing.T) {
	t.Setenv(extproc.EnvPluginPath, "")
	root := t.TempDir()
	for id, platforms := range map[string]string{"x.win": `["windows"]`, "x.mac": `["darwin"]`, "x.any": `[]`} {
		dir := filepath.Join(root, id)
		os.MkdirAll(dir, 0o755)
		m := `{"id":"` + id + `","name":"N","version":"1","protocol":1,"run":{"command":"x.exe"},"platforms":` + platforms + `}`
		if err := os.WriteFile(filepath.Join(dir, extproc.ManifestFile), []byte(m), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	windowsOnly := []string{"builtin.scoop", "builtin.winget", "x.win"}
	for _, tc := range []struct {
		name string
		ps   []plugin.Plugin
		win  bool
	}{
		{"Fake", Fake("", root), true},
		{"windows", FakeOn(plugin.PlatformWindows, "", root), true},
		{"darwin", FakeOn(plugin.PlatformDarwin, "", root), false},
		{"linux", FakeOn(plugin.PlatformLinux, "", root), false},
	} {
		got := ids(tc.ps)
		for _, id := range windowsOnly {
			if slices.Contains(got, id) != tc.win {
				t.Errorf("%s: %s loaded = %v", tc.name, id, !tc.win)
			}
		}
		if slices.Contains(got, "x.mac") != (tc.name == "darwin") {
			t.Errorf("%s: x.mac loaded = %v", tc.name, tc.name != "darwin")
		}
		for _, id := range []string{"x.any", "builtin.apps", "builtin.everything", "builtin.terminal", "builtin.processes"} {
			if !slices.Contains(got, id) {
				t.Errorf("%s: %s missing from %v", tc.name, id, got)
			}
		}
	}
}

// TestBuiltinManifestPlatforms: the built-ins name only known platforms.
func TestBuiltinManifestPlatforms(t *testing.T) {
	t.Setenv(extproc.EnvPluginPath, "")
	for _, p := range FakeOn("none", "") { // a platform no plugin names: only the cross-platform ones
		if m := p.Manifest(); len(m.Platforms) != 0 {
			t.Errorf("%s with platforms %q loaded on another platform", m.ID, m.Platforms)
		}
	}
	for _, platform := range plugin.Platforms {
		for _, p := range FakeOn(platform, "") {
			if err := p.Manifest().CheckPlatforms(); err != nil {
				t.Errorf("%s: %v", p.Manifest().ID, err)
			}
		}
	}
}

// TestExternalReservesFilteredBuiltins: an external plugin cannot take the
// ID of a built-in left out on this platform.
func TestExternalReservesFilteredBuiltins(t *testing.T) {
	t.Setenv(extproc.EnvPluginPath, "")
	root := t.TempDir()
	m := `{"id":"builtin.scoop","name":"N","version":"1","protocol":1,"run":{"command":"x.exe"}}`
	if err := os.WriteFile(filepath.Join(root, extproc.ManifestFile), []byte(m), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ids(FakeOn(plugin.PlatformDarwin, "", root)); slices.Contains(got, "builtin.scoop") {
		t.Fatalf("external builtin.scoop loaded: %v", got)
	}
}
