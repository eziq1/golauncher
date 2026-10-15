package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestGoldenTerminal: Windows Terminal profiles and WSL distributions from
// testdata/fixtures/terminal (builtin.terminal).
func TestGoldenTerminal(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"query_terminal_wsl", []string{"query", "ubuntu", "--fixtures", fixtures}},
		// A context folder that does not exist: only the terminal plugin
		// offers to open it.
		{"query_terminal_profile", []string{"query", "git bash", "--fixtures", fixtures, "--context-folder", `C:\Work Dir`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, code := glctl(t, tt.args...)
			if code != 0 {
				t.Fatalf("exit code %d", code)
			}
			golden(t, tt.name, out)
		})
	}
}

// TestSessionTerminal shuts down WSL (after confirming; the fixture's
// runner starts nothing) and queues Update WSL as a background task.
func TestSessionTerminal(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "shut down wsl")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	if !strings.Contains(out, `"confirm"`) {
		t.Fatalf("no confirmation:\n%s", out)
	}
	out, _ = glctl(t, "session", "key", "-s", s, "Enter")
	if !strings.Contains(out, "WSL was shut down") {
		t.Errorf("no toast:\n%s", out)
	}

	glctl(t, "session", "type", "-s", s, "update wsl")
	glctl(t, "session", "key", "-s", s, "Enter")
	glctl(t, "session", "wait-tasks", "-s", s)
	out, code := glctl(t, "tasks", "list", "-s", s, "--format", "text")
	if code != 0 || !strings.Contains(out, "succeeded Update WSL — wsl.exe --update") {
		t.Errorf("tasks list (%d):\n%s", code, out)
	}
}
