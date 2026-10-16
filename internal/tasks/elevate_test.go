package tasks

import (
	"encoding/json/v2"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// The tests of R68 never show a UAC prompt: FakeRunner is the elevator,
// and proc.StartElevated refuses to run in a test binary anyway.

func elevated(argv ...string) plugin.ProcessSpec {
	return plugin.ProcessSpec{Argv: argv, Elevate: true}
}

func TestElevatedSuccess(t *testing.T) {
	runner := &FakeRunner{Results: []FakeResult{{Match: "ipconfig", Output: "Successfully flushed"}}}
	m := newManager(t, Options{Runner: runner})
	id := m.RunProcess("p", plugin.TaskSpec{Title: "Flush"}, elevated("ipconfig", "/flushdns"))
	wait(t, m)
	i, _ := m.Get(id)
	if i.State != plugin.TaskSucceeded || i.ExitCode != 0 || i.NeedsElevation || !i.Cmd.Elevate {
		t.Errorf("info %+v", i)
	}
	out, _ := m.Output(id, 0)
	// The program's own output is not captured, as for a real elevated one.
	want := "Asking for administrator rights (UAC)…\nRunning as administrator…\nRan as administrator; output is not available."
	if out != want {
		t.Errorf("output %q, want %q", out, want)
	}
	if i.LastLine != "Ran as administrator; output is not available." {
		t.Errorf("last line %q", i.LastLine)
	}
	if c := runner.Commands(); len(c) != 1 || !c[0].Elevate {
		t.Errorf("commands %+v", c)
	}
}

func TestElevatedExitCode(t *testing.T) {
	runner := &FakeRunner{Results: []FakeResult{{Match: "ipconfig", ExitCode: 1}}}
	m := newManager(t, Options{Runner: runner})
	id := m.RunProcess("p", plugin.TaskSpec{Title: "Flush"}, elevated("ipconfig", "/flushdns"))
	wait(t, m)
	i, _ := m.Get(id)
	// A failed elevated task does not offer to retry as administrator.
	if i.State != plugin.TaskFailed || i.ExitCode != 1 || i.NeedsElevation || i.RetryableElevated() {
		t.Errorf("info %+v", i)
	}
	if got := StateText(loc(t), i); got != "Failed (exit code 1)" {
		t.Errorf("state %q", got)
	}
}

func TestElevationDeclined(t *testing.T) {
	runner := &FakeRunner{DeclineUAC: true}
	var finished []Info
	m := newManager(t, Options{Runner: runner, OnFinished: func(i Info) { finished = append(finished, i) }})
	var res plugin.TaskResult
	id := m.RunProcess("p", plugin.TaskSpec{Title: "Flush", Notify: plugin.NotifyAlways, OnDone: func(r plugin.TaskResult) { res = r }},
		elevated("ipconfig", "/flushdns"))
	wait(t, m)
	i, _ := m.Get(id)
	const msg = "Cancelled: the administrator prompt was declined."
	if i.State != plugin.TaskCancelled || i.ExitCode != -1 || i.Error != msg || res.State != plugin.TaskCancelled || res.Error != msg {
		t.Errorf("info %+v, result %+v", i, res)
	}
	if got := StateText(loc(t), i); got != msg {
		t.Errorf("state %q", got)
	}
	if out, _ := m.Output(id, 0); !strings.HasSuffix(out, msg) {
		t.Errorf("output %q", out)
	}
	// Declining is the user's own choice: no notification.
	if len(finished) != 1 || ShouldNotify(finished[0]) {
		t.Errorf("finished %+v", finished)
	}
	if _, failed := m.Indicator(); failed != 0 {
		t.Error("a declined prompt counts as a failure")
	}
}

func TestElevatedStop(t *testing.T) {
	for _, fails := range []bool{false, true} {
		started := make(chan struct{})
		runner := &FakeRunner{StopFails: fails, Hook: func(_ plugin.ProcessSpec, _ io.Writer, stop <-chan struct{}) int {
			close(started)
			<-stop
			return 1
		}}
		m := newManager(t, Options{Runner: runner})
		id := m.RunProcess("p", plugin.TaskSpec{Title: "Flush"}, elevated("ipconfig", "/flushdns"))
		<-started
		if err := m.Stop(id); err != nil {
			t.Fatal(err)
		}
		wait(t, m)
		i, _ := m.Get(id)
		out, _ := m.Output(id, 0)
		// Ended: the code TerminateProcess gave; not ended: unknown.
		if code := map[bool]int{false: 1, true: -1}[fails]; i.State != plugin.TaskCancelled || i.ExitCode != code {
			t.Errorf("stop fails %v: info %+v", fails, i)
		}
		if notEnded := strings.Contains(out, "may still be running"); notEnded != fails {
			t.Errorf("stop fails %v: output %q", fails, out)
		}
	}
}

// TestElevateWhenAdmin: when golauncher already runs as administrator, an
// Elevate task runs as usual, with its output.
func TestElevateWhenAdmin(t *testing.T) {
	runner := &FakeRunner{Admin: true, Results: []FakeResult{{Match: "ipconfig", Output: "Successfully flushed"}}}
	m := newManager(t, Options{Runner: runner})
	id := m.RunProcess("p", plugin.TaskSpec{}, elevated("ipconfig", "/flushdns"))
	wait(t, m)
	if out, _ := m.Output(id, 0); out != "Successfully flushed" {
		t.Errorf("output %q", out)
	}
}

// noElevator is a Runner that cannot elevate.
type noElevator struct{ Runner }

func TestElevateUnsupported(t *testing.T) {
	m := newManager(t, Options{Runner: noElevator{&FakeRunner{}}})
	id := m.RunProcess("p", plugin.TaskSpec{}, elevated("ipconfig", "/flushdns"))
	wait(t, m)
	if i, _ := m.Get(id); i.State != plugin.TaskFailed || !strings.Contains(i.Error, "not supported") {
		t.Errorf("info %+v", i)
	}
}

// TestNeedsElevation: error 740 from CreateProcess, and the known messages
// of ipconfig, netsh and sc, offer Retry as Administrator, which runs the
// same command with Elevate.
func TestNeedsElevation(t *testing.T) {
	runner := &FakeRunner{Results: []FakeResult{
		{Match: "manifest-admin", ElevationRequired: true},
		{Match: "ipconfig /flushdns", Output: "Windows IP Configuration\nThe requested operation requires elevation.", ExitCode: 1},
		{Match: "ipconfig /renew", Output: "Windows IP 配置\n请求的操作需要提升。", ExitCode: 1},
		{Match: "netsh", Output: "The requested operation requires elevation (Run as administrator).", ExitCode: 1},
		{Match: "sc stop", Output: "[SC] OpenService FAILED 5:\n\nAccess is denied.", ExitCode: 5},
		{Match: "denied", Output: "Access is denied.", ExitCode: 1},
		{Match: "fine-but-says", Output: "requires elevation"},
	}}
	l := loc(t)
	var mu sync.Mutex
	notices := map[string]plugin.Toast{}
	m := newManager(t, Options{Runner: runner, OnFinished: func(i Info) {
		if ShouldNotify(i) {
			mu.Lock()
			notices[i.ID] = FinishToast(l, i)
			mu.Unlock()
		}
	}})
	dones := 0
	onDone := func(plugin.TaskResult) { mu.Lock(); dones++; mu.Unlock() }
	cases := []struct {
		argv []string
		want bool
	}{
		{[]string{"manifest-admin.exe"}, true},
		{[]string{"ipconfig", "/flushdns"}, true},
		{[]string{"ipconfig", "/renew"}, true},
		{[]string{"netsh", "interface", "ip", "set"}, true},
		{[]string{"sc", "stop", "Spooler"}, true},
		{[]string{"denied"}, false}, // "Access is denied" alone is not enough
		{[]string{"fine-but-says"}, false},
	}
	ids := make([]string, len(cases))
	for k, c := range cases {
		ids[k] = m.RunProcess("p", plugin.TaskSpec{Title: c.argv[0], Queue: "q", OnDone: onDone}, plugin.ProcessSpec{Argv: c.argv, Dir: `C:\work`})
	}
	wait(t, m)
	for k, c := range cases {
		i, _ := m.Get(ids[k])
		if i.NeedsElevation != c.want || i.RetryableElevated() != c.want {
			t.Errorf("%v: needs elevation %v, want %v (%+v)", c.argv, i.NeedsElevation, c.want, i)
		}
	}
	if i, _ := m.Get(ids[0]); i.ExitCode != -1 || StateText(l, i) != "Failed: requires administrator rights" {
		t.Errorf("740: %+v %q", i, StateText(l, i))
	}
	if len(notices) != len(cases)-1 || notices[ids[0]].Message != "It requires administrator rights. Open the task to retry as administrator." ||
		notices[ids[5]].Message != "Access is denied." {
		t.Errorf("notices %+v", notices)
	}

	// Retry as Administrator: the same command, description and OnDone,
	// with Elevate.
	before := len(runner.Commands())
	n := dones
	retry, err := m.RetryElevated(ids[1])
	if err != nil {
		t.Fatal(err)
	}
	wait(t, m)
	i, _ := m.Get(retry)
	c := runner.Commands()
	if i.Title != "ipconfig" || i.Queue != "q" || !i.Cmd.Elevate || len(c) != before+1 || !c[before].Elevate ||
		strings.Join(c[before].Argv, " ") != "ipconfig /flushdns" || c[before].Dir != `C:\work` || dones != n+1 {
		t.Errorf("retry %+v, commands %+v", i, c)
	}
	if _, err := m.RetryElevated(ids[5]); err == nil {
		t.Error("retried a task that does not need elevation")
	}
	if _, err := m.RetryElevated(retry); err == nil {
		t.Error("retried an elevated task as administrator")
	}
	if _, err := m.RetryElevated("t0"); err != ErrNotFound {
		t.Errorf("unknown task: %v", err)
	}
}

func TestProcessSpecElevateJSON(t *testing.T) {
	b, err := json.Marshal(plugin.ProcessSpec{Argv: []string{"ipconfig"}})
	if err != nil || strings.Contains(string(b), "elevate") {
		t.Errorf("%s %v", b, err)
	}
	b, _ = json.Marshal(elevated("ipconfig", "/flushdns"))
	var back plugin.ProcessSpec
	if err := json.Unmarshal(b, &back); err != nil || !back.Elevate || !strings.Contains(string(b), `"elevate":true`) {
		t.Errorf("%s → %+v %v", b, back, err)
	}
}
