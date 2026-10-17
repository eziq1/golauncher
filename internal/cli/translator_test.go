package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// The translator (docs/translator.md) in the fake environment: its engines
// are engines.Fake, which answer "[to] text" at once and never go online.

func TestGoldenTranslator(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"query_translate", []string{"query", "tr hello world"}},
		{"query_translate_lang", []string{"query", "tr en>ja good morning"}},
		{"zh_query_translate", []string{"query", "翻译 你好", "--locale", "zh-CN"}},
		{"run_translate", []string{"run", "builtin.translator/translate", "--text", ">de thank you"}},
		{"run_translate_history_off", []string{"run", "builtin.translator/history"}}, // off by default
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, code := glctl(t, tc.args...)
			if code != 0 {
				t.Fatalf("exit code %d", code)
			}
			golden(t, tc.name, out)
		})
	}
}

// TestSessionTranslateWithAI: "Translate with AI" (plugin.ActionAskAI) is
// handed to builtin.ai, whose chat first asks to turn AI on (off in a new
// session); once on, the instruction and the quoted text are sent.
func TestSessionTranslateWithAI(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s)
	glctl(t, "session", "type", "-s", s, "tr >ja good morning")
	out, _ := glctl(t, "session", "action", "-s", s, "builtin.translator/result:google", "ai")
	golden(t, "session_translate_ai", out)
	if !strings.Contains(out, `"plugin": "builtin.ai"`) || !strings.Contains(out, "Turn On AI") {
		t.Fatalf("no AI prompt:\n%s", out)
	}
	out, _ = glctl(t, "session", "key", "-s", s, "Enter")
	for _, want := range []string{"Translate the quoted text into Japanese.", "> good morning", "You asked: Translate the quoted text"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}

// TestSessionTranslate copies a translation with Enter; it is not recorded
// as recently used (the text is not stored).
func TestSessionTranslate(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s)
	glctl(t, "session", "type", "-s", s, "tr good morning")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_translate_copy", out)
	if !strings.Contains(out, "[zh-CN] good morning") {
		t.Errorf("no copied translation in:\n%s", out)
	}
	out, _ = glctl(t, "session", "type", "-s", s, "")
	if strings.Contains(out, "good morning") {
		t.Errorf("the translation was recorded:\n%s", out)
	}
}
