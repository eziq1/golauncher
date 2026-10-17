package gemini

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/ai/acp"
	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/internal/winapi/proc"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// The tests in this file use the installed Gemini CLI and the user's Google
// sign-in. They only run with GOLAUNCHER_AI_TESTS=1; TestRealIsolation
// sends no prompt (no model quota), the others a few short ones.

func realOnly(t *testing.T) {
	t.Helper()
	if os.Getenv("GOLAUNCHER_AI_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_AI_TESTS=1 to use the real Gemini CLI")
	}
}

// turn is what a real turn delivered.
type turn struct {
	text    string
	final   backend.Event
	steps   []backend.Event
	perms   []*backend.PermissionRequest
	notices []string
	elapsed time.Duration
}

// send runs a turn; permission requests are rejected (and recorded).
func send(t *testing.T, c backend.Conversation, prompt string) turn {
	t.Helper()
	start := time.Now()
	ch, err := c.Send(context.Background(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	var r turn
	var b strings.Builder
	timeout := time.After(4 * time.Minute)
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
				t.Logf("permission request: tool %q kind %q title %q options %+v", ev.Permission.Tool, ev.Permission.Kind, ev.Permission.Title, ev.Permission.Options)
				ev.Permission.Respond("")
			case backend.EventDone, backend.EventError:
				r.text, r.final, r.elapsed = b.String(), ev, time.Since(start)
				return r
			}
		case <-timeout:
			t.Fatal("turn did not end")
		}
	}
}

// TestRealCLI: two turns on one process, then a third after Release (a new
// process loads the session): its answer must not contain the replayed
// history.
func TestRealCLI(t *testing.T) {
	realOnly(t)
	b := New(Options{Dir: t.TempDir()})
	defer b.Close()
	st, err := b.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Gemini CLI %s (untested %v)", st.Version, st.Untested)
	c, err := b.Open(context.Background(), backend.OpenOptions{SessionID: "real-test-chat", SystemPrompt: "Answer as briefly as possible."})
	if err != nil {
		t.Fatal(err)
	}
	words := []string{"ALPHA", "BRAVO", "CHARLIE"}
	for i, w := range words {
		if i == 2 {
			c.Release()
		}
		r := send(t, c, "Reply with exactly this word and nothing else: "+w)
		t.Logf("turn %d: %q in %v, usage %+v, notices %v, steps %d", i, r.text, r.elapsed, r.final.Usage, r.notices, len(r.steps))
		if r.final.Kind != backend.EventDone || !strings.Contains(strings.ToUpper(r.text), w) {
			t.Fatalf("turn %d: answer %q, final %+v", i, r.text, r.final)
		}
		for _, old := range words[:i] {
			if strings.Contains(strings.ToUpper(r.text), old) {
				t.Errorf("turn %d: the answer %q contains an earlier answer", i, r.text)
			}
		}
		if len(r.notices) != 0 {
			t.Errorf("turn %d: notices %v", i, r.notices)
		}
	}
}

// TestRealCLITools lets Gemini CLI use golauncher's tools: glctl mcp on the
// fake environment (every action is only recorded), asking for a read-only
// tool only.
func TestRealCLITools(t *testing.T) {
	realOnly(t)
	dir := t.TempDir()
	glctl := filepath.Join(dir, "glctl.exe")
	if out, err := exec.Command("go", "build", "-o", glctl, "github.com/eziq1/golauncher/cmd/glctl").CombinedOutput(); err != nil {
		t.Fatalf("building glctl: %v\n%s", err, out)
	}
	fixtures, _ := filepath.Abs("../../../testdata/fixtures")
	b := New(Options{Dir: filepath.Join(dir, "work"), Tools: &ToolServer{Command: glctl, Args: []string{"mcp", "--fixtures", fixtures}}})
	defer b.Close()
	if _, err := b.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := b.Open(context.Background(), backend.OpenOptions{SessionID: "real-test-tools", Model: "gemini-2.5-flash", LauncherTools: true,
		SystemPrompt: "Answer as briefly as possible."})
	if err != nil {
		t.Fatal(err)
	}
	r := send(t, c, "Use the golauncher search tool to search for Visual Studio Code, then reply with only the number of items it returned.")
	if r.final.Kind != backend.EventDone {
		t.Fatalf("final %+v", r.final)
	}
	var sawInput, sawResult bool
	for _, st := range r.steps {
		t.Logf("step %s %q %q input %q result %.120q error %v", st.Kind, st.ToolID, st.Tool, st.Input, st.Result, st.IsError)
		if st.Kind == backend.EventTool && st.Tool == "mcp__golauncher__search" && strings.Contains(st.Input, "Visual Studio Code") {
			sawInput = true
		}
		if st.Kind == backend.EventToolResult && !st.IsError && strings.Contains(st.Result, `"handle"`) {
			sawResult = true
		}
	}
	t.Logf("answer %q in %v, %d steps, %d permission requests", r.text, r.elapsed, len(r.steps), len(r.perms))
	if !sawInput || !sawResult {
		t.Fatalf("no search step with its result")
	}
	if len(r.perms) != 0 {
		t.Errorf("golauncher's tools needed permission")
	}
}

// syncBuffer is a strings.Builder safe for concurrent use.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// pipeConn is an agentrt.Conn over a process's pipes.
type pipeConn struct {
	w     io.Writer
	lines chan []byte
}

func (c *pipeConn) Write(b []byte) error { _, err := c.w.Write(b); return err }

func (c *pipeConn) Read(ctx context.Context) ([]byte, error) {
	select {
	case l, ok := <-c.lines:
		if !ok {
			return nil, io.EOF
		}
		return l, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestRealIsolation starts Gemini CLI as golauncher does, with a working
// directory whose workspace settings configure an MCP server and a tool
// discovery command that would leave marker files, and golauncher's tool
// server replaced by a command that leaves one too. After initialize and
// session/new (no prompt: no model quota) only golauncher's server may have
// been started.
func TestRealIsolation(t *testing.T) {
	realOnly(t)
	b := New(Options{})
	st, err := b.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := candidates(hostEnv())
	path := paths[0]
	t.Logf("Gemini CLI %s", st.Version)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".gemini"), 0o700)
	marker := func(name string) string { return filepath.Join(dir, name+".marker") }
	// The commands run in dir (the working directory) and leave name.marker.
	echo := func(name string) []string { return []string{"/c", "echo started>" + name + ".marker"} }
	ws := fmt.Sprintf(`{"mcpServers": {"canary": {"command": "cmd.exe", "args": [%q, %q]}},
		"tools": {"discoveryCommand": %q, "core": ["run_shell_command"]},
		"hooksConfig": {"enabled": true}}`, echo("workspace-mcp")[0], echo("workspace-mcp")[1], "cmd.exe /c echo []>discovery.marker")
	os.WriteFile(filepath.Join(dir, ".gemini", "settings.json"), []byte(ws), 0o600)

	l := agentrt.Launch{Path: path, Dir: dir, Persistent: true,
		Tools: &agentrt.MCPServer{Name: agentrt.ToolServerName, Command: "cmd.exe", Args: echo("golauncher"), Env: map[string]string{}}}
	a := acp.NewAdapter(config(hostEnv()))
	cmd, err := a.Command(l)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range cmd.Files {
		if os.Getenv("GEMINI_ISOLATION_API_KEY") == "1" {
			// Sign in with a dummy API key (never used: no prompt is sent),
			// for an account whose Google sign-in cannot start sessions.
			var s map[string]any
			json.Unmarshal(content, &s)
			s["security"].(map[string]any)["auth"] = map[string]any{"selectedType": "gemini-api-key"}
			content, _ = json.Marshal(s)
		}
		os.WriteFile(filepath.Join(dir, name), content, 0o600)
	}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(envDrop, func(d string) bool { return strings.EqualFold(k, d) })
	})
	env = proc.MergeEnv(env, cmd.Env)
	if os.Getenv("GEMINI_ISOLATION_API_KEY") == "1" {
		env = append(env, "GEMINI_API_KEY=dummy-key-for-the-isolation-test")
	}
	stdout, out := io.Pipe()
	var stderr syncBuffer
	p, err := proc.Start(proc.Options{Argv: cmd.Argv, Dir: dir, Env: env, Output: out, Stderr: &stderr, StdinPipe: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { p.Stop(); p.Wait(); out.Close() }()
	conn := &pipeConn{w: p.Stdin(), lines: make(chan []byte, 100)}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(nil, 1<<24)
		for sc.Scan() {
			conn.lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(conn.lines)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	codec := a.NewCodec(l)
	id, err := codec.(agentrt.Handshaker).Handshake(ctx, conn)
	if err != nil {
		t.Fatalf("handshake: %v\nstderr: %s", err, stderr.String())
	}
	t.Logf("session started in %v (ID of %d characters)", time.Since(start), len(id))
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker("golauncher")); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(2 * time.Second)
	for name, want := range map[string]bool{"golauncher": true, "workspace-mcp": false, "discovery": false} {
		_, err := os.Stat(marker(name))
		if got := err == nil; got != want {
			t.Errorf("%s started: %v, want %v", name, got, want)
		}
	}
}
