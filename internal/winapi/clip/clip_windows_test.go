//go:build windows

package clip

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// realClipboard skips the test in -short mode and restores the user's
// clipboard text when the test ends.
func realClipboard(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("uses the real clipboard")
	}
	orig, err := Read()
	if err != nil {
		t.Skipf("clipboard unavailable: %v", err)
	}
	t.Cleanup(func() {
		if orig.Text != "" && !orig.Exclude {
			_ = WriteText(orig.Text)
		}
	})
}

func TestTextRoundTrip(t *testing.T) {
	realClipboard(t)
	want := "héllo 世界 🎉\r\nsecond line"
	if err := WriteText(want); err != nil {
		t.Fatal(err)
	}
	c, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if c.Text != want {
		t.Fatalf("Text = %q, want %q", c.Text, want)
	}
	if c.Exclude || len(c.Files) != 0 || len(c.Image) != 0 {
		t.Fatalf("unexpected content %+v", c)
	}
}

func TestImageRoundTrip(t *testing.T) {
	realClipboard(t)
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(7, 4)); err != nil {
		t.Fatal(err)
	}
	if err := WriteImage(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	c, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c.Image, buf.Bytes()) {
		t.Fatalf("PNG not returned unchanged (%d bytes, want %d)", len(c.Image), buf.Len())
	}

	// Without the PNG format the DIB is converted.
	if err := write(clipData{cfDIB, imageToDIB(testImage(7, 4))}); err != nil {
		t.Fatal(err)
	}
	c, err = Read()
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(c.Image))
	if err != nil {
		t.Fatalf("decode converted DIB: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 7 || b.Dy() != 4 {
		t.Fatalf("size %v, want 7x4", b)
	}
	if err := WriteImage([]byte("not a png")); err == nil {
		t.Fatal("WriteImage accepted invalid PNG")
	}
}

func TestFilesRoundTrip(t *testing.T) {
	realClipboard(t)
	dir := t.TempDir()
	want := []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "文件 b.png")}
	for _, p := range want {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteFiles(want); err != nil {
		t.Fatal(err)
	}
	c, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.Files, want) {
		t.Fatalf("Files = %q, want %q", c.Files, want)
	}
	if err := WriteFiles(nil); err == nil {
		t.Fatal("WriteFiles(nil) succeeded")
	}
}

func TestExcludeFormats(t *testing.T) {
	realClipboard(t)
	f := formats()
	zero := make([]byte, 4)
	one := binary.LittleEndian.AppendUint32(nil, 1)
	for _, tc := range []struct {
		name    string
		extra   clipData
		exclude bool
	}{
		{"monitor", clipData{f.exclude, zero}, true},
		{"viewer ignore", clipData{f.viewerIgnore, zero}, true},
		{"history 0", clipData{f.history, zero}, true},
		{"history 1", clipData{f.history, one}, false},
	} {
		if err := write(clipData{cfUnicodeText, utf16Bytes("secret")}, tc.extra); err != nil {
			t.Fatal(err)
		}
		c, err := Read()
		if err != nil {
			t.Fatal(err)
		}
		if c.Exclude != tc.exclude {
			t.Errorf("%s: Exclude = %v, want %v", tc.name, c.Exclude, tc.exclude)
		}
		if c.Exclude && c.Text != "" {
			t.Errorf("%s: excluded content was read", tc.name)
		}
	}
}

func TestProcessName(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got := processName(uint32(os.Getpid())); !strings.EqualFold(got, filepath.Base(exe)) {
		t.Fatalf("processName = %q, want %q", got, filepath.Base(exe))
	}
	if got := processName(0xFFFFFFF0); got != "" {
		t.Fatalf("processName(bogus) = %q", got)
	}
}

func TestWatch(t *testing.T) {
	realClipboard(t)
	fired := make(chan struct{}, 10)
	stop, err := Watch(func() { fired <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	// Our own writes are filtered out.
	if err := WriteText("own write"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
		t.Error("Watch fired for our own write")
	case <-time.After(300 * time.Millisecond):
	}

	// With the filter disabled a write from another goroutine is seen once.
	watchOwnWrites.Store(true)
	defer watchOwnWrites.Store(false)
	done := make(chan error)
	go func() { done <- WriteText("watched write") }()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not fire")
	}

	stop()
	stop() // idempotent
	if err := WriteText("after stop"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
		t.Error("Watch fired after stop")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWatchOtherProcess(t *testing.T) {
	realClipboard(t)
	clipExe, err := exec.LookPath("clip.exe")
	if err != nil {
		t.Skip("clip.exe not found")
	}
	fired := make(chan struct{}, 10)
	stop, err := Watch(func() { fired <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	cmd := exec.Command(clipExe)
	cmd.Stdin = strings.NewReader("from another process")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not fire for another process")
	}
	c, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Text, "from another process") {
		t.Fatalf("Text = %q", c.Text)
	}
}

func TestPasteToInvalid(t *testing.T) {
	if err := PasteTo(0); err == nil {
		t.Fatal("PasteTo(0) succeeded")
	}
}
