package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// fakePath is the built testdata/fakecodex, named codex.exe.
var fakePath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakecodex")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := "codex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	fakePath = filepath.Join(dir, name)
	if out, err := exec.Command("go", "build", "-o", fakePath, "./testdata/fakecodex").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building fakecodex: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fake is a test setup around the fake CLI.
type fake struct {
	t     *testing.T
	b     *Backend
	o     Options
	log   string
	state string
}

// newFake returns a backend running the fake CLI.
func newFake(t *testing.T, edit ...func(*Options)) *fake {
	t.Helper()
	dir := t.TempDir()
	f := &fake{t: t, log: filepath.Join(dir, "log.jsonl"), state: filepath.Join(dir, "state")}
	os.MkdirAll(f.state, 0o755)
	f.o = Options{
		Path:        fakePath,
		Dir:         filepath.Join(dir, "work"),
		Env:         []string{"FAKECODEX_LOG=" + f.log, "FAKECODEX_STATE=" + f.state},
		GracePeriod: 2 * time.Second,
	}
	for _, e := range edit {
		e(&f.o)
	}
	f.b = New(f.o)
	t.Cleanup(func() { f.b.Close() })
	return f
}

func (f *fake) open(o backend.OpenOptions) backend.Conversation {
	f.t.Helper()
	if o.SessionID == "" {
		o.SessionID = "00000000-0000-4000-8000-" + fmt.Sprintf("%012d", time.Now().UnixNano()%1e12)
	}
	c, err := f.b.Open(context.Background(), o)
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}

// logEntry is a line of the fake's log.
type logEntry struct {
	Argv  []string `json:"argv"`
	Stdin *string  `json:"stdin"`
	// ImageSizes are the sizes of the --image files.
	ImageSizes []int64 `json:"imageSizes"`
}

func (f *fake) entries() []logEntry {
	file, err := os.Open(f.log)
	if err != nil {
		return nil
	}
	defer file.Close()
	var out []logEntry
	s := bufio.NewScanner(file)
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		var e logEntry
		if json.Unmarshal(s.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// starts returns the argv of every exec process started.
func (f *fake) starts() [][]string {
	var out [][]string
	for _, e := range f.entries() {
		if e.Argv != nil {
			out = append(out, e.Argv)
		}
	}
	return out
}

// stdin returns every prompt the fake read.
func (f *fake) stdin() []string {
	var out []string
	for _, e := range f.entries() {
		if e.Stdin != nil {
			out = append(out, *e.Stdin)
		}
	}
	return out
}

// answer is a collected turn.
type answer struct {
	text    string
	steps   []backend.Event
	notices []string
	final   backend.Event
}

// collect reads a turn's events until the channel closes.
func collect(t *testing.T, ch <-chan backend.Event) answer {
	t.Helper()
	var a answer
	timeout := time.After(30 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if a.final.Kind == "" {
					t.Fatal("channel closed without a final event")
				}
				return a
			}
			if a.final.Kind != "" {
				t.Fatalf("event after the final one: %+v", ev)
			}
			switch ev.Kind {
			case backend.EventText:
				a.text += ev.Text
			case backend.EventTool, backend.EventToolResult:
				a.steps = append(a.steps, ev)
			case backend.EventNotice:
				a.notices = append(a.notices, ev.Text)
			default:
				a.final = ev
			}
		case <-timeout:
			t.Fatal("turn did not end")
		}
	}
}

func send(t *testing.T, c backend.Conversation, text string) answer {
	t.Helper()
	ch, err := c.Send(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, ch)
}

func wantDone(t *testing.T, a answer, text string) {
	t.Helper()
	if a.final.Kind != backend.EventDone || a.final.Cancelled || a.text != text {
		t.Fatalf("answer %q, final %+v (err %v), want done with %q", a.text, a.final, a.final.Err, text)
	}
}

// waitText waits for the first text of a turn.
func waitText(t *testing.T, ch <-chan backend.Event) string {
	t.Helper()
	timeout := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("turn ended before any text")
			}
			if ev.Kind == backend.EventText {
				return ev.Text
			}
			if ev.Kind == backend.EventDone || ev.Kind == backend.EventError {
				t.Fatalf("turn ended before any text: %+v", ev)
			}
		case <-timeout:
			t.Fatal("no text")
		}
	}
}

// resumed returns the thread ID an argv resumes, "" for a new thread.
func resumed(argv []string) string {
	if i := slices.Index(argv, "resume"); i >= 0 && i+1 < len(argv) {
		return argv[i+1]
	}
	return ""
}

func hasConfig(argv []string, kv string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-c" && argv[i+1] == kv {
			return true
		}
	}
	return false
}

// threads returns the thread IDs the fake knows.
func (f *fake) threads() []string {
	es, _ := os.ReadDir(f.state)
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestMultiTurnResume(t *testing.T) {
	f := newFake(t)
	chat := "11111111-1111-4111-8111-111111111111"
	c := f.open(backend.OpenOptions{SessionID: chat, SystemPrompt: "Be brief."})
	a := send(t, c, "hello there")
	wantDone(t, a, "echo: hello there")
	if u := a.final.Usage; u == nil || u.InputTokens != 100 || u.OutputTokens != 7 || u.Model != "" {
		t.Errorf("usage %+v", a.final.Usage)
	}
	wantDone(t, send(t, c, "again"), "echo: again")
	starts := f.starts()
	threads := f.threads()
	if len(starts) != 2 || len(threads) != 1 || resumed(starts[0]) != "" || resumed(starts[1]) != threads[0] {
		t.Fatalf("starts %q, threads %q", starts, threads)
	}
	for i, argv := range starts {
		if argv[0] != "exec" || argv[len(argv)-1] != "-" {
			t.Errorf("start %d: %q", i, argv)
		}
		for _, flag := range []string{"--json", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check"} {
			if !slices.Contains(argv, flag) {
				t.Errorf("start %d: no %s in %q", i, flag, argv)
			}
		}
		for _, kv := range []string{`approval_policy="never"`, "features.shell_tool=false", "features.unified_exec=false", "features.code_mode_host=false",
			`web_search="disabled"`, `developer_instructions="Be brief."`} {
			if !hasConfig(argv, kv) {
				t.Errorf("start %d: no -c %s in %q", i, kv, argv)
			}
		}
		if j := slices.Index(argv, "--sandbox"); j < 0 || argv[j+1] != "read-only" {
			t.Errorf("start %d: sandbox %q", i, argv)
		}
		for _, bad := range []string{"--dangerously-bypass-approvals-and-sandbox", "--full-auto", "workspace-write", "danger-full-access"} {
			if slices.Contains(argv, bad) {
				t.Errorf("start %d: %s in %q", i, bad, argv)
			}
		}
	}
	if in := f.stdin(); !slices.Equal(in, []string{"hello there", "again"}) {
		t.Errorf("stdin %q", in)
	}

	// A new backend on the same directory (a restart of the launcher)
	// resumes the chat's thread.
	b2 := New(f.o)
	defer b2.Close()
	c2, err := b2.Open(context.Background(), backend.OpenOptions{SessionID: chat, Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	wantDone(t, send(t, c2, "after restart"), "echo: after restart")
	if starts := f.starts(); len(starts) != 3 || resumed(starts[2]) != threads[0] {
		t.Fatalf("starts %q", starts)
	}

	// Forgetting the chat drops its thread: it starts a new one.
	b2.Forget(chat)
	c3, err := b2.Open(context.Background(), backend.OpenOptions{SessionID: chat, Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	a = send(t, c3, "forgotten")
	wantDone(t, a, "echo: forgotten")
	if starts := f.starts(); len(starts) != 4 || resumed(starts[3]) != "" || len(a.notices) != 0 {
		t.Fatalf("starts %q, notices %q", starts, a.notices)
	}
}

func TestArgvOptions(t *testing.T) {
	f := newFake(t, func(o *Options) {
		o.Tools = &ToolServer{Command: `C:\gl\golauncher.exe`, Args: []string{"mcp", "--x"},
			Env: map[string]string{"GOLAUNCHER_AGENT_TOKEN": "tok"}, SessionEnv: "GOLAUNCHER_AGENT_SESSION"}
	})
	chat := "22222222-2222-4222-8222-222222222222"
	c := f.open(backend.OpenOptions{SessionID: chat, Model: "fake-model", Effort: "high", WebSearch: true, LauncherTools: true,
		SystemPrompt: "Line one.\nLine \"two\"."})
	wantDone(t, send(t, c, "hi"), "echo: hi")
	argv := f.starts()[0]
	for _, kv := range []string{
		`web_search="live"`,
		`model_reasoning_effort="high"`, // R109

		`developer_instructions="Line one.\nLine \"two\"."`,
		`mcp_servers.golauncher.command="C:\\gl\\golauncher.exe"`,
		`mcp_servers.golauncher.args=["mcp", "--x"]`,
		`mcp_servers.golauncher.env.GOLAUNCHER_AGENT_SESSION="` + chat + `"`,
		`mcp_servers.golauncher.env.GOLAUNCHER_AGENT_TOKEN="tok"`,
		`mcp_servers.golauncher.default_tools_approval_mode="approve"`,
	} {
		if !hasConfig(argv, kv) {
			t.Errorf("no -c %s in %q", kv, argv)
		}
	}
	if i := slices.Index(argv, "-m"); i < 0 || argv[i+1] != "fake-model" {
		t.Errorf("model: %q", argv)
	}
	// The model calls golauncher's tools through the code-mode host.
	if hasConfig(argv, "features.code_mode_host=false") || !hasConfig(argv, "features.shell_tool=false") {
		t.Errorf("features: %q", argv)
	}
	// Without LauncherTools there is no MCP server at all.
	c = f.open(backend.OpenOptions{})
	wantDone(t, send(t, c, "hi"), "echo: hi")
	for _, a := range f.starts()[1] {
		if strings.Contains(a, "mcp_servers") {
			t.Errorf("tool server without LauncherTools: %q", f.starts()[1])
		}
	}
}

func TestEvents(t *testing.T) {
	f := newFake(t)
	c := f.open(backend.OpenOptions{})

	wantDone(t, send(t, c, "multi"), "Let me think.\n\nDone.")

	a := send(t, c, "tool Visual Studio Code")
	wantDone(t, a, "1")
	want := []backend.Event{
		{Kind: backend.EventTool, Tool: "mcp__golauncher__search", ToolID: "item_0", Input: `{"query":"Visual Studio Code"}`},
		{Kind: backend.EventToolResult, ToolID: "item_0", Result: `{"items":[{"handle":"h1","title":"Example"}]}`},
	}
	if fmt.Sprint(a.steps) != fmt.Sprint(want) {
		t.Errorf("steps %+v", a.steps)
	}

	a = send(t, c, "toolfail")
	wantDone(t, a, "It failed.")
	want = []backend.Event{
		{Kind: backend.EventTool, Tool: "mcp__golauncher__run_action", ToolID: "item_0", Input: `{"handle":"h1"}`},
		{Kind: backend.EventToolResult, ToolID: "item_0", Result: "tool call failed (fake)", IsError: true},
	}
	if fmt.Sprint(a.steps) != fmt.Sprint(want) {
		t.Errorf("steps %+v", a.steps)
	}

	a = send(t, c, "web")
	wantDone(t, a, "Sunny.")
	want = []backend.Event{
		{Kind: backend.EventTool, Tool: "WebSearch", ToolID: "ws_1"},
		{Kind: backend.EventTool, Tool: "WebSearch", ToolID: "ws_1", Input: `{"query":"weather today"}`},
		{Kind: backend.EventToolResult, ToolID: "ws_1"},
	}
	if fmt.Sprint(a.steps) != fmt.Sprint(want) {
		t.Errorf("steps %+v", a.steps)
	}

	wantDone(t, send(t, c, "retry"), "made it")
	wantDone(t, send(t, c, "garbage"), "still fine")
}

func TestErrorKinds(t *testing.T) {
	f := newFake(t)
	c := f.open(backend.OpenOptions{})
	for _, tt := range []struct {
		text string
		kind backend.ErrorKind
		msg  string
	}{
		{"auth", backend.ErrNotLoggedIn, "unexpected status 401 Unauthorized: Missing bearer or basic authentication in header (fake)"},
		{"quota", backend.ErrQuota, "You've hit your usage limit (fake). Try again later."},
		{"newer", backend.ErrVersion, "The 'fake-model' model requires a newer version of Codex. Please upgrade to the latest app or CLI and try again."},
	} {
		a := send(t, c, tt.text)
		e, ok := errors.AsType[*backend.Error](a.final.Err)
		if a.final.Kind != backend.EventError || !ok || e.Kind != tt.kind || e.Message != tt.msg || a.text != "" {
			t.Errorf("%s: answer %q, final %+v", tt.text, a.text, a.final)
		}
	}
	wantDone(t, send(t, c, "ok"), "echo: ok")
}

func TestCrash(t *testing.T) {
	f := newFake(t)
	c := f.open(backend.OpenOptions{})
	a := send(t, c, "crash")
	if a.text != "partial" || a.final.Kind != backend.EventError || backend.KindOf(a.final.Err) != backend.ErrFailed ||
		!strings.Contains(a.final.Err.Error(), "fake crash: something broke") {
		t.Fatalf("answer %q, final %+v", a.text, a.final)
	}
	// The thread started before the crash: the next turn resumes it.
	wantDone(t, send(t, c, "still there?"), "echo: still there?")
	starts := f.starts()
	if len(starts) != 2 || resumed(starts[1]) == "" {
		t.Fatalf("starts %q", starts)
	}
}

func TestCancel(t *testing.T) {
	f := newFake(t)
	c := f.open(backend.OpenOptions{})
	wantDone(t, send(t, c, "first"), "echo: first")
	ch, err := c.Send(context.Background(), "slow")
	if err != nil {
		t.Fatal(err)
	}
	waitText(t, ch)
	start := time.Now()
	c.Cancel()
	if a := collect(t, ch); a.final.Kind != backend.EventDone || !a.final.Cancelled || time.Since(start) > 5*time.Second {
		t.Fatalf("final %+v after %v", a.final, time.Since(start))
	}
	wantDone(t, send(t, c, "next"), "echo: next")
	if starts := f.starts(); len(starts) != 3 || resumed(starts[2]) == "" {
		t.Fatalf("starts %q", starts)
	}
}

// TestLostSession: the thread to resume is unknown (Codex reports it on
// stderr before the turn), so the runtime starts a new one and says so.
func TestLostSession(t *testing.T) {
	f := newFake(t)
	chat := "33333333-3333-4333-8333-333333333333"
	c := f.open(backend.OpenOptions{SessionID: chat})
	wantDone(t, send(t, c, "one"), "echo: one")
	for _, th := range f.threads() {
		os.Remove(filepath.Join(f.state, th)) // Codex lost it
	}
	// An unknown thread is reported on stderr, before any output: the
	// runtime starts a new session and says so.
	a := send(t, c, "two")
	wantDone(t, a, "echo: two")
	if !slices.Equal(a.notices, []string{backend.NoticeNewSession}) {
		t.Errorf("notices %q", a.notices)
	}
	starts := f.starts()
	if len(starts) != 3 || resumed(starts[1]) == "" || resumed(starts[2]) != "" {
		t.Fatalf("starts %q", starts)
	}
	wantDone(t, send(t, c, "three"), "echo: three")
	if starts := f.starts(); len(starts) != 4 || resumed(starts[3]) == "" || resumed(starts[3]) == resumed(starts[1]) {
		t.Fatalf("starts %q", starts)
	}
}

func TestCheck(t *testing.T) {
	f := newFake(t)
	st, err := f.b.Check(context.Background())
	if err != nil || st.Version != "0.162.0" || st.Untested {
		t.Fatalf("Check = %+v, %v", st, err)
	}

	f = newFake(t, func(o *Options) { o.Env = append(o.Env, "FAKECODEX_LOGGED_OUT=1") })
	_, err = f.b.Check(context.Background())
	if e, ok := errors.AsType[*backend.Error](err); !ok || e.Kind != backend.ErrNotLoggedIn || e.Message != "Not logged in" {
		t.Fatalf("Check = %v", err)
	}

	f = newFake(t, func(o *Options) { o.Env = append(o.Env, "FAKECODEX_VERSION=codex-cli 0.46.0") })
	_, err = f.b.Check(context.Background())
	if e, ok := errors.AsType[*backend.Error](err); !ok || e.Kind != backend.ErrVersion || e.Version != "0.46.0" {
		t.Fatalf("Check = %v", err)
	}
	c := f.open(backend.OpenOptions{})
	if a := send(t, c, "hi"); backend.KindOf(a.final.Err) != backend.ErrVersion {
		t.Fatalf("final %+v", a.final)
	}

	f = newFake(t, func(o *Options) { o.Env = append(o.Env, "FAKECODEX_VERSION=codex-cli 1.2.0") })
	if st, err := f.b.Check(context.Background()); err != nil || st.Version != "1.2.0" || !st.Untested {
		t.Fatalf("Check = %+v, %v", st, err)
	}
}

// copyFake puts a copy of the fake CLI of the given version at path.
func copyFake(t *testing.T, path, version string) {
	t.Helper()
	b, err := os.ReadFile(fakePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "version.txt"), []byte(version), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCheckCandidates: of the CLIs found, the one with the highest version
// is used; a too old one is skipped. The installations are this system's
// (codex.exe on Windows, codex elsewhere).
func TestCheckCandidates(t *testing.T) {
	root := t.TempDir()
	pathDir := filepath.Join(root, "path")
	local := filepath.Join(root, "local")
	exe, env := "codex", func(e findEnv) findEnv { e.OS = runtime.GOOS; return e }
	var older, newest string
	if runtime.GOOS == "windows" {
		exe = "codex.exe"
		older = filepath.Join(local, "OpenAI", "Codex", "bin", exe)
		newest = filepath.Join(local, "OpenAI", "Codex", "bin", "0123abcd", exe)
	} else {
		older = filepath.Join(root, "brew", exe)
		newest = filepath.Join(root, "Codex.app", "Contents", "Resources", exe)
	}
	copyFake(t, filepath.Join(pathDir, exe), "codex-cli 0.46.0")
	copyFake(t, older, "codex-cli 0.130.0-alpha.5")
	copyFake(t, newest, "codex-cli 0.162.0-alpha.17.2")
	setCandidateEnv(t, env(findEnv{Path: []string{pathDir}, LocalAppData: local, Arch: "amd64",
		Dirs: binDirs(filepath.Dir(older)), Apps: []string{newest}}))
	log := filepath.Join(root, "log.jsonl")
	b := New(Options{Dir: filepath.Join(root, "work"), Env: []string{"FAKECODEX_LOG=" + log, "FAKECODEX_STATE=" + root}})
	defer b.Close()
	st, err := b.Check(context.Background())
	if err != nil || st.Version != "0.162.0" {
		t.Fatalf("Check = %+v, %v", st, err)
	}
	cmd, err := b.Command(backend.OpenOptions{SessionID: "x"}, newest, false, false)
	if err != nil || cmd.Argv[0] != newest {
		t.Fatalf("Command = %q, %v", cmd.Argv, err)
	}
	c, _ := b.Open(context.Background(), backend.OpenOptions{SessionID: "44444444-4444-4444-8444-444444444444"})
	wantDone(t, send(t, c, "hi"), "echo: hi")

	// Only a too old one.
	setCandidateEnv(t, env(findEnv{Path: []string{pathDir}, Arch: "amd64"}))
	b2 := New(Options{Dir: filepath.Join(root, "work2")})
	defer b2.Close()
	if _, err := b2.Check(context.Background()); backend.KindOf(err) != backend.ErrVersion {
		t.Fatalf("Check = %v", err)
	}

	// Only the npm launcher.
	shimDir := filepath.Join(root, "npm")
	os.MkdirAll(shimDir, 0o755)
	if runtime.GOOS == "windows" {
		os.WriteFile(filepath.Join(shimDir, "codex.cmd"), []byte("@echo off\r\n"), 0o644)
	} else {
		os.WriteFile(filepath.Join(shimDir, "codex"), []byte("#!/usr/bin/env node\n"), 0o755)
	}
	setCandidateEnv(t, env(findEnv{Path: []string{shimDir}, Arch: "amd64"}))
	b3 := New(Options{Dir: filepath.Join(root, "work3")})
	defer b3.Close()
	if _, err := b3.Check(context.Background()); backend.KindOf(err) != backend.ErrNotInstalled || !strings.Contains(err.Error(), "Node.js") {
		t.Fatalf("Check = %v", err)
	}

	// Nothing.
	setCandidateEnv(t, env(findEnv{Path: []string{filepath.Join(root, "empty")}, Arch: "amd64"}))
	b4 := New(Options{Dir: filepath.Join(root, "work4")})
	defer b4.Close()
	if _, err := b4.Check(context.Background()); backend.KindOf(err) != backend.ErrNotInstalled {
		t.Fatalf("Check = %v", err)
	}
}

func TestEnv(t *testing.T) {
	for _, k := range envDrop {
		t.Setenv(k, "1")
	}
	t.Setenv("CODEX_HOME", `C:\codex-home`)
	env := New(Options{Env: []string{"EXTRA_FAKE=1"}}).Env()
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if slices.ContainsFunc(envDrop, func(d string) bool { return strings.EqualFold(k, d) }) {
			t.Errorf("env has %s", kv)
		}
	}
	if !slices.Contains(env, "EXTRA_FAKE=1") || !slices.Contains(env, `CODEX_HOME=C:\codex-home`) {
		t.Error("Options.Env or CODEX_HOME missing")
	}
}
