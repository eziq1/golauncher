package claudecode

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestInstallPaths: Claude Code's usual install locations on each system
// (R111: macOS has its own).
func TestInstallPaths(t *testing.T) {
	home := filepath.FromSlash("/Users/someone")
	if got, want := installPaths("darwin", home, ""), []string{filepath.Join(home, ".local", "bin", "claude"),
		filepath.Join(home, ".claude", "local", "claude"), "/opt/homebrew/bin/claude", "/usr/local/bin/claude"}; !slices.Equal(got, want) {
		t.Errorf("darwin: %q", got)
	}
	roaming := filepath.Join(home, "AppData", "Roaming")
	if got, want := installPaths("windows", home, roaming), []string{filepath.Join(home, ".local", "bin", "claude.exe"),
		filepath.Join(roaming, "npm", "claude.cmd")}; !slices.Equal(got, want) {
		t.Errorf("windows: %q", got)
	}
	if got := installPaths("windows", "", ""); len(got) != 0 {
		t.Errorf("windows without folders: %q", got)
	}
}
