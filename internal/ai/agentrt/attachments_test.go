package agentrt

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// attAdapter is testAdapter with Info.Attachments and, with codec, an
// AttachmentCodec.
type attAdapter struct {
	testAdapter
	caps    backend.AttachmentCaps
	codec   bool
	prompts *atomic.Int32 // Prompt calls (attCodec)
}

func (a attAdapter) Info() Info {
	i := a.testAdapter.Info()
	i.Attachments = a.caps
	return i
}

func (a attAdapter) NewCodec(l Launch) Codec {
	c := a.testAdapter.NewCodec(l).(*testCodec)
	if !a.codec {
		return c
	}
	return &attCodec{testCodec: c, prompts: a.prompts, launch: l}
}

// attCodec sends "<text> [n attachments]".
type attCodec struct {
	*testCodec
	prompts *atomic.Int32
	launch  Launch
}

func (c *attCodec) Prompt(text string) []byte {
	c.prompts.Add(1)
	return c.testCodec.Prompt(text)
}

func (c *attCodec) MessageWith(text string, atts []backend.Attachment) ([]byte, error) {
	return c.Message(fmt.Sprintf("%s [%d attachments]", text, len(atts)))
}

func (c *attCodec) PromptWith(text string, atts []backend.Attachment) ([]byte, error) {
	if len(c.launch.Attachments) != len(atts) {
		return nil, fmt.Errorf("Launch.Attachments %d, message %d", len(c.launch.Attachments), len(atts))
	}
	return fmt.Appendf(nil, "%s [%d attachments]", text, len(atts)), nil
}

// recorder is a slog.Handler that keeps every record's text.
type recorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *recorder) handler() slog.Handler {
	return slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.buf.Write(p)
	}), &slog.HandlerOptions{Level: slog.LevelDebug})
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// newAttTest is newTest with a log recorder.
func newAttTest(t *testing.T, a Adapter) (*Runtime, string, *recorder) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "log.txt")
	rec := &recorder{}
	r := New(a, Options{Path: exe, Dir: filepath.Join(dir, "work"), Log: slog.New(rec.handler()),
		Env: []string{"AGENTRT_FAKE=1", "AGENTRT_FAKE_LOG=" + log}})
	t.Cleanup(func() { r.Close() })
	return r, log, rec
}

func sendAtts(t *testing.T, r *Runtime, chat, text string, atts ...backend.Attachment) (string, backend.Event) {
	t.Helper()
	c, err := r.Open(context.Background(), backend.OpenOptions{SessionID: chat})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := c.Send(context.Background(), text, atts...)
	if err != nil {
		t.Fatal(err)
	}
	s, final, _ := collect(t, ch, "")
	return s, final
}

var (
	testImage = backend.Attachment{Kind: backend.AttachImage, Name: "secret-photo.png", Path: `C:\chats\secret-photo.png`, MediaType: "image/png", Size: 100}
	testText  = backend.Attachment{Kind: backend.AttachText, Name: "secret-notes.md", Path: `C:\chats\secret-notes.md`, MediaType: "text/plain", Size: 20}
)

// TestAttachmentsWithoutCodec: a CLI whose codec cannot send attachments
// ends the turn with ErrAttachment, persistent or per turn.
func TestAttachmentsWithoutCodec(t *testing.T) {
	for _, perTurn := range []bool{false, true} {
		t.Run(fmt.Sprint("perTurn=", perTurn), func(t *testing.T) {
			r, _, _ := newAttTest(t, attAdapter{testAdapter: testAdapter{perTurnOnly: perTurn}, caps: backend.AllAttachments()})
			if caps := r.Attachments(); !caps.Images || !caps.Text {
				t.Errorf("caps %+v", caps)
			}
			_, final := sendAtts(t, r, "chat-1", "hi", testText)
			if final.Kind != backend.EventError || backend.KindOf(final.Err) != backend.ErrAttachment {
				t.Fatalf("final %+v", final)
			}
		})
	}
}

// TestAttachmentsRefused: a kind Info.Attachments does not accept is refused
// before any process starts.
func TestAttachmentsRefused(t *testing.T) {
	for _, caps := range []backend.AttachmentCaps{{}, {Text: true}, {Images: true, ImageTypes: []string{"image/jpeg"}, Text: true}} {
		r, log, _ := newAttTest(t, attAdapter{testAdapter: testAdapter{perTurnOnly: true}, caps: caps, codec: true, prompts: &atomic.Int32{}})
		atts := []backend.Attachment{testText, testImage}
		if !caps.Text {
			atts = atts[:1]
		}
		_, final := sendAtts(t, r, "chat-1", "hi", atts...)
		if final.Kind != backend.EventError || backend.KindOf(final.Err) != backend.ErrAttachment {
			t.Fatalf("%+v: final %+v", caps, final)
		}
		if s := starts(t, log); len(s) != 0 {
			t.Errorf("%+v: started %q", caps, s)
		}
	}
}

// TestAttachmentsSent: MessageWith and PromptWith carry the attachments (and
// Prompt is not called for them); messages without attachments go as
// before; the log has counts and sizes, never names or paths.
func TestAttachmentsSent(t *testing.T) {
	for _, perTurn := range []bool{false, true} {
		t.Run(fmt.Sprint("perTurn=", perTurn), func(t *testing.T) {
			prompts := &atomic.Int32{}
			r, _, rec := newAttTest(t, attAdapter{testAdapter: testAdapter{perTurnOnly: perTurn}, caps: backend.AllAttachments(), codec: true, prompts: prompts})
			c, err := r.Open(context.Background(), backend.OpenOptions{SessionID: "chat-1"})
			if err != nil {
				t.Fatal(err)
			}
			for _, tt := range []struct {
				text string
				atts []backend.Attachment
				want string
			}{
				{"hi", []backend.Attachment{testImage, testText}, "echo: hi [2 attachments]"},
				{"plain", nil, "echo: plain"},
			} {
				ch, err := c.Send(context.Background(), tt.text, tt.atts...)
				if err != nil {
					t.Fatal(err)
				}
				if s, final, _ := collect(t, ch, ""); s != tt.want || final.Kind != backend.EventDone {
					t.Fatalf("%q %+v, want %q", s, final, tt.want)
				}
			}
			if want := map[bool]int32{false: 0, true: 1}[perTurn]; prompts.Load() != want {
				t.Errorf("%d Prompt calls, want %d (the plain message only)", prompts.Load(), want)
			}
			logs := rec.String()
			if strings.Contains(logs, "secret") || strings.Contains(logs, `C:\chats`) {
				t.Errorf("the log names an attachment:\n%s", logs)
			}
			if !strings.Contains(logs, "images=1 texts=1 attached_bytes=120") {
				t.Errorf("the log lacks the counts:\n%s", logs)
			}
		})
	}
}
