//go:build windows

package tasks

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestStopEndsProcessTree runs a real cmd.exe that starts a child process
// and stops it: the whole tree ends and the task is cancelled.
func TestStopEndsProcessTree(t *testing.T) {
	if _, err := exec.LookPath("cmd"); err != nil {
		t.Skip("cmd.exe is not available")
	}
	m := newManager(t, Options{Runner: ProcessRunner{}})
	id := m.RunProcess("p", plugin.TaskSpec{Title: "Ping"}, plugin.ProcessSpec{
		Argv: []string{"cmd", "/d", "/c", "echo started & ping -n 60 127.0.0.1 >nul & echo end"},
		Env:  []string{"GOLAUNCHER_TEST=1"},
	})
	eventually(t, "the output", func() bool { out, _ := m.Output(id, 0); return strings.Contains(out, "started") })
	start := time.Now()
	if err := m.Stop(id); err != nil {
		t.Fatal(err)
	}
	wait(t, m)
	// A surviving ping would keep the output pipe open for the wait delay.
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("stopping took %v", d)
	}
	i, _ := m.Get(id)
	out, _ := m.Output(id, 0)
	if i.State != plugin.TaskCancelled || strings.Contains(out, "end") {
		t.Errorf("after stop: %+v %q", i, out)
	}
}

// TestProcessExitCodeAndEnv runs a real batch-free command line.
func TestProcessExitCodeAndEnv(t *testing.T) {
	if _, err := exec.LookPath("cmd"); err != nil {
		t.Skip("cmd.exe is not available")
	}
	m := newManager(t, Options{Runner: ProcessRunner{}})
	id := m.RunProcess("p", plugin.TaskSpec{}, plugin.ProcessSpec{
		Argv: []string{"cmd", "/d", "/c", "echo [%GOLAUNCHER_TEST%] & exit 7"},
		Env:  []string{"GOLAUNCHER_TEST=yes"},
	})
	missing := m.RunProcess("p", plugin.TaskSpec{}, plugin.ProcessSpec{Argv: []string{`C:\no\such\program.exe`}})
	wait(t, m)
	i, _ := m.Get(id)
	if out, _ := m.Output(id, 0); i.State != plugin.TaskFailed || i.ExitCode != 7 || out != "[yes] " {
		t.Errorf("task %+v, output %q", i, out)
	}
	if i, _ := m.Get(missing); i.State != plugin.TaskFailed || i.Error == "" || i.ExitCode != -1 {
		t.Errorf("missing program %+v", i)
	}
}
