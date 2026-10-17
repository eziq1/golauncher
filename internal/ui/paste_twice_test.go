package ui_test

import (
	"bytes"
	"encoding/json/jsontext"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"strings"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// solidPNG is a w×h PNG of one color.
func solidPNG(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// aiPasteTest is a launcher with the AI chat (fake backend, AI on) and a
// fake clipboard.
type aiPasteTest struct {
	*launcherTest
	clip *hostfake.Clipboard
	fake *backend.Fake
}

func newAIPasteLauncher(t *testing.T) *aiPasteTest {
	t.Helper()
	fake := &backend.Fake{}
	fake.Go = func(fn func()) { go fn() }
	clip := &hostfake.Clipboard{}
	rec := &app.Recorder{Clipboard: clip}
	p := ai.NewWithOptions(ai.Options{Backend: fake, HasProtocol: func(string) bool { return false }})
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{testPlugin{}, p}, Fake: true, Locale: "en", System: rec, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	if err := env.Config.Update(func(c *config.Config) {
		c.Plugins = map[string]config.PluginConfig{ai.ID: {Preferences: map[string]jsontext.Value{"enabled": jsontext.Value("true")}}}
	}); err != nil {
		t.Fatal(err)
	}
	lt := &launcherTest{Env: env, t: t, rec: rec, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	lt.typeText("ai chat")
	lt.key(0, mui.KeyEnter)
	if s := lt.Controller.Snapshot(); s.Depth != 1 || !s.AcceptFiles {
		t.Fatalf("the chat did not open: depth %d accept %v", s.Depth, s.AcceptFiles)
	}
	return &aiPasteTest{launcherTest: lt, clip: clip, fake: fake}
}

// paste copies an image (as another app would) and presses Ctrl+V.
func (pt *aiPasteTest) paste(c plugin.ClipboardContent) {
	pt.clip.Set(c)
	pt.key(ui.Primary(), mui.KeyV)
}

// chips is how many chips the chat shows.
func (pt *aiPasteTest) chips() int { return len(pt.Controller.Snapshot().Chips) }

func (pt *aiPasteTest) wantChips(what string, n int) {
	pt.t.Helper()
	if got := pt.chips(); got != n {
		pt.t.Fatalf("%s: %d chips, want %d (text %q)", what, got, n, pt.Controller.Snapshot().Text)
	}
}

// TestAIPasteTwice (R96): pasting an image into the AI chat works every
// time, not just the first: the same image twice, two different images,
// after sending, after typing, after removing the first chip.
func TestAIPasteTwice(t *testing.T) {
	red := solidPNG(t, 8, 8, color.NRGBA{R: 255, A: 255})
	blue := solidPNG(t, 12, 6, color.NRGBA{B: 255, A: 255})

	t.Run("same image twice", func(t *testing.T) {
		pt := newAIPasteLauncher(t)
		pt.paste(plugin.ClipboardContent{Image: red})
		pt.wantChips("first", 1)
		pt.paste(plugin.ClipboardContent{Image: red})
		pt.wantChips("second", 2)
	})
	t.Run("two images", func(t *testing.T) {
		pt := newAIPasteLauncher(t)
		pt.paste(plugin.ClipboardContent{Image: red})
		pt.paste(plugin.ClipboardContent{Image: blue})
		pt.wantChips("two", 2)
		pt.wantText("12×6")
	})
	t.Run("after sending", func(t *testing.T) {
		pt := newAIPasteLauncher(t)
		pt.paste(plugin.ClipboardContent{Image: red})
		pt.typeText("what color")
		pt.key(0, mui.KeyEnter)
		pt.waitFor("the answer", func(md string) bool { return strings.Contains(md, "You asked: what color") })
		pt.wantChips("sent", 0)
		pt.paste(plugin.ClipboardContent{Image: blue})
		pt.wantChips("after sending", 1)
	})
	t.Run("after typing", func(t *testing.T) {
		pt := newAIPasteLauncher(t)
		pt.paste(plugin.ClipboardContent{Image: red})
		pt.typeText("describe")
		pt.paste(plugin.ClipboardContent{Image: blue})
		pt.wantChips("after typing", 2)
		if s := pt.Controller.Snapshot(); s.Text != "describe" {
			t.Fatalf("text %q", s.Text)
		}
	})
	t.Run("after removing the chip", func(t *testing.T) {
		pt := newAIPasteLauncher(t)
		pt.paste(plugin.ClipboardContent{Image: red})
		chips := pt.Controller.Snapshot().Chips
		if err := pt.tt.Click("Remove " + chips[0].Title); err != nil {
			t.Fatalf("%v in %q", err, pt.tt.Texts())
		}
		pt.step()
		pt.wantChips("removed", 0)
		pt.paste(plugin.ClipboardContent{Image: red})
		pt.wantChips("after removing", 1)
	})
	t.Run("image with its address", func(t *testing.T) {
		// What browsers' and chat apps' "Copy Image" put: the paste takes the
		// image, not the text (the cause of R96).
		pt := newAIPasteLauncher(t)
		pt.paste(plugin.ClipboardContent{Image: red})
		for i, text := range []string{"https://example.com/photo.png", "[图片]", " "} {
			pt.tt.SetClipboard(text)
			pt.paste(plugin.ClipboardContent{Image: blue, Text: text})
			pt.wantChips(text, i+2)
			if s := pt.Controller.Snapshot(); s.Text != "" {
				t.Fatalf("%q was also pasted as text: %q", text, s.Text)
			}
		}
	})
	t.Run("image with real text", func(t *testing.T) {
		// A spreadsheet cell comes with a picture of it: the text is pasted.
		pt := newAIPasteLauncher(t)
		pt.tt.SetClipboard("Total 42")
		pt.paste(plugin.ClipboardContent{Image: red, Text: "Total 42\r\n"})
		pt.wantChips("real text", 0)
		if s := pt.Controller.Snapshot(); runtime.GOOS != "darwin" && s.Text != "Total 42" { // see TestPasteFilesUI
			t.Fatalf("text %q", s.Text)
		}
	})
	t.Run("from the Edit menu", func(t *testing.T) {
		// ⌘V on macOS pastes through the Edit menu, never through a key.
		pt := newAIPasteLauncher(t)
		pt.clip.Set(plugin.ClipboardContent{Image: red})
		pt.tt.SetClipboard("")
		pt.tt.Command("paste")
		pt.step()
		pt.wantChips("first", 1)
		pt.clip.Set(plugin.ClipboardContent{Image: blue})
		pt.tt.Command("paste")
		pt.step()
		pt.wantChips("second", 2)
	})
	t.Run("after hiding and showing", func(t *testing.T) {
		// Copying the second image in another app hides the launcher; the
		// chat is kept (R75) and shown again.
		pt := newAIPasteLauncher(t)
		pt.paste(plugin.ClipboardContent{Image: red})
		pt.hideShow(core.ShowOptions{})
		if s := pt.Controller.Snapshot(); s.Depth != 1 || !s.AcceptFiles {
			t.Fatalf("the chat was not kept: depth %d", s.Depth)
		}
		pt.paste(plugin.ClipboardContent{Image: blue})
		pt.wantChips("after showing again", 2)
	})
}
