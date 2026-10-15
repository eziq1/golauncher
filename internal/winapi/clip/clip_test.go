package clip

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"sync/atomic"
	"testing"
	"time"
)

func TestDebouncerCoalesces(t *testing.T) {
	var n atomic.Int32
	fired := make(chan struct{}, 10)
	d := &debouncer{delay: 30 * time.Millisecond, fn: func() { n.Add(1); fired <- struct{}{} }}
	for range 5 {
		d.trigger()
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("debouncer did not fire")
	}
	time.Sleep(60 * time.Millisecond)
	if got := n.Load(); got != 1 {
		t.Fatalf("fired %d times, want 1", got)
	}
	// A later burst fires again.
	d.trigger()
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("debouncer did not fire a second time")
	}
}

func TestDebouncerStop(t *testing.T) {
	var n atomic.Int32
	d := &debouncer{delay: 20 * time.Millisecond, fn: func() { n.Add(1) }}
	d.trigger()
	d.stop()
	d.trigger()
	time.Sleep(60 * time.Millisecond)
	if n.Load() != 0 {
		t.Fatal("stopped debouncer fired")
	}
}

func testImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 40), G: uint8(y * 60), B: 200, A: 255})
		}
	}
	return img
}

func TestDIBRoundTrip(t *testing.T) {
	src := testImage(5, 3)
	got := dibToImage(imageToDIB(src))
	if got == nil {
		t.Fatal("dibToImage returned nil")
	}
	if got.Bounds() != src.Bounds() {
		t.Fatalf("bounds %v, want %v", got.Bounds(), src.Bounds())
	}
	for y := range 3 {
		for x := range 5 {
			if a, b := color.NRGBAModel.Convert(got.At(x, y)), src.At(x, y); a != b {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, a, b)
			}
		}
	}
}

func TestDIBTransparentBlendsOnWhite(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1, 1)) // fully transparent
	got := dibToImage(imageToDIB(src))
	if c := color.NRGBAModel.Convert(got.At(0, 0)); c != (color.NRGBA{255, 255, 255, 255}) {
		t.Fatalf("got %v, want opaque white", c)
	}
}

// v5DIB builds a top-down 32-bit BITMAPV5HEADER bitmap with alpha.
func v5DIB(w, h int, px color.NRGBA) []byte {
	le := binary.LittleEndian
	out := make([]byte, 124+4*w*h)
	le.PutUint32(out, 124)
	le.PutUint32(out[4:], uint32(w))
	le.PutUint32(out[8:], uint32(int32(-h)))
	le.PutUint16(out[12:], 1)
	le.PutUint16(out[14:], 32)
	le.PutUint32(out[16:], biBitfields)
	le.PutUint32(out[40:], 0x00ff0000)
	le.PutUint32(out[44:], 0x0000ff00)
	le.PutUint32(out[48:], 0x000000ff)
	le.PutUint32(out[52:], 0xff000000)
	for i := 124; i < len(out); i += 4 {
		out[i], out[i+1], out[i+2], out[i+3] = px.B, px.G, px.R, px.A
	}
	return out
}

func TestDIBV5Alpha(t *testing.T) {
	want := color.NRGBA{R: 10, G: 20, B: 30, A: 128}
	img := dibToImage(v5DIB(2, 2, want))
	if img == nil {
		t.Fatal("nil image")
	}
	if c := img.At(1, 1).(color.NRGBA); c != want {
		t.Fatalf("got %v, want %v", c, want)
	}
	// All-zero alpha means the program did not fill it in.
	img = dibToImage(v5DIB(1, 1, color.NRGBA{R: 1, G: 2, B: 3}))
	if c := img.At(0, 0).(color.NRGBA); c.A != 255 {
		t.Fatalf("alpha %d, want 255", c.A)
	}
}

func TestDIB24Bit(t *testing.T) {
	// 2x1 bottom-up 24-bit: stride is padded to 8 bytes.
	le := binary.LittleEndian
	dib := make([]byte, 40+8)
	le.PutUint32(dib, 40)
	le.PutUint32(dib[4:], 2)
	le.PutUint32(dib[8:], 1)
	le.PutUint16(dib[12:], 1)
	le.PutUint16(dib[14:], 24)
	copy(dib[40:], []byte{1, 2, 3, 4, 5, 6})
	img := dibToImage(dib)
	if img == nil {
		t.Fatal("nil image")
	}
	if c := img.At(1, 0).(color.NRGBA); c != (color.NRGBA{R: 6, G: 5, B: 4, A: 255}) {
		t.Fatalf("got %v", c)
	}
}

func TestDIBRejectsBadInput(t *testing.T) {
	for name, dib := range map[string][]byte{
		"short":     make([]byte, 10),
		"zero size": make([]byte, 40),
		"truncated": imageToDIB(testImage(4, 4))[:50],
	} {
		if dibToImage(dib) != nil {
			t.Errorf("%s: want nil", name)
		}
	}
}

func TestTrimPNG(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(2, 2)); err != nil {
		t.Fatal(err)
	}
	padded := append(bytes.Clone(buf.Bytes()), 0, 0, 0, 0)
	got, ok := trimPNG(padded)
	if !ok || !bytes.Equal(got, buf.Bytes()) {
		t.Fatalf("trimPNG did not remove padding (ok=%v, len %d want %d)", ok, len(got), buf.Len())
	}
	if _, ok := trimPNG([]byte("not a png")); ok {
		t.Fatal("accepted non-PNG")
	}
}

func TestUTF16(t *testing.T) {
	for _, s := range []string{"", "hello", "héllo 世界 🎉\r\nline"} {
		if got := utf16String(utf16Bytes(s)); got != s {
			t.Errorf("round trip %q = %q", s, got)
		}
	}
}

func TestDropFiles(t *testing.T) {
	b := dropFiles([]string{`C:\a.txt`, `D:\文件.png`})
	if binary.LittleEndian.Uint32(b) != 20 || binary.LittleEndian.Uint32(b[16:]) != 1 {
		t.Fatal("bad DROPFILES header")
	}
	rest := b[20:]
	first := utf16String(rest)
	second := utf16String(rest[2*len([]rune(first))+2:])
	if first != `C:\a.txt` || second != `D:\文件.png` {
		t.Fatalf("got %q, %q", first, second)
	}
	if !bytes.HasSuffix(b, []byte{0, 0, 0, 0}) {
		t.Fatal("missing double NUL terminator")
	}
}
