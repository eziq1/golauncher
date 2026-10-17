//go:build darwin

// Tests that read or write the real pasteboard are opt-in: they run only
// when GOLAUNCHER_CLIPBOARD_TESTS=1 is set and -short is not given, e.g.
//
//	GOLAUNCHER_CLIPBOARD_TESTS=1 go test ./internal/winapi/clip
//
// Every item and type on the pasteboard before the test is snapshotted and
// put back afterwards. No test sends keystrokes (PasteTo).

package clip

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/darwin/ns"
)

const clipboardTestsEnv = "GOLAUNCHER_CLIPBOARD_TESTS"

// savedItem is one pasteboard item: its types and their data.
type savedItem struct {
	types []string
	data  [][]byte
}

func snapshot() []savedItem {
	var items []savedItem
	ns.Pool(func() {
		all := ns.Send(pasteboard(), "pasteboardItems")
		for i := range ns.SendInt(all, "count") {
			it := ns.Send(all, "objectAtIndex:", i)
			var s savedItem
			for _, typ := range ns.Strings(ns.Send(it, "types")) {
				if d := ns.Send(it, "dataForType:", ns.String(typ)); d != 0 {
					s.types = append(s.types, typ)
					s.data = append(s.data, ns.Bytes(d))
				}
			}
			items = append(items, s)
		}
	})
	return items
}

func restore(items []savedItem) {
	ns.Pool(func() {
		objs := make([]ns.ID, 0, len(items))
		for _, s := range items {
			it := ns.Send(ns.Send(ns.Send(ns.Class("NSPasteboardItem"), "alloc"), "init"), "autorelease")
			for i, typ := range s.types {
				ns.SendBool(it, "setData:forType:", ns.Data(s.data[i]), ns.String(typ))
			}
			objs = append(objs, it)
		}
		pb := pasteboard()
		ns.Send(pb, "clearContents")
		if len(objs) > 0 {
			ns.SendBool(pb, "writeObjects:", ns.Array(objs...))
		}
	})
}

// realClipboard skips the test unless real-pasteboard tests are enabled
// and restores the user's pasteboard when the test ends.
func realClipboard(t *testing.T) {
	t.Helper()
	if os.Getenv(clipboardTestsEnv) != "1" {
		t.Skipf("uses the real pasteboard; set %s=1 to run", clipboardTestsEnv)
	}
	if testing.Short() {
		t.Skip("uses the real pasteboard")
	}
	orig := snapshot()
	t.Cleanup(func() { restore(orig) })
}

func TestTextRoundTrip(t *testing.T) {
	realClipboard(t)
	const s = "golauncher 测试 ✓"
	if err := WriteText(s); err != nil {
		t.Fatal(err)
	}
	c, err := Read()
	if err != nil || c.Text != s || c.Exclude || len(c.Files) != 0 || c.Image != nil {
		t.Fatalf("Read = %+v, %v", c, err)
	}
}

func TestSecretIsExcluded(t *testing.T) {
	realClipboard(t)
	if err := WriteSecret("hunter2"); err != nil {
		t.Fatal(err)
	}
	c, err := Read()
	if err != nil || !c.Exclude || c.Text != "" {
		t.Fatalf("Read = %+v, %v", c, err)
	}
}

func TestImageRoundTrip(t *testing.T) {
	realClipboard(t)
	var b bytes.Buffer
	if err := png.Encode(&b, testImage(3, 2)); err != nil {
		t.Fatal(err)
	}
	if err := WriteImage(b.Bytes()); err != nil {
		t.Fatal(err)
	}
	c, err := Read()
	if err != nil || c.Image == nil {
		t.Fatalf("Read = %+v, %v", c, err)
	}
	if img, err := png.Decode(bytes.NewReader(c.Image)); err != nil || img.Bounds().Dx() != 3 {
		t.Fatalf("image: %v", err)
	}
	if c, _ := ReadWith(ReadOptions{}); c.Image != nil {
		t.Error("image read without Images")
	}
	// TIFF only, as some apps copy: converted to PNG.
	tiff := pngToTIFF(b.Bytes())
	if err := write("", false, pbData{typeTIFF, tiff}); err != nil {
		t.Fatal(err)
	}
	if c, _ := Read(); !bytes.HasPrefix(c.Image, pngSignature) {
		t.Error("TIFF not converted to PNG")
	}
}

func TestFilesRoundTrip(t *testing.T) {
	realClipboard(t)
	dir := t.TempDir()
	files := []string{filepath.Join(dir, "a b.txt"), filepath.Join(dir, "图片.png")}
	for _, f := range files {
		os.WriteFile(f, nil, 0o644)
	}
	if err := WriteFiles(files); err != nil {
		t.Fatal(err)
	}
	c, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	// /var is a link to /private/var: compare the resolved paths.
	var got []string
	for _, f := range c.Files {
		r, _ := filepath.EvalSymlinks(f)
		got = append(got, r)
	}
	var want []string
	for _, f := range files {
		r, _ := filepath.EvalSymlinks(f)
		want = append(want, r)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("files = %q, want %q", c.Files, files)
	}
}

func TestWatch(t *testing.T) {
	realClipboard(t)
	changed := make(chan struct{}, 4)
	stop, err := Watch(func() { changed <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	// Our own write is ignored...
	if err := WriteText("own"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
		t.Fatal("own write reported")
	case <-time.After(3 * pollInterval):
	}
	// ...a write by someone else is not.
	watchOwnWrites.Store(true)
	defer watchOwnWrites.Store(false)
	if err := WriteText("other"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(5 * pollInterval):
		t.Fatal("change not reported")
	}
	stop()
	stop()
}

func TestForegroundWindow(t *testing.T) {
	// Read-only: whatever app is frontmost (none in some CI sessions).
	if p := ForegroundWindow(); p != 0 && int(p) < 0 {
		t.Errorf("ForegroundWindow = %d", p)
	}
	_ = Trusted()
	if err := PasteTo(0); err == nil {
		t.Error("PasteTo(0) succeeded")
	}
}

func TestCopySelectionUnsupported(t *testing.T) {
	if _, err := CopySelection(1, CopyOptions{}); err == nil {
		t.Error("CopySelection succeeded")
	}
}
