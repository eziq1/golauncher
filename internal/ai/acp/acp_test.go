package acp

import (
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

func TestMain(m *testing.M) {
	if os.Getenv("ACP_FAKE") == "1" {
		fakeAgent()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// testConfig is the fake agent's configuration.
func testConfig() Config {
	return Config{
		Info: agentrt.Info{ID: "fakeacp", Name: "Fake ACP", VersionArgs: []string{"--version"}, MinVersion: "1.0.0", TestedMajor: 1},
		Args: func(agentrt.Launch) (agentrt.Command, error) {
			return agentrt.Command{Argv: []string{"--acp"}}, nil
		},
		ClientVersion: "9.8.7",
	}
}

// fixture is a runtime on the fake agent.
type fixture struct {
	r        *agentrt.Runtime
	log, dir string
}

func newFixture(t *testing.T, tools *agentrt.ToolServer, env ...string) *fixture {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log.jsonl")
	work := filepath.Join(dir, "work")
	r := agentrt.New(NewAdapter(testConfig()), agentrt.Options{Path: exe, Dir: work, Tools: tools,
		Env: append([]string{"ACP_FAKE=1", "ACP_FAKE_LOG=" + log}, env...)})
	t.Cleanup(func() { r.Close() })
	return &fixture{r: r, log: log, dir: work}
}

func (f *fixture) open(t *testing.T, o backend.OpenOptions) backend.Conversation {
	t.Helper()
	if o.SessionID == "" {
		o.SessionID = "chat-1"
	}
	c, err := f.r.Open(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// turnResult is what a turn delivered.
type turnResult struct {
	text    string
	final   backend.Event
	steps   []backend.Event
	perms   []*backend.PermissionRequest
	notices []string
}

// send runs a turn; onPerm handles permission requests (nil: answer "").
func send(t *testing.T, c backend.Conversation, text string, onPerm func(*backend.PermissionRequest)) turnResult {
	t.Helper()
	ch, err := c.Send(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	var r turnResult
	var b strings.Builder
	timeout := time.After(30 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("closed without a final event")
			}
			switch ev.Kind {
			case backend.EventText:
				b.WriteString(ev.Text)
			case backend.EventTool, backend.EventToolResult:
				r.steps = append(r.steps, ev)
			case backend.EventNotice:
				r.notices = append(r.notices, ev.Text)
			case backend.EventPermission:
				r.perms = append(r.perms, ev.Permission)
				if onPerm != nil {
					onPerm(ev.Permission)
				} else {
					ev.Permission.Respond("")
				}
			case backend.EventDone, backend.EventError:
				r.text, r.final = b.String(), ev
				return r
			}
		case <-timeout:
			t.Fatal("turn did not end")
		}
	}
}

func count(ss []string, s string) int {
	n := 0
	for _, x := range ss {
		if x == s {
			n++
		}
	}
	return n
}

// TestHandshakeAndTurns: the exact initialize and session/new the agent
// receives, the system prompt as the first prompt's leading block, several
// turns on one process, a load after Release that does not leak the
// replayed history, and Forget.
func TestHandshakeAndTurns(t *testing.T) {
	f := newFixture(t, &agentrt.ToolServer{Command: `C:\tools\glctl.exe`, Args: []string{"mcp"},
		Env: map[string]string{"B_VAR": "2", "A_VAR": "1"}, SessionEnv: "GOLAUNCHER_AGENT_SESSION"})
	c := f.open(t, backend.OpenOptions{SystemPrompt: "Be brief.", LauncherTools: true})
	r := send(t, c, "hello", nil)
	if r.text != "echo: hello" || r.final.Kind != backend.EventDone || r.final.Usage == nil || r.final.Usage.Model != "fake-model" {
		t.Fatalf("turn 1: %q %+v", r.text, r.final)
	}
	if r := send(t, c, "again", nil); r.text != "echo: again" {
		t.Fatalf("turn 2: %q", r.text)
	}
	msgs := received(f.log)
	want := `{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":false,"writeTextFile":false},"terminal":false},"clientInfo":{"name":"golauncher","version":"9.8.7"}}`
	if msgs[0].Method != "initialize" || string(msgs[0].Params) != want {
		t.Errorf("initialize %s\nwant %s", msgs[0].Params, want)
	}
	cwd, _ := json.Marshal(f.dir)
	want = `{"cwd":` + string(cwd) + `,"mcpServers":[{"name":"golauncher","command":"C:\\tools\\glctl.exe","args":["mcp"],"env":[{"name":"A_VAR","value":"1"},{"name":"B_VAR","value":"2"},{"name":"GOLAUNCHER_AGENT_SESSION","value":"chat-1"}]}]}`
	if msgs[1].Method != "session/new" || string(msgs[1].Params) != want {
		t.Errorf("session/new %s\nwant %s", msgs[1].Params, want)
	}
	var p1, p2 promptParams
	json.Unmarshal(msgs[2].Params, &p1)
	json.Unmarshal(msgs[3].Params, &p2)
	if len(p1.Prompt) != 2 || p1.Prompt[0].Text != "Be brief." || p1.Prompt[1].Text != "hello" || len(p2.Prompt) != 1 || p2.Prompt[0].Text != "again" {
		t.Errorf("prompts %+v %+v", p1, p2)
	}
	session := p1.SessionID
	if !strings.HasPrefix(session, "sess-") || p2.SessionID != session {
		t.Errorf("sessions %q %q", p1.SessionID, p2.SessionID)
	}

	// Released, the next process loads the session; its history is dropped.
	c.Release()
	r = send(t, c, "third", nil)
	if r.text != "echo: third" || len(r.steps) != 0 || len(r.notices) != 0 || r.final.Usage.Model != "fake-model-loaded" {
		t.Fatalf("after load: %q steps %v notices %v usage %+v", r.text, r.steps, r.notices, r.final.Usage)
	}
	m := methods(f.log)
	if count(m, "initialize") != 2 || count(m, "session/load") != 1 || count(m, "session/new") != 1 {
		t.Fatalf("methods %q", m)
	}
	msgs = received(f.log)
	for _, msg := range msgs {
		if msg.Method == "session/load" && !strings.Contains(string(msg.Params), `"sessionId":"`+session+`"`) {
			t.Errorf("session/load %s", msg.Params)
		}
	}
	var p3 promptParams
	json.Unmarshal(msgs[len(msgs)-1].Params, &p3)
	if len(p3.Prompt) != 1 {
		t.Errorf("a loaded session got the system prompt again: %+v", p3)
	}

	// A deleted chat's session is forgotten: the next process starts anew.
	c.Close()
	f.r.Forget("chat-1")
	c2 := f.open(t, backend.OpenOptions{Resume: true})
	if r := send(t, c2, "new", nil); r.text != "echo: new" || len(r.notices) != 0 {
		t.Fatalf("%q %v", r.text, r.notices)
	}
	if m := methods(f.log); count(m, "session/new") != 2 || count(m, "session/load") != 1 {
		t.Fatalf("methods %q", m)
	}
}

// TestResumeLost: an agent without loadSession, and a load that fails, start
// a new session and tell the user.
func TestResumeLost(t *testing.T) {
	for _, env := range []string{"ACP_FAKE_NOLOAD=1", "ACP_FAKE_LOADFAIL=1"} {
		t.Run(env, func(t *testing.T) {
			f := newFixture(t, nil, env)
			c := f.open(t, backend.OpenOptions{SystemPrompt: "Be brief."})
			send(t, c, "one", nil)
			c.Release()
			r := send(t, c, "two", nil)
			if r.text != "echo: two" || !slices.Equal(r.notices, []string{backend.NoticeNewSession}) {
				t.Fatalf("%q notices %v", r.text, r.notices)
			}
			m := methods(f.log)
			if count(m, "session/new") != 2 || count(m, "session/load") != map[bool]int{true: 0, false: 1}[env == "ACP_FAKE_NOLOAD=1"] {
				t.Fatalf("methods %q", m)
			}
			// The new session gets the system prompt again.
			msgs := received(f.log)
			var p promptParams
			json.Unmarshal(msgs[len(msgs)-1].Params, &p)
			if len(p.Prompt) != 2 || p.Prompt[0].Text != "Be brief." {
				t.Errorf("prompt %+v", p)
			}
		})
	}
}

// TestTools: tool calls become steps: an MCP call titled with its JSON
// arguments, and a failed web search.
func TestTools(t *testing.T) {
	f := newFixture(t, nil)
	c := f.open(t, backend.OpenOptions{})
	r := send(t, c, "tool", nil)
	want := []backend.Event{
		{Kind: backend.EventTool, ToolID: "t1", Tool: MCPTool, Input: `{"query":"Visual Studio Code"}`},
		{Kind: backend.EventToolResult, ToolID: "t1", Result: `{"items":[{"handle":"i1"}]}`},
	}
	if r.text != "1" || !slices.EqualFunc(r.steps, want, sameEvent) {
		t.Fatalf("%q steps %+v", r.text, r.steps)
	}
	r = send(t, c, "web", nil)
	want = []backend.Event{
		{Kind: backend.EventTool, ToolID: "w1", Tool: "WebSearch", Input: `{"query":"weather"}`},
		{Kind: backend.EventToolResult, ToolID: "w1", Result: "no network", IsError: true},
	}
	if r.text != "done" || !slices.EqualFunc(r.steps, want, sameEvent) {
		t.Fatalf("%q steps %+v", r.text, r.steps)
	}
}

func sameEvent(a, b backend.Event) bool {
	return a.Kind == b.Kind && a.ToolID == b.ToolID && a.Tool == b.Tool && a.Input == b.Input && a.Result == b.Result && a.IsError == b.IsError
}

// TestPermission: a permission request, allowed, rejected, timed out, and
// pending when the user stops the answer.
func TestPermission(t *testing.T) {
	f := newFixture(t, nil)
	c := f.open(t, backend.OpenOptions{})
	r := send(t, c, "perm", func(p *backend.PermissionRequest) { p.Respond(p.Option("allow_once")) })
	if r.text != "outcome: selected proceed_once" || len(r.perms) != 1 {
		t.Fatalf("allow: %q", r.text)
	}
	p := r.perms[0]
	if p.ToolID != "p1" || p.Tool != "WebFetch" || p.Input != `{"url":"https://example.com/"}` || p.Kind != "fetch" ||
		p.Title != "Fetching content from: https://example.com/" || len(p.Options) != 3 || p.Options[0] != (backend.PermissionOption{ID: "proceed_always", Title: "Always Allow", Kind: "allow_always"}) {
		t.Errorf("request %+v", p)
	}
	if len(r.steps) != 2 || r.steps[0].Tool != "WebFetch" || r.steps[1].Kind != backend.EventToolResult || r.steps[1].Result != "page" {
		t.Errorf("steps %+v", r.steps)
	}
	// The response echoes the request's (string) ID.
	var resp string
	for _, m := range received(f.log) {
		if string(m.ID) == `"perm-1"` {
			resp = string(m.Result)
		}
	}
	if resp != `{"outcome":{"outcome":"selected","optionId":"proceed_once"}}` {
		t.Errorf("response %s", resp)
	}

	if r := send(t, c, "perm", nil); r.text != "outcome: selected cancel" {
		t.Fatalf("reject: %q", r.text)
	}

	old := agentrt.PermissionTimeout
	agentrt.PermissionTimeout = 200 * time.Millisecond
	defer func() { agentrt.PermissionTimeout = old }()
	if r := send(t, c, "perm", func(*backend.PermissionRequest) {}); r.text != "outcome: selected cancel" || r.final.Kind != backend.EventDone {
		t.Fatalf("timeout: %q %+v", r.text, r.final)
	}
	agentrt.PermissionTimeout = old

	// Stop while the request is pending: it is rejected before session/cancel.
	r = send(t, c, "perm", func(*backend.PermissionRequest) { c.Cancel() })
	if !r.final.Cancelled {
		t.Fatalf("stop: %q %+v", r.text, r.final)
	}
	msgs := received(f.log)
	last := msgs[len(msgs)-2:]
	if string(last[0].Result) != `{"outcome":{"outcome":"selected","optionId":"cancel"}}` || last[1].Method != "session/cancel" {
		t.Errorf("last messages %s %s %s", last[0].ID, last[0].Result, last[1].Method)
	}
	select {
	case <-r.perms[0].Done:
	default:
		t.Error("Done not closed")
	}
	// The process answers the next turn.
	if r := send(t, c, "after", nil); r.text != "echo: after" || count(methods(f.log), "initialize") != 1 {
		t.Fatalf("after stop: %q %q", r.text, methods(f.log))
	}
}

// TestCancelStreaming: stopping a streaming answer sends session/cancel; the
// turn ends cancelled and the process stays.
func TestCancelStreaming(t *testing.T) {
	f := newFixture(t, nil)
	c := f.open(t, backend.OpenOptions{})
	ch, err := c.Send(context.Background(), "slow")
	if err != nil {
		t.Fatal(err)
	}
	var final backend.Event
	for ev := range ch {
		if ev.Kind == backend.EventText {
			c.Cancel()
		}
		if ev.Kind == backend.EventDone || ev.Kind == backend.EventError {
			final = ev
		}
	}
	if final.Kind != backend.EventDone || !final.Cancelled {
		t.Fatalf("final %+v", final)
	}
	if m := methods(f.log); count(m, "session/cancel") != 1 {
		t.Fatalf("methods %q", m)
	}
	if r := send(t, c, "next", nil); r.text != "echo: next" || count(methods(f.log), "initialize") != 1 {
		t.Fatalf("%q", r.text)
	}
}

// TestRequestsRefused: a file system request is answered with -32601.
func TestRequestsRefused(t *testing.T) {
	f := newFixture(t, nil)
	c := f.open(t, backend.OpenOptions{})
	if r := send(t, c, "fs", nil); r.text != "fs error -32601" {
		t.Fatalf("%q", r.text)
	}
}

// TestCommandsGuarded: a message that looks like a slash command reaches the
// agent with a leading zero width space.
func TestCommandsGuarded(t *testing.T) {
	f := newFixture(t, nil)
	c := f.open(t, backend.OpenOptions{})
	send(t, c, " /memory add x", nil)
	msgs := received(f.log)
	var p promptParams
	json.Unmarshal(msgs[len(msgs)-1].Params, &p)
	if p.Prompt[0].Text != "\u200b /memory add x" {
		t.Errorf("prompt %q", p.Prompt[0].Text)
	}
	if looksLikeCommand("what is $PATH?") || !looksLikeCommand("\n$x") {
		t.Error("looksLikeCommand")
	}
}

// TestErrors: sign-in and quota errors keep their kinds; a crash fails the
// turn and the next turn loads the session in a new process; a garbage line
// restarts the process.
func TestErrors(t *testing.T) {
	for env, kind := range map[string]backend.ErrorKind{"ACP_FAKE_NEWERR=auth": backend.ErrNotLoggedIn, "ACP_FAKE_NEWERR=quota": backend.ErrQuota} {
		f := newFixture(t, nil, env)
		r := send(t, f.open(t, backend.OpenOptions{}), "hi", nil)
		if r.final.Kind != backend.EventError || backend.KindOf(r.final.Err) != kind {
			t.Errorf("%s: final %+v", env, r.final)
		}
	}

	f := newFixture(t, nil)
	c := f.open(t, backend.OpenOptions{})
	if r := send(t, c, "quota", nil); r.final.Kind != backend.EventError || backend.KindOf(r.final.Err) != backend.ErrQuota ||
		!strings.Contains(r.final.Err.Error(), "Rate limit exceeded") {
		t.Fatalf("quota: %+v", r.final)
	}
	r := send(t, c, "crash", nil)
	if r.final.Kind != backend.EventError || !strings.Contains(r.final.Err.Error(), "boom: fake crash") || r.text != "partial" {
		t.Fatalf("crash: %q %+v", r.text, r.final)
	}
	if r := send(t, c, "later", nil); r.text != "echo: later" {
		t.Fatalf("after crash: %q %+v", r.text, r.final)
	}
	if m := methods(f.log); count(m, "initialize") != 2 || count(m, "session/load") != 1 {
		t.Fatalf("methods %q", m)
	}
	if r := send(t, c, "garbage", nil); r.text != "echo: garbage" || r.final.Kind != backend.EventDone {
		t.Fatalf("garbage: %q %+v", r.text, r.final)
	}
	if m := methods(f.log); count(m, "initialize") != 3 {
		t.Fatalf("methods %q", m)
	}
}

// TestProtocolVersion: an agent of another protocol version is refused.
func TestProtocolVersion(t *testing.T) {
	f := newFixture(t, nil, "ACP_FAKE_PROTO=2")
	r := send(t, f.open(t, backend.OpenOptions{}), "hi", nil)
	if r.final.Kind != backend.EventError || !strings.Contains(r.final.Err.Error(), "version 2 of the Agent Client Protocol") {
		t.Fatalf("%+v", r.final)
	}
}

func TestDefaultTool(t *testing.T) {
	for _, tc := range []struct {
		c           ToolCall
		tool, input string
	}{
		{ToolCall{Title: `{"handle": "i5"}`, Kind: "other"}, MCPTool, `{"handle":"i5"}`},
		{ToolCall{Title: `Searching the web for: "a "b""`, Kind: "search"}, "WebSearch", `{"query":"a \"b\""}`},
		{ToolCall{Title: "Fetching content from: https://example.com/a", Kind: "fetch"}, "WebFetch", `{"url":"https://example.com/a"}`},
		{ToolCall{Title: `Processing URLs and instructions from prompt: "x"`, Kind: "fetch"}, "WebFetch", ""},
		{ToolCall{Title: "Read notes.txt", Kind: "read", RawInput: []byte(`{ "path": "notes.txt" }`)}, "Read notes.txt", `{"path":"notes.txt"}`},
		{ToolCall{Kind: "think"}, "think", ""},
		{ToolCall{Title: "{not json"}, "{not json", ""},
	} {
		if tool, input := DefaultTool(tc.c); tool != tc.tool || input != tc.input {
			t.Errorf("DefaultTool(%+v) = %q, %q; want %q, %q", tc.c, tool, input, tc.tool, tc.input)
		}
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		e    rpcError
		kind backend.ErrorKind
	}{
		{rpcError{Code: -32000, Message: "Authentication required."}, backend.ErrNotLoggedIn},
		{rpcError{Code: -32000, Message: "Gemini API key is missing or not configured."}, backend.ErrNotLoggedIn},
		{rpcError{Code: 429, Message: "Rate limit exceeded. Try again later."}, backend.ErrQuota},
		{rpcError{Code: 500, Message: "You have exhausted your daily quota."}, backend.ErrQuota},
		{rpcError{Code: -32603, Message: "Internal error"}, backend.ErrFailed},
		{rpcError{Code: -32000, Message: "This client is no longer supported for this service. Please migrate."}, backend.ErrFailed},
	} {
		if k := classifyRPC(&tc.e); k != tc.kind {
			t.Errorf("classifyRPC(%+v) = %s, want %s", tc.e, k, tc.kind)
		}
	}
	if o := NewAdapter(testConfig()).ExitOutcome("Error: Please set an Auth method... not logged in"); backend.KindOf(o.Err) != backend.ErrNotLoggedIn {
		t.Errorf("ExitOutcome %+v", o)
	}
}

func TestCustom(t *testing.T) {
	cfg := Custom("My Agent", `C:\agents\agent.exe`, []string{"--acp", "--quiet"})
	a := NewAdapter(cfg)
	info := a.Info()
	if !info.NoPerTurn || !info.AssignsSessionID || info.WorkDir != "acp-My_Agent" || !slices.Equal(info.InstallPaths, []string{`C:\agents\agent.exe`}) ||
		info.ParseVersion("no version") != "0.0.0" || info.ParseVersion("agent 2.3.4") != "2.3.4" {
		t.Errorf("info %+v", info)
	}
	cmd, err := a.Command(agentrt.Launch{Path: `C:\agents\agent.exe`})
	if err != nil || !slices.Equal(cmd.Argv, []string{`C:\agents\agent.exe`, "--acp", "--quiet"}) {
		t.Errorf("command %q %v", cmd.Argv, err)
	}
	if info := NewAdapter(Custom("x", "agent", nil)).Info(); !slices.Equal(info.Commands, []string{"agent"}) {
		t.Errorf("info %+v", info)
	}
}
