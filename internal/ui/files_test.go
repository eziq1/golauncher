package ui_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// filesPlugin has a chat-like detail view that accepts files and shows
// them as chips, with an "Add Attachment…" action (Ctrl+O).
type filesPlugin struct {
	mu       sync.Mutex
	received []plugin.Files
	attached []string
	handled  []string
	markdown string
}

func (p *filesPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "filesui", Name: "Files UI", Version: "1", Commands: []plugin.Command{
		{Name: "chat", Title: "Attach Chat", Mode: plugin.ModeView},
	}}
}

func (p *filesPlugin) Init(context.Context, plugin.Host) error { return nil }
func (p *filesPlugin) Close() error                            { return nil }

func (p *filesPlugin) View(context.Context, plugin.ViewRequest) (plugin.View, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	payload, _ := json.Marshal(plugin.ChooseFilesOptions{Multiple: true})
	v := plugin.View{Kind: plugin.ViewDetail, Title: "Chat", AcceptFiles: true, Draft: len(p.attached) > 0,
		Detail: &plugin.Detail{Markdown: "Hello" + p.markdown},
		Sections: []plugin.Section{{Items: []plugin.Item{{ID: "chat", Title: "Chat", Actions: []plugin.Action{
			{ID: "send", Title: "Send", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "send"}},
			{ID: "attach", Title: "Add Attachment…", Shortcut: "Ctrl+O", Do: plugin.ActionDo{Kind: plugin.ActionChooseFiles, Payload: payload}},
		}}}}},
	}
	for i, name := range p.attached {
		id := fmt.Sprintf("c%d", i)
		v.Chips = append(v.Chips, plugin.Chip{ID: id, Title: name, Subtitle: "12 KB", Icon: plugin.Icon{Name: "file"}, Error: name == "bad.bin", Tooltip: "tip",
			Remove: &plugin.Action{ID: "remove", Title: "Remove", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: id}}})
	}
	return v, nil
}

func (p *filesPlugin) ReceiveFiles(_ context.Context, f plugin.Files) (plugin.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.received = append(p.received, f)
	for _, path := range f.Paths {
		p.attached = append(p.attached, filepath.Base(path))
	}
	if len(f.Image) > 0 {
		p.attached = append(p.attached, "pasted.png")
	}
	return plugin.Result{Refresh: true}, nil
}

func (p *filesPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handled = append(p.handled, a.Target)
	var i int
	if _, err := fmt.Sscanf(a.Target, "c%d", &i); err == nil && i < len(p.attached) {
		p.attached = slices.Delete(p.attached, i, i+1)
	}
	return plugin.Result{Refresh: true}, nil
}

func (p *filesPlugin) got() []plugin.Files {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.received)
}

type filesTest struct {
	*launcherTest
	fp   *filesPlugin
	clip *hostfake.Clipboard
}

func newFilesLauncher(t *testing.T) *filesTest {
	t.Helper()
	clip := &hostfake.Clipboard{}
	fp := &filesPlugin{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{testPlugin{}, fp}, Fake: true, Locale: "en",
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	lt := &launcherTest{Env: env, t: t, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	return &filesTest{launcherTest: lt, fp: fp, clip: clip}
}

func (ft *filesTest) openChat(t *testing.T) {
	t.Helper()
	ft.typeText("attach chat")
	ft.key(0, mui.KeyEnter)
	if s := ft.Controller.Snapshot(); s.Depth != 1 || !s.AcceptFiles {
		t.Fatalf("chat did not open: depth %d accept %v", s.Depth, s.AcceptFiles)
	}
}

func TestDropFilesUI(t *testing.T) {
	ft := newFilesLauncher(t)
	ft.openChat(t)
	ft.l.DropFiles([]string{filepath.Join(t.TempDir(), "report.pdf")})
	ft.step()
	if got := ft.fp.got(); len(got) != 1 || got[0].Source != plugin.FilesDropped || got[0].Instance == "" {
		t.Fatalf("received %+v", got)
	}
	ft.wantText("report.pdf")
	ft.wantText("12 KB")
}

func TestPasteFilesUI(t *testing.T) {
	ft := newFilesLauncher(t)
	// The root search takes no files: Ctrl+V pastes text as usual.
	ft.clip.Set(plugin.ClipboardContent{Files: []string{"x.txt"}})
	ft.tt.SetClipboard("from the field")
	ft.key(ui.Primary(), mui.KeyV)
	if got := ft.fp.got(); len(got) != 0 {
		t.Fatalf("root: received %+v", got)
	}
	// MyGo's text input pastes through the Edit menu on macOS, which the
	// tester's key press does not reach; the plain-text paste is checked on
	// the other platforms.
	if s := ft.Controller.Snapshot(); runtime.GOOS != "darwin" && s.Text != "from the field" {
		t.Fatalf("root: text %q", s.Text)
	}
	ft.key(ui.Primary(), mui.KeyA)
	ft.key(0, mui.KeyBackspace)
	ft.openChat(t)
	// An image goes to the view.
	ft.clip.Set(plugin.ClipboardContent{Image: []byte("\x89PNG fake")})
	ft.key(ui.Primary(), mui.KeyV)
	if got := ft.fp.got(); len(got) != 1 || got[0].Source != plugin.FilesPasted || len(got[0].Image) == 0 {
		t.Fatalf("received %+v", got)
	}
	if s := ft.Controller.Snapshot(); s.Text != "" {
		t.Fatalf("the image was also pasted as text: %q", s.Text)
	}
	ft.wantText("pasted.png")
	// Text is pasted into the search field, not taken.
	ft.clip.Set(plugin.ClipboardContent{Text: "plain words"})
	ft.tt.SetClipboard("plain words")
	ft.key(ui.Primary(), mui.KeyV)
	if n := len(ft.fp.got()); n != 1 {
		t.Fatalf("plain text was taken as files: received %d", n)
	}
	if s := ft.Controller.Snapshot(); runtime.GOOS != "darwin" && s.Text != "plain words" { // see above
		t.Fatalf("text: %q", s.Text)
	}
}

func TestChooseFilesUI(t *testing.T) {
	ft := newFilesLauncher(t)
	ft.openChat(t)
	ft.host.chosen = []string{"a.txt", "b.txt"}
	ft.key(ui.Primary(), mui.KeyO)
	got := ft.fp.got()
	if len(got) != 1 || got[0].Source != plugin.FilesChosen || !slices.Equal(got[0].Paths, []string{"a.txt", "b.txt"}) {
		t.Fatalf("received %+v", got)
	}
	if len(ft.host.chooseOpts) != 1 || !ft.host.chooseOpts[0].Multiple {
		t.Fatalf("dialogs %+v", ft.host.chooseOpts)
	}
	// Cancelled: nothing.
	ft.host.chosen = nil
	ft.key(ui.Primary(), mui.KeyO)
	if len(ft.fp.got()) != 1 || len(ft.host.chooseOpts) != 2 {
		t.Fatalf("after cancel: received %d, dialogs %d", len(ft.fp.got()), len(ft.host.chooseOpts))
	}
}

// TestChipsKeepSearchBar: the chips row never changes the search bar, and
// a chip's × runs its Remove action.
func TestChipsKeepSearchBar(t *testing.T) {
	ft := newFilesLauncher(t)
	ft.openChat(t)
	before, ok := ft.tt.Find("Search")
	if !ok {
		t.Fatalf("no search field in %q", ft.tt.Texts())
	}
	ft.l.DropFiles([]string{"one.txt", "bad.bin", "three.txt"})
	ft.step()
	ft.wantText("one.txt")
	after, _ := ft.tt.Find("Search")
	if after != before {
		t.Fatalf("search field %+v with chips, %+v without", after, before)
	}
	chip, ok := ft.tt.Find("bad.bin")
	if !ok {
		t.Fatal("no chip")
	}
	if footer, ok := ft.tt.Find("Send"); ok && chip.Y > footer.Y {
		t.Fatalf("chips (y %v) below the footer (y %v)", chip.Y, footer.Y)
	}
	if err := ft.tt.Click("Remove bad.bin"); err != nil {
		t.Fatalf("%v in %q", err, ft.tt.Texts())
	}
	ft.step()
	ft.fp.mu.Lock()
	handled := slices.Clone(ft.fp.handled)
	ft.fp.mu.Unlock()
	if !slices.Equal(handled, []string{"c1"}) {
		t.Fatalf("handled %v", handled)
	}
	if ft.tt.HasText("bad.bin") || !ft.tt.HasText("three.txt") {
		t.Fatalf("after removing: %q", ft.tt.Texts())
	}
	if again, _ := ft.tt.Find("Search"); again != before {
		t.Fatalf("search field %+v after removing, %+v before", again, before)
	}
}

func TestMarkdownLocalImages(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "thumb.png")
	for _, tc := range []struct {
		dest string
		ok   bool
	}{
		{abs, true},
		{"<" + filepath.ToSlash(abs) + ">", true},
		{"file:" + filepath.ToSlash(abs), true},
		{"thumb.png", false},
		{"https://example.com/a.png", false},
		{"data:image/png;base64,AAAA", false},
		{filepath.Join(filepath.Dir(abs), "notes.txt"), false},
		{`\\server\share\a.png`, false},
	} {
		if _, ok := ui.LocalImagePath(tc.dest); ok != tc.ok {
			t.Errorf("LocalImagePath(%q) = %v, want %v", tc.dest, ok, tc.ok)
		}
	}
	if runtime.GOOS == "windows" {
		if p, ok := ui.LocalImagePath("file:///C:/x/y.png"); !ok || p != `C:\x\y.png` {
			t.Errorf("file URL: %q, %v", p, ok)
		}
	}

	// A thumbnail in the transcript loads.
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 64, 48))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	ft := newFilesLauncher(t)
	ft.fp.markdown = "\n\n![thumb](<" + filepath.ToSlash(abs) + ">)"
	ft.openChat(t)
	deadline := time.Now().Add(3 * time.Second)
	for {
		ft.step()
		// Loaded, the image (labeled with its description) is 48 high; the
		// placeholder before is a line of text.
		if r, ok := ft.tt.Find("thumb"); ok && r.H > 40 {
			break
		}
		if time.Now().After(deadline) {
			r, _ := ft.tt.Find("thumb")
			t.Fatalf("the image did not load: %+v in %q", r, ft.tt.Texts())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
