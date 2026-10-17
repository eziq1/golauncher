package ui_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// fileActions offers one action for the files handed to the root search.
type fileActions struct{}

func (fileActions) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "fileactions", Name: "File Actions", Version: "1", Permissions: []plugin.Permission{plugin.PermFiles}}
}
func (fileActions) Init(context.Context, plugin.Host) error { return nil }
func (fileActions) Close() error                            { return nil }

func (fileActions) FileItems(_ context.Context, q plugin.FilesQuery) ([]plugin.Item, error) {
	return []plugin.Item{{ID: "inspect", Title: "Inspect " + q.Files[0].Name, Score: 1,
		Actions: []plugin.Action{{ID: "copy", Title: "Copy", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: q.Files[0].Path}}}}}, nil
}

func newRootFilesLauncher(t *testing.T) (*launcherTest, *hostfake.Clipboard) {
	t.Helper()
	clip := &hostfake.Clipboard{}
	rec := &app.Recorder{Clipboard: clip}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{testPlugin{}, fileActions{}}, Fake: true, Locale: "en", System: rec, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	lt := &launcherTest{Env: env, t: t, rec: rec, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	return lt, clip
}

// TestRootFilesUI (R97): files dropped onto, pasted into (Ctrl+V) or chosen
// for (Ctrl+O) the root search show as chips with the Files section at the
// top; the chips never move the search bar, and their × drops the file.
func TestRootFilesUI(t *testing.T) {
	lt, clip := newRootFilesLauncher(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before, ok := lt.tt.Find("Search")
	if !ok {
		t.Fatalf("no search field in %q", lt.tt.Texts())
	}

	lt.l.DropFiles([]string{a})
	lt.step()
	lt.wantText("Files")
	lt.wantText("Inspect a.txt")
	lt.wantText("a.txt")
	if after, _ := lt.tt.Find("Search"); after != before {
		t.Fatalf("search field %+v with chips, %+v without", after, before)
	}
	if s := lt.Controller.Snapshot(); s.Sections[0].Title != "Files" || s.Selected != 0 {
		t.Fatalf("sections %+v selected %d", s.Sections, s.Selected)
	}

	// Ctrl+V with files on the clipboard: they replace the dropped one.
	clip.Set(plugin.ClipboardContent{Files: []string{b}})
	lt.key(ui.Primary(), mui.KeyV)
	lt.wantText("Inspect b.txt")
	if s := lt.Controller.Snapshot(); s.Text != "" || len(s.Chips) != 1 || s.Chips[0].Title != "b.txt" {
		t.Fatalf("after the paste: text %q chips %+v", s.Text, s.Chips)
	}

	// The chip's × drops the file: the section goes.
	if err := lt.tt.Click("Remove b.txt"); err != nil {
		t.Fatalf("%v in %q", err, lt.tt.Texts())
	}
	lt.step()
	if s := lt.Controller.Snapshot(); len(s.Chips) != 0 || lt.tt.HasText("Inspect b.txt") {
		t.Fatalf("after removing: %+v", s.Chips)
	}
	if again, _ := lt.tt.Find("Search"); again != before {
		t.Fatalf("search field %+v after removing, %+v before", again, before)
	}

	// Ctrl+O: the open dialog.
	lt.host.chosen = []string{a, b}
	lt.key(ui.Primary(), mui.KeyO)
	if s := lt.Controller.Snapshot(); len(lt.host.chooseOpts) != 1 || !lt.host.chooseOpts[0].Multiple || !slices.Equal(chipNames(s.Chips), []string{"a.txt", "b.txt"}) {
		t.Fatalf("dialogs %+v chips %+v", lt.host.chooseOpts, s.Chips)
	}
	// Escape drops the files before it hides.
	lt.key(0, mui.KeyEscape)
	if s := lt.Controller.Snapshot(); len(s.Chips) != 0 || lt.host.hidden.Load() != 0 {
		t.Fatalf("after Escape: chips %+v hidden %d", s.Chips, lt.host.hidden.Load())
	}
}

func chipNames(chips []plugin.Chip) []string {
	var out []string
	for _, c := range chips {
		out = append(out, c.Title)
	}
	return out
}
