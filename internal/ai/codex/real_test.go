package codex

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// realChatID returns a random chat ID.
func realChatID() string {
	var u [16]byte
	rand.Read(u[:])
	u[6], u[8] = u[6]&0x0f|0x40, u[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:])
}

// TestRealCLI chats through the installed Codex CLI, which uses the ChatGPT
// subscription, for two turns: the second one resumes the thread of the
// first. It only runs with GOLAUNCHER_AI_TESTS=1.
func TestRealCLI(t *testing.T) {
	if os.Getenv("GOLAUNCHER_AI_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_AI_TESTS=1 to chat with the real Codex CLI")
	}
	dir := t.TempDir()
	b := New(Options{Dir: dir})
	defer b.Close()
	st, err := b.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Codex %s (untested %v)", st.Version, st.Untested)
	chat := realChatID()
	c, err := b.Open(context.Background(), backend.OpenOptions{SessionID: chat, SystemPrompt: "Answer as briefly as possible."})
	if err != nil {
		t.Fatal(err)
	}
	a := send(t, c, "Remember the word PLUM. Reply with OK")
	if a.final.Kind != backend.EventDone || !strings.Contains(strings.ToUpper(a.text), "OK") {
		t.Fatalf("turn 1: answer %q, final %+v (err %v)", a.text, a.final, a.final.Err)
	}
	t.Logf("turn 1: %q, usage %+v", a.text, a.final.Usage)
	var m map[string]string
	if raw, err := os.ReadFile(filepath.Join(dir, "sessions.json")); err != nil || json.Unmarshal(raw, &m) != nil || m[chat] == "" {
		t.Fatalf("no thread recorded for the chat: %v %q", err, m)
	}
	a = send(t, c, "Reply with OK again, then the word I asked you to remember.")
	if a.final.Kind != backend.EventDone || !strings.Contains(strings.ToUpper(a.text), "PLUM") || len(a.notices) != 0 {
		t.Fatalf("turn 2: answer %q, notices %q, final %+v (err %v)", a.text, a.notices, a.final, a.final.Err)
	}
	t.Logf("turn 2: %q, usage %+v", a.text, a.final.Usage)
}

// TestRealCLIAttachments sends a red image and a text file to the installed
// Codex CLI, then a blue image on the resumed thread (--image after
// "resume"). It only runs with GOLAUNCHER_AI_TESTS=1.
func TestRealCLIAttachments(t *testing.T) {
	if os.Getenv("GOLAUNCHER_AI_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_AI_TESTS=1 to chat with the real Codex CLI")
	}
	dir := t.TempDir()
	red := writePNG(t, dir, "red.png", 16, 16, color.RGBA{R: 255, A: 255})
	blue := writePNG(t, dir, "blue.png", 16, 16, color.RGBA{B: 255, A: 255})
	txt := writeText(t, dir, "secret.txt", "The secret word is pineapple.")
	b := New(Options{Dir: filepath.Join(dir, "work")})
	defer b.Close()
	if _, err := b.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := b.Open(context.Background(), backend.OpenOptions{SessionID: realChatID(), SystemPrompt: "Answer as briefly as possible."})
	if err != nil {
		t.Fatal(err)
	}
	a := sendWith(t, c, "What color is the image, and what is the secret word in the attached file? Reply with two words.", red, txt)
	low := strings.ToLower(a.text)
	if a.final.Kind != backend.EventDone || !strings.Contains(low, "red") || !strings.Contains(low, "pineapple") {
		t.Fatalf("turn 1: answer %q, final %+v (err %v)", a.text, a.final, a.final.Err)
	}
	a = sendWith(t, c, "What color is this new image? Reply with one word.", blue)
	if a.final.Kind != backend.EventDone || !strings.Contains(strings.ToLower(a.text), "blue") || len(a.notices) != 0 {
		t.Fatalf("turn 2: answer %q, notices %q, final %+v (err %v)", a.text, a.notices, a.final, a.final.Err)
	}
}

// TestRealCLITools lets the real CLI use golauncher's tools: glctl mcp on
// the fake environment (every action is only recorded; confirmations are
// denied); only a read-only tool is asked for. It only runs with
// GOLAUNCHER_AI_TESTS=1.
func TestRealCLITools(t *testing.T) {
	if os.Getenv("GOLAUNCHER_AI_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_AI_TESTS=1 to chat with the real Codex CLI")
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
	c, err := b.Open(context.Background(), backend.OpenOptions{SessionID: realChatID(), LauncherTools: true,
		SystemPrompt: "Answer as briefly as possible."})
	if err != nil {
		t.Fatal(err)
	}
	a := send(t, c, "Use the golauncher search tool to search for Visual Studio Code, then reply with only the number of items it returned.")
	if a.final.Kind != backend.EventDone {
		t.Fatalf("final %+v (err %v), steps %+v", a.final, a.final.Err, a.steps)
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
