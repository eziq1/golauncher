package agentrt

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// The test binary is also the fake CLI of these tests: with AGENTRT_FAKE=1
// it speaks the line protocol of testAdapter instead of running the tests.
func TestMain(m *testing.M) {
	if os.Getenv("AGENTRT_FAKE") == "1" {
		fakeCLI()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeCLI is a CLI with a line protocol that assigns its own session IDs:
//
//	--version                  prints "fake 1.2.3"
//	persist [resume=<id>]      HELLO → SESSION <id>; MSG <text> → TEXT echo: <text>, END;
//	                           MSG perm → PERM p1, waits for ALLOW|DENY, TEXT <answer>, END
//	turn [resume=<id>]         reads the prompt from stdin: SESSION <id>, TEXT echo: <prompt>, END
//
// It appends its argv to $AGENTRT_FAKE_LOG.
func fakeCLI() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--version" {
		fmt.Println("fake 1.2.3")
		return
	}
	if f, err := os.OpenFile(os.Getenv("AGENTRT_FAKE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(f, strings.Join(args, " "))
		f.Close()
	}
	id := fmt.Sprintf("s-%d", os.Getpid())
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "resume="); ok {
			id = v
		}
	}
	if len(args) > 0 && args[0] == "turn" {
		in, _ := io.ReadAll(os.Stdin)
		fmt.Printf("SESSION %s\nTEXT echo: %s\nEND\n", id, in)
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "HELLO":
			fmt.Println("SESSION " + id)
		case line == "MSG perm":
			fmt.Println("PERM p1")
			if !sc.Scan() {
				return
			}
			fmt.Printf("TEXT %s\nEND\n", strings.ToLower(sc.Text()))
		case strings.HasPrefix(line, "MSG "):
			fmt.Printf("TEXT echo: %s\nEND\n", strings.TrimPrefix(line, "MSG "))
		}
	}
}

// testAdapter drives fakeCLI.
type testAdapter struct {
	perTurnOnly bool
}

func (a testAdapter) Info() Info {
	return Info{ID: "fake", Name: "Fake CLI", VersionArgs: []string{"--version"}, MinVersion: "1.0.0", TestedMajor: 1,
		PerTurnOnly: a.perTurnOnly, NoPerTurn: !a.perTurnOnly, AssignsSessionID: true, EnvDrop: []string{"AGENTRT_DROPPED"}}
}

func (testAdapter) Command(l Launch) (Command, error) {
	argv := []string{l.Path, "persist"}
	if !l.Persistent {
		argv[1] = "turn"
	}
	if l.Resume {
		argv = append(argv, "resume="+l.SessionID)
	}
	return Command{Argv: argv}, nil
}

func (testAdapter) MCPConfig(MCPServer) ([]byte, error) { return nil, nil }
func (testAdapter) NewCodec(Launch) Codec               { return &testCodec{} }
func (testAdapter) ExitOutcome(text string) Outcome {
	return Outcome{Kind: Failed, Err: &backend.Error{Kind: backend.ErrFailed, Message: text}}
}

type testCodec struct{}

func (*testCodec) Message(text string) ([]byte, error) { return []byte("MSG " + text + "\n"), nil }
func (*testCodec) Prompt(text string) []byte           { return []byte(text) }
func (*testCodec) Interrupt() []byte                   { return nil }
func (*testCodec) NewDecoder(io TurnIO) Decoder        { return &testDecoder{io: io} }

// Handshake implements Handshaker.
func (*testCodec) Handshake(ctx context.Context, c Conn) (string, error) {
	if err := c.Write([]byte("HELLO\n")); err != nil {
		return "", err
	}
	line, err := c.Read(ctx)
	if err != nil {
		return "", err
	}
	id, ok := strings.CutPrefix(string(line), "SESSION ")
	if !ok {
		return "", fmt.Errorf("handshake: %q", line)
	}
	return id, nil
}

type testDecoder struct {
	io TurnIO
	pr Progress
}

func (d *testDecoder) Line(line []byte) (end, garbage bool) {
	s := string(bytes.TrimSpace(line))
	d.pr.Output = true
	switch {
	case strings.HasPrefix(s, "SESSION "):
		d.pr.SessionID, d.pr.Started = strings.TrimPrefix(s, "SESSION "), true
	case strings.HasPrefix(s, "TEXT "):
		d.pr.Emitted = true
		d.io.Emit(backend.Event{Kind: backend.EventText, Text: strings.TrimPrefix(s, "TEXT ")})
	case strings.HasPrefix(s, "PERM "):
		var once sync.Once
		d.io.Emit(backend.Event{Kind: backend.EventPermission, Permission: &backend.PermissionRequest{
			ToolID: strings.TrimPrefix(s, "PERM "), Tool: "fake_tool",
			Options: []backend.PermissionOption{{ID: "allow", Title: "Allow", Kind: "allow_once"}},
			Respond: func(id string) {
				once.Do(func() {
					answer := "DENY"
					if id == "allow" {
						answer = "ALLOW"
					}
					_ = d.io.Reply([]byte(answer + "\n"))
				})
			},
		}})
	case s == "END":
		return true, false
	default:
		return false, true
	}
	return false, false
}

func (d *testDecoder) Outcome(cancelled bool) Outcome {
	if cancelled {
		return Outcome{Kind: Cancelled}
	}
	return Outcome{Kind: Done, Usage: &backend.Usage{Model: "fake"}}
}

func (d *testDecoder) Progress() Progress { return d.pr }

// newTest returns a runtime of a on the fake CLI, and the fake's log.
func newTest(t *testing.T, a Adapter, dir string) (*Runtime, string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("Windows only")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log.txt")
	r := New(a, Options{Path: exe, Dir: filepath.Join(dir, "work"), Env: []string{"AGENTRT_FAKE=1", "AGENTRT_FAKE_LOG=" + log}})
	t.Cleanup(func() { r.Close() })
	return r, log
}

func starts(t *testing.T, log string) []string {
	t.Helper()
	b, _ := os.ReadFile(log)
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(b)), " ", "_"))
}

// collect reads a turn, answering permission requests with answer.
func collect(t *testing.T, ch <-chan backend.Event, answer string) (string, backend.Event, int) {
	t.Helper()
	var text strings.Builder
	perms := 0
	timeout := time.After(20 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("closed without a final event")
			}
			switch ev.Kind {
			case backend.EventText:
				text.WriteString(ev.Text)
			case backend.EventPermission:
				perms++
				ev.Permission.Respond(answer)
			case backend.EventDone, backend.EventError:
				return text.String(), ev, perms
			}
		case <-timeout:
			t.Fatal("turn did not end")
		}
	}
}

func send(t *testing.T, c backend.Conversation, text, answer string) (string, backend.Event, int) {
	t.Helper()
	ch, err := c.Send(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, ch, answer)
}

// TestAssignedSessionPerTurn: a per-turn CLI that assigns session IDs; the
// runtime maps the chat to it, also across launcher restarts.
func TestAssignedSessionPerTurn(t *testing.T) {
	dir := t.TempDir()
	r, log := newTest(t, testAdapter{perTurnOnly: true}, dir)
	if st, err := r.Check(context.Background()); err != nil || st.Version != "1.2.3" || st.Untested {
		t.Fatalf("Check = %+v, %v", st, err)
	}
	c, err := r.Open(context.Background(), backend.OpenOptions{SessionID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	if text, final, _ := send(t, c, "hi", ""); text != "echo: hi" || final.Kind != backend.EventDone {
		t.Fatalf("%q %+v", text, final)
	}
	if text, _, _ := send(t, c, "again", ""); text != "echo: again" {
		t.Fatalf("%q", text)
	}
	s := starts(t, log)
	if len(s) != 2 || s[0] != "turn" || !strings.HasPrefix(s[1], "turn_resume=s-") {
		t.Fatalf("starts %q", s)
	}
	assigned := strings.TrimPrefix(s[1], "turn_resume=")
	if r.LiveCount() != 0 {
		t.Error("a per-turn CLI has a live process")
	}

	// After a restart of the launcher, the chat resumes the same session.
	r.Close()
	r2, _ := newTest(t, testAdapter{perTurnOnly: true}, dir)
	c2, _ := r2.Open(context.Background(), backend.OpenOptions{SessionID: "chat-1", Resume: true})
	send(t, c2, "later", "")
	if s := starts(t, log); len(s) != 3 || s[2] != "turn_resume="+assigned {
		t.Fatalf("starts %q, want resume=%s", s, assigned)
	}
	// An unknown chat starts a new session even when asked to resume.
	c3, _ := r2.Open(context.Background(), backend.OpenOptions{SessionID: "chat-2", Resume: true})
	send(t, c3, "new", "")
	if s := starts(t, log); len(s) != 4 || s[3] != "turn" {
		t.Fatalf("starts %q", s)
	}
}

// TestHandshakeAndPermission: a persistent CLI with a handshake, which asks
// for a permission that the reader answers through the event.
func TestHandshakeAndPermission(t *testing.T) {
	r, log := newTest(t, testAdapter{}, t.TempDir())
	c, err := r.Open(context.Background(), backend.OpenOptions{SessionID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	if text, _, _ := send(t, c, "one", ""); text != "echo: one" {
		t.Fatalf("%q", text)
	}
	if text, final, perms := send(t, c, "perm", "allow"); text != "allow" || perms != 1 || final.Kind != backend.EventDone {
		t.Fatalf("%q %+v %d", text, final, perms)
	}
	if text, _, _ := send(t, c, "perm", ""); text != "deny" {
		t.Fatalf("%q", text)
	}
	if n := len(starts(t, log)); n != 1 {
		t.Errorf("%d processes", n)
	}
	// Released, the next process resumes the session the handshake reported.
	c.Release()
	send(t, c, "two", "")
	s := starts(t, log)
	if len(s) != 2 || s[0] != "persist" || !strings.HasPrefix(s[1], "persist_resume=s-") {
		t.Fatalf("starts %q", s)
	}
}

func TestEnvDrop(t *testing.T) {
	t.Setenv("AGENTRT_DROPPED", "1")
	r := New(testAdapter{}, Options{Env: []string{"EXTRA=1"}})
	env := r.Env()
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(strings.ToUpper(kv), "AGENTRT_DROPPED=") }) || !slices.Contains(env, "EXTRA=1") {
		t.Errorf("env %q", env)
	}
}

func TestVersions(t *testing.T) {
	for in, want := range map[string]string{"2.1.296 (Claude Code)": "2.1.296", "v3.0.0": "3.0.0", "codex-cli 0.46.0": "0.46.0", "x 2.10.1-beta": "", "nonsense": ""} {
		if got := ParseVersion(in); got != want {
			t.Errorf("ParseVersion(%q) = %q, want %q", in, got, want)
		}
	}
	if CompareVersions("2.1.0", "2.0.99") <= 0 || CompareVersions("2.10.0", "2.9.0") <= 0 || CompareVersions("2.1.0", "2.1.0") != 0 || MajorOf("3.0.1") != 3 {
		t.Error("CompareVersions")
	}
}

func TestSessionMap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "sessions.json")
	m := NewSessionMap(path)
	if m.Get("a") != "" {
		t.Error("empty map")
	}
	if err := m.Set("a", "x"); err != nil {
		t.Fatal(err)
	}
	m.Set("b", "y")
	m.Delete("b")
	m2 := NewSessionMap(path)
	if m2.Get("a") != "x" || m2.Get("b") != "" {
		t.Errorf("reloaded: a=%q b=%q", m2.Get("a"), m2.Get("b"))
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Errorf("file %v", err)
	}
}

func TestToolServer(t *testing.T) {
	ts := &ToolServer{Command: "g.exe", Env: map[string]string{"T": "1"}, SessionEnv: "S"}
	s := ts.server("id")
	if s.Name != ToolServerName || s.Args == nil || len(s.Args) != 0 || s.Env["T"] != "1" || s.Env["S"] != "id" || len(ts.Env) != 1 {
		t.Errorf("server %+v (tool server env %v)", s, ts.Env)
	}
	if got := MCPConfigName(`../x\y`); got != "mcp-___x_y.json" {
		t.Errorf("MCPConfigName = %q", got)
	}
}
