package claudecode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// writePNG writes a w×h PNG of one color and returns its attachment.
func writePNG(t *testing.T, dir string, w, h int, c color.Color) backend.Attachment {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "image.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return backend.Attachment{Kind: backend.AttachImage, Name: "red.png", Path: path, MediaType: "image/png", Size: int64(buf.Len())}
}

// writeText writes a text attachment.
func writeText(t *testing.T, dir, name, content string) backend.Attachment {
	t.Helper()
	path := filepath.Join(dir, name+".txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return backend.Attachment{Kind: backend.AttachText, Name: name, Path: path, MediaType: "text/plain", Size: int64(len(content))}
}

func sendWith(t *testing.T, c backend.Conversation, text string, atts ...backend.Attachment) answer {
	t.Helper()
	ch, err := c.Send(context.Background(), text, atts...)
	if err != nil {
		t.Fatal(err)
	}
	return collect(t, ch)
}

func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAttachmentCaps(t *testing.T) {
	caps := New(Options{}).Attachments()
	if !caps.Accepts(backend.AttachImage, "image/png") || !caps.Accepts(backend.AttachText, "text/plain") {
		t.Errorf("caps %+v", caps)
	}
}

// TestMessageWith pins the stream-json line of a message with attachments.
func TestMessageWith(t *testing.T) {
	dir := t.TempDir()
	img := writePNG(t, dir, 2, 2, color.RGBA{R: 255, A: 255})
	txt := writeText(t, dir, "notes.md", "line 1\nline 2\n")
	c := (adapter{}).NewCodec(agentrt.Launch{Persistent: true}).(agentrt.AttachmentCodec)
	got, err := c.MessageWith("/look", []backend.Attachment{img, txt})
	if err != nil {
		t.Fatal(err)
	}
	data := base64.StdEncoding.EncodeToString(fileBytes(t, img.Path))
	want := `{"type":"user","message":{"role":"user","content":[` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + data + `"}},` +
		`{"type":"text","text":"\u003cattached-file name=\"notes.md\"\u003e\nline 1\nline 2\n\u003c/attached-file\u003e"},` +
		`{"type":"text","text":" /look"}]}}` + "\n"
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	// Without text: no text block of its own.
	got, _ = c.MessageWith("", []backend.Attachment{txt})
	if n := strings.Count(string(got), `"type":"text"`); n != 1 {
		t.Errorf("%d text blocks: %s", n, got)
	}
	// The plain message is unchanged.
	plain, _ := (adapter{}).NewCodec(agentrt.Launch{Persistent: true}).Message("hi")
	if string(plain) != `{"type":"user","message":{"role":"user","content":"hi"}}`+"\n" {
		t.Errorf("plain %s", plain)
	}
	// A file that cannot be read: ErrAttachment, without its name.
	_, err = c.MessageWith("x", []backend.Attachment{{Kind: backend.AttachText, Name: "secret-name.txt", Path: filepath.Join(dir, "secret-name.txt")}})
	if backend.KindOf(err) != backend.ErrAttachment || strings.Contains(err.Error(), "secret-name") {
		t.Errorf("err %v", err)
	}
}

// TestFoldWithAttachments: the system prompt of a CLI without
// --append-system-prompt goes with the first message, attachments or not.
func TestFoldWithAttachments(t *testing.T) {
	txt := writeText(t, t.TempDir(), "a.txt", "A")
	flags := flagsWithout(t, flagAppendSystem)
	c := (adapter{}).NewCodec(agentrt.Launch{SystemPrompt: "Be brief.", Flags: flags, Attachments: []backend.Attachment{txt}}).(agentrt.AttachmentCodec)
	in, err := c.PromptWith("q", []backend.Attachment{txt})
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Type    string
		Message struct{ Content []inBlock }
	}
	if err := json.Unmarshal(in, &m); err != nil || m.Type != "user" || len(m.Message.Content) != 2 ||
		m.Message.Content[1].Text != foldSystemPrompt("Be brief.", "q") {
		t.Errorf("prompt %s", in)
	}
}

func TestAttachmentsPersistent(t *testing.T) {
	f := newFake(t, nil)
	dir := t.TempDir()
	img := writePNG(t, dir, 3, 2, color.RGBA{R: 255, A: 255})
	txt := writeText(t, dir, "notes.md", "The word is pineapple.")
	c := f.open(backend.OpenOptions{})
	wantDone(t, sendWith(t, c, "look", img, txt), "echo: look [images: 1, files: 1]")
	wantDone(t, send(t, c, "again"), "echo: again")
	if n := len(f.starts()); n != 1 {
		t.Errorf("%d processes", n)
	}
	in := f.stdin()
	if len(in) != 2 || !strings.Contains(in[0], base64.StdEncoding.EncodeToString(fileBytes(t, img.Path))) {
		t.Errorf("stdin %q", in)
	}
	if in[1] != `{"type":"user","message":{"role":"user","content":"again"}}` {
		t.Errorf("plain message %q", in[1])
	}
	checkImageLog(t, f, []int{len(fileBytes(t, img.Path))})
}

// checkImageLog checks the image sizes the fake decoded.
func checkImageLog(t *testing.T, f *fake, sizes []int) {
	t.Helper()
	for _, e := range f.entries() {
		if e.Images != nil {
			if !slices.Equal(e.ImageBytes, sizes) || !slices.Equal(e.ImageTypes, []string{"image/png"}) {
				t.Errorf("fake decoded %v %q, want %v", e.ImageBytes, e.ImageTypes, sizes)
			}
			return
		}
	}
	t.Error("the fake saw no content blocks")
}

func TestAttachmentsPerTurn(t *testing.T) {
	f := newFake(t, nil, func(o *Options) { o.PerTurn = true })
	dir := t.TempDir()
	img := writePNG(t, dir, 2, 2, color.RGBA{R: 255, A: 255})
	txt := writeText(t, dir, "notes.md", "content")
	c := f.open(backend.OpenOptions{SystemPrompt: "Be brief."})
	wantDone(t, sendWith(t, c, "look", img, txt), "echo: look [images: 1, files: 1]")
	wantDone(t, send(t, c, "again"), "echo: again")
	wantDone(t, sendWith(t, c, "more", txt), "echo: more [images: 0, files: 1]")
	starts := f.starts()
	if len(starts) != 3 {
		t.Fatalf("starts %q", starts)
	}
	if !hasFlag(starts[0], flagInputFormat, "stream-json") || !hasFlag(starts[2], flagInputFormat, "stream-json") {
		t.Errorf("no stream-json input with attachments: %q", starts)
	}
	if slices.Contains(starts[1], flagInputFormat) || !hasFlag(starts[1], flagResume, starts[0][slices.Index(starts[0], flagSessionID)+1]) {
		t.Errorf("plain turn: %q", starts[1])
	}
	in := f.stdin()
	if len(in) != 3 || in[1] != "again" {
		t.Errorf("stdin %q", in)
	}
	checkImageLog(t, f, []int{len(fileBytes(t, img.Path))})
}

// TestAttachmentsOlderCLI: a CLI without --input-format takes text files
// inline, not images.
func TestAttachmentsOlderCLI(t *testing.T) {
	f := newFake(t, map[string]any{"helpFile": helpWithout(t, helpReal, flagInputFormat)})
	dir := t.TempDir()
	img := writePNG(t, dir, 2, 2, color.RGBA{R: 255, A: 255})
	txt := writeText(t, dir, "notes.md", "content")
	c := f.open(backend.OpenOptions{})
	wantDone(t, sendWith(t, c, "hi", txt), "echo: <attached-file name=\"notes.md\">\ncontent\n</attached-file>\n\nhi")
	a := sendWith(t, c, "look", img, txt)
	if a.final.Kind != backend.EventError || backend.KindOf(a.final.Err) != backend.ErrAttachment || !strings.Contains(a.final.Err.Error(), "images") {
		t.Fatalf("final %+v", a.final)
	}
	if n := len(f.starts()); n != 1 {
		t.Errorf("%d processes, want 1 (none for the refused message)", n)
	}
	for _, argv := range f.starts() {
		if slices.Contains(argv, flagInputFormat) {
			t.Errorf("%s in %q", flagInputFormat, argv)
		}
	}
	wantDone(t, send(t, c, "again"), "echo: again")
}

// TestArgvAttachmentsTable pins the command lines of per-turn processes
// whose message has attachments to testdata/argv.attachments.golden.json
// (run with -update for an intended change).
func TestArgvAttachmentsTable(t *testing.T) {
	dir := filepath.Join("C:\\", "work", "ai")
	b := New(Options{Dir: dir, Tools: &ToolServer{Command: `C:\golauncher.exe`, Args: []string{"mcp"},
		Env: map[string]string{"GOLAUNCHER_AGENT_TOKEN": "tok"}, SessionEnv: "GOLAUNCHER_AGENT_SESSION"}})
	const id = "12345678-1234-4234-8234-123456789abc"
	img := backend.Attachment{Kind: backend.AttachImage, Name: "a.png", Path: `C:\work\chats\a.png`, MediaType: "image/png", Size: 10}
	txt := backend.Attachment{Kind: backend.AttachText, Name: "b.md", Path: `C:\work\chats\b.md`, MediaType: "text/plain", Size: 10}
	sets := []struct {
		name    string
		version string
		flags   agentrt.Flags
	}{
		{"unknown", "", nil},
		{"2.1.292 probed", "2.1.292", parseHelp(readHelp(t, helpReal))},
		{"no input-format", "2.1.292", flagsWithout(t, flagInputFormat)},
	}
	var got []argvCase
	for _, set := range sets {
		for _, resume := range []bool{false, true} {
			for _, atts := range []struct {
				name string
				atts []backend.Attachment
			}{{"none", nil}, {"image", []backend.Attachment{img}}, {"text", []backend.Attachment{txt}}, {"image+text", []backend.Attachment{img, txt}}} {
				oo := backend.OpenOptions{SessionID: id, Resume: resume, Model: "opus", WebSearch: true, LauncherTools: true, SystemPrompt: "Be brief."}
				argv, env, err := commandAtts(b, oo, `C:\bin\claude.exe`, set.version, set.flags, resume, atts.atts)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, argvCase{
					Name: fmt.Sprintf("%s per-turn resume=%v attachments=%s", set.name, resume, atts.name),
					Argv: windowsPaths(argv, dir), ExtraEnv: env,
				})
			}
		}
	}
	// Without attachments the per-turn command line is the usual one.
	for _, c := range got {
		if strings.HasSuffix(c.Name, "attachments=none") && slices.Contains(c.Argv, flagInputFormat) {
			t.Errorf("%s: %q", c.Name, c.Argv)
		}
	}
	out, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	golden := filepath.Join("testdata", "argv.attachments.golden.json")
	if *updateArgv {
		if err := os.WriteFile(golden, out, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), out) {
		t.Fatalf("command lines differ from %s; rerun with -update if intended", golden)
	}
}
