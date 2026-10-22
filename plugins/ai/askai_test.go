package ai

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// TestAskAI: plugin.ActionAskAI (routed here by the host) opens a chat with
// the instruction and the quoted text; with AI off the chat asks to turn it
// on first and sends nothing until then.
func TestAskAI(t *testing.T) {
	fake := &backend.Fake{}
	p, h := newTestPlugin(t, fake, false)
	r, err := p.AskAI(t.Context(), plugin.AskAI{Instruction: "Translate the quoted text into Japanese. Output only the translation.",
		Text: "good morning", Title: "Translate: good morning"})
	if err != nil || r.Push == nil || r.Push.ID != viewChat {
		t.Fatalf("result %+v %v", r, err)
	}
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: viewChat, Instance: "v1", Payload: r.Push.Payload})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Sends()) != 0 || v.Sections[0].Items[0].Actions[0].ID != "enable" {
		t.Fatalf("AI off: sends %q, view %+v", fake.Sends(), v.Sections[0].Items[0])
	}
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true")})
	p.View(t.Context(), plugin.ViewRequest{ID: viewChat, Instance: "v1", Payload: r.Push.Payload})
	sends := fake.Sends()
	if len(sends) != 1 || !strings.HasPrefix(sends[0], "Translate the quoted text into Japanese.") || !strings.Contains(sends[0], "> good morning") {
		t.Fatalf("sends %q", sends)
	}
	// Without text the instruction is the message; nothing at all opens
	// nothing.
	r, _ = p.AskAI(t.Context(), plugin.AskAI{Instruction: "What is a haiku?"})
	if r.Push == nil || !strings.Contains(string(r.Push.Payload), `"text":"What is a haiku?"`) {
		t.Errorf("instruction only %+v", r)
	}
	if r, _ := p.AskAI(t.Context(), plugin.AskAI{}); r.Push != nil {
		t.Errorf("empty %+v", r)
	}
	// Files alone (the Action Ring's Ask AI on a file, R123): a new chat
	// with them in its draft, nothing sent until the user asks.
	txt := writeFile(t, t.TempDir(), "notes.txt", []byte("hello\n"))
	r, _ = p.AskAI(t.Context(), plugin.AskAI{Files: []string{txt}})
	if r.Push == nil {
		t.Fatalf("files only %+v", r)
	}
	before := len(fake.Sends())
	v, err = p.View(t.Context(), plugin.ViewRequest{ID: viewChat, Instance: "v2", Payload: r.Push.Payload})
	if err != nil || len(fake.Sends()) != before || len(v.Chips) != 1 {
		t.Fatalf("files only: sends %q, chips %+v, %v", fake.Sends(), v.Chips, err)
	}
}
