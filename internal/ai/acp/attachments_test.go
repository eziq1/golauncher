package acp

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// tinyPNG is a 1×1 PNG.
var tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\xa6\x8d\xa9\x1c\x00\x00\x00\x00IEND\xaeB`\x82")

// attachments writes an image and a text file and returns them.
func attachments(t *testing.T) (img, txt backend.Attachment) {
	t.Helper()
	dir := t.TempDir()
	img = backend.Attachment{Kind: backend.AttachImage, Name: "pic.png", Path: filepath.Join(dir, "1.png"), MediaType: "image/png", Size: int64(len(tinyPNG))}
	txt = backend.Attachment{Kind: backend.AttachText, Name: "notes.md", Path: filepath.Join(dir, "2.txt"), MediaType: "text/plain", Size: 7}
	if err := os.WriteFile(img.Path, tinyPNG, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(txt.Path, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	return img, txt
}

func sendWith(t *testing.T, c backend.Conversation, text string, atts ...backend.Attachment) turnResult {
	t.Helper()
	ch, err := c.Send(context.Background(), text, atts...)
	if err != nil {
		t.Fatal(err)
	}
	var r turnResult
	var b strings.Builder
	for ev := range ch {
		switch ev.Kind {
		case backend.EventText:
			b.WriteString(ev.Text)
		case backend.EventDone, backend.EventError:
			r.text, r.final = b.String(), ev
		}
	}
	return r
}

// lastPrompt is the params of the last session/prompt the fake read, raw.
func lastPrompt(t *testing.T, log string) string {
	t.Helper()
	msgs := received(log)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Method == "session/prompt" {
			return string(msgs[i].Params)
		}
	}
	t.Fatal("no session/prompt")
	return ""
}

// TestAttachments: the exact blocks of a prompt with an image and a text
// file (after the system prompt, before the text), and an unchanged prompt
// without attachments.
func TestAttachments(t *testing.T) {
	f := newFixture(t, nil)
	img, txt := attachments(t)
	c := f.open(t, backend.OpenOptions{SystemPrompt: "Be brief."})
	r := sendWith(t, c, "/look", img, txt)
	if r.final.Kind != backend.EventDone || r.text != "echo: /look [images: 1: image/png "+itoa(len(tinyPNG))+"]" {
		t.Fatalf("%q %+v", r.text, r.final)
	}
	p := lastPrompt(t, f.log)
	want := `"prompt":[{"type":"text","text":"Be brief."},{"type":"image","mimeType":"image/png","data":"` + base64.StdEncoding.EncodeToString(tinyPNG) + `"},` +
		`{"type":"text","text":"<attached-file name=\"notes.md\">\ncontent\n</attached-file>"},{"type":"text","text":"​/look"}]`
	if !strings.Contains(p, want) {
		t.Errorf("prompt %s\nwant %s", p, want)
	}
	if r := send(t, c, "plain", nil); r.text != "echo: plain" {
		t.Fatalf("%q", r.text)
	}
	if p := lastPrompt(t, f.log); !strings.HasSuffix(p, `"prompt":[{"type":"text","text":"plain"}]}`) {
		t.Errorf("plain prompt %s", p)
	}
	// An empty text keeps its "text".
	if r := send(t, c, "", nil); r.final.Kind != backend.EventDone {
		t.Fatalf("%+v", r.final)
	}
	if p := lastPrompt(t, f.log); !strings.HasSuffix(p, `"prompt":[{"type":"text","text":""}]}`) {
		t.Errorf("empty prompt %s", p)
	}
}

// TestAttachmentsNoImage: an agent without the image prompt capability
// refuses images (nothing is sent; the process stays usable) and takes text
// files.
func TestAttachmentsNoImage(t *testing.T) {
	f := newFixture(t, nil, "ACP_FAKE_NOIMAGE=1")
	img, txt := attachments(t)
	c := f.open(t, backend.OpenOptions{SystemPrompt: "Be brief."})
	r := sendWith(t, c, "look", img)
	if r.final.Kind != backend.EventError || backend.KindOf(r.final.Err) != backend.ErrAttachment ||
		!strings.Contains(r.final.Err.Error(), "Fake ACP") || !strings.Contains(r.final.Err.Error(), "images") {
		t.Fatalf("%+v", r.final)
	}
	r = sendWith(t, c, "read", txt)
	if r.final.Kind != backend.EventDone || r.text != "echo: read" {
		t.Fatalf("%q %+v", r.text, r.final)
	}
	// The system prompt was kept for the prompt that was sent.
	var p promptParams
	msgs := received(f.log)
	_ = json.Unmarshal(msgs[len(msgs)-1].Params, &p)
	if len(p.Prompt) != 3 || p.Prompt[0].Text != "Be brief." || !strings.HasPrefix(p.Prompt[1].Text, "<attached-file") {
		t.Errorf("prompt %+v", p)
	}
	if m := methods(f.log); count(m, "initialize") != 1 || count(m, "session/prompt") != 1 {
		t.Errorf("methods %q", m)
	}
}

// TestPromptWith: there is no per-turn mode.
func TestPromptWith(t *testing.T) {
	c := NewAdapter(testConfig()).NewCodec(agentrt.Launch{}).(agentrt.AttachmentCodec)
	if b, err := c.PromptWith("x", nil); b != nil || err == nil {
		t.Errorf("%q %v", b, err)
	}
}
