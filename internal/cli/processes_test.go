package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// The process manager (docs/process-manager.md) on the invented processes
// of testdata/fixtures/processes.json: two samples one second apart, so
// that CPU percentages are fixed. Nothing is ended: the fixture backend
// records what it is asked to do.

func TestGoldenProcesses(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"query_processes", []string{"query", "ps ", "--fixtures", fixtures}},
		{"query_processes_search", []string{"query", "ps fab", "--fixtures", fixtures}},
		{"query_processes_kill", []string{"query", "kill writer", "--fixtures", fixtures}},
		{"query_processes_chinese", []string{"query", "进程 nfbj", "--fixtures", fixtures}},
		{"zh_query_processes", []string{"query", "ps ", "--locale", "zh-CN", "--fixtures", fixtures}},
		{"run_processes", []string{"run", "builtin.processes/processes", "--fixtures", fixtures}},
		{"run_processes_hung", []string{"run", "builtin.processes/hung", "--fixtures", fixtures}},
		{"query_processes_foreground", []string{"query", "", "--context-app", "paint.exe", "--fixtures", fixtures}},
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

// TestProcessesPrefixNeedsSpace: "ps" alone is not the prefix (PowerShell,
// Photoshop), "ps " is.
func TestProcessesPrefixNeedsSpace(t *testing.T) {
	out, _ := glctl(t, "query", "ps", "--fixtures", fixtures, "--format", "text")
	if strings.Contains(out, "builtin.processes/p:") || strings.Contains(out, "builtin.processes/summary") {
		t.Errorf("ps without a space lists processes:\n%s", out)
	}
	out, _ = glctl(t, "query", "ps ", "--fixtures", fixtures, "--format", "text")
	if !strings.Contains(out, "builtin.processes/summary") {
		t.Errorf("ps with a space lists no processes:\n%s", out)
	}
}

// TestSessionProcessesEndTask: "kill writer", Enter asks Contoso Writer to
// close at once (End task needs no confirmation); Force end asks first.
func TestSessionProcessesEndTask(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "kill writer")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_processes_end_task", out)
	if strings.Contains(out, `"confirm"`) {
		t.Errorf("End task asked for confirmation:\n%s", out)
	}

	s2 := filepath.Join(t.TempDir(), "s2.json")
	glctl(t, "session", "new", "-s", s2, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s2, "kill writer")
	out, _ = glctl(t, "session", "key", "-s", s2, "Ctrl+Shift+X")
	golden(t, "session_processes_force_ask", out)
	if !strings.Contains(out, `"confirm"`) || !strings.Contains(out, "unsaved work is lost") {
		t.Errorf("Force end did not ask:\n%s", out)
	}
	out, _ = glctl(t, "session", "key", "-s", s2, "Enter")
	golden(t, "session_processes_force_done", out)
}

// TestProcessesCriticalHaveNoEnd: the critical lsass.exe has no end action,
// the look-alike in Downloads has.
func TestProcessesCriticalHaveNoEnd(t *testing.T) {
	out, code := glctl(t, "query", "ps lsass", "--fixtures", fixtures, "--format", "text")
	if code != 0 {
		t.Fatal(out)
	}
	var real, fake string
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.Contains(line, "builtin.processes/p:920:"):
			real = line
		case strings.Contains(line, "builtin.processes/p:4000:"):
			fake = line
		}
	}
	if real == "" || strings.Contains(real, "end-task") || strings.Contains(real, "force-end") {
		t.Errorf("critical lsass: %q", real)
	}
	if !strings.Contains(fake, "force-end") {
		t.Errorf("look-alike lsass: %q", fake)
	}
}
