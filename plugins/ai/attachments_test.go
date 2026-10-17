package ai

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// pngBytes is a w×h PNG of one color.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// writeFile writes a file into dir and returns its path.
func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// receive hands files to the plugin, as the host does.
func receive(t *testing.T, p *Plugin, f plugin.Files) plugin.Result {
	t.Helper()
	r, err := p.ReceiveFiles(t.Context(), f)
	if err != nil {
		t.Fatalf("ReceiveFiles: %v", err)
	}
	return r
}

// TestAttachmentsDropAndSend: files dropped onto a chat show as chips, make
// the view hold a draft, go to the backend with the next message (copies in
// the chat's directory), and show in the transcript and the history.
func TestAttachmentsDropAndSend(t *testing.T) {
	fake := &backend.Fake{}
	p, h := newTestPlugin(t, fake, true)
	src := t.TempDir()
	img := writeFile(t, src, "photo.png", pngBytes(t, 40, 20))
	txt := writeFile(t, src, "notes.md", []byte("# Notes\nThe secret word is pineapple.\n"))

	v := openChat(t, p, "v1", chatPayload{})
	if !v.AcceptFiles || len(v.Chips) != 0 || v.Draft {
		t.Fatalf("empty chat: %+v", v)
	}
	if r := receive(t, p, plugin.Files{Instance: "v1", View: viewChat, Paths: []string{img, txt}, Source: plugin.FilesDropped}); !r.Refresh || r.Toast != nil {
		t.Fatalf("result = %+v", r)
	}
	v = openChat(t, p, "v1", chatPayload{})
	if len(v.Chips) != 2 || !v.Draft {
		t.Fatalf("chips = %+v", v.Chips)
	}
	if c := v.Chips[0]; c.Title != "photo.png" || c.Error || c.Image == "" || !strings.Contains(c.Subtitle, "40×20") || c.Remove == nil {
		t.Fatalf("image chip = %+v", c)
	}
	if c := v.Chips[1]; c.Title != "notes.md" || c.Error || c.Image != "" || !strings.HasSuffix(c.Subtitle, " B") {
		t.Fatalf("text chip = %+v", c)
	}
	// The send action is the view's first; it takes the draft along.
	if r := act(t, p, "send", "v1", "What is this?"); !r.ClearText {
		t.Fatalf("send = %+v", r)
	}
	att := fake.Attached()
	if len(att) != 1 || len(att[0]) != 2 {
		t.Fatalf("attached = %+v", att)
	}
	a, b := att[0][0], att[0][1]
	if a.Kind != backend.AttachImage || a.MediaType != "image/png" || a.Name != "photo.png" ||
		b.Kind != backend.AttachText || b.Name != "notes.md" {
		t.Fatalf("attachments = %+v", att[0])
	}
	if data, err := os.ReadFile(b.Path); err != nil || !strings.Contains(string(data), "pineapple") {
		t.Fatalf("text copy %q: %q %v", b.Path, data, err)
	}
	dir := filepath.Join(h.DataDir(), "attachments", "id-1")
	for _, x := range att[0] {
		if filepath.Dir(x.Path) != dir {
			t.Fatalf("copy %q is not in the chat's directory %q", x.Path, dir)
		}
	}
	v = openChat(t, p, "v1", chatPayload{})
	if len(v.Chips) != 0 || v.Draft {
		t.Fatalf("the draft was not emptied: %+v", v.Chips)
	}
	md := v.Detail.Markdown
	if !strings.Contains(md, "📎 photo.png") || !strings.Contains(md, "notes.md") || !strings.Contains(md, ".thumb.png>)") ||
		!strings.Contains(md, "What is this?") {
		t.Fatalf("markdown:\n%s", md)
	}
	// Stored with the message; the history counts them.
	sess, msgs, err := p.st.Get("id-1")
	if err != nil || sess.Attachments != 2 || len(msgs[0].Attachments) != 2 {
		t.Fatalf("stored: %+v %+v %v", sess, msgs, err)
	}
	hv := p.historyView()
	if it := hv.Sections[0].Items[0]; len(it.Accessories) != 1 || it.Accessories[0].Text != "📎 2" {
		t.Fatalf("history item = %+v", it)
	}
	// Deleting the chat deletes the copies.
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "delete", Payload: plugin.MustPayload("id-1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the attachments of a deleted chat are kept: %v", err)
	}
}

// TestAttachmentsBlockSending: an attachment no backend can send (a PDF) or
// one the chat's backend does not accept (images) is a red chip with the
// reason, and the message is not sent until it is removed.
func TestAttachmentsBlockSending(t *testing.T) {
	fake := &backend.Fake{Caps: &backend.AttachmentCaps{Text: true}}
	p, _ := newTestPlugin(t, fake, true)
	src := t.TempDir()
	pdf := writeFile(t, src, "report.pdf", []byte("%PDF-1.7\n%âãÏÓ\n1 0 obj\n"))
	img := writeFile(t, src, "photo.png", pngBytes(t, 8, 8))
	txt := writeFile(t, src, "a.txt", []byte("hello\n"))
	openChat(t, p, "v1", chatPayload{})
	receive(t, p, plugin.Files{Instance: "v1", Paths: []string{pdf, img, txt}, Source: plugin.FilesChosen})
	v := openChat(t, p, "v1", chatPayload{})
	if len(v.Chips) != 3 {
		t.Fatalf("chips = %+v", v.Chips)
	}
	if c := v.Chips[0]; !c.Error || !strings.Contains(c.Subtitle, "PDF") || !strings.Contains(c.Tooltip, "report.pdf") {
		t.Fatalf("pdf chip = %+v", c)
	}
	if c := v.Chips[1]; !c.Error || c.Subtitle != "Claude Code doesn't accept images" {
		t.Fatalf("image chip = %+v", c)
	}
	if c := v.Chips[2]; c.Error {
		t.Fatalf("text chip = %+v", c)
	}
	r := act(t, p, "send", "v1", "look")
	if r.ClearText || r.Toast == nil || r.Toast.Style != plugin.ToastFailure || !strings.Contains(r.Toast.Message, "report.pdf") ||
		!strings.Contains(r.Toast.Message, "photo.png") || strings.Contains(r.Toast.Message, "a.txt") {
		t.Fatalf("send with unsupported attachments = %+v", r)
	}
	if len(fake.Sends()) != 0 {
		t.Fatalf("sent: %q", fake.Sends())
	}
	for _, c := range v.Chips[:2] {
		if _, err := p.HandleAction(t.Context(), c.Remove.Do); err != nil {
			t.Fatal(err)
		}
	}
	if v := openChat(t, p, "v1", chatPayload{}); len(v.Chips) != 1 || v.Chips[0].Title != "a.txt" {
		t.Fatalf("after removing: %+v", v.Chips)
	}
	if r := act(t, p, "send", "v1", "look"); !r.ClearText {
		t.Fatalf("send = %+v", r)
	}
	if att := fake.Attached(); len(att) != 1 || len(att[0]) != 1 || att[0][0].Name != "a.txt" {
		t.Fatalf("attached = %+v", att)
	}
}

// TestAttachmentsDropOnRoot: files dropped onto the root search open a new
// chat with them; with AI off its prompt to turn AI on keeps them.
func TestAttachmentsDropOnRoot(t *testing.T) {
	fake := &backend.Fake{}
	p, h := newTestPlugin(t, fake, false)
	src := t.TempDir()
	txt := writeFile(t, src, "todo.txt", []byte("milk\n"))
	if _, err := p.ReceiveFiles(t.Context(), plugin.Files{Paths: []string{txt}, Source: plugin.FilesPasted}); err != plugin.ErrFilesNotTaken {
		t.Fatalf("pasted on the root: %v", err)
	}
	r := receive(t, p, plugin.Files{Paths: []string{txt}, Source: plugin.FilesDropped, Query: "typed"})
	if r.Push == nil || r.Push.ID != viewChat {
		t.Fatalf("result = %+v", r)
	}
	req := *r.Push
	req.Instance = "v1"
	v, err := p.View(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v.Detail.Markdown, "1 attachment is waiting") || !v.Draft {
		t.Fatalf("off view:\n%s", v.Detail.Markdown)
	}
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true")})
	v, err = p.View(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Chips) != 1 || v.Chips[0].Title != "todo.txt" || len(fake.Sends()) != 0 {
		t.Fatalf("chips = %+v, sends %q", v.Chips, fake.Sends())
	}
	// A message of attachments only: the model gets a short request, the
	// history the file's name as the title.
	if r := act(t, p, "send", "v1", ""); !r.ClearText {
		t.Fatalf("send = %+v", r)
	}
	if s := fake.Sends(); len(s) != 1 || s[0] != attachmentsOnlyPrompt {
		t.Fatalf("sends = %q", s)
	}
	if list, _ := p.st.Sessions(); len(list) != 1 || list[0].Title != "todo.txt" {
		t.Fatalf("sessions = %+v", list)
	}
}

// TestAttachmentsPasteAndLimits: a pasted image joins the draft under a
// generated name; at most maxAttachments files fit.
func TestAttachmentsPasteAndLimits(t *testing.T) {
	p, _ := newTestPlugin(t, &backend.Fake{}, true)
	openChat(t, p, "v1", chatPayload{})
	receive(t, p, plugin.Files{Instance: "v1", Image: pngBytes(t, 4, 4), Source: plugin.FilesPasted})
	v := openChat(t, p, "v1", chatPayload{})
	if len(v.Chips) != 1 || v.Chips[0].Title != "Pasted Image.png" || v.Chips[0].Error {
		t.Fatalf("chips = %+v", v.Chips)
	}
	src := t.TempDir()
	var paths []string
	for i := range maxAttachments {
		paths = append(paths, writeFile(t, src, string(rune('a'+i))+".txt", []byte("x\n")))
	}
	r := receive(t, p, plugin.Files{Instance: "v1", Paths: paths, Source: plugin.FilesDropped})
	if r.Toast == nil || !strings.Contains(r.Toast.Title, "at most 10") || r.Toast.Message != "1 file was not added." {
		t.Fatalf("result = %+v", r)
	}
	if v := openChat(t, p, "v1", chatPayload{}); len(v.Chips) != maxAttachments {
		t.Fatalf("%d chips", len(v.Chips))
	}
}

// TestAttachmentsPrune: pruning a chat deletes its attachments; the
// directories of drafts never sent go after a day.
func TestAttachmentsPrune(t *testing.T) {
	p, h := newTestPlugin(t, &backend.Fake{}, true)
	root := filepath.Join(h.DataDir(), "attachments")
	now := h.Now()
	old := Session{ID: "old", Title: "t", Created: now.Add(-40 * 24 * time.Hour), Updated: now.Add(-40 * 24 * time.Hour)}
	if err := p.st.Save(old, []Message{{Role: RoleUser, Text: "q"}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old", "orphan", "fresh-orphan"} {
		if err := os.MkdirAll(filepath.Join(root, id), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(root, id), "x.txt", []byte("x"))
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "orphan"), past, past); err != nil {
		t.Fatal(err)
	}
	p.historyView() // prunes
	for id, kept := range map[string]bool{"old": false, "orphan": false, "fresh-orphan": true} {
		if _, err := os.Stat(filepath.Join(root, id)); (err == nil) != kept {
			t.Errorf("%s: kept %v, want %v", id, err == nil, kept)
		}
	}
}

// TestAttachmentsDraftKeepsView: a draft with attachments counts as a draft
// for the kept view (R76), and the chat restores with it.
func TestAttachmentsDraftKeepsView(t *testing.T) {
	p, _ := newTestPlugin(t, &backend.Fake{}, true)
	openChat(t, p, "v1", chatPayload{})
	receive(t, p, plugin.Files{Instance: "v1", Paths: []string{writeFile(t, t.TempDir(), "a.txt", []byte("x\n"))}, Source: plugin.FilesDropped})
	v, err := restoreChat(t, p, "v1", chatPayload{})
	if err != nil || !v.Draft || !v.KeepOnHide || len(v.Chips) != 1 {
		t.Fatalf("restored = %+v, %v", v, err)
	}
}

// TestAttachmentRecordJSON: the stored form of a message's attachments.
func TestAttachmentRecordJSON(t *testing.T) {
	b, err := json.Marshal(Message{Role: RoleUser, Text: "x", Attachments: []Attached{{ID: "a1", Name: "n.png", Kind: backend.AttachImage,
		MediaType: "image/png", Size: 3, File: "a1.png", Thumb: "a1.thumb.png", Width: 2, Height: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"attachments":[{"id":"a1","name":"n.png","kind":"image","mediaType":"image/png","size":3,"file":"a1.png","thumb":"a1.thumb.png","width":2,"height":1}]`) {
		t.Fatalf("json = %s", b)
	}
	b, _ = json.Marshal(Message{Role: RoleUser, Text: "x"})
	if strings.Contains(string(b), "attachments") {
		t.Fatalf("json without attachments = %s", b)
	}
}
