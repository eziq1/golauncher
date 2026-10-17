package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// The AI chat answers from a fake backend in glctl ("You asked: …"); AI is
// off as for a new user until the first-use prompt turns it on.

// TestSessionAIChat opens a chat with Tab, turns AI on from the prompt, asks
// a follow-up and finds the chat in the history.
func TestSessionAIChat(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s)
	glctl(t, "session", "type", "-s", s, "why is the sky blue")
	out, _ := glctl(t, "session", "key", "-s", s, "Tab")
	golden(t, "session_ai_off", out)
	out, _ = glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_ai_chat", out)
	glctl(t, "session", "type", "-s", s, "and at sunset?")
	out, _ = glctl(t, "session", "key", "-s", s, "Enter", "--format", "text")
	for _, want := range []string{"| You asked: why is the sky blue", "| You asked: and at sunset?", `text: ""`} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	// Esc goes back (no answer is running); the question now offers Ask AI.
	glctl(t, "session", "key", "-s", s, "Escape")
	out, _ = glctl(t, "session", "type", "-s", s, "zzqx ask something")
	golden(t, "session_ai_fallback", out)
	glctl(t, "session", "type", "-s", s, "ai chat history")
	out, _ = glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_ai_history", out)
}

// TestSessionAIChatChinese shows the first-use prompt in Chinese.
func TestSessionAIChatChinese(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--locale", "zh-CN")
	glctl(t, "session", "type", "-s", s, "天空为什么是蓝色的")
	out, _ := glctl(t, "session", "key", "-s", s, "Tab")
	golden(t, "zh_session_ai_off", out)
	if !strings.Contains(out, "打开 AI") {
		t.Errorf("not in Chinese:\n%s", out)
	}
}

// TestSessionAIChatKept hides the launcher in a chat and shows it again: the
// chat comes back with the footer's hint, then with a draft (no hint over a draft; docs/keep-view.md);
// shown as by the selected-text hotkey, the root search instead.
func TestSessionAIChatKept(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s)
	glctl(t, "session", "type", "-s", s, "why is the sky blue")
	glctl(t, "session", "key", "-s", s, "Tab")
	glctl(t, "session", "key", "-s", s, "Enter") // turns AI on and asks
	glctl(t, "session", "hide", "-s", s)
	out, _ := glctl(t, "session", "reopen", "-s", s)
	golden(t, "session_ai_kept", out)
	glctl(t, "session", "type", "-s", s, "a draft")
	glctl(t, "session", "hide", "-s", s)
	out, _ = glctl(t, "session", "reopen", "-s", s, "--format", "text")
	for _, want := range []string{"| You asked: why is the sky blue", `text: "a draft"`} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	glctl(t, "session", "hide", "-s", s)
	out, _ = glctl(t, "session", "reopen", "-s", s, "root", "--format", "text")
	if strings.Contains(out, "You asked") || !strings.Contains(out, "(depth 0)") {
		t.Errorf("reopen root:\n%s", out)
	}
}

// TestAIBackends lists the fake environment's made-up installations of the
// backends' programs (R84), as JSON and as text.
func TestAIBackends(t *testing.T) {
	out, _ := glctl(t, "ai", "backends")
	golden(t, "ai_backends", out)
	out, _ = glctl(t, "ai", "backends", "--format", "text")
	golden(t, "ai_backends_text", out)
	for _, want := range []string{"Codex (ChatGPT) (codex): available", "launcher script, not used", "too old (needs 0.130.0)", "Gemini CLI (gemini): notSignedIn"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if _, code := glctl(t, "ai", "nope"); code != 2 {
		t.Errorf("exit code %d for a wrong subcommand", code)
	}
}
