package files

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func received(t *testing.T, p *Plugin, files ...plugin.FileInfo) []plugin.Item {
	t.Helper()
	items, err := p.FileItems(t.Context(), plugin.FilesQuery{Files: files, Source: plugin.FilesDropped})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func fileInfo(t *testing.T, path string, media string) plugin.FileInfo {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f := plugin.FileInfo{Path: path, Name: filepath.Base(path), Size: st.Size(), MediaType: media, Kind: plugin.FileOther}
	if st.IsDir() {
		f.Dir, f.Kind = true, plugin.FileFolder
	}
	return f
}

// TestFileItems (R97): the actions for files handed to the root search, by
// what they are.
func TestFileItems(t *testing.T) {
	p, _ := newPlugin(t)
	dir := t.TempDir()
	txt, png, sub := filepath.Join(dir, "notes.txt"), filepath.Join(dir, "shot.png"), filepath.Join(dir, "photos")
	for _, f := range []string{txt, png} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if items := received(t, p); items != nil {
		t.Fatalf("no files: %v", ids(items))
	}
	one := received(t, p, fileInfo(t, txt, "text/plain"))
	if got := ids(one); !slices.Equal(got, []string{"open", "open-with", "reveal", "copy-selected", "copy-name", "zip", "sha256"}) {
		t.Fatalf("one file: %v", got)
	}
	if a := find(t, one, "open").Actions[0]; a.Do.Kind != plugin.ActionOpen || a.Do.Target != txt {
		t.Fatalf("open %+v", a)
	}
	if o := openOpts(t, find(t, one, "open-with").Actions[0]); o.Verb != "openas" {
		t.Fatalf("open with %+v", o)
	}
	if o := openOpts(t, find(t, one, "reveal").Actions[0]); !o.Reveal {
		t.Fatalf("reveal %+v", o)
	}
	if a := find(t, one, "copy-name").Actions[0]; a.Do.Kind != plugin.ActionCopyText || a.Do.Target != "notes.txt" {
		t.Fatalf("copy name %+v", a)
	}
	if a := find(t, one, "copy-selected").Actions[0]; a.Do.Target != txt {
		t.Fatalf("copy path %+v", a)
	}

	// A PNG can be copied as an image.
	image := received(t, p, fileInfo(t, png, "image/png"))
	if a := find(t, image, "copy-image").Actions[0]; a.Do.Kind != plugin.ActionCopyImage || a.Do.Target != png {
		t.Fatalf("copy image %+v", a)
	}

	// Several files and a folder: no open, no checksum.
	many := received(t, p, fileInfo(t, txt, "text/plain"), fileInfo(t, sub, ""))
	if got := ids(many); !slices.Equal(got, []string{"reveal", "copy-selected", "copy-name", "zip"}) {
		t.Fatalf("two files: %v", got)
	}
	if a := find(t, many, "copy-name").Actions[0]; a.Do.Target != "notes.txt\r\nphotos" {
		t.Fatalf("names %q", a.Do.Target)
	}
	folder := received(t, p, fileInfo(t, sub, ""))
	if got := ids(folder); slices.Contains(got, "open-with") || slices.Contains(got, "sha256") || !slices.Contains(got, "open") {
		t.Fatalf("folder: %v", got)
	}

	// A pasted image (the host's temporary file): only opened or copied.
	pasted := fileInfo(t, png, "image/png")
	pasted.Pasted, pasted.Name = true, "Pasted Image.png"
	if got := ids(received(t, p, pasted)); !slices.Equal(got, []string{"open", "open-with", "copy-image"}) {
		t.Fatalf("pasted: %v", got)
	}
}

// TestFileItemsMac: Finder takes the place of File Explorer, and there is
// no Open With.
func TestFileItemsMac(t *testing.T) {
	e := newMacPlugin(t)
	dir := t.TempDir()
	txt := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(txt, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	items := received(t, e.p, fileInfo(t, txt, "text/plain"))
	if got := ids(items); slices.Contains(got, "open-with") {
		t.Fatalf("mac: %v", got)
	}
	if it := find(t, items, "reveal"); it.Title != "Show in Finder" {
		t.Fatalf("reveal %q", it.Title)
	}
	if a := find(t, items, "copy-selected").Actions[0]; a.Do.Target != txt {
		t.Fatalf("copy path %+v", a)
	}
}

// TestChooseFilesItem: "Choose Files…" is a root item for what starts its
// words, and opens the dialog for the root search.
func TestChooseFilesItem(t *testing.T) {
	p, _ := newPlugin(t)
	for text, want := range map[string]bool{"choose": true, "Cho": true, "files": true, "选择": true, "tea": false, "c": false, "": false} {
		items, err := p.RootItems(t.Context(), plugin.Query{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		if got := len(items) == 1; got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
	items, _ := p.RootItems(t.Context(), plugin.Query{Text: "choose"})
	it := items[0]
	if a := it.Actions[0]; a.Do.Kind != plugin.ActionChooseFiles || !it.Sensitive || it.Transient {
		t.Fatalf("item %+v", it)
	}
}
