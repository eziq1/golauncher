package tasks

import (
	"runtime"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestProcessRunnerElevator: the real runner can elevate on Windows only
// (the UAC prompt); elsewhere it is no Elevator.
func TestProcessRunnerElevator(t *testing.T) {
	_, ok := any(ProcessRunner{}).(Elevator)
	if want := runtime.GOOS == "windows"; ok != want {
		t.Errorf("ProcessRunner is an Elevator: %v, want %v", ok, want)
	}
}

// TestElevateUnsupportedReal: outside Windows a task asking for elevation
// fails at once with tasks.error.noElevation; it neither runs the program
// nor prints the UAC prompt line.
func TestElevateUnsupportedReal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows elevates through the UAC prompt")
	}
	m := newManager(t, Options{Runner: ProcessRunner{}})
	id := m.RunProcess("p", plugin.TaskSpec{Title: "Flush"}, elevated("/bin/echo", "ran"))
	wait(t, m)
	i, _ := m.Get(id)
	if i.State != plugin.TaskFailed || i.Error != "Running programs as administrator is not supported here." || i.RetryableElevated() {
		t.Errorf("info %+v", i)
	}
	out, _ := m.Output(id, 0)
	if out != i.Error || strings.Contains(out, "ran") || strings.Contains(out, "UAC") {
		t.Errorf("output %q", out)
	}
}

// TestNoElevatorNoRetry: a runner that cannot elevate never offers Retry
// as Administrator, whatever the program printed or the start error.
func TestNoElevatorNoRetry(t *testing.T) {
	runner := noElevator{&FakeRunner{Results: []FakeResult{
		{Match: "manifest-admin", ElevationRequired: true},
		{Match: "ipconfig", Output: "The requested operation requires elevation.", ExitCode: 1},
		{Match: "sc stop", Output: "[SC] OpenService FAILED 5:\n\nAccess is denied.", ExitCode: 5},
	}}}
	m := newManager(t, Options{Runner: runner})
	var ids []string
	for _, argv := range [][]string{{"manifest-admin"}, {"ipconfig", "/flushdns"}, {"sc", "stop", "x"}} {
		ids = append(ids, m.RunProcess("p", plugin.TaskSpec{Title: argv[0], Queue: "q"}, plugin.ProcessSpec{Argv: argv}))
	}
	wait(t, m)
	for _, id := range ids {
		i, _ := m.Get(id)
		if i.State != plugin.TaskFailed || i.NeedsElevation || i.RetryableElevated() {
			t.Errorf("%s: %+v", i.Title, i)
		}
		if _, err := m.RetryElevated(id); err == nil {
			t.Errorf("%s: retried as administrator", i.Title)
		}
	}
}
