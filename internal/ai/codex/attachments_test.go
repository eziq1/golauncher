package codex

import (
	"bytes"
	"context"
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

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// writePNG writes a w×h PNG of one color named name and returns its
// attachment.
func writePNG(t *testing.T, dir, name string, w, h int, c color.Color) backend.Attachment {
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
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return backend.Attachment{Kind: backend.AttachImage, Name: name, Path: path, MediaType: "image/png", Size: int64(buf.Len())}
}

// writeText writes a text attachment.
func writeText(t *testing.T, dir, name, content string) backend.Attachment {
	t.Helper()
	path := filepath.Join(dir, name)
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

func TestAttachments(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	img := writePNG(t, dir, "a.png", 2, 2, color.RGBA{R: 255, A: 255})
	img2 := writePNG(t, dir, "b.png", 3, 3, color.RGBA{B: 255, A: 255})
	txt := writeText(t, dir, "notes.md", "content")
	c := f.open(backend.OpenOptions{})
	wantDone(t, sendWith(t, c, "look", img, txt), "echo: <attached-file name=\"notes.md\">\ncontent\n</attached-file>\n\nlook [images: 1]")
	wantDone(t, sendWith(t, c, "more", img, img2), "echo: more [images: 2]")
	wantDone(t, send(t, c, "plain"), "echo: plain")
	starts := f.starts()
	if len(starts) != 3 {
		t.Fatalf("starts %q", starts)
	}
	if !slices.Equal(starts[0][:2], []string{"exec", "--image=" + img.Path}) || starts[0][2] != "--json" {
		t.Errorf("first turn: %q", starts[0])
	}
	i := slices.Index(starts[1], "resume")
	if i < 0 || !slices.Equal(starts[1][i+1:i+3], []string{"--image=" + img.Path, "--image=" + img2.Path}) || starts[1][len(starts[1])-1] != "-" {
		t.Errorf("resumed turn: %q", starts[1])
	}
	for _, a := range starts[2] {
		if strings.HasPrefix(a, "--image") {
			t.Errorf("plain turn: %q", starts[2])
		}
	}
	var sizes [][]int64
	for _, e := range f.entries() {
		if e.ImageSizes != nil {
			sizes = append(sizes, e.ImageSizes)
		}
	}
	if len(sizes) != 2 || !slices.Equal(sizes[0], []int64{img.Size}) || !slices.Equal(sizes[1], []int64{img.Size, img2.Size}) {
		t.Errorf("image sizes %v", sizes)
	}
	if in := f.stdin(); len(in) != 3 || in[1] != "more" || in[2] != "plain" {
		t.Errorf("stdin %q", in)
	}
}

func TestAttachmentCommaPath(t *testing.T) {
	f := newFake(t)
	dir := filepath.Join(t.TempDir(), "a,b")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	img := writePNG(t, dir, "a.png", 2, 2, color.RGBA{R: 255, A: 255})
	a := sendWith(t, f.open(backend.OpenOptions{}), "look", img)
	if a.final.Kind != backend.EventError || backend.KindOf(a.final.Err) != backend.ErrAttachment {
		t.Fatalf("final %+v", a.final)
	}
	if n := len(f.starts()); n != 0 {
		t.Errorf("%d processes started", n)
	}
}

// TestArgvAttachmentsTable pins the command lines with images to
// testdata/argv.attachments.golden.json (run with -update for an intended
// change).
func TestArgvAttachmentsTable(t *testing.T) {
	const chat = "12345678-1234-4234-8234-123456789abc"
	const thread = "0190f0e1-2d3c-7b4a-8596-a7b8c9d0e1f2"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(`{"`+chat+`":"`+thread+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	withTools := New(Options{Dir: dir, Tools: &ToolServer{Command: `C:\golauncher.exe`, Args: []string{"mcp"},
		Env: map[string]string{"GOLAUNCHER_AGENT_TOKEN": "tok"}, SessionEnv: "GOLAUNCHER_AGENT_SESSION"}})
	img := backend.Attachment{Kind: backend.AttachImage, Name: "a.png", Path: `C:\work\chats\a.png`, MediaType: "image/png", Size: 10}
	img2 := backend.Attachment{Kind: backend.AttachImage, Name: "b.jpg", Path: `C:\work\chats\b.jpg`, MediaType: "image/jpeg", Size: 10}
	txt := backend.Attachment{Kind: backend.AttachText, Name: "c.md", Path: `C:\work\chats\c.md`, MediaType: "text/plain", Size: 10}
	var got []argvCase
	for _, resume := range []bool{false, true} {
		for _, tools := range []bool{false, true} {
			for _, atts := range []struct {
				name string
				atts []backend.Attachment
			}{{"text", []backend.Attachment{txt}}, {"image", []backend.Attachment{img}}, {"two images+text", []backend.Attachment{img, txt, img2}}} {
				oo := backend.OpenOptions{SessionID: chat, Resume: resume, WebSearch: true, LauncherTools: tools, SystemPrompt: "Be brief."}
				cmd, err := withTools.CommandWith(oo, `C:\bin\codex.exe`, "0.162.0", nil, false, resume, atts.atts)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, argvCase{Name: fmt.Sprintf("resume=%v tools=%v attachments=%s", resume, tools, atts.name), Argv: cmd.Argv})
			}
		}
	}
	// A text file only changes stdin.
	for _, c := range got {
		if strings.HasSuffix(c.Name, "attachments=text") {
			oo := backend.OpenOptions{SessionID: chat, Resume: strings.Contains(c.Name, "resume=true"), WebSearch: true,
				LauncherTools: strings.Contains(c.Name, "tools=true"), SystemPrompt: "Be brief."}
			if want := commandFor(withTools, oo, `C:\bin\codex.exe`, oo.Resume); !slices.Equal(c.Argv, want) {
				t.Errorf("%s: %q\nwant %q", c.Name, c.Argv, want)
			}
		}
	}
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	golden := filepath.Join("testdata", "argv.attachments.golden.json")
	if *updateArgv {
		if err := os.WriteFile(golden, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), b) {
		t.Fatalf("command lines differ from %s; rerun with -update if intended", golden)
	}
	if _, err := withTools.CommandWith(backend.OpenOptions{SessionID: chat}, `C:\bin\codex.exe`, "0.162.0", nil, false, false,
		[]backend.Attachment{{Kind: backend.AttachImage, Path: `C:\a,b\x.png`, MediaType: "image/png"}}); backend.KindOf(err) != backend.ErrAttachment {
		t.Errorf("comma path: %v", err)
	}
}
