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
