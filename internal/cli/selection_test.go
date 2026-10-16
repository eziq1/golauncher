package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// The selected text is injected with --selection (docs/selected-text.md
// §9.2); nothing is read from any window.

const meetingText = "Let's move the meeting to Thursday afternoon, the room is booked."

// TestGoldenSelection: the "Selected Text" section of the empty root, for
// plain text (AI off: its hint row), a URL, an expression, typed text, with
// a context, and in Chinese.
func TestGoldenSelection(t *testing.T) {
	dir, norm := contextDir(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"query_selection", []string{"query", "", "--selection", meetingText}},
		{"query_selection_url", []string{"query", "", "--selection", " https://example.com/docs?id=7 "}},
		{"query_selection_calc", []string{"query", "", "--selection", "10 km in mi", "--selection-editable"}},
		{"query_selection_typed", []string{"query", "translate", "--selection", meetingText}},
		{"zh_query_selection", []string{"query", "", "--locale", "zh-CN", "--selection", "让我们把会议改到星期四下午。", "--selection-app", "Weixin.exe", "--selection-source", "clipboard", "--selection-truncated"}},
	} {
		out, code := glctl(t, tc.args...)
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s", tc.name, code, out)
		}
		golden(t, tc.name, out)
	}
	out, code := glctl(t, "query", "", "--selection", meetingText, "--context-folder", dir)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	golden(t, "query_selection_context", norm(out))
	sel, ctx := strings.Index(out, `"Selected Text"`), strings.Index(out, `"In `)
	if sel < 0 || ctx < 0 || sel > ctx {
		t.Errorf("want the selection section above the context's:\n%s", out)
	}
}

// TestSessionSelectionAI turns AI on from the hint row (back to the root),
// then translates the selection: the message holds the prompt and the
// quoted text, and the fake backend answers it.
func TestSessionSelectionAI(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--selection", meetingText, "--selection-editable")
	glctl(t, "session", "key", "-s", s, "Down") // the AI hint, below the preview
	out, _ := glctl(t, "session", "key", "-s", s, "Enter", "--format", "text")
	if !strings.Contains(out, "Turn On AI") && !strings.Contains(out, "Enable") {
		t.Fatalf("no AI prompt:\n%s", out)
	}
	out, _ = glctl(t, "session", "key", "-s", s, "Enter", "--format", "text")
	if !strings.Contains(out, "Selected Text") {
		t.Fatalf("not back at the root with the selection:\n%s", out)
	}
	glctl(t, "session", "type", "-s", s, "simplified chinese")
	out, _ = glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_selection_ai", out)
	for _, want := range []string{"Translate the quoted text into", "> " + meetingText, "You asked: Translate the quoted text"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Replace Selected Text") {
		t.Errorf("an editable selection: no Replace Selected Text in:\n%s", out)
	}
}

// TestSessionSelectionInstruction: text typed with a selection offers "AI
// handles the selection: <text>", which sends the text as the instruction.
func TestSessionSelectionInstruction(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--selection", meetingText)
	glctl(t, "session", "key", "-s", s, "Down")
	glctl(t, "session", "key", "-s", s, "Enter")
	glctl(t, "session", "key", "-s", s, "Enter") // AI on, back at the root
	out, _ := glctl(t, "session", "type", "-s", s, "make it formal")
	golden(t, "session_selection_instruction", out)
	if !strings.Contains(out, "AI handles the selection: make it formal") {
		t.Fatalf("no instruction row:\n%s", out)
	}
}
