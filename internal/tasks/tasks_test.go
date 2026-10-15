package tasks

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// loc is the host's English catalog.
func loc(t *testing.T) plugin.Localizer {
	t.Helper()
	m := i18n.NewManager(i18n.Source, nil)
	if err := m.Register(i18n.CoreNamespace, i18n.CoreLocales()); err != nil {
		t.Fatal(err)
	}
	m.SetLocale(i18n.Source)
	return m.Localizer(i18n.CoreNamespace)
}

var t0 = time.Date(2026, 10, 15, 9, 30, 0, 0, time.UTC)

// newManager returns a manager logging to a temporary directory.
func newManager(t *testing.T, o Options) *Manager {
	t.Helper()
	if o.Dir == "" {
		o.Dir = t.TempDir()
	}
	if o.Runner == nil {
		o.Runner = &FakeRunner{}
	}
	if o.Loc == nil {
		o.Loc = loc(t)
	}
	m := New(o)
	t.Cleanup(func() { m.Close() })
	return m
}

func wait(t *testing.T, m *Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func cmd(argv ...string) plugin.ProcessSpec { return plugin.ProcessSpec{Argv: argv} }

func TestOutcomes(t *testing.T) {
	runner := &FakeRunner{Results: []FakeResult{
		{Match: "ok", Output: "one\ntwo"},
		{Match: "bad", Output: "boom", ExitCode: 3},
	}}
	var finished []Info
	var mu sync.Mutex
	m := newManager(t, Options{Runner: runner, Allowed: func(id string) bool { return id != "nope" },
		OnFinished: func(i Info) { mu.Lock(); finished = append(finished, i); mu.Unlock() }})
	results := map[string]plugin.TaskResult{}
	onDone := func(r plugin.TaskResult) { mu.Lock(); results[r.ID] = r; mu.Unlock() }
	ok := m.RunProcess("p", plugin.TaskSpec{Title: "OK", OnDone: onDone}, cmd("ok"))
	bad := m.RunProcess("p", plugin.TaskSpec{Title: "Bad", OnDone: onDone}, cmd("bad"))
	denied := m.RunProcess("nope", plugin.TaskSpec{Title: "Denied", OnDone: onDone}, cmd("ok"))
	empty := m.RunProcess("p", plugin.TaskSpec{OnDone: onDone}, plugin.ProcessSpec{})
	goOK := m.RunTask("p", plugin.TaskSpec{Title: "Go", OnDone: onDone}, func(_ context.Context, tk plugin.Task) error {
		io.WriteString(tk, "working\n")
		tk.SetStatus("Step 2/2")
		tk.SetProgress(0.5)
		return nil
	})
	goErr := m.RunTask("p", plugin.TaskSpec{Title: "GoErr", OnDone: onDone}, func(context.Context, plugin.Task) error {
		return errors.New("disk full")
	})
	goPanic := m.RunTask("p", plugin.TaskSpec{Title: "GoPanic", OnDone: onDone}, func(context.Context, plugin.Task) error {
		panic("oops")
	})
	wait(t, m)

	tests := []struct {
		id    string
		state plugin.TaskState
		code  int
		last  string
		err   string
	}{
		{ok, plugin.TaskSucceeded, 0, "two", ""},
		{bad, plugin.TaskFailed, 3, "boom", ""},
		{denied, plugin.TaskFailed, -1, "", "exec permission"},
		{empty, plugin.TaskFailed, -1, "", "nothing to run"},
		{goOK, plugin.TaskSucceeded, 0, "working", ""},
		{goErr, plugin.TaskFailed, -1, "disk full", "disk full"},
		{goPanic, plugin.TaskFailed, -1, "panic: oops", "panic: oops"},
	}
	for _, tt := range tests {
		i, found := m.Get(tt.id)
		r := results[tt.id]
		if !found || i.State != tt.state || i.ExitCode != tt.code || r.State != tt.state || !strings.Contains(r.Error, tt.err) ||
			(tt.last != "" && r.LastLine != tt.last) {
			t.Errorf("%s: info %+v, result %+v", i.Title, i, r)
		}
	}
	if len(runner.Commands()) != 2 {
		t.Errorf("commands %+v (the denied and empty ones must not run)", runner.Commands())
	}
	if g, _ := m.Get(goOK); g.Status != "Step 2/2" || g.Progress == nil || *g.Progress != 0.5 || g.Kind != KindGo {
		t.Errorf("go task %+v", g)
	}
	if len(finished) != len(tests) {
		t.Errorf("OnFinished called %d times", len(finished))
	}
	if out, _ := m.Output(ok, 0); out != "one\ntwo" {
		t.Errorf("output %q", out)
	}
}

// blocking is a runner whose commands run until released (or stopped),
// tracking how many run at once.
type blocking struct {
	FakeRunner
	release             chan struct{}
	running, maxRunning atomic.Int32
	mu                  sync.Mutex
	started             []string
}

func newBlocking() *blocking {
	b := &blocking{release: make(chan struct{})}
	b.Hook = func(c plugin.ProcessSpec, out io.Writer, stop <-chan struct{}) int {
		n := b.running.Add(1)
		defer b.running.Add(-1)
		for {
			m := b.maxRunning.Load()
			if n <= m || b.maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		b.mu.Lock()
		b.started = append(b.started, c.Argv[0])
		b.mu.Unlock()
		io.WriteString(out, "started "+c.Argv[0]+"\n")
		select {
		case <-b.release:
			return 0
		case <-stop:
			return 1
		}
	}
	return b
}

func (b *blocking) order() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.started)
}

func states(m *Manager, ids ...string) []plugin.TaskState {
	var out []plugin.TaskState
	for _, id := range ids {
		i, _ := m.Get(id)
		out = append(out, i.State)
	}
	return out
}

func TestQueuesAndLimit(t *testing.T) {
	b := newBlocking()
	m := newManager(t, Options{Runner: b, MaxParallel: 2})
	a1 := m.RunProcess("p", plugin.TaskSpec{Queue: "a"}, cmd("a1"))
	a2 := m.RunProcess("p", plugin.TaskSpec{Queue: "a"}, cmd("a2"))
	b1 := m.RunProcess("p", plugin.TaskSpec{Queue: "b"}, cmd("b1"))
	c1 := m.RunProcess("p", plugin.TaskSpec{}, cmd("c1"))
	eventually(t, "two tasks", func() bool { return b.running.Load() == 2 })
	// a2 waits for a1 (same queue); c1 waits for the global limit.
	if got := states(m, a1, a2, b1, c1); !slices.Equal(got, []plugin.TaskState{plugin.TaskRunning, plugin.TaskQueued, plugin.TaskRunning, plugin.TaskQueued}) {
		t.Fatalf("states %v", got)
	}
	if active, _ := m.Indicator(); active != 4 {
		t.Errorf("active %d", active)
	}
	list := m.List()
	if list[0].ID != a1 || list[1].ID != b1 || list[2].ID != a2 || list[3].ID != c1 {
		t.Errorf("list order %v", list)
	}
	close(b.release)
	wait(t, m)
	if b.maxRunning.Load() != 2 {
		t.Errorf("%d ran at once, limit 2", b.maxRunning.Load())
	}
	order := b.order()
	if slices.Index(order, "a2") < slices.Index(order, "a1") {
		t.Errorf("queue order %v", order)
	}
	if got := states(m, a1, a2, b1, c1); slices.ContainsFunc(got, func(s plugin.TaskState) bool { return s != plugin.TaskSucceeded }) {
		t.Errorf("final states %v", got)
	}
	// Finished: newest first.
	if l := m.List(); !l[0].Ended.After(l[3].Ended) && l[0].ID == a1 {
		t.Errorf("finished order %v", l)
	}
}

func TestStop(t *testing.T) {
	b := newBlocking()
	m := newManager(t, Options{Runner: b})
	var mu sync.Mutex
	done := map[string]plugin.TaskState{}
	onDone := func(r plugin.TaskResult) { mu.Lock(); done[r.ID] = r.State; mu.Unlock() }
	running := m.RunProcess("p", plugin.TaskSpec{Queue: "q", OnDone: onDone}, cmd("r"))
	queued := m.RunProcess("p", plugin.TaskSpec{Queue: "q", OnDone: onDone}, cmd("q"))
	goTask := m.RunTask("p", plugin.TaskSpec{OnDone: onDone}, func(ctx context.Context, _ plugin.Task) error {
		<-ctx.Done()
		return ctx.Err()
	})
	eventually(t, "the first task", func() bool { return b.running.Load() == 1 })
	if err := m.Stop(queued); err != nil {
		t.Fatal(err)
	}
	if i, _ := m.Get(queued); i.State != plugin.TaskCancelled {
		t.Errorf("stopped queued task: %+v", i)
	}
	m.Stop(running)
	m.Stop(goTask)
	wait(t, m)
	if got := states(m, running, queued, goTask); !slices.Equal(got, []plugin.TaskState{plugin.TaskCancelled, plugin.TaskCancelled, plugin.TaskCancelled}) {
		t.Errorf("states %v", got)
	}
	if len(done) != 3 {
		t.Errorf("OnDone %v", done)
	}
	if o := b.order(); !slices.Equal(o, []string{"r"}) {
		t.Errorf("started %v (the stopped queued task must not run)", o)
	}
	if err := m.Stop("t-missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stop missing: %v", err)
	}
	if active, failed := m.Indicator(); active != 0 || failed != 0 {
		t.Errorf("indicator %d %d (cancelled tasks are no failures)", active, failed)
	}
}

func TestOutputLimitAndLog(t *testing.T) {
	line := strings.Repeat("x", 99)
	m := newManager(t, Options{MaxOutput: 1000, Runner: &FakeRunner{Hook: func(_ plugin.ProcessSpec, out io.Writer, _ <-chan struct{}) int {
		for i := range 50 {
			io.WriteString(out, line[:98]+string(rune('a'+i%26))+"\n")
		}
		io.WriteString(out, "\x1b[31mred\x1b[0m 10%\rdone 100%\r\nlast")
		return 0
	}}})
	id := m.RunProcess("p", plugin.TaskSpec{}, cmd("x"))
	wait(t, m)
	out, more := m.Output(id, 0)
	if !more || len(out) > 1000 || !strings.HasSuffix(out, "done 100%\nlast") {
		t.Errorf("output (%d bytes, more %v): ...%q", len(out), more, out[max(0, len(out)-40):])
	}
	tail, more := m.Output(id, 2)
	if tail != "done 100%\nlast" || !more {
		t.Errorf("tail %q %v", tail, more)
	}
	i, _ := m.Get(id)
	log, err := os.ReadFile(i.LogPath)
	if err != nil || strings.Count(string(log), "\n") != 52 || !strings.HasPrefix(string(log), line[:98]+"a\n") {
		t.Errorf("log file: %v, %d lines", err, strings.Count(string(log), "\n"))
	}
	if i.LastLine != "last" {
		t.Errorf("last line %q", i.LastLine)
	}
}

func TestHistory(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, Options{Dir: dir, KeepFinished: 3, Runner: &FakeRunner{Results: []FakeResult{{Match: "fail", ExitCode: 2, Output: "bad"}}}})
	var ids []string
	for i := range 5 {
		name := "ok"
		if i == 4 {
			name = "fail"
		}
		ids = append(ids, m.RunProcess("p", plugin.TaskSpec{Title: name}, cmd(name)))
		wait(t, m)
	}
	if n := len(m.List()); n != 3 {
		t.Fatalf("%d tasks kept, want 3", n)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(files) != 6 {
		t.Errorf("files %v (3 tasks, a log and a description each)", files)
	}
	if _, ok := m.Get(ids[0]); ok {
		t.Error("the oldest task was kept")
	}
	// A task that was running when golauncher quit.
	os.WriteFile(filepath.Join(dir, "t991231-000000-1.json"),
		[]byte(`{"id":"t991231-000000-1","plugin":"p","title":"Old","kind":"process","state":"running","exitCode":0,"created":"2026-10-01T10:00:00Z","started":"2026-10-01T10:00:00Z"}`), 0o644)
	m.Close()

	m2 := newManager(t, Options{Dir: dir, KeepFinished: 4})
	list := m2.List()
	if len(list) != 4 {
		t.Fatalf("history %+v", list)
	}
	last := list[0]
	if !last.History || last.Title != "fail" || last.State != plugin.TaskFailed || last.ExitCode != 2 || last.Rerunnable() {
		t.Errorf("newest history task %+v", last)
	}
	if out, _ := m2.Output(last.ID, 0); out != "bad" {
		t.Errorf("history output %q", out)
	}
	old := list[3]
	if old.Title != "Old" || !old.Interrupted || old.State != plugin.TaskFailed {
		t.Errorf("interrupted task %+v", old)
	}
	if _, failed := m2.Indicator(); failed != 0 {
		t.Error("failures of a previous run count as unseen")
	}
	if _, err := m2.Rerun(last.ID); err == nil {
		t.Error("a task of a previous run was run again")
	}
	// New IDs do not collide with the history.
	id := m2.RunProcess("p", plugin.TaskSpec{}, cmd("x"))
	if slices.ContainsFunc(list, func(i Info) bool { return i.ID == id }) {
		t.Errorf("duplicate id %s", id)
	}
	wait(t, m2)
	if n := m2.ClearFinished(); n != 4 {
		t.Errorf("cleared %d", n)
	}
	files, _ = filepath.Glob(filepath.Join(dir, "*"))
	if len(files) != 0 {
		t.Errorf("files after clearing %v", files)
	}
}

func TestReadOnlyAndNoDir(t *testing.T) {
	dir := t.TempDir()
	m := newManager(t, Options{Dir: dir, ReadOnly: true})
	id := m.RunProcess("p", plugin.TaskSpec{}, cmd("x"))
	wait(t, m)
	if i, _ := m.Get(id); i.LogPath != "" {
		t.Errorf("read-only manager has a log: %+v", i)
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Errorf("read-only manager wrote %v", files)
	}
	if out, _ := m.Output(id, 0); out != "[not run] x" {
		t.Errorf("output %q", out)
	}
}

func TestRerunRemoveAndSeen(t *testing.T) {
	runner := &FakeRunner{Results: []FakeResult{{Match: "fail", ExitCode: 1}}}
	m := newManager(t, Options{Runner: runner})
	var calls atomic.Int32
	id := m.RunProcess("p", plugin.TaskSpec{Title: "F", Queue: "q", OnDone: func(plugin.TaskResult) { calls.Add(1) }},
		plugin.ProcessSpec{Argv: []string{"fail", "a b"}, Dir: `C:\x`, Env: []string{"A=1"}})
	wait(t, m)
	if _, failed := m.Indicator(); failed != 1 {
		t.Fatalf("failed %d", failed)
	}
	m.MarkSeen()
	if _, failed := m.Indicator(); failed != 0 {
		t.Errorf("failed after MarkSeen %d", failed)
	}
	id2, err := m.Rerun(id)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, m)
	i2, _ := m.Get(id2)
	if i2.Title != "F" || i2.Queue != "q" || i2.Cmd.Dir != `C:\x` || calls.Load() != 2 || CommandLine(*i2.Cmd) != `fail "a b"` {
		t.Errorf("rerun %+v, OnDone %d", i2, calls.Load())
	}
	if err := m.Remove(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get(id); ok {
		t.Error("removed task still listed")
	}
}

func TestCloseStopsTasks(t *testing.T) {
	b := newBlocking()
	m := New(Options{Runner: b, Loc: loc(t)})
	id := m.RunProcess("p", plugin.TaskSpec{}, cmd("x"))
	eventually(t, "the task", func() bool { return b.running.Load() == 1 })
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if i, _ := m.Get(id); i.State != plugin.TaskCancelled {
		t.Errorf("after Close %+v", i)
	}
	late := m.RunProcess("p", plugin.TaskSpec{}, cmd("y"))
	wait(t, m)
	if i, _ := m.Get(late); i.State != plugin.TaskFailed || !strings.Contains(i.Error, "quitting") {
		t.Errorf("task after Close %+v", i)
	}
}

func TestOnChange(t *testing.T) {
	m := newManager(t, Options{})
	var state, output atomic.Int32
	m.OnChange(func(s bool) {
		if s {
			state.Add(1)
		} else {
			output.Add(1)
		}
	})
	m.RunProcess("p", plugin.TaskSpec{}, cmd("x"))
	wait(t, m)
	if state.Load() < 2 || output.Load() < 1 {
		t.Errorf("state changes %d, output changes %d", state.Load(), output.Load())
	}
}

func TestNotifyPolicy(t *testing.T) {
	l := loc(t)
	tests := []struct {
		mode  plugin.NotifyMode
		state plugin.TaskState
		want  bool
	}{
		{plugin.NotifyOnFailure, plugin.TaskFailed, true},
		{plugin.NotifyOnFailure, plugin.TaskSucceeded, false},
		{plugin.NotifyAlways, plugin.TaskSucceeded, true},
		{plugin.NotifyAlways, plugin.TaskCancelled, false},
		{plugin.NotifyNever, plugin.TaskFailed, false},
		{plugin.NotifyAlways, plugin.TaskRunning, false},
	}
	for _, tt := range tests {
		if got := ShouldNotify(Info{Notify: tt.mode, State: tt.state}); got != tt.want {
			t.Errorf("%q %s: %v", tt.mode, tt.state, got)
		}
	}
	ok := FinishToast(l, Info{Title: "Build", State: plugin.TaskSucceeded, LastLine: "3 files"})
	if ok.Style != plugin.ToastSuccess || ok.Title != "Build finished" || ok.Message != "3 files" {
		t.Errorf("success toast %+v", ok)
	}
	bad := FinishToast(l, Info{Title: "Build", State: plugin.TaskFailed, ExitCode: 2})
	if bad.Style != plugin.ToastFailure || bad.Title != "Build failed" || bad.Message != "Exit code 2" {
		t.Errorf("failure toast %+v", bad)
	}
}

func TestFormats(t *testing.T) {
	l := loc(t)
	half := 0.4
	tests := []struct {
		i    Info
		want string
	}{
		{Info{State: plugin.TaskRunning, Started: t0}, "03:12"},
		{Info{State: plugin.TaskRunning, Started: t0, Progress: &half}, "03:12 · 40%"},
		{Info{State: plugin.TaskQueued}, "Queued"},
		{Info{State: plugin.TaskSucceeded, Started: t0, Ended: t0.Add(151 * time.Second)}, "✓ 2m 31s"},
		{Info{State: plugin.TaskFailed, ExitCode: 1}, "✗ exit 1"},
		{Info{State: plugin.TaskFailed, ExitCode: -1, Error: "x"}, "✗ failed"},
		{Info{State: plugin.TaskCancelled}, "⊘ cancelled"},
	}
	for _, tt := range tests {
		if got := Accessory(l, tt.i, t0.Add(192*time.Second)); got != tt.want {
			t.Errorf("Accessory(%+v) = %q, want %q", tt.i, got, tt.want)
		}
	}
	if Clock(3723*time.Second) != "1:02:03" || Duration(l, 45*time.Second) != "45s" || Duration(l, 3900*time.Second) != "1h 5m" {
		t.Error("Clock/Duration")
	}
	if s := StateText(l, Info{State: plugin.TaskFailed, ExitCode: 3}); s != "Failed (exit code 3)" {
		t.Errorf("StateText %q", s)
	}
}
