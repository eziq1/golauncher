package claudecode

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

// fakePath and fakeMCPPath are the built testdata/fakeclaude and
// testdata/fakemcp.
var fakePath, fakeMCPPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fakeclaude")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	fakePath = filepath.Join(dir, name)
	fakeMCPPath = filepath.Join(dir, strings.Replace(name, "claude", "fakemcp", 1))
	for _, b := range [][2]string{{fakePath, "./testdata/fakeclaude"}, {fakeMCPPath, "./testdata/fakemcp"}} {
		out, err := exec.Command("go", "build", "-o", b[0], b[1]).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "building %s: %v\n%s", b[1], err, out)
			os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fake is a test setup around the fake CLI.
type fake struct {
	t      *testing.T
	b      *Backend
	o      Options
	script string // the script file
	log    string
	state  string
}

// newFake returns a backend running the fake CLI with script s.
func newFake(t *testing.T, s map[string]any, edit ...func(*Options)) *fake {
	t.Helper()
	dir := t.TempDir()
	f := &fake{t: t, log: filepath.Join(dir, "log.jsonl"), state: filepath.Join(dir, "state")}
	os.MkdirAll(f.state, 0o755)
	scriptFile := filepath.Join(dir, "script.json")
	b, _ := json.Marshal(s)
	os.WriteFile(scriptFile, b, 0o644)
	o := Options{
		Path:        fakePath,
		Dir:         filepath.Join(dir, "work"),
		Env:         []string{"FAKECLAUDE_SCRIPT=" + scriptFile, "FAKECLAUDE_LOG=" + f.log, "FAKECLAUDE_STATE=" + f.state},
		GracePeriod: 2 * time.Second,
	}
	for _, e := range edit {
		e(&o)
	}
	f.b, f.o, f.script = New(o), o, scriptFile
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
	PID   int      `json:"pid"`
	Argv  []string `json:"argv"`
	Stdin *string  `json:"stdin"`
	// After a call to the MCP server: the tools it listed and what it saw.
	MCPTools []string `json:"mcpTools"`
	MCPMeta  *mcpMeta `json:"mcpMeta"`
	MCPError string   `json:"mcpError"`
	// Help: a --help run; Permission: the answer to a permission request.
	Help       bool   `json:"help"`
	Permission string `json:"permission"`
	// After a message with content blocks: what the fake found in them.
	Images     *int     `json:"images"`
	ImageBytes []int    `json:"imageBytes"`
	ImageTypes []string `json:"imageTypes"`
	Files      int      `json:"files"`
}

// mcpMeta is what testdata/fakemcp echoes in a tool result's _meta.
type mcpMeta struct {
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env"`
	Client      string            `json:"client"`
	Initialized bool              `json:"initialized"`
	Tool        string            `json:"tool"`
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

// starts returns the argv of every process started (not --version).
func (f *fake) starts() [][]string {
	var out [][]string
	for _, e := range f.entries() {
		if e.Argv != nil {
			out = append(out, e.Argv)
		}
	}
	return out
}

// stdin returns every input line the fake received.
func (f *fake) stdin() []string {
	var out []string
	for _, e := range f.entries() {
		if e.Stdin != nil {
			out = append(out, *e.Stdin)
		}
	}
	return out
}

// answer is a collected turn: tools has the names of the tool calls as they
// started, steps every tool event.
type answer struct {
	text  string
	tools []string
	steps []backend.Event
	final backend.Event
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
			case backend.EventTool:
				if ev.Input == "" {
					a.tools = append(a.tools, ev.Tool)
				}
				a.steps = append(a.steps, ev)
			case backend.EventToolResult:
				a.steps = append(a.steps, ev)
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
		t.Fatalf("answer %q, final %+v, want done with %q", a.text, a.final, text)
	}
}

func hasFlag(argv []string, flag, value string) bool {
	i := slices.Index(argv, flag)
	return i >= 0 && i+1 < len(argv) && argv[i+1] == value
}

func TestMultiTurnOneProcess(t *testing.T) {
	f := newFake(t, nil)
	c := f.open(backend.OpenOptions{SessionID: "11111111-1111-4111-8111-111111111111"})
	a := send(t, c, "hello there")
	wantDone(t, a, "echo: hello there")
	if u := a.final.Usage; u == nil || u.InputTokens != 112 || u.OutputTokens != 4 || u.Model != "claude-fake-1" {
		t.Errorf("usage %+v", a.final.Usage)
	}
	wantDone(t, send(t, c, "again"), "echo: again")
	starts := f.starts()
	if len(starts) != 1 || !hasFlag(starts[0], "--session-id", "11111111-1111-4111-8111-111111111111") {
		t.Fatalf("starts %q", starts)
	}
	in := f.stdin()
	if len(in) != 2 || in[0] != `{"type":"user","message":{"role":"user","content":"hello there"}}` {
		t.Errorf("stdin %q", in)
	}
}

func TestArgv(t *testing.T) {
	f := newFake(t, nil)
	c := f.open(backend.OpenOptions{SystemPrompt: "Be brief.\nAnswer in English.", Model: "fake-model"})
	wantDone(t, send(t, c, "hi"), "echo: hi")
	w := f.open(backend.OpenOptions{WebSearch: true})
	a := send(t, w, "tool")
	wantDone(t, a, "echo: tool")
	if !slices.Equal(a.tools, []string{"WebSearch"}) {
		t.Errorf("tools %q", a.tools)
	}
	starts := f.starts()
	if len(starts) != 2 {
		t.Fatalf("starts %q", starts)
	}
	for i, argv := range starts {
		for _, flag := range []string{"-p", "--safe-mode", "--strict-mcp-config", "--disable-slash-commands", "--include-partial-messages", "--verbose"} {
			if !slices.Contains(argv, flag) {
				t.Errorf("start %d: no %s in %q", i, flag, argv)
			}
		}
		for _, bad := range []string{"--bare", "--continue", "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "bypassPermissions"} {
			if slices.Contains(argv, bad) {
				t.Errorf("start %d: %s in %q", i, bad, argv)
			}
		}
		if !hasFlag(argv, "--setting-sources", "") || !hasFlag(argv, "--permission-prompts", "none") ||
			!hasFlag(argv, "--input-format", "stream-json") || !hasFlag(argv, "--output-format", "stream-json") {
			t.Errorf("start %d: %q", i, argv)
		}
	}
	if !hasFlag(starts[0], "--tools", "") || slices.Contains(starts[0], "--allowedTools") ||
		!hasFlag(starts[0], "--append-system-prompt", "Be brief.\nAnswer in English.") || !hasFlag(starts[0], "--model", "fake-model") {
		t.Errorf("plain: %q", starts[0])
	}
	if !hasFlag(starts[1], "--tools", "WebSearch,WebFetch") || !hasFlag(starts[1], "--allowedTools", "WebSearch,WebFetch") ||
		slices.Contains(starts[1], "--append-system-prompt") || slices.Contains(starts[1], "--model") {
		t.Errorf("web: %q", starts[1])
	}
}

func TestResumeAfterRelease(t *testing.T) {
	f := newFake(t, nil)
	id := "22222222-2222-4222-8222-222222222222"
	c := f.open(backend.OpenOptions{SessionID: id})
	wantDone(t, send(t, c, "one"), "echo: one")
	c.Release()
	if n := f.b.liveCount(); n != 0 {
		t.Errorf("%d live processes after Release", n)
	}
	c.Release() // nothing to release
	wantDone(t, send(t, c, "two"), "echo: two")
	starts := f.starts()
	if len(starts) != 2 || !hasFlag(starts[0], "--session-id", id) || !hasFlag(starts[1], "--resume", id) {
		t.Fatalf("starts %q", starts)
	}
}

// waitText reads the first event, which must be text, and returns it.
func waitText(t *testing.T, ch <-chan backend.Event) string {
	t.Helper()
	select {
	case ev := <-ch:
		if ev.Kind != backend.EventText {
			t.Fatalf("first event %+v", ev)
		}
		return ev.Text
	case <-time.After(20 * time.Second):
		t.Fatal("no text")
	}
	return ""
}

func TestCancelPersistent(t *testing.T) {
	f := newFake(t, nil)
	c := f.open(backend.OpenOptions{})
	ch, err := c.Send(context.Background(), "slow")
	if err != nil {
		t.Fatal(err)
	}
	waitText(t, ch)
	if _, err := c.Send(context.Background(), "busy"); err == nil {
		t.Error("Send while an answer is written")
	}
	start := time.Now()
	c.Cancel()
	a := collect(t, ch)
	if a.final.Kind != backend.EventDone || !a.final.Cancelled || time.Since(start) > 3*time.Second {
		t.Fatalf("final %+v after %v", a.final, time.Since(start))
	}
	c.Cancel() // nothing running
	wantDone(t, send(t, c, "after"), "echo: after")
	if n := len(f.starts()); n != 1 {
		t.Errorf("%d processes", n)
	}
	if !slices.ContainsFunc(f.stdin(), func(s string) bool { return strings.Contains(s, `"subtype":"interrupt"`) }) {
		t.Errorf("no interrupt in %q", f.stdin())
	}

	// Cancelling the context works the same.
	ctx, cancel := context.WithCancel(context.Background())
	ch, err = c.Send(ctx, "slow")
	if err != nil {
		t.Fatal(err)
	}
	waitText(t, ch)
	cancel()
	if a := collect(t, ch); !a.final.Cancelled {
		t.Fatalf("final %+v", a.final)
	}
}

func TestCancelPerTurn(t *testing.T) {
	f := newFake(t, nil, func(o *Options) { o.PerTurn = true })
	id := "33333333-3333-4333-8333-333333333333"
	c := f.open(backend.OpenOptions{SessionID: id})
	wantDone(t, send(t, c, "first"), "echo: first")
	ch, err := c.Send(context.Background(), "slow")
	if err != nil {
		t.Fatal(err)
	}
	waitText(t, ch)
	start := time.Now()
	c.Cancel()
	if a := collect(t, ch); a.final.Kind != backend.EventDone || !a.final.Cancelled || time.Since(start) > 3*time.Second {
		t.Fatalf("final %+v after %v", a.final, time.Since(start))
	}
	wantDone(t, send(t, c, "/help me"), "echo: /help me")
	starts := f.starts()
	if len(starts) != 3 || !hasFlag(starts[0], "--session-id", id) || !hasFlag(starts[2], "--resume", id) {
		t.Fatalf("starts %q", starts)
	}
	for _, argv := range starts {
		if slices.Contains(argv, "--input-format") || !slices.Contains(argv, "--verbose") || !hasFlag(argv, "--tools", "") {
			t.Errorf("per-turn argv %q", argv)
		}
	}
	if in := f.stdin(); in[len(in)-1] != " /help me" {
		t.Errorf("stdin %q", in)
	}
}

func TestLeadingSlash(t *testing.T) {
	f := newFake(t, nil)
	c := f.open(backend.OpenOptions{})
	wantDone(t, send(t, c, "/compact"), "echo: /compact")
	if in := f.stdin(); len(in) != 1 || !strings.Contains(in[0], `"content":" /compact"`) {
		t.Errorf("stdin %q", in)
	}
}

func TestCrashMidTurn(t *testing.T) {
	f := newFake(t, nil)
	id := "44444444-4444-4444-8444-444444444444"
	c := f.open(backend.OpenOptions{SessionID: id})
	a := send(t, c, "crash")
	if a.text != "partial" || a.final.Kind != backend.EventError || backend.KindOf(a.final.Err) != backend.ErrFailed ||
		!strings.Contains(a.final.Err.Error(), "fake crash: something broke") {
		t.Fatalf("answer %q, final %+v", a.text, a.final)
	}
	wantDone(t, send(t, c, "still there?"), "echo: still there?")
	starts := f.starts()
	if len(starts) != 2 || !hasFlag(starts[1], "--resume", id) || !hasFlag(starts[1], "--input-format", "stream-json") {
		t.Fatalf("starts %q", starts)
	}
}

func TestMaxProcesses(t *testing.T) {
	f := newFake(t, nil)
	var convs []backend.Conversation
	for i := range 3 {
		c := f.open(backend.OpenOptions{SessionID: fmt.Sprintf("55555555-5555-4555-8555-55555555555%d", i)})
		wantDone(t, send(t, c, "hi"), "echo: hi")
		convs = append(convs, c)
	}
	if n := f.b.liveCount(); n != 2 {
		t.Errorf("%d live processes", n)
	}
	// The second is still running; the first was released and resumes.
	wantDone(t, send(t, convs[1], "two"), "echo: two")
	if n := len(f.starts()); n != 3 {
		t.Errorf("%d starts", n)
	}
	wantDone(t, send(t, convs[0], "one"), "echo: one")
	starts := f.starts()
	if len(starts) != 4 || !hasFlag(starts[3], "--resume", "55555555-5555-4555-8555-555555555550") {
		t.Fatalf("starts %q", starts)
	}
	if n := f.b.liveCount(); n != 2 {
		t.Errorf("%d live processes", n)
	}
}

// TestMaxProcessesBusy: a conversation in the middle of an answer is never
// stopped to make room.
func TestMaxProcessesBusy(t *testing.T) {
	f := newFake(t, nil, func(o *Options) { o.MaxProcesses = 1 })
	busy := f.open(backend.OpenOptions{})
	ch, err := busy.Send(context.Background(), "slow")
	if err != nil {
		t.Fatal(err)
	}
	first := waitText(t, ch)
	other := f.open(backend.OpenOptions{})
	wantDone(t, send(t, other, "hi"), "echo: hi")
	if n := f.b.liveCount(); n != 2 {
		t.Errorf("%d live processes while one is busy", n)
	}
	a := collect(t, ch)
	if a.final.Kind != backend.EventDone || a.final.Cancelled || len(first+a.text) != 200 {
		t.Fatalf("busy answer %d bytes, final %+v", len(first+a.text), a.final)
	}
}

func TestVersionGate(t *testing.T) {
	f := newFake(t, map[string]any{"version": "2.0.77 (Claude Code)"})
	_, err := f.b.Check(context.Background())
	if e, ok := errors.AsType[*backend.Error](err); !ok || e.Kind != backend.ErrVersion || e.Version != "2.0.77" {
		t.Fatalf("Check = %v", err)
	}
	c := f.open(backend.OpenOptions{})
	if a := send(t, c, "hi"); backend.KindOf(a.final.Err) != backend.ErrVersion {
		t.Fatalf("final %+v", a.final)
	}

	f = newFake(t, map[string]any{"version": "3.0.1 (Claude Code)"})
	st, err := f.b.Check(context.Background())
	if err != nil || st.Version != "3.0.1" || !st.Untested {
		t.Fatalf("Check = %+v, %v", st, err)
	}
	c = f.open(backend.OpenOptions{})
	wantDone(t, send(t, c, "hi"), "echo: hi")
	if starts := f.starts(); len(starts) != 1 || slices.Contains(starts[0], "--input-format") {
		t.Fatalf("starts %q", starts)
	}

	f = newFake(t, nil)
	if st, err := f.b.Check(context.Background()); err != nil || st.Version != "2.1.296" || st.Untested {
		t.Fatalf("Check = %+v, %v", st, err)
	}
}

func TestNotInstalled(t *testing.T) {
	// A configured CLI that is missing falls back to the automatic choice
	// (R84), which finds nothing here either.
	empty := t.TempDir()
	for _, k := range []string{"PATH", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "NVM_DIR", "NVM_HOME", "NVM_SYMLINK", "FNM_DIR",
		"XDG_DATA_HOME", "VOLTA_HOME", "ASDF_DATA_DIR", "MISE_DATA_DIR", "NODENV_ROOT", "N_PREFIX", "npm_config_prefix", "NPM_CONFIG_PREFIX"} {
		t.Setenv(k, empty)
	}
	if runtime.GOOS != "windows" {
		// The fixed system folders cannot be made up.
		for _, p := range []string{"/opt/homebrew/bin/claude", "/usr/local/bin/claude"} {
			if _, err := os.Stat(p); err == nil {
				t.Skip("Claude Code is installed in " + filepath.Dir(p))
			}
		}
	}
	f := newFake(t, nil, func(o *Options) { o.Path = filepath.Join(t.TempDir(), "missing", "claude.exe") })
	if _, err := f.b.Check(context.Background()); backend.KindOf(err) != backend.ErrNotInstalled {
		t.Fatalf("Check = %v", err)
	}
	c := f.open(backend.OpenOptions{})
	if a := send(t, c, "hi"); a.final.Kind != backend.EventError || backend.KindOf(a.final.Err) != backend.ErrNotInstalled {
		t.Fatalf("final %+v", a.final)
	}
}

func TestErrorKinds(t *testing.T) {
	f := newFake(t, nil)
	c := f.open(backend.OpenOptions{})
	for _, tt := range []struct {
		text string
		kind backend.ErrorKind
		msg  string
	}{
		{"auth", backend.ErrNotLoggedIn, "Not logged in · Please run /login"},
		{"quota", backend.ErrQuota, "Claude AI usage limit reached|1700000000"},
		{"fail", backend.ErrFailed, "Something went wrong (fake)"},
	} {
		a := send(t, c, tt.text)
		e, ok := errors.AsType[*backend.Error](a.final.Err)
		if a.final.Kind != backend.EventError || !ok || e.Kind != tt.kind || e.Message != tt.msg || a.text != "" {
			t.Errorf("%s: answer %q, final %+v", tt.text, a.text, a.final)
		}
	}
	wantDone(t, send(t, c, "ok"), "echo: ok")
	if n := len(f.starts()); n != 1 {
		t.Errorf("%d processes", n)
	}
}

func TestFallback(t *testing.T) {
	for _, s := range []string{"garbage", "crashAtStart"} {
		t.Run(s, func(t *testing.T) {
			f := newFake(t, map[string]any{s: true})
			id := "66666666-6666-4666-8666-666666666666"
			c := f.open(backend.OpenOptions{SessionID: id})
			wantDone(t, send(t, c, "hi"), "echo: hi")
			wantDone(t, send(t, c, "more"), "echo: more")
			starts := f.starts()
			if len(starts) != 3 || !hasFlag(starts[0], "--input-format", "stream-json") ||
				slices.Contains(starts[1], "--input-format") || !hasFlag(starts[1], "--session-id", id) ||
				slices.Contains(starts[2], "--input-format") || !hasFlag(starts[2], "--resume", id) {
				t.Fatalf("starts %q", starts)
			}
			if n := f.b.liveCount(); n != 0 {
				t.Errorf("%d live processes", n)
			}
		})
	}
}

func TestLostSession(t *testing.T) {
	for _, perTurn := range []bool{false, true} {
		t.Run(fmt.Sprint("perTurn=", perTurn), func(t *testing.T) {
			f := newFake(t, nil, func(o *Options) { o.PerTurn = perTurn })
			id := "77777777-7777-4777-8777-777777777777"
			c := f.open(backend.OpenOptions{SessionID: id, Resume: true})
			wantDone(t, send(t, c, "remember me?"), "echo: remember me?")
			wantDone(t, send(t, c, "next"), "echo: next")
			starts := f.starts()
			if len(starts) < 2 || !hasFlag(starts[0], "--resume", id) || !hasFlag(starts[1], "--session-id", id) {
				t.Fatalf("starts %q", starts)
			}
			if perTurn && (len(starts) != 3 || !hasFlag(starts[2], "--resume", id)) || !perTurn && len(starts) != 2 {
				t.Fatalf("starts %q", starts)
			}
		})
	}
}

func TestSessionInUse(t *testing.T) {
	for _, asResult := range []bool{false, true} {
		t.Run(fmt.Sprint("result=", asResult), func(t *testing.T) {
			f := newFake(t, map[string]any{"inUseAsResult": asResult})
			id := "88888888-8888-4888-8888-888888888888"
			os.WriteFile(filepath.Join(f.state, id), nil, 0o644) // an earlier, failed first turn
			c := f.open(backend.OpenOptions{SessionID: id})
			wantDone(t, send(t, c, "hi"), "echo: hi")
			starts := f.starts()
			if len(starts) != 2 || !hasFlag(starts[0], "--session-id", id) || !hasFlag(starts[1], "--resume", id) ||
				!hasFlag(starts[1], "--input-format", "stream-json") {
				t.Fatalf("starts %q", starts)
			}
		})
	}
}

func TestCloseMidTurn(t *testing.T) {
	f := newFake(t, nil)
	c := f.open(backend.OpenOptions{})
	ch, err := c.Send(context.Background(), "slow")
	if err != nil {
		t.Fatal(err)
	}
	waitText(t, ch)
	c.Close()
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("channel not closed after Close")
	}
	if _, err := c.Send(context.Background(), "x"); err == nil {
		t.Error("Send after Close")
	}
	if n := f.b.liveCount(); n != 0 {
		t.Errorf("%d live processes", n)
	}
	f.b.Close()
	if _, err := f.b.Open(context.Background(), backend.OpenOptions{SessionID: "x"}); err == nil {
		t.Error("Open after Close")
	}
}

// TestUnreadEvents: a turn whose events nobody reads does not hold up the
// conversation.
func TestUnreadEvents(t *testing.T) {
	f := newFake(t, nil)
	c := f.open(backend.OpenOptions{})
	if _, err := c.Send(context.Background(), strings.Repeat("long ", 200)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		ch, err := c.Send(context.Background(), "next")
		if err == nil {
			wantDone(t, collect(t, ch), "echo: next")
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestEnv(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("CLAUDE_CODE_SSE_PORT", "12345")
	env := New(Options{Env: []string{"EXTRA_FAKE=1"}}).env()
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if slices.ContainsFunc(envDrop, func(d string) bool { return strings.EqualFold(k, d) }) {
			t.Errorf("env has %s", kv)
		}
	}
	if !slices.Contains(env, "EXTRA_FAKE=1") {
		t.Error("Options.Env missing")
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]string{"2.1.296 (Claude Code)": "2.1.296", "v3.0.0": "3.0.0", "claude 2.10.1-beta": "", "nonsense": ""} {
		if got := parseVersion(in); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", in, got, want)
		}
	}
	if compareVersions("2.1.0", "2.0.99") <= 0 || compareVersions("2.10.0", "2.9.0") <= 0 || compareVersions("2.1.0", "2.1.0") != 0 {
		t.Error("compareVersions")
	}
	for _, tt := range []struct {
		code, text string
		want       backend.ErrorKind
	}{
		{"authentication_failed", "x", backend.ErrNotLoggedIn},
		{"", "Invalid API key · Fix external API key", backend.ErrNotLoggedIn},
		{"billing_error", "x", backend.ErrQuota},
		{"", "5-hour limit reached ∙ resets 3pm", backend.ErrQuota},
		{"model_not_found", "There's an issue with the selected model (x).", backend.ErrFailed},
	} {
		if got := classify(tt.code, tt.text, false); got != tt.want {
			t.Errorf("classify(%q, %q) = %s", tt.code, tt.text, got)
		}
	}
	if !sessionInUse("Error: Session ID x is already in use.") || !sessionInUse("session already exists") || sessionInUse("in use") {
		t.Error("sessionInUse")
	}

	// A synthetic message without stream events is used; a streamed one is
	// not repeated; non-text deltas are ignored.
	var got []backend.Event
	p := newParser(func(ev backend.Event) { got = append(got, ev) })
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s","model":"claude-fake-2"}`,
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"t"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Hi"}]}}`,
		`{"type":"assistant","message":{"id":"m2","model":"<synthetic>","content":[{"type":"text","text":"Made up"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"r"}]}}`,
		`{"type":"assistant","message":"unexpected shape"}`,
	}
	for _, l := range lines {
		if res, garbage := p.handle([]byte(l)); res != nil || garbage {
			t.Fatalf("%s: result %v, garbage %v", l, res, garbage)
		}
	}
	if len(got) != 2 || got[0].Text != "Hi" || got[1].Text != "Made up" || !p.sawInit {
		t.Errorf("events %+v", got)
	}
	res, _ := p.handle([]byte(`{"type":"result","is_error":false,"usage":{"input_tokens":1,"cache_read_input_tokens":2,"output_tokens":3},"modelUsage":{"a":{"outputTokens":1}}}`))
	if u := p.usageOf(res); u.InputTokens != 3 || u.OutputTokens != 3 || u.Model != "claude-fake-2" {
		t.Errorf("usage %+v", u)
	}
	p.model = ""
	res, _ = p.handle([]byte(`{"type":"result","modelUsage":{"small":{"outputTokens":1},"main":{"outputTokens":9}}}`))
	if u := p.usageOf(res); u.Model != "main" {
		t.Errorf("usage model %q", u.Model)
	}
	if _, garbage := p.handle([]byte("Error: something")); !garbage {
		t.Error("garbage not detected")
	}
	res, _ = p.handle([]byte(`{"type":"result","is_error":true,"errors":["one","two"]}`))
	if resultText(res) != "one\ntwo" {
		t.Errorf("resultText %q", resultText(res))
	}
}

// toolServer is the test's golauncher MCP server: testdata/fakemcp.
func toolServer() *ToolServer {
	return &ToolServer{
		Command:    fakeMCPPath,
		Args:       []string{"mcp", "--fake"},
		Env:        map[string]string{"GOLAUNCHER_AGENT_TOKEN": "tok-123"},
		SessionEnv: "GOLAUNCHER_AGENT_SESSION",
	}
}

// checkSafe checks the flags every command line has, and the ones none has.
func checkSafe(t *testing.T, argv []string) {
	t.Helper()
	for _, flag := range []string{"-p", "--strict-mcp-config", "--disable-slash-commands"} {
		if !slices.Contains(argv, flag) {
			t.Errorf("no %s in %q", flag, argv)
		}
	}
	// --safe-mode drops the --mcp-config servers: only without golauncher's.
	if mcp := slices.Contains(argv, "--mcp-config"); mcp == slices.Contains(argv, "--safe-mode") {
		t.Errorf("--mcp-config %v but --safe-mode %v in %q", mcp, !mcp, argv)
	}
	for _, bad := range []string{"--bare", "--continue", "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "bypassPermissions", "--permission-mode"} {
		if slices.Contains(argv, bad) {
			t.Errorf("%s in %q", bad, argv)
		}
	}
	if !hasFlag(argv, "--setting-sources", "") || !hasFlag(argv, "--permission-prompts", "none") {
		t.Errorf("argv %q", argv)
	}
}

func TestLauncherTools(t *testing.T) {
	for _, perTurn := range []bool{false, true} {
		t.Run(fmt.Sprint("perTurn=", perTurn), func(t *testing.T) {
			f := newFake(t, nil, func(o *Options) { o.Tools = toolServer(); o.PerTurn = perTurn })
			id := "99999999-9999-4999-8999-999999999999"
			c := f.open(backend.OpenOptions{SessionID: id, LauncherTools: true})
			a := send(t, c, "launcher port 8080")
			wantDone(t, a, "echo: launcher port 8080")

			// The steps: the call as it starts, its input, its result.
			if len(a.steps) != 3 {
				t.Fatalf("steps %+v", a.steps)
			}
			start, input, result := a.steps[0], a.steps[1], a.steps[2]
			toolID := start.ToolID
			if start.Kind != backend.EventTool || start.Tool != "mcp__golauncher__search" || !strings.HasPrefix(toolID, "toolu_") || start.Input != "" {
				t.Errorf("start %+v", start)
			}
			if input.Kind != backend.EventTool || input.ToolID != toolID || input.Tool != start.Tool || input.Input != `{"query":"port 8080"}` {
				t.Errorf("input %+v", input)
			}
			wantResult := `{"items":[{"handle":"i1","plugin":"builtin.fake","title":"Item for port 8080"}],"query":"port 8080"}`
			if result.Kind != backend.EventToolResult || result.ToolID != toolID || result.IsError || result.Result != wantResult {
				t.Errorf("result %+v", result)
			}

			// The command line.
			starts := f.starts()
			if len(starts) != 1 {
				t.Fatalf("starts %q", starts)
			}
			argv := starts[0]
			checkSafe(t, argv)
			cfgPath := filepath.Join(f.b.o.Dir, "mcp-"+id+".json")
			if !hasFlag(argv, "--mcp-config", cfgPath) || !hasFlag(argv, "--allowedTools", "mcp__golauncher__*") || !hasFlag(argv, "--tools", "") {
				t.Errorf("argv %q", argv)
			}

			// The configuration file.
			b, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			var cfg struct {
				MCPServers map[string]struct {
					Type    string            `json:"type"`
					Command string            `json:"command"`
					Args    []string          `json:"args"`
					Env     map[string]string `json:"env"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal(b, &cfg); err != nil {
				t.Fatal(err)
			}
			srv, ok := cfg.MCPServers["golauncher"]
			if len(cfg.MCPServers) != 1 || !ok || srv.Type != "stdio" || srv.Command != fakeMCPPath || !slices.Equal(srv.Args, []string{"mcp", "--fake"}) ||
				len(srv.Env) != 2 || srv.Env["GOLAUNCHER_AGENT_TOKEN"] != "tok-123" || srv.Env["GOLAUNCHER_AGENT_SESSION"] != id {
				t.Errorf("config %s", b)
			}

			// What the MCP server saw.
			var meta *mcpMeta
			var tools []string
			for _, e := range f.entries() {
				if e.MCPError != "" {
					t.Errorf("MCP error %s", e.MCPError)
				}
				if e.MCPMeta != nil {
					meta, tools = e.MCPMeta, e.MCPTools
				}
			}
			if meta == nil {
				t.Fatal("the MCP server was not called")
			}
			if meta.Env["GOLAUNCHER_AGENT_TOKEN"] != "tok-123" || meta.Env["GOLAUNCHER_AGENT_SESSION"] != id || !slices.Equal(meta.Args, []string{"mcp", "--fake"}) ||
				meta.Client != "claude-code" || !meta.Initialized || meta.Tool != "search" {
				t.Errorf("server saw %+v", meta)
			}
			if !slices.Equal(tools, []string{"list_plugins", "search", "run_command", "get_detail", "run_action"}) {
				t.Errorf("tools %q", tools)
			}

			// A failed call.
			a = send(t, c, "launcher fail")
			wantDone(t, a, "echo: launcher fail")
			if n := len(a.steps); n != 3 || !a.steps[2].IsError || a.steps[2].Result != `{"status":"error","error":"fake failure"}` {
				t.Errorf("steps %+v", a.steps)
			}

			c.Close()
			if _, err := os.Stat(cfgPath); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("config after Close: %v", err)
			}
		})
	}
}

func TestLauncherToolsArgv(t *testing.T) {
	f := newFake(t, nil, func(o *Options) { o.Tools = toolServer() })
	web := f.open(backend.OpenOptions{SessionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", LauncherTools: true, WebSearch: true})
	wantDone(t, send(t, web, "hi"), "echo: hi")
	off := f.open(backend.OpenOptions{SessionID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"})
	wantDone(t, send(t, off, "hi"), "echo: hi")
	starts := f.starts()
	if len(starts) != 2 {
		t.Fatalf("starts %q", starts)
	}
	for _, argv := range starts {
		checkSafe(t, argv)
	}
	if !hasFlag(starts[0], "--tools", "WebSearch,WebFetch") || !hasFlag(starts[0], "--allowedTools", "mcp__golauncher__*,WebSearch,WebFetch") ||
		!hasFlag(starts[0], "--mcp-config", filepath.Join(f.b.o.Dir, "mcp-aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.json")) {
		t.Errorf("web and tools: %q", starts[0])
	}
	if !hasFlag(starts[1], "--tools", "") || slices.Contains(starts[1], "--allowedTools") || slices.Contains(starts[1], "--mcp-config") {
		t.Errorf("tools off: %q", starts[1])
	}
	if _, err := os.Stat(filepath.Join(f.b.o.Dir, "mcp-bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.json")); err == nil {
		t.Error("a configuration without launcher tools")
	}

	// Without a tool server, LauncherTools does nothing.
	g := newFake(t, nil)
	c := g.open(backend.OpenOptions{LauncherTools: true})
	wantDone(t, send(t, c, "hi"), "echo: hi")
	if argv := g.starts()[0]; slices.Contains(argv, "--mcp-config") || slices.Contains(argv, "--allowedTools") || !hasFlag(argv, "--tools", "") {
		t.Errorf("no tool server: %q", argv)
	}

	// The flags in one place.
	for _, tt := range []struct {
		web  bool
		cfg  string
		want []string
	}{
		{false, "", []string{"--tools", ""}},
		{true, "", []string{"--tools", "WebSearch,WebFetch", "--allowedTools", "WebSearch,WebFetch"}},
		{false, "c.json", []string{"--tools", "", "--mcp-config", "c.json", "--allowedTools", "mcp__golauncher__*"}},
		{true, "c.json", []string{"--tools", "WebSearch,WebFetch", "--mcp-config", "c.json", "--allowedTools", "mcp__golauncher__*,WebSearch,WebFetch"}},
	} {
		if got := toolFlags(tt.web, tt.cfg); !slices.Equal(got, tt.want) {
			t.Errorf("toolFlags(%v, %q) = %q", tt.web, tt.cfg, got)
		}
	}
	if got := toolConfigName(`../x\y`); got != "mcp-___x_y.json" {
		t.Errorf("toolConfigName = %q", got)
	}
}

func TestParseToolSteps(t *testing.T) {
	var got []backend.Event
	p := newParser(func(ev backend.Event) { got = append(got, ev) })
	lines := []string{
		// Streamed: the start has the ID, the complete message the input.
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"m1"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"mcp__golauncher__run_action","input":{}}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"handle\":"}}}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__golauncher__run_action","input":{ "handle" : "i7" }}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"{\"status\":\"declined\"}","is_error":true}]}}`,
		// Not streamed: one event with ID and input.
		`{"type":"assistant","message":{"id":"m2","model":"<synthetic>","content":[{"type":"tool_use","id":"toolu_2","name":"mcp__golauncher__search","input":{"query":"x"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":[{"type":"text","text":"a"},{"type":"image"},{"type":"text","text":"b"}]}]}}`,
		// Ignored.
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`,
		`{"type":"user","message":{"role":"user","content":"plain"}}`,
	}
	for _, l := range lines {
		if res, garbage := p.handle([]byte(l)); res != nil || garbage {
			t.Fatalf("%s: result %v, garbage %v", l, res, garbage)
		}
	}
	want := []backend.Event{
		{Kind: backend.EventTool, Tool: "mcp__golauncher__run_action", ToolID: "toolu_1"},
		{Kind: backend.EventTool, Tool: "mcp__golauncher__run_action", ToolID: "toolu_1", Input: `{"handle":"i7"}`},
		{Kind: backend.EventToolResult, ToolID: "toolu_1", Result: `{"status":"declined"}`, IsError: true},
		{Kind: backend.EventTool, Tool: "mcp__golauncher__search", ToolID: "toolu_2", Input: `{"query":"x"}`},
		{Kind: backend.EventToolResult, ToolID: "toolu_2", Result: "a\nb"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("events\n%+v\nwant\n%+v", got, want)
	}
}
