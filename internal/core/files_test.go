package core_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// filesPlugin receives files (plugin.FileReceiver): its "chat" view accepts
// them and shows them as chips, its "plain" view does not. In the root
// search it takes them only when take is set, opening its chat.
type filesPlugin struct {
	id, name string
	take     bool

	mu       sync.Mutex
	calls    int            // ReceiveFiles calls
	received []plugin.Files // the files taken
	attached []string       // the chips' titles
	requests []plugin.ViewRequest
	handled  []plugin.ActionDo
	multiple bool // the attach action's ChooseFilesOptions.Multiple
}

func (p *filesPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: p.id, Name: p.name, Version: "1", Commands: []plugin.Command{
		{Name: "chat", Title: p.name + " Chat", Mode: plugin.ModeView},
		{Name: "plain", Title: p.name + " Plain", Mode: plugin.ModeView},
	}}
}

func (p *filesPlugin) Init(context.Context, plugin.Host) error { return nil }
func (p *filesPlugin) Close() error                            { return nil }

func (p *filesPlugin) View(_ context.Context, req plugin.ViewRequest) (plugin.View, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if req.ID == "plain" {
		return plugin.View{Kind: plugin.ViewList, Title: "plain", Sections: []plugin.Section{{Items: []plugin.Item{{ID: "x", Title: "x"}}}}}, nil
	}
	payload, _ := json.Marshal(plugin.ChooseFilesOptions{Title: "Attach", Multiple: p.multiple})
	v := plugin.View{Kind: plugin.ViewDetail, Title: "chat", AcceptFiles: true, KeepOnHide: true, Draft: len(p.attached) > 0,
		Detail: &plugin.Detail{Markdown: "chat"},
		Sections: []plugin.Section{{Items: []plugin.Item{{ID: "chat", Title: "chat", Actions: []plugin.Action{
			{ID: "send", Title: "Send", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "send"}},
			{ID: "attach", Title: "Add Attachment…", Shortcut: "Ctrl+O", Do: plugin.ActionDo{Kind: plugin.ActionChooseFiles, Payload: payload}},
		}}}}},
	}
	for i, name := range p.attached {
		id := fmt.Sprintf("c%d", i)
		v.Chips = append(v.Chips, plugin.Chip{ID: id, Title: name, Subtitle: "file",
			Remove: &plugin.Action{ID: "remove", Title: "Remove", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "remove:" + id}}})
	}
	return v, nil
}

func (p *filesPlugin) ReceiveFiles(_ context.Context, f plugin.Files) (plugin.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if f.Instance == "" && !p.take {
		return plugin.Result{}, plugin.ErrFilesNotTaken
	}
	p.received = append(p.received, f)
	for _, path := range f.Paths {
		p.attached = append(p.attached, filepath.Base(path))
	}
	if len(f.Image) > 0 {
		p.attached = append(p.attached, "image.png")
	}
	if f.Instance == "" {
		return plugin.Result{Push: &plugin.ViewRequest{ID: "chat"}}, nil
	}
	return plugin.Result{Refresh: true}, nil
}

func (p *filesPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handled = append(p.handled, a)
	var i int
	if _, err := fmt.Sscanf(a.Target, "remove:c%d", &i); err == nil && i < len(p.attached) {
		p.attached = slices.Delete(p.attached, i, i+1)
	}
	return plugin.Result{Refresh: true}, nil
}

func (p *filesPlugin) state() (calls int, received []plugin.Files, lastReq plugin.ViewRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) > 0 {
		lastReq = p.requests[len(p.requests)-1]
	}
	return p.calls, slices.Clone(p.received), lastReq
}

type filesEnv struct {
	*env
	clip  *hostfake.Clipboard
	a, b  *filesPlugin // a never takes files in the root search, b does
	chose []plugin.ChooseFilesOptions
}

func newFilesEnv(t *testing.T) *filesEnv {
	t.Helper()
	clip := &hostfake.Clipboard{}
	e := &filesEnv{env: &env{rec: &app.Recorder{Clipboard: clip}, tp: &testPlugin{}, now: hostfake.Fixed}, clip: clip,
		a: &filesPlugin{id: "files.a", name: "Alpha"}, b: &filesPlugin{id: "files.b", name: "Bravo", take: true}}
	var err error
	e.Env, err = app.Build(t.Context(), app.Options{
		Plugins:   []plugin.Plugin{e.tp, e.a, e.b},
		Fake:      true,
		Now:       func() time.Time { return e.now },
		Locale:    "en",
		System:    e.rec,
		Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	return e
}

func (e *filesEnv) open(t *testing.T, title string) {
	t.Helper()
	e.typeText(t, title)
	e.key(t, "Enter")
	if s := e.Controller.Snapshot(); s.Depth != 1 {
		t.Fatalf("%s did not open: depth %d", title, s.Depth)
	}
	e.typeText(t, "")
}

func (e *filesEnv) drop(t *testing.T, paths ...string) core.Outcome {
	t.Helper()
	out, err := e.Controller.ReceiveFiles(t.Context(), plugin.Files{Paths: paths, Source: plugin.FilesDropped})
	if err != nil {
		t.Fatalf("ReceiveFiles: %v", err)
	}
	e.Controller.Wait()
	return out
}

func chipTitles(s core.Snapshot) []string {
	var out []string
	for _, c := range s.Chips {
		out = append(out, c.Title)
	}
	return out
}

// TestDropOnRoot: files dropped onto the root search no longer go to the
// FileReceivers (R97): its Files section takes them, and without a plugin
// offering actions for files a toast says they cannot be used
// (rootfiles_test.go has the section).
func TestDropOnRoot(t *testing.T) {
	e := newFilesEnv(t)
	e.typeText(t, "what is this")
	report := filepath.Join(t.TempDir(), "report.pdf")
	writeTestFile(t, report, "%PDF")
	out := e.drop(t, report)
	if calls, _, _ := e.a.state(); calls != 0 {
		t.Fatalf("alpha: %d calls", calls)
	}
	if calls, _, _ := e.b.state(); calls != 0 {
		t.Fatalf("bravo: %d calls", calls)
	}
	s := e.Controller.Snapshot()
	if out.Toast == nil || s.Depth != 0 || len(s.Chips) != 0 || s.Toast == nil || s.Toast.Title != "These files can't be used here" {
		t.Fatalf("after the drop: depth %d chips %v toast %+v", s.Depth, s.Chips, s.Toast)
	}
}

func TestDropNobodyTakes(t *testing.T) {
	e := newFilesEnv(t)
	e.b.take = false
	e.drop(t, "a.txt")
	s := e.Controller.Snapshot()
	if s.Depth != 0 || s.Toast == nil || s.Toast.Title != "These files can't be used here" {
		t.Fatalf("depth %d toast %+v", s.Depth, s.Toast)
	}
}

func TestDropOnAcceptingView(t *testing.T) {
	e := newFilesEnv(t)
	e.open(t, "Bravo Chat")
	_, _, req := e.b.state()
	e.typeText(t, "summarize")
	e.drop(t, "one.txt", "two.png")
	_, got, _ := e.b.state()
	if len(got) != 1 || got[0].Instance != req.Instance || got[0].Instance == "" || got[0].View != "chat" || got[0].Query != "summarize" || len(got[0].Paths) != 2 {
		t.Fatalf("received %+v, view instance %q", got, req.Instance)
	}
	// Alpha's own chat takes them too, whatever it does in the root search.
	e.key(t, "Escape")
	e.key(t, "Escape")
	e.open(t, "Alpha Chat")
	e.drop(t, "three.txt")
	if _, got, _ := e.a.state(); len(got) != 1 || got[0].Instance == "" {
		t.Fatalf("alpha received %+v", got)
	}
}

func TestDropOnOtherView(t *testing.T) {
	e := newFilesEnv(t)
	e.open(t, "Bravo Plain")
	out := e.drop(t, "a.txt")
	if calls, _, _ := e.b.state(); calls != 0 {
		t.Fatalf("the plain view's plugin was called %d times", calls)
	}
	s := e.Controller.Snapshot()
	if out.Toast == nil || s.Toast == nil || s.Toast.Title != "These files can't be used here" || s.Toast.Style != plugin.ToastFailure {
		t.Fatalf("toast %+v", s.Toast)
	}
	if s.AcceptFiles || len(s.Chips) != 0 {
		t.Fatalf("accept %v chips %v", s.AcceptFiles, s.Chips)
	}
}

func TestPasteFiles(t *testing.T) {
	e := newFilesEnv(t)
	paste := func() bool {
		t.Helper()
		_, ok, err := e.Controller.PasteFiles(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		e.Controller.Wait()
		return ok
	}
	// The root search takes the paste too (R97); with nobody offering file
	// actions, only a toast says so, and no FileReceiver sees it.
	e.clip.Set(plugin.ClipboardContent{Files: []string{"x.txt"}})
	if !paste() {
		t.Fatal("the root search took no paste")
	}
	if calls, _, _ := e.b.state(); calls != 0 {
		t.Fatalf("the paste into the root search reached bravo (%d calls)", calls)
	}
	e.open(t, "Bravo Chat")
	if !paste() {
		t.Fatal("files not pasted")
	}
	e.clip.Set(plugin.ClipboardContent{Image: []byte("\x89PNG fake")})
	if !paste() {
		t.Fatal("image not pasted")
	}
	_, got, _ := e.b.state()
	if len(got) != 2 || got[0].Source != plugin.FilesPasted || !slices.Equal(got[0].Paths, []string{"x.txt"}) ||
		len(got[1].Paths) != 0 || string(got[1].Image) != "\x89PNG fake" {
		t.Fatalf("received %+v", got)
	}
	if !e.clip.HasFilesOrImage() {
		t.Fatal("the fake clipboard's probe misses the image")
	}
	for _, c := range []plugin.ClipboardContent{
		{Text: "just text"},
		{Text: "text", Image: []byte("\x89PNG")},                          // real text: pasted as text
		{Text: "https://example.com/a.png\r\n", Image: []byte("\x89PNG")}, // a spreadsheet cell
		{Files: []string{"secret.txt"}, Exclude: true},
		{},
	} {
		e.clip.Set(c)
		if paste() || e.clip.HasFilesOrImage() {
			t.Fatalf("%+v was taken", c)
		}
	}
	// An image with text that only describes it (R96): its address, as
	// browsers' "Copy Image" put, or a chat app's placeholder.
	for _, text := range []string{"https://example.com/a.png", "[图片]"} {
		e.clip.Set(plugin.ClipboardContent{Text: text, Image: []byte("\x89PNG two")})
		if !e.clip.HasFilesOrImage() || !paste() {
			t.Fatalf("the image next to %q was not taken", text)
		}
	}
	if s := e.Controller.Snapshot(); !slices.Equal(chipTitles(s), []string{"x.txt", "image.png", "image.png", "image.png"}) {
		t.Fatalf("chips %v", chipTitles(s))
	}
}

func TestChooseFiles(t *testing.T) {
	e := newFilesEnv(t)
	var answer []string
	e.Engine.SetFileChooser(func(_ context.Context, o plugin.ChooseFilesOptions) ([]string, error) {
		e.chose = append(e.chose, o)
		return answer, nil
	})
	e.open(t, "Bravo Chat")

	// Cancelled: nothing.
	e.key(t, "Ctrl+O")
	if _, got, _ := e.b.state(); len(got) != 0 || len(e.chose) != 1 || e.chose[0].Title != "Attach" {
		t.Fatalf("received %+v, dialogs %+v", got, e.chose)
	}
	// One file only, though several came back.
	answer = []string{"a.txt", "b.txt"}
	e.key(t, "Ctrl+O")
	if _, got, _ := e.b.state(); len(got) != 1 || got[0].Source != plugin.FilesChosen || !slices.Equal(got[0].Paths, []string{"a.txt"}) {
		t.Fatalf("received %+v", got)
	}
	// Several, from the action panel.
	e.b.mu.Lock()
	e.b.multiple = true
	e.b.mu.Unlock()
	e.Controller.Refresh(t.Context())
	e.Controller.Wait()
	if _, err := e.Controller.RunAction(t.Context(), "files.b/chat", "attach"); err != nil {
		t.Fatal(err)
	}
	e.Controller.Wait()
	if _, got, _ := e.b.state(); len(got) != 2 || !slices.Equal(got[1].Paths, answer) || !e.chose[2].Multiple {
		t.Fatalf("received %+v, dialogs %+v", got, e.chose)
	}
	if s := e.Controller.Snapshot(); !slices.Equal(chipTitles(s), []string{"a.txt", "a.txt", "b.txt"}) {
		t.Fatalf("chips %v", chipTitles(s))
	}
}

func TestChooseFilesElsewhere(t *testing.T) {
	e := newFilesEnv(t)
	// No open dialog: the action fails with a toast.
	e.open(t, "Bravo Chat")
	if _, err := e.Controller.Key(t.Context(), "Ctrl+O"); err == nil {
		t.Fatal("no error without an open dialog")
	}
	if s := e.Controller.Snapshot(); s.Toast == nil || s.Toast.Style != plugin.ToastFailure {
		t.Fatalf("toast %+v", s.Toast)
	}
	// The engine never opens one for anybody else (AI tools).
	shown := false
	e.Engine.SetFileChooser(func(context.Context, plugin.ChooseFilesOptions) ([]string, error) {
		shown = true
		return []string{"a.txt"}, nil
	})
	_, err := e.Engine.Run(t.Context(), core.Entry{Plugin: "files.b"}, plugin.Action{ID: "attach", Do: plugin.ActionDo{Kind: plugin.ActionChooseFiles}}, false)
	if !errors.Is(err, core.ErrChooseFilesHere) || shown {
		t.Fatalf("Engine.Run: %v, dialog shown %v", err, shown)
	}
}

func TestRemoveChip(t *testing.T) {
	e := newFilesEnv(t)
	e.open(t, "Bravo Chat")
	e.drop(t, "one.txt", "two.txt", "three.txt")
	if _, err := e.Controller.RemoveChip(t.Context(), "c1"); err != nil {
		t.Fatal(err)
	}
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); !slices.Equal(chipTitles(s), []string{"one.txt", "three.txt"}) {
		t.Fatalf("chips %v", chipTitles(s))
	}
	e.b.mu.Lock()
	handled := slices.Clone(e.b.handled)
	e.b.mu.Unlock()
	if len(handled) != 1 || handled[0].Target != "remove:c1" {
		t.Fatalf("handled %+v", handled)
	}
	if _, err := e.Controller.RemoveChip(t.Context(), "nosuch"); err == nil {
		t.Fatal("removed an unknown chip")
	}
}
