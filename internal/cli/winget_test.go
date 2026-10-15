package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// The WinGet plugin reads testdata/fixtures/winget (docs/winget.md); its
// operations are background tasks, which the fake runner records.

// TestSessionWingetInstall installs a package from the search view and
// opens the details of another.
func TestSessionWingetInstall(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "search winget packages")
	glctl(t, "session", "key", "-s", s, "Enter")
	glctl(t, "session", "type", "-s", s, "tailspin")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_winget_install", out)
	if !strings.Contains(out, "Install Tailspin Toolbox: running in the background") {
		t.Errorf("no toast:\n%s", out)
	}
	glctl(t, "session", "wait-tasks", "-s", s)
	out, code := glctl(t, "tasks", "list", "-s", s, "--format", "text")
	want := "succeeded Install Tailspin Toolbox — winget install --id Tailspin.Toolbox --exact --source winget --accept-source-agreements --accept-package-agreements --disable-interactivity"
	if code != 0 || !strings.Contains(out, want) {
		t.Errorf("tasks list (%d):\n%s", code, out)
	}
}

// TestSessionWingetDetails opens the details (winget show) of a package.
func TestSessionWingetDetails(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "search winget packages")
	glctl(t, "session", "key", "-s", s, "Enter")
	glctl(t, "session", "type", "-s", s, "textpad")
	out, _ := glctl(t, "session", "key", "-s", s, "Ctrl+D")
	golden(t, "session_winget_details", out)
}

// TestSessionWingetUninstallConfirms asks before uninstalling, and before
// upgrading everything.
func TestSessionWingetUninstallConfirms(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "installed winget packages")
	glctl(t, "session", "key", "-s", s, "Enter")
	glctl(t, "session", "type", "-s", s, "gitty")
	out, _ := glctl(t, "session", "key", "-s", s, "Ctrl+X")
	if !strings.Contains(out, `"confirm"`) {
		t.Fatalf("no confirmation:\n%s", out)
	}
	glctl(t, "session", "key", "-s", s, "Enter")
	out, _ = glctl(t, "tasks", "list", "-s", s, "--format", "text")
	if !strings.Contains(out, "winget uninstall --id Fabrikam.Gitty --exact --source winget") {
		t.Errorf("the uninstall command did not run:\n%s", out)
	}

	s = filepath.Join(t.TempDir(), "s2.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "upgrade all winget packages")
	out, _ = glctl(t, "session", "key", "-s", s, "Enter")
	if !strings.Contains(out, `"confirm"`) {
		t.Fatalf("Upgrade All did not ask:\n%s", out)
	}
	glctl(t, "session", "key", "-s", s, "Enter")
	out, _ = glctl(t, "tasks", "list", "-s", s, "--format", "text")
	if !strings.Contains(out, "winget upgrade --all") {
		t.Errorf("upgrade --all did not run:\n%s", out)
	}
}
