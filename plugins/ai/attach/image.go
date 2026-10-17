package attach

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	"golang.org/x/image/tiff"
	"golang.org/x/image/webp"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// EXIF orientation is ignored in this version (phase 2): a JPEG kept as is
// keeps its tag; a re-encoded one is stored as its pixels are.

const jpegQuality = 85

type codec struct {
	decode       func(io.Reader) (image.Image, error)
	decodeConfig func(io.Reader) (image.Config, error)
	ext          string
	// asIs: the format is sent as it is (when within the limits).
	asIs bool
}

var codecs = map[string]codec{
	"image/png":  {png.Decode, png.DecodeConfig, ".png", true},
	"image/jpeg": {jpeg.Decode, jpeg.DecodeConfig, ".jpg", true},
	"image/gif":  {gif.Decode, gif.DecodeConfig, ".gif", true}, // Decode: the first frame
	"image/webp": {webp.Decode, webp.DecodeConfig, ".webp", true},
	"image/bmp":  {bmp.Decode, bmp.DecodeConfig, ".png", false},
	"image/tiff": {tiff.Decode, tiff.DecodeConfig, ".png", false},
}

// safely runs a decoder, turning a panic on hostile input into an error.
func safely[T any](f func(io.Reader) (T, error), data []byte) (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("decoder panic: %v", r)
		}
	}()
	return f(bytes.NewReader(data))
}

// prepareImage prepares the bytes of an image of media type mt (as sniffed).
func prepareImage(data []byte, mt string, lim Limits) Prepared {
	size := int64(len(data))
	c, ok := codecs[mt]
	if !ok {
		return refused(ReasonImageInvalid, size)
	}
	if size > lim.MaxSourceImageBytes {
		return refused(ReasonImageTooLarge, size)
	}
	cfg, err := safely(c.decodeConfig, data)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return refused(ReasonImageInvalid, size)
	}
	if int64(cfg.Width)*int64(cfg.Height) > lim.MaxImagePixels {
		return refused(ReasonImageTooLarge, size)
	}
	img, err := safely(c.decode, data)
	if err != nil || img.Bounds().Empty() {
		return refused(ReasonImageInvalid, size)
	}

	p := Prepared{Kind: backend.AttachImage, SourceSize: size}
	if c.asIs && max(cfg.Width, cfg.Height) <= lim.MaxImageEdge && size <= lim.MaxImageBytes {
		p.MediaType, p.Ext, p.Data = mt, c.ext, data
		p.Width, p.Height = cfg.Width, cfg.Height
		p.Thumb = thumbnail(img, lim.ThumbEdge)
		return p
	}

	alpha := hasAlpha(img)
	outPNG := alpha || mt == "image/png" || mt == "image/gif" || mt == "image/bmp" || mt == "image/tiff"
	b := img.Bounds()
	w, h := fit(b.Dx(), b.Dy(), lim.MaxImageEdge)
	for range 40 {
		scaled := img
		if w != b.Dx() || h != b.Dy() {
			scaled = scale(img, w, h)
		}
		enc, err := encode(scaled, outPNG)
		if err != nil {
			return refused(ReasonImageInvalid, size)
		}
		if int64(len(enc)) <= lim.MaxImageBytes {
			p.Data = enc
			p.Width, p.Height = w, h
			p.Resized = w != b.Dx() || h != b.Dy()
			if outPNG {
				p.MediaType, p.Ext = "image/png", ".png"
			} else {
				p.MediaType, p.Ext = "image/jpeg", ".jpg"
			}
			p.Converted = p.MediaType != mt || !p.Resized
			p.Thumb = thumbnail(scaled, lim.ThumbEdge)
			return p
		}
		switch {
		case outPNG && !alpha:
			outPNG = false // the same size as JPEG first
		case w <= 1 && h <= 1:
			return refused(ReasonImageTooLarge, size)
		default:
			w, h = max(1, w*4/5), max(1, h*4/5)
		}
	}
	return refused(ReasonImageTooLarge, size)
}

// fit returns w×h scaled down (aspect kept) so that its long edge is at most
// edge; never scaled up.
func fit(w, h, edge int) (int, int) {
	if edge <= 0 || max(w, h) <= edge {
		return w, h
	}
	if w >= h {
		return edge, max(1, int((int64(h)*int64(edge)+int64(w)/2)/int64(w)))
	}
	return max(1, int((int64(w)*int64(edge)+int64(h)/2)/int64(h))), edge
}

func scale(img image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	return dst
}

func encode(img image.Image, asPNG bool) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	if asPNG {
		err = png.Encode(&buf, img)
	} else {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality})
	}
	return buf.Bytes(), err
}

func hasAlpha(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return !o.Opaque()
	}
	return true
}

// thumbnail is a PNG of img with its long edge at most edge.
func thumbnail(img image.Image, edge int) []byte {
	b := img.Bounds()
	w, h := fit(b.Dx(), b.Dy(), edge)
	var src image.Image = img
	if w != b.Dx() || h != b.Dy() {
		src = scale(img, w, h)
	}
	enc, err := encode(src, true)
	if err != nil {
		return nil
	}
	return enc
}
