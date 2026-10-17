package core_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// actionsPlugin offers actions for the files handed to the root search
// (plugin.FileActionsProvider), with the files permission unless noPerm.
type actionsPlugin struct {
	id     string
	noPerm bool

	mu      sync.Mutex
	queries []plugin.FilesQuery
	handled []plugin.ActionDo
}

func (p *actionsPlugin) Manifest() plugin.Manifest {
	m := plugin.Manifest{ID: p.id, Name: p.id, Version: "1"}
	if !p.noPerm {
		m.Permissions = []plugin.Permission{plugin.PermFiles}
	}
	return m
}

func (p *actionsPlugin) Init(context.Context, plugin.Host) error { return nil }
func (p *actionsPlugin) Close() error                            { return nil }

func (p *actionsPlugin) FileItems(_ context.Context, q plugin.FilesQuery) ([]plugin.Item, error) {
	p.mu.Lock()
	p.queries = append(p.queries, q)
	p.mu.Unlock()
	cb := func(id string) []plugin.Action {
		return []plugin.Action{{ID: id, Title: id, Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: id, Payload: plugin.MustPayload(q.Files[0].Path)}}}
	}
	items := []plugin.Item{
		{ID: "open", Title: "Open the File", Actions: []plugin.Action{{ID: "open", Title: "Open", Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: "kept"}}}},
		{ID: "zip", Title: "Compress to ZIP", Keywords: []string{"archive"}, Actions: cb("zip")},
		{ID: "ask", Title: "Ask About It", Actions: cb("ask")},
	}
	if q.Text == "" {
		items[2].Score = 1 // a clear hit while nothing is typed
	} else {
		items = append(items, plugin.Item{ID: "typed", Title: "Do: " + q.Text, Score: 0.6, Actions: cb("typed")})
	}
	return items, nil
}

// View is a view kept across a hide (KeepOnHide).
func (p *actionsPlugin) View(context.Context, plugin.ViewRequest) (plugin.View, error) {
	return plugin.View{Kind: plugin.ViewDetail, Title: "kept", KeepOnHide: true, Detail: &plugin.Detail{Markdown: "kept"}}, nil
}

func (p *actionsPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handled = append(p.handled, a)
	return plugin.Result{Hide: true}, nil
}

func (p *actionsPlugin) lastQuery() plugin.FilesQuery {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queries) == 0 {
		return plugin.FilesQuery{}
	}
	return p.queries[len(p.queries)-1]
}

type rootFilesEnv struct {
	*env
	clip       *hostfake.Clipboard
	fa, noPerm *actionsPlugin
	chosen     []string
	dialogs    []plugin.ChooseFilesOptions
}

func newRootFilesEnv(t *testing.T) *rootFilesEnv {
	t.Helper()
	clip := &hostfake.Clipboard{}
	e := &rootFilesEnv{env: &env{rec: &app.Recorder{Clipboard: clip}, tp: &testPlugin{}, now: hostfake.Fixed}, clip: clip,
		fa: &actionsPlugin{id: "files.actions"}, noPerm: &actionsPlugin{id: "files.noperm", noPerm: true}}
	var err error
	e.Env, err = app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{e.tp, e.fa, e.noPerm}, Fake: true, Now: func() time.Time { return e.now },
		Locale: "en", System: e.rec, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	e.Engine.SetFileChooser(func(_ context.Context, o plugin.ChooseFilesOptions) ([]string, error) {
		e.dialogs = append(e.dialogs, o)
		return e.chosen, nil
	})
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	return e
}

func (e *rootFilesEnv) drop(t *testing.T, paths ...string) core.Outcome {
	t.Helper()
	out, err := e.Controller.ReceiveFiles(t.Context(), plugin.Files{Paths: paths, Source: plugin.FilesDropped})
	if err != nil {
		t.Fatalf("ReceiveFiles: %v", err)
	}
	e.Controller.Wait()
	return out
}

// itemTitles are the titles of the items of a section.
func itemTitles(s core.Section) []string {
	var out []string
	for _, it := range s.Items {
		out = append(out, it.Title)
	}
	return out
}

// testFiles writes a.txt, b.png and a folder.
func testFiles(t *testing.T) (txt, png, dir string) {
	t.Helper()
	root := t.TempDir()
	txt, png, dir = filepath.Join(root, "a.txt"), filepath.Join(root, "b.png"), filepath.Join(root, "photos")
	writeTestFile(t, txt, "hello")
	writeTestFile(t, png, "\x89PNG")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return txt, png, dir
}

// TestRootFilesSection: files dropped onto the root search show as chips,
// and the actions of the plugins with the files permission form its first
// section, the clear hit first; plugins without the permission never see
// the paths. The files are described from their metadata.
func TestRootFilesSection(t *testing.T) {
	e := newRootFilesEnv(t)
	txt, png, dir := testFiles(t)
	e.drop(t, txt, png, dir, filepath.Join(filepath.Dir(txt), "missing.txt"))
	s := e.Controller.Snapshot()
	if s.Depth != 0 || len(s.Sections) == 0 || s.Sections[0].Title != "Files" {
		t.Fatalf("snapshot %+v", s.Sections)
	}
	if got := itemTitles(s.Sections[0]); !slices.Equal(got, []string{"Ask About It", "Open the File", "Compress to ZIP"}) {
		t.Fatalf("files section %q", got)
	}
	for _, it := range s.Sections[0].Items {
		if !it.Sensitive || !it.Transient || !it.Files || !it.Context {
			t.Fatalf("entry %+v is not marked", it)
		}
	}
	if s.Selected != 0 {
		t.Fatalf("selected %d", s.Selected)
	}
	if got := chipTitles(s); !slices.Equal(got, []string{"a.txt", "b.png", "photos"}) {
		t.Fatalf("chips %v", got)
	}
	if c := s.Chips[2]; c.Subtitle != "Folder" || c.Icon.Name != "folder" || c.Image != "" || c.Remove == nil {
		t.Fatalf("folder chip %+v", c)
	}
	if c := s.Chips[0]; c.Subtitle != "5 B" || c.Icon.Name != "text" {
		t.Fatalf("text chip %+v", c)
	}
	q := e.fa.lastQuery()
	if q.Source != plugin.FilesDropped || len(q.Files) != 3 || q.Text != "" {
		t.Fatalf("query %+v", q)
	}
	if f := q.Files[1]; f.Name != "b.png" || f.Kind != plugin.FileImage || f.MediaType != "image/png" || f.Size != 4 || f.Pasted {
		t.Fatalf("image file %+v", f)
	}
	if f := q.Files[2]; !f.Dir || f.Kind != plugin.FileFolder {
		t.Fatalf("folder %+v", f)
	}
	if len(e.noPerm.queries) != 0 {
		t.Fatal("a plugin without the files permission was asked")
	}
	// The rest of the root search follows.
	if len(s.Sections) < 2 {
		t.Fatalf("only the files section: %+v", s.Sections)
	}

	// Typing filters the actions; the section stays first.
	s = e.typeText(t, "zip")
	if got := itemTitles(s.Sections[0]); s.Sections[0].Title != "Files" || len(got) != 2 || !slices.Contains(got, "Do: zip") || !slices.Contains(got, "Compress to ZIP") {
		t.Fatalf("typed: %q %q", s.Sections[0].Title, got)
	}
	if q := e.fa.lastQuery(); q.Text != "zip" {
		t.Fatalf("query text %q", q.Text)
	}
	s = e.typeText(t, "archive")
	if got := itemTitles(s.Sections[0]); !slices.Contains(got, "Compress to ZIP") || slices.Contains(got, "Open the File") {
		t.Fatalf("by keyword: %q", got)
	}
	e.typeText(t, "")

	// An action runs with the paths it carries; it is not recorded.
	if out := e.key(t, "Enter"); !out.Hide {
		t.Fatalf("outcome %+v", out)
	}
	if len(e.fa.handled) != 1 || e.fa.handled[0].Target != "ask" || !strings.Contains(string(e.fa.handled[0].Payload), "a.txt") {
		t.Fatalf("handled %+v", e.fa.handled)
	}
	if recent, _ := e.Services.DB.Recent(10); len(recent) != 0 {
		t.Fatalf("recorded %d recent entries", len(recent))
	}
}

// TestRootFilesRemoveAndClear: a chip's × drops its file, the last one all
// of them; Escape drops them before it hides; a hide drops them unless the
// stack above is kept; new files replace them.
func TestRootFilesRemoveAndClear(t *testing.T) {
	e := newRootFilesEnv(t)
	txt, png, dir := testFiles(t)
	e.drop(t, txt, png)
	if _, err := e.Controller.RemoveChip(t.Context(), "file:0"); err != nil {
		t.Fatal(err)
	}
	e.Controller.Wait()
	if got := chipTitles(e.Controller.Snapshot()); !slices.Equal(got, []string{"b.png"}) {
		t.Fatalf("after removing: %v", got)
	}
	if _, err := e.Controller.RemoveChip(t.Context(), "file:7"); err == nil {
		t.Fatal("removed an unknown chip")
	}
	if _, err := e.Controller.RemoveChip(t.Context(), "file:0"); err != nil {
		t.Fatal(err)
	}
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); len(s.Chips) != 0 || len(s.Sections) > 0 && s.Sections[0].Title == "Files" {
		t.Fatalf("after removing the last: %+v", s.Chips)
	}

	// New files replace the ones before.
	e.drop(t, txt)
	e.drop(t, dir)
	if got := chipTitles(e.Controller.Snapshot()); !slices.Equal(got, []string{"photos"}) {
		t.Fatalf("replaced: %v", got)
	}
	// Escape: the text first, then the files, then the window.
	e.typeText(t, "x")
	if out := e.key(t, "Escape"); out.Hide {
		t.Fatal("hid with text typed")
	}
	if out := e.key(t, "Escape"); out.Hide || len(e.Controller.Snapshot().Chips) != 0 {
		t.Fatalf("Escape did not drop the files first: %+v", out)
	}
	if out := e.key(t, "Escape"); !out.Hide {
		t.Fatal("the third Escape did not hide")
	}

	// A hide keeps them while a view above is kept, and they are there when
	// the user goes back.
	e.drop(t, txt)
	if _, err := e.Controller.RunAction(t.Context(), "files.actions/open", "open"); err != nil {
		t.Fatal(err)
	}
	e.Controller.Wait()
	e.Controller.Hidden()
	e.Controller.Show(t.Context(), core.ShowOptions{})
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); s.Depth != 1 {
		t.Fatalf("the view was not kept: depth %d", s.Depth)
	}
	e.key(t, "Escape")
	if got := chipTitles(e.Controller.Snapshot()); !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("back at the root: %v", got)
	}
	e.key(t, "Escape")

	// A hide drops them; the next show starts without them.
	e.drop(t, txt)
	e.Controller.Hidden()
	e.Controller.Show(t.Context(), core.ShowOptions{})
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); len(s.Chips) != 0 {
		t.Fatalf("kept after a hide: %v", chipTitles(s))
	}
}

// TestRootFilesPastedImage: an image pasted into the root search is written
// to a temporary PNG file the providers get as Pasted, shown with its
// thumbnail, and deleted once the files are dropped.
func TestRootFilesPastedImage(t *testing.T) {
	defer core.SetPastedGrace(0)()
	e := newRootFilesEnv(t)
	img := []byte("\x89PNG\r\n\x1a\nfake")
	e.clip.Set(plugin.ClipboardContent{Image: img})
	_, ok, err := e.Controller.PasteFiles(t.Context())
	e.Controller.Wait()
	if err != nil || !ok {
		t.Fatalf("paste: %v %v", ok, err)
	}
	q := e.fa.lastQuery()
	if q.Source != plugin.FilesPasted || len(q.Files) != 1 {
		t.Fatalf("query %+v", q)
	}
	f := q.Files[0]
	if !f.Pasted || f.Name != "Pasted Image.png" || f.Kind != plugin.FileImage || f.Size != int64(len(img)) || filepath.Base(f.Path) != f.Name {
		t.Fatalf("pasted file %+v", f)
	}
	if data, err := os.ReadFile(f.Path); err != nil || string(data) != string(img) {
		t.Fatalf("temporary file: %q %v", data, err)
	}
	if st, err := os.Stat(f.Path); err != nil || runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("temporary file mode: %v %v", st.Mode(), err)
	}
	if c := e.Controller.Snapshot().Chips; len(c) != 1 || c[0].Image != f.Path {
		t.Fatalf("chip %+v", c)
	}
	e.key(t, "Escape") // drops the files
	if _, err := os.Stat(filepath.Dir(f.Path)); !os.IsNotExist(err) {
		t.Fatalf("the temporary file was kept: %v", err)
	}
	// Text is pasted as text, not taken.
	e.clip.Set(plugin.ClipboardContent{Text: "words"})
	if _, ok, _ := e.Controller.PasteFiles(t.Context()); ok {
		t.Fatal("text was taken")
	}
}

// TestRootFilesChoose: Ctrl+O in the root search shows the open dialog, and
// the files chosen show in its Files section; cancelling changes nothing.
func TestRootFilesChoose(t *testing.T) {
	e := newRootFilesEnv(t)
	txt, png, _ := testFiles(t)
	e.key(t, "Ctrl+O")
	if len(e.dialogs) != 1 || !e.dialogs[0].Multiple || e.dialogs[0].Title != "Choose Files" || len(e.Controller.Snapshot().Chips) != 0 {
		t.Fatalf("dialogs %+v", e.dialogs)
	}
	e.chosen = []string{txt, png}
	e.key(t, "Ctrl+O")
	if got := chipTitles(e.Controller.Snapshot()); !slices.Equal(got, []string{"a.txt", "b.png"}) {
		t.Fatalf("chips %v", got)
	}
	if q := e.fa.lastQuery(); q.Source != plugin.FilesChosen {
		t.Fatalf("source %q", q.Source)
	}
}

// TestRootFilesNobody: without a plugin offering actions, or without a file
// that exists, the files are not kept and a toast says so.
func TestRootFilesNobody(t *testing.T) {
	e := newRootFilesEnv(t)
	out := e.drop(t, filepath.Join(t.TempDir(), "gone.txt"))
	if out.Toast == nil || len(e.Controller.Snapshot().Chips) != 0 {
		t.Fatalf("a missing file: %+v", out)
	}
}
