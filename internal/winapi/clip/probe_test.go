//go:build windows || darwin

package clip

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestHasFilesOrImage agrees with Read: files, or an image without text.
// It uses the real clipboard (GOLAUNCHER_CLIPBOARD_TESTS=1).
func TestHasFilesOrImage(t *testing.T) {
	realClipboard(t)
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.NRGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		write func() error
		want  bool
	}{
		{"text", func() error { return WriteText("golauncher probe") }, false},
		{"files", func() error { return WriteFiles([]string{file}) }, true},
		{"image", func() error { return WriteImage(buf.Bytes()) }, true},
		{"secret", func() error { return WriteSecret("hunter2") }, false},
	} {
		if err := tc.write(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := HasFilesOrImage(); got != tc.want {
			t.Errorf("%s: HasFilesOrImage() = %v, want %v", tc.name, got, tc.want)
		}
		c, err := ReadPaste()
		if err != nil {
			t.Fatalf("%s: ReadPaste: %v", tc.name, err)
		}
		if read := len(c.Files) > 0 || len(c.Image) > 0; read != tc.want {
			t.Errorf("%s: ReadPaste has files %d, image %d bytes; the probe says %v", tc.name, len(c.Files), len(c.Image), tc.want)
		}
	}
}
