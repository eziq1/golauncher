//go:build windows

package clip

import (
	"bytes"
	"image/png"
	"testing"
)

// TestPasteProbe (R96) uses the real clipboard
// (GOLAUNCHER_CLIPBOARD_TESTS=1): HasFilesOrImage and ReadPaste agree, again
// and again after reading (a read leaves nothing behind), for an image alone
// and with the text browsers, chat apps and spreadsheets put next to it, and
// for the user's copy put back by CopySelection with our markers.
func TestPasteProbe(t *testing.T) {
	realClipboard(t)
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(7, 4)); err != nil {
		t.Fatal(err)
	}
	// A watcher makes its window the clipboard's open owner, as in the app.
	stop, err := Watch(func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	f := formats()
	html := registerFormat("HTML Format")
	pngData := clipData{f.png, buf.Bytes()}
	dib := clipData{cfDIB, imageToDIB(testImage(7, 4))}
	text := func(s string) clipData { return clipData{cfUnicodeText, utf16Bytes(s)} }
	zero := []byte{0, 0, 0, 0}
	for _, tc := range []struct {
		name     string
		items    []clipData
		restored bool // as CopySelection put it back
		want     bool
	}{
		{"png and dib", []clipData{pngData, dib}, false, true},
		{"dib", []clipData{dib}, false, true},
		{"png and html", []clipData{pngData, {html, []byte("Version:0.9\r\n<img src=\"https://example.com/a.png\">")}}, false, true},
		{"image and its address", []clipData{pngData, dib, text("https://example.com/a.png")}, false, true},
		{"image and a placeholder", []clipData{dib, text("[图片]")}, false, true},
		{"image and blank text", []clipData{dib, text("\r\n")}, false, true},
		{"image and real text", []clipData{pngData, dib, text("Total 42\r\n")}, false, false},
		{"text", []clipData{text("hello")}, false, false},
		{"marked image", []clipData{pngData, {f.exclude, zero}}, false, false},
		{"restored image", []clipData{pngData, {f.exclude, zero}, {f.history, zero}, {f.cloud, zero}}, true, true},
		{"restored but ignored", []clipData{pngData, {f.viewerIgnore, zero}}, true, false},
	} {
		if err := write(tc.items...); err != nil {
			t.Fatal(err)
		}
		restoredSeq.Store(0)
		if tc.restored {
			restoredSeq.Store(sequenceNumber())
		}
		for i := range 3 {
			probe := HasFilesOrImage()
			c, err := ReadPaste()
			if err != nil {
				t.Fatalf("%s: ReadPaste: %v", tc.name, err)
			}
			if got := len(c.Image) > 0; probe != tc.want || got != tc.want {
				t.Errorf("%s, #%d: probe %v, ReadPaste image %v; want %v", tc.name, i, probe, got, tc.want)
			}
			// The history's Read is unchanged: our markers still keep the
			// restored copy out of it.
			if r, _ := Read(); tc.restored && !r.Exclude {
				t.Errorf("%s: Read is not excluded", tc.name)
			}
		}
	}
	restoredSeq.Store(0)
}
