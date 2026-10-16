package claudecode

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// TestRealCLI chats through the installed Claude Code CLI, which uses the
// subscription. It only runs with GOLAUNCHER_AI_TESTS=1.
func TestRealCLI(t *testing.T) {
	if os.Getenv("GOLAUNCHER_AI_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_AI_TESTS=1 to chat with the real Claude Code CLI")
	}
	b := New(Options{Dir: t.TempDir()})
	defer b.Close()
	st, err := b.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Claude Code %s (untested %v)", st.Version, st.Untested)
	var u [16]byte
	rand.Read(u[:])
	u[6], u[8] = u[6]&0x0f|0x40, u[8]&0x3f|0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:])
	c, err := b.Open(context.Background(), backend.OpenOptions{SessionID: id, SystemPrompt: "Answer as briefly as possible."})
	if err != nil {
		t.Fatal(err)
	}
	for i, prompt := range []string{"Reply with OK", "Reply with OK again", "release"} {
		if prompt == "release" {
			c.Release()
			prompt = "Reply with OK once more"
		}
		a := send(t, c, prompt)
		if a.final.Kind != backend.EventDone || !strings.Contains(strings.ToUpper(a.text), "OK") {
			t.Fatalf("turn %d: answer %q, final %+v", i, a.text, a.final)
		}
		t.Logf("turn %d: %q, usage %+v", i, a.text, a.final.Usage)
	}
}

// TestRealCLITools lets the real CLI use golauncher's tools: glctl mcp on
// the fake environment (every action is only recorded; confirmations are
// denied), only read-only tools are asked for. It only runs with
// GOLAUNCHER_AI_TESTS=1.
func TestRealCLITools(t *testing.T) {
	if os.Getenv("GOLAUNCHER_AI_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_AI_TESTS=1 to chat with the real Claude Code CLI")
	}
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
	var u [16]byte
	rand.Read(u[:])
	u[6], u[8] = u[6]&0x0f|0x40, u[8]&0x3f|0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:])
	c, err := b.Open(context.Background(), backend.OpenOptions{SessionID: id, Model: "haiku", LauncherTools: true,
		SystemPrompt: "Answer as briefly as possible."})
	if err != nil {
		t.Fatal(err)
	}
	a := send(t, c, "Use the golauncher search tool to search for Visual Studio Code, then reply with only the number of items it returned.")
	if a.final.Kind != backend.EventDone {
		t.Fatalf("final %+v", a.final)
	}
	var sawInput, sawResult bool
	for _, st := range a.steps {
		if st.Kind == backend.EventTool && st.Tool == "mcp__golauncher__search" && strings.Contains(st.Input, "Visual Studio Code") {
			sawInput = true
		}
		if st.Kind == backend.EventToolResult && !st.IsError && strings.Contains(st.Result, `"handle"`) {
			sawResult = true
		}
	}
	if !sawInput || !sawResult {
		t.Fatalf("steps %+v", a.steps)
	}
	t.Logf("answer %q, %d steps", a.text, len(a.steps))
}
