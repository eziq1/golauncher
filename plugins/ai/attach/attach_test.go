package attach

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/image/bmp"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	return img
}

func encPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func decodeSize(t *testing.T, data []byte) (string, int, int) {
	t.Helper()
	var cfg image.Config
	var err error
	format := ""
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG")):
		format = "png"
		cfg, err = png.DecodeConfig(bytes.NewReader(data))
	case bytes.HasPrefix(data, []byte("\xff\xd8")):
		format = "jpeg"
		cfg, err = jpeg.DecodeConfig(bytes.NewReader(data))
	case bytes.HasPrefix(data, []byte("GIF")):
		format = "gif"
		cfg, err = gif.DecodeConfig(bytes.NewReader(data))
	default:
		t.Fatalf("unknown image format % x", data[:min(8, len(data))])
	}
	if err != nil {
		t.Fatal(err)
	}
	return format, cfg.Width, cfg.Height
}

func checkThumb(t *testing.T, p Prepared, wantW, wantH int) {
	t.Helper()
	f, w, h := decodeSize(t, p.Thumb)
	if f != "png" || w != wantW || h != wantH {
		t.Errorf("thumb = %s %d×%d, want png %d×%d", f, w, h, wantW, wantH)
	}
}

func TestSniffByContent(t *testing.T) {
	pngData := encPNG(t, gradient(4, 4))
	pad := func(s string) []byte { return append([]byte(s), make([]byte, 600)...) }
	rnd := make([]byte, 4096)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range rnd {
		rnd[i] = byte(r.Uint32())
	}
	rnd[0] = 0x01
	tar := make([]byte, 1024)
	copy(tar, "file.txt")
	copy(tar[257:], "ustar\x0000")

	tests := []struct {
		name   string
		file   string
		data   []byte
		kind   string
		mt     string
		reason Reason
	}{
		{"png named txt", "notes.txt", pngData, KindImage, "image/png", ""},
		{"text named png", "photo.png", []byte("just some words\n"), KindText, "text/plain", ""},
		{"jpeg", "a", pad("\xff\xd8\xff\xe0"), KindImage, "image/jpeg", ""},
		{"gif", "a", pad("GIF89a"), KindImage, "image/gif", ""},
		{"webp", "a", pad("RIFF\x10\x00\x00\x00WEBPVP8L"), KindImage, "image/webp", ""},
		{"bmp", "a", pad("BM\x00\x00\x00\x00\x00\x00\x00\x00\x36\x00\x00\x00\x28\x00\x00\x00"), KindImage, "image/bmp", ""},
		{"BM text", "a", []byte("BMW is a car brand.\n"), KindText, "text/plain", ""},
		{"tiff", "a", pad("II*\x00"), KindImage, "image/tiff", ""},
		{"pdf", "a.txt", pad("%PDF-1.7\n"), "", "", ReasonPDF},
		{"docx", "Report.DOCX", pad("PK\x03\x04"), "", "", ReasonOffice},
		{"zip", "report.zip", pad("PK\x03\x04"), "", "", ReasonArchive},
		{"empty zip", "x.zip", pad("PK\x05\x06"), "", "", ReasonArchive},
		{"ole", "old.doc", pad("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"), "", "", ReasonOffice},
		{"7z", "a", pad("7z\xbc\xaf\x27\x1c"), "", "", ReasonArchive},
		{"rar", "a", pad("Rar!\x1a\x07\x01\x00"), "", "", ReasonArchive},
		{"gzip", "a", pad("\x1f\x8b\x08"), "", "", ReasonArchive},
		{"xz", "a", pad("\xfd7zXZ\x00"), "", "", ReasonArchive},
		{"zstd", "a", pad("\x28\xb5\x2f\xfd"), "", "", ReasonArchive},
		{"bzip2", "a", pad("BZh91AY&SY\x01\x02"), "", "", ReasonArchive},
		{"tar", "a", tar, "", "", ReasonArchive},
		{"mp4", "clip", pad("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00mp42isom"), "", "", ReasonVideo},
		{"mov", "clip", pad("\x00\x00\x00\x14ftypqt  \x00\x00\x00\x00qt  "), "", "", ReasonVideo},
		{"3gp", "clip", pad("\x00\x00\x00\x14ftyp3gp5\x00\x00\x00\x00"), "", "", ReasonVideo},
		{"webm", "clip", pad("\x1a\x45\xdf\xa3\x9f\x42\x86\x81"), "", "", ReasonVideo},
		{"avi", "clip", pad("RIFF\x00\x00\x00\x00AVI LIST"), "", "", ReasonVideo},
		{"flv", "clip", pad("FLV\x01\x05"), "", "", ReasonVideo},
		{"mpeg-ps", "clip", pad("\x00\x00\x01\xba\x44"), "", "", ReasonVideo},
		{"mp3 id3", "song", pad("ID3\x03\x00\x00\x00\x00\x00\x00"), "", "", ReasonAudio},
		{"mp3 frame", "song", pad("\xff\xfb\x90\x64"), "", "", ReasonAudio},
		{"wav", "song", pad("RIFF\x00\x00\x00\x00WAVEfmt "), "", "", ReasonAudio},
		{"ogg", "song", pad("OggS\x00\x02"), "", "", ReasonAudio},
		{"flac", "song", pad("fLaC\x00"), "", "", ReasonAudio},
		{"m4a", "song", pad("\x00\x00\x00\x18ftypM4A \x00\x00\x00\x00M4A mp42"), "", "", ReasonAudio},
		{"aiff", "song", pad("FORM\x00\x00\x00\x00AIFFCOMM"), "", "", ReasonAudio},
		{"heic", "IMG_1.jpg", pad("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic"), "", "", ReasonHEIC},
		{"heif mif1", "a", pad("\x00\x00\x00\x18ftypmif1\x00\x00\x00\x00mif1heic"), "", "", ReasonHEIC},
		{"avif", "a", pad("\x00\x00\x00\x1cftypavif\x00\x00\x00\x00avifmif1miaf"), "", "", ReasonHEIC},
		{"random", "data.txt", rnd, "", "", ReasonBinary},
		{"exe", "a.txt", pad("MZ\x90\x00"), "", "", ReasonBinary},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, mt, reason := Sniff(tt.data, tt.file)
			if kind != tt.kind || mt != tt.mt || reason != tt.reason {
				t.Errorf("Sniff = %q %q %q, want %q %q %q", kind, mt, reason, tt.kind, tt.mt, tt.reason)
			}
		})
	}
}

func TestSniffTruncatedHead(t *testing.T) {
	// A head that ends in the middle of a character is still text.
	s := []byte(strings.Repeat("世界", 300))
	if kind, _, _ := Sniff(s[:len(s)-1], "a"); kind != KindText {
		t.Errorf("UTF-8 head cut in a rune: kind %q", kind)
	}
	u := utf16LE(strings.Repeat("a😀", 200))
	if kind, _, _ := Sniff(u[:len(u)-3], "a"); kind != KindText {
		t.Errorf("UTF-16 head cut in a surrogate pair: kind %q", kind)
	}
}

func utf16LE(s string) []byte {
	out := []byte{0xff, 0xfe}
	for _, u := range utf16.Encode([]rune(s)) {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return out
}

func utf16BE(s string) []byte {
	out := []byte{0xfe, 0xff}
	for _, u := range utf16.Encode([]rune(s)) {
		out = binary.BigEndian.AppendUint16(out, u)
	}
	return out
}

func TestPrepareFileText(t *testing.T) {
	lim := DefaultLimits()
	tests := []struct {
		name   string
		file   string
		data   []byte
		reason Reason
		want   string
		mt     string
		ext    string
	}{
		{"plain", "notes", []byte("hello\r\nworld\n"), "", "hello\r\nworld\n", "text/plain", ".txt"},
		{"markdown", "README.MD", []byte("# Title\n"), "", "# Title\n", "text/markdown", ".md"},
		{"go", "main.go", []byte("package main\n"), "", "package main\n", "text/x-go", ".go"},
		{"json", "a.json", []byte(`{"a":1}`), "", `{"a":1}`, "application/json", ".json"},
		{"utf8 bom", "a.csv", []byte("\xef\xbb\xbfa,b\n"), "", "a,b\n", "text/csv", ".csv"},
		{"utf16le", "a.txt", utf16LE("héllo 世界 😀"), "", "héllo 世界 😀", "text/plain", ".txt"},
		{"utf16be", "a.txt", utf16BE("héllo 世界"), "", "héllo 世界", "text/plain", ".txt"},
		{"utf16 nul", "a.txt", utf16LE("a\x00b"), ReasonBinary, "", "", ""},
		{"utf16 lone surrogate", "a.txt", append(utf16LE("ab"), 0x00, 0xd8, 0x41, 0x00), ReasonBinary, "", "", ""},
		{"utf16 odd", "a.txt", append(utf16LE("ab"), 0x41), ReasonBinary, "", "", ""},
		{"nul", "a.txt", []byte("abc\x00def"), ReasonBinary, "", "", ""},
		{"invalid utf8", "a.txt", []byte("abc\xc3\x28def"), ReasonBinary, "", "", ""},
		{"allowed controls", "a.txt", []byte("a\tb\fc\vd\be\x1b[0mf\n"), "", "a\tb\fc\vd\be\x1b[0mf\n", "text/plain", ".txt"},
		{"few controls", "a.txt", append([]byte(strings.Repeat("x", 99)), 0x01), "", strings.Repeat("x", 99) + "\x01", "text/plain", ".txt"},
		{"many controls", "a.txt", []byte(strings.Repeat("ab\x01\x02", 50)), ReasonBinary, "", "", ""},
		{"del", "a.txt", []byte(strings.Repeat("abc\x7f", 50)), ReasonBinary, "", "", ""},
		{"empty", "a.txt", nil, ReasonEmpty, "", "", ""},
		{"bom only", "a.txt", []byte("\xef\xbb\xbf"), ReasonEmpty, "", "", ""},
		{"utf16 bom only", "a.txt", []byte("\xff\xfe"), ReasonEmpty, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := PrepareFile(writeFile(t, tt.file, tt.data), lim)
			if p.Reason != tt.reason {
				t.Fatalf("Reason = %q, want %q", p.Reason, tt.reason)
			}
			if p.SourceSize != int64(len(tt.data)) {
				t.Errorf("SourceSize = %d, want %d", p.SourceSize, len(tt.data))
			}
			if tt.reason != "" {
				if p.Kind != "" || p.Data != nil {
					t.Errorf("refused with Kind %q, %d bytes", p.Kind, len(p.Data))
				}
				return
			}
			if p.Kind != backend.AttachText || string(p.Data) != tt.want || p.MediaType != tt.mt || p.Ext != tt.ext {
				t.Errorf("got %q %q %q %q, want text %q %q %q", p.Kind, p.Data, p.MediaType, p.Ext, tt.want, tt.mt, tt.ext)
			}
		})
	}
}

func TestPrepareFileFolderAndMissing(t *testing.T) {
	dir := t.TempDir()
	if p := PrepareFile(dir, DefaultLimits()); p.Reason != ReasonFolder {
		t.Errorf("folder: %q", p.Reason)
	}
	if p := PrepareFile(filepath.Join(dir, "nope.txt"), DefaultLimits()); p.Reason != ReasonUnreadable {
		t.Errorf("missing: %q", p.Reason)
	}
}

func TestTextSizeLimit(t *testing.T) {
	lim := DefaultLimits()
	line := []byte(strings.Repeat("abcdefghi", 7) + "\n")
	at := bytes.Repeat(line, int(lim.MaxTextBytes)/len(line)+1)[:lim.MaxTextBytes]
	if p := PrepareFile(writeFile(t, "at.txt", at), lim); p.Reason != "" || len(p.Data) != len(at) {
		t.Errorf("at the limit: Reason %q, %d bytes", p.Reason, len(p.Data))
	}
	over := append(at, 'x')
	if p := PrepareFile(writeFile(t, "over.txt", over), lim); p.Reason != ReasonTextTooLarge {
		t.Errorf("one byte over: Reason %q", p.Reason)
	}
	big := bytes.Repeat(line, 300*1024/len(line))
	if p := PrepareFile(writeFile(t, "big.log", big), lim); p.Reason != ReasonTextTooLarge || p.SourceSize != int64(len(big)) {
		t.Errorf("300 KB: Reason %q, SourceSize %d", p.Reason, p.SourceSize)
	}
	// A large binary file is binary, not textTooLarge.
	bin := make([]byte, 300*1024)
	if p := PrepareFile(writeFile(t, "big.bin", bin), lim); p.Reason != ReasonBinary {
		t.Errorf("300 KB of zeros: Reason %q", p.Reason)
	}
}

// bigFile writes head and extends the file to size (without writing the rest).
func bigFile(t *testing.T, name string, head []byte, size int64) string {
	t.Helper()
	p := writeFile(t, name, head)
	if err := os.Truncate(p, size); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLargeFilesClassifiedByHead(t *testing.T) {
	lim := DefaultLimits()
	mp4 := bigFile(t, "movie.mp4", []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"), 100<<20)
	if p := PrepareFile(mp4, lim); p.Reason != ReasonVideo || p.SourceSize != 100<<20 {
		t.Errorf("100 MB mp4: %q %d", p.Reason, p.SourceSize)
	}
	pngHead := encPNG(t, gradient(8, 8))
	huge := bigFile(t, "huge.png", pngHead, lim.MaxSourceImageBytes+1)
	if p := PrepareFile(huge, lim); p.Reason != ReasonImageTooLarge {
		t.Errorf("image over MaxSourceImageBytes: %q", p.Reason)
	}
}

func TestImageWithinLimitsKept(t *testing.T) {
	lim := DefaultLimits()
	data := encPNG(t, gradient(400, 200))
	p := PrepareFile(writeFile(t, "shot.txt", data), lim)
	if p.Reason != "" || p.Kind != backend.AttachImage || p.MediaType != "image/png" || p.Ext != ".png" {
		t.Fatalf("got %q %q %q %q", p.Reason, p.Kind, p.MediaType, p.Ext)
	}
	if !bytes.Equal(p.Data, data) || p.Resized || p.Converted || p.Width != 400 || p.Height != 200 {
		t.Errorf("not kept as is: resized %v converted %v %d×%d", p.Resized, p.Converted, p.Width, p.Height)
	}
	checkThumb(t, p, 160, 80)

	// A small image: the thumbnail is not upscaled.
	small := PrepareImage(encPNG(t, gradient(50, 20)), lim)
	checkThumb(t, small, 50, 20)
}

func TestGIFKeptByteIdentical(t *testing.T) {
	pal := color.Palette{color.Black, color.White, color.RGBA{255, 0, 0, 255}}
	var frames []*image.Paletted
	for i := range 3 {
		f := image.NewPaletted(image.Rect(0, 0, 120, 60), pal)
		for x := range 120 {
			f.SetColorIndex(x, (x+i*10)%60, uint8(1+i%2))
		}
		frames = append(frames, f)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, &gif.GIF{Image: frames, Delay: []int{10, 10, 10}}); err != nil {
		t.Fatal(err)
	}
	p := PrepareFile(writeFile(t, "anim.gif", b.Bytes()), DefaultLimits())
	if p.Reason != "" || p.MediaType != "image/gif" || p.Ext != ".gif" || !bytes.Equal(p.Data, b.Bytes()) {
		t.Fatalf("got %q %q %q, identical %v", p.Reason, p.MediaType, p.Ext, bytes.Equal(p.Data, b.Bytes()))
	}
	checkThumb(t, p, 120, 60)
}

func TestImageDownscalePNG(t *testing.T) {
	p := PrepareImage(encPNG(t, gradient(4000, 1000)), DefaultLimits())
	if p.Reason != "" || p.MediaType != "image/png" || p.Ext != ".png" || !p.Resized || p.Converted {
		t.Fatalf("got %q %q %q resized %v converted %v", p.Reason, p.MediaType, p.Ext, p.Resized, p.Converted)
	}
	if f, w, h := decodeSize(t, p.Data); f != "png" || w != 2000 || h != 500 || p.Width != 2000 || p.Height != 500 {
		t.Errorf("Data = %s %d×%d (Prepared %d×%d), want png 2000×500", f, w, h, p.Width, p.Height)
	}
	checkThumb(t, p, 160, 40)
}

func TestImageDownscaleJPEGStaysJPEG(t *testing.T) {
	p := PrepareFile(writeFile(t, "photo.jpeg", encJPEG(t, gradient(1500, 3000))), DefaultLimits())
	if p.Reason != "" || p.MediaType != "image/jpeg" || p.Ext != ".jpg" || !p.Resized || p.Converted {
		t.Fatalf("got %q %q %q resized %v converted %v", p.Reason, p.MediaType, p.Ext, p.Resized, p.Converted)
	}
	if f, w, h := decodeSize(t, p.Data); f != "jpeg" || w != 1000 || h != 2000 {
		t.Errorf("Data = %s %d×%d, want jpeg 1000×2000", f, w, h)
	}
	checkThumb(t, p, 80, 160)
}

func TestNoisyPNGBroughtUnderMaxBytes(t *testing.T) {
	lim := DefaultLimits()
	img := image.NewRGBA(image.Rect(0, 0, 2000, 2000))
	r := rand.New(rand.NewPCG(3, 4))
	for i := range img.Pix {
		if i%4 == 3 {
			img.Pix[i] = 255
		} else {
			img.Pix[i] = byte(r.Uint32())
		}
	}
	src := encPNG(t, img)
	if int64(len(src)) <= lim.MaxImageBytes {
		t.Fatalf("test image is only %d bytes", len(src))
	}
	p := PrepareImage(src, lim)
	if p.Reason != "" {
		t.Fatalf("Reason %q", p.Reason)
	}
	if int64(len(p.Data)) > lim.MaxImageBytes {
		t.Errorf("Data is %d bytes, over %d", len(p.Data), lim.MaxImageBytes)
	}
	if p.MediaType != "image/jpeg" || p.Ext != ".jpg" || !p.Converted {
		t.Errorf("got %q %q converted %v, want jpeg converted", p.MediaType, p.Ext, p.Converted)
	}
	f, w, h := decodeSize(t, p.Data)
	if f != "jpeg" || w != h || w > 2000 || w != p.Width || h != p.Height {
		t.Errorf("Data = %s %d×%d (Prepared %d×%d)", f, w, h, p.Width, p.Height)
	}
}

func TestTransparencyKeptAsPNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3000, 1000))
	for y := range 1000 {
		for x := range 3000 {
			img.SetNRGBA(x, y, color.NRGBA{200, uint8(x % 256), 50, uint8(y % 256)})
		}
	}
	p := PrepareImage(encPNG(t, img), DefaultLimits())
	if p.Reason != "" || p.MediaType != "image/png" || !p.Resized {
		t.Fatalf("got %q %q resized %v", p.Reason, p.MediaType, p.Resized)
	}
	out, err := png.Decode(bytes.NewReader(p.Data))
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Dx() != 2000 || out.Bounds().Dy() != 667 {
		t.Errorf("size %v, want 2000×667", out.Bounds())
	}
	if _, _, _, a := out.At(10, 300).RGBA(); a == 0xffff {
		t.Error("alpha lost")
	}
}

func TestBMPConvertedToPNG(t *testing.T) {
	var b bytes.Buffer
	if err := bmp.Encode(&b, gradient(64, 32)); err != nil {
		t.Fatal(err)
	}
	p := PrepareFile(writeFile(t, "pic.bmp", b.Bytes()), DefaultLimits())
	if p.Reason != "" || p.MediaType != "image/png" || p.Ext != ".png" || !p.Converted || p.Resized {
		t.Fatalf("got %q %q %q converted %v resized %v", p.Reason, p.MediaType, p.Ext, p.Converted, p.Resized)
	}
	if f, w, h := decodeSize(t, p.Data); f != "png" || w != 64 || h != 32 {
		t.Errorf("Data = %s %d×%d", f, w, h)
	}
	checkThumb(t, p, 64, 32)
}

// pngHeader is a PNG signature and an IHDR chunk claiming w×h (no data).
func pngHeader(w, h uint32) []byte {
	ihdr := []byte("IHDR")
	ihdr = binary.BigEndian.AppendUint32(ihdr, w)
	ihdr = binary.BigEndian.AppendUint32(ihdr, h)
	ihdr = append(ihdr, 8, 2, 0, 0, 0) // 8-bit RGB
	out := []byte("\x89PNG\r\n\x1a\n")
	out = binary.BigEndian.AppendUint32(out, 13)
	out = append(out, ihdr...)
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(ihdr))
	return out
}

func TestPixelGuard(t *testing.T) {
	lim := DefaultLimits()
	if p := PrepareImage(pngHeader(20000, 20000), lim); p.Reason != ReasonImageTooLarge {
		t.Errorf("20000×20000 header: %q", p.Reason)
	}
	// Within the pixel limit, the same truncated file does not decode.
	if p := PrepareImage(pngHeader(100, 100), lim); p.Reason != ReasonImageInvalid {
		t.Errorf("truncated 100×100: %q", p.Reason)
	}
	big := encPNG(t, gradient(10, 10))
	if p := PrepareImage(big, Limits{MaxImageEdge: 2000, MaxImageBytes: 1 << 20, MaxSourceImageBytes: 10, MaxImagePixels: 1 << 20, ThumbEdge: 160}); p.Reason != ReasonImageTooLarge {
		t.Errorf("over MaxSourceImageBytes: %q", p.Reason)
	}
}

func TestInvalidImages(t *testing.T) {
	lim := DefaultLimits()
	broken := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0xab}, 100)...)
	if p := PrepareFile(writeFile(t, "broken.png", broken), lim); p.Reason != ReasonImageInvalid {
		t.Errorf("broken png: %q", p.Reason)
	}
	if p := PrepareImage(append([]byte("\xff\xd8\xff"), make([]byte, 50)...), lim); p.Reason != ReasonImageInvalid {
		t.Errorf("broken jpeg: %q", p.Reason)
	}
	if p := PrepareImage([]byte("hello"), lim); p.Reason != ReasonImageInvalid {
		t.Errorf("text as image: %q", p.Reason)
	}
	if p := PrepareImage(nil, lim); p.Reason != ReasonEmpty {
		t.Errorf("no bytes: %q", p.Reason)
	}
	heic := append([]byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic"), make([]byte, 100)...)
	if p := PrepareImage(heic, lim); p.Reason != ReasonHEIC {
		t.Errorf("heic: %q", p.Reason)
	}
	// Hostile input never panics.
	r := rand.New(rand.NewPCG(5, 6))
	for _, magic := range []string{"\x89PNG\r\n\x1a\n", "\xff\xd8\xff", "GIF89a", "RIFF\x00\x01\x00\x00WEBPVP8 ", "II*\x00", "BM\x00\x00\x00\x00\x00\x00\x00\x00\x36\x00\x00\x00\x28\x00\x00\x00"} {
		for range 50 {
			junk := make([]byte, 200)
			for i := range junk {
				junk[i] = byte(r.Uint32())
			}
			p := PrepareImage(append([]byte(magic), junk...), lim)
			if p.Reason == "" && len(p.Data) == 0 {
				t.Errorf("%q: accepted without data", magic)
			}
		}
	}
}

func TestClipboardPNG(t *testing.T) {
	data := encPNG(t, gradient(300, 300))
	p := PrepareImage(data, DefaultLimits())
	if p.Reason != "" || p.Kind != backend.AttachImage || p.MediaType != "image/png" || !bytes.Equal(p.Data, data) || p.SourceSize != int64(len(data)) {
		t.Fatalf("got %q %q %q identical %v", p.Reason, p.Kind, p.MediaType, bytes.Equal(p.Data, data))
	}
	checkThumb(t, p, 160, 160)
}

func TestWebP(t *testing.T) {
	// 1×1 lossless and lossy WebP images (the well-known feature-detection
	// samples).
	for _, s := range []string{
		"UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==",
		"UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA",
	} {
		data, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		p := PrepareImage(data, DefaultLimits())
		if p.Reason != "" || p.MediaType != "image/webp" || p.Ext != ".webp" || !bytes.Equal(p.Data, data) || p.Width != 1 || p.Height != 1 {
			t.Errorf("got %q %q %q %d×%d", p.Reason, p.MediaType, p.Ext, p.Width, p.Height)
			continue
		}
		checkThumb(t, p, 1, 1)
	}
}

func TestFit(t *testing.T) {
	for _, tt := range []struct{ w, h, edge, ww, wh int }{
		{4000, 1000, 2000, 2000, 500},
		{1000, 4000, 2000, 500, 2000},
		{100, 50, 2000, 100, 50},
		{10000, 1, 160, 160, 1},
		{3, 2, 2, 2, 1},
	} {
		if w, h := fit(tt.w, tt.h, tt.edge); w != tt.ww || h != tt.wh {
			t.Errorf("fit(%d, %d, %d) = %d, %d, want %d, %d", tt.w, tt.h, tt.edge, w, h, tt.ww, tt.wh)
		}
	}
}

// TestScaleAverages: scale gives each pixel the mean of the source pixels
// it covers: a checkerboard becomes gray, a solid color stays itself at any
// ratio, translucent pixels keep their color (premultiplied averaging), and
// the result has the size asked for.
func TestScaleAverages(t *testing.T) {
	checker := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			if (x+y)%2 == 0 {
				checker.Set(x, y, color.RGBA{255, 255, 255, 255})
			} else {
				checker.Set(x, y, color.RGBA{0, 0, 0, 255})
			}
		}
	}
	if got := scale(checker, 2, 2).RGBAAt(1, 1); got != (color.RGBA{128, 128, 128, 255}) {
		t.Errorf("checkerboard: %v, want gray", got)
	}
	solid := image.NewNRGBA(image.Rect(10, 20, 10+997, 20+601)) // odd ratios, an offset origin
	for i := range solid.Pix {
		solid.Pix[i] = []uint8{30, 140, 220, 255}[i%4]
	}
	out := scale(solid, 160, 97)
	if out.Bounds() != image.Rect(0, 0, 160, 97) {
		t.Fatalf("bounds %v", out.Bounds())
	}
	for _, pt := range []image.Point{{0, 0}, {159, 96}, {80, 48}} {
		if got := out.RGBAAt(pt.X, pt.Y); got != (color.RGBA{30, 140, 220, 255}) {
			t.Errorf("solid at %v: %v", pt, got)
		}
	}
	// Half transparent red next to fully transparent pixels: the mean is a
	// more transparent red, never a darkened one.
	half := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	half.SetNRGBA(0, 0, color.NRGBA{255, 0, 0, 128})
	got := color.NRGBAModel.Convert(scale(half, 1, 1).At(0, 0)).(color.NRGBA)
	if got.R < 250 || got.G != 0 || got.A < 60 || got.A > 68 {
		t.Errorf("translucent: %v, want red at about 64 alpha", got)
	}
}

// TestScaleMemory (R124): shrinking a 12-megapixel photo allocates about
// the result and a few rows, not x/image/draw's buffer of float64 pixels
// (about 190 MB).
func TestScaleMemory(t *testing.T) {
	src := image.NewYCbCr(image.Rect(0, 0, 4032, 3024), image.YCbCrSubsampleRatio420) // a decoded JPEG
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	out := scale(src, 2000, 1500)
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 20<<20 {
		t.Errorf("allocated %d MB, want about the 12 MB result", n>>20)
	}
	runtime.KeepAlive(out)
}
