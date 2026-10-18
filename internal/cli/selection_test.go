package cli

import (
	"encoding/json/v2"
	"path/filepath"
	"slices"
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

// formalActions are AI actions as the settings store them (R103): a custom
// action in the result mode, and Explain turned off.
const formalActions = `{"builtin":{"explain":{"disabled":true}},"custom":[{"id":"custom-1","title":"Formal Email",` +
	`"prompt":"Rewrite as a formal email: {selection}","targets":"selection","result":"result","keywords":"mail","icon":"message-square"}]}`

// newActionSession starts a session with the selected text (editable), AI
// on and formalActions, and opens the custom action's result view.
func newActionSession(t *testing.T) (string, string) {
	t.Helper()
	s := filepath.Join(t.TempDir(), "s.json")
	out, code := glctl(t, "session", "new", "-s", s, "--selection", meetingText, "--selection-editable",
		"--pref", "builtin.ai:enabled=true", "--pref", "builtin.ai:actions="+formalActions)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	out, _ = glctl(t, "session", "show", "-s", s, "--format", "text")
	if !strings.Contains(out, "Formal Email") || strings.Contains(out, "Explain") {
		t.Fatalf("the rows do not follow the actions:\n%s", out)
	}
	glctl(t, "session", "type", "-s", s, "formal email")
	out, _ = glctl(t, "session", "key", "-s", s, "Enter")
	return s, out
}

// TestSessionSelectionActionResult: a custom AI action in the result mode
// shows only the answer; Enter replaces the selected text with it, Tab
// turns the view into the chat, Escape goes back.
func TestSessionSelectionActionResult(t *testing.T) {
	s, out := newActionSession(t)
	golden(t, "session_selection_action_result", out)
	for _, want := range []string{`"markdown": "You asked: Rewrite as a formal email: ` + meetingText, `"title": "Replace Selected Text"`, `"shortcut": "Tab"`} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	out, _ = glctl(t, "session", "key", "-s", s, "Enter", "--format", "text")
	if !strings.Contains(out, "paste") || !strings.Contains(out, "You asked: Rewrite as a formal email") {
		t.Errorf("Enter did not paste the answer:\n%s", out)
	}

	s, _ = newActionSession(t)
	out, _ = glctl(t, "session", "key", "-s", s, "Tab", "--format", "text")
	if !strings.Contains(out, "| **You**") || !strings.Contains(out, "builtin.ai/chat") {
		t.Errorf("Tab did not open the chat:\n%s", out)
	}

	s, _ = newActionSession(t)
	out, _ = glctl(t, "session", "key", "-s", s, "Escape", "--format", "text")
	if !strings.Contains(out, "(depth 0)") {
		t.Errorf("Escape did not go back:\n%s", out)
	}
}

// TestSelectionActionRows: the AI rows follow the user's order (R103), and
// with nothing typed the section keeps its row limit; typing finds an
// action beyond it by its keywords.
func TestSelectionActionRows(t *testing.T) {
	prefs := `{"order":["reply","custom-1","translate"],"custom":[` +
		`{"id":"custom-1","title":"Formal Email","prompt":"p"},{"id":"custom-2","title":"Haiku","prompt":"p","keywords":"poem"},` +
		`{"id":"custom-3","title":"Bullet Points","prompt":"p"},{"id":"custom-4","title":"Fix Grammar","prompt":"p"}]}`
	rows := func(text string) []string {
		t.Helper()
		out, code := glctl(t, "query", text, "--selection", meetingText, "--pref", "builtin.ai:enabled=true", "--pref", "builtin.ai:actions="+prefs)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, out)
		}
		var res struct {
			Sections []struct {
				Title string `json:"title"`
				Items []struct {
					Key string `json:"key"`
				} `json:"items"`
			} `json:"sections"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, s := range res.Sections {
			if s.Title == "Selected Text" {
				for _, it := range s.Items {
					keys = append(keys, it.Key)
				}
			}
		}
		return keys
	}
	keys := rows("")
	if len(keys) != 7 {
		t.Fatalf("%d rows, want the limit of 7: %q", len(keys), keys)
	}
	var ai []string
	for _, k := range keys {
		if id, ok := strings.CutPrefix(k, "builtin.ai/"); ok {
			ai = append(ai, id)
		}
	}
	if want := []string{"reply", "ask", "custom-1", "translate"}; !slices.Equal(ai[:min(len(ai), 4)], want) {
		t.Errorf("AI rows %q, want them to start with %q", ai, want)
	}
	if slices.Contains(ai, "custom-2") {
		t.Errorf("custom-2 is beyond the limit: %q", ai)
	}
	if keys := rows("poem"); !slices.Contains(keys, "builtin.ai/custom-2") {
		t.Errorf("typing a keyword: %q", keys)
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
