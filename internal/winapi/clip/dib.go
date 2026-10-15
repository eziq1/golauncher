package clip

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math/bits"
	"unicode"
	"unicode/utf16"
)

// Pure-Go conversions between clipboard formats and Go values. They are
// platform independent so they can be tested everywhere.

const (
	biRGB       = 0
	biBitfields = 3
	maxPixels   = 64 << 20 / 4 // bound for untrusted bitmap sizes
)

// dibToImage decodes an uncompressed 24 or 32-bit device independent bitmap
// (BITMAPINFOHEADER, V4 or V5 header). It returns nil for unsupported data.
// Adapted from MyGo (MIT), extended with bit masks and color-table offsets.
func dibToImage(dib []byte) image.Image {
	if !dibSupported(dib) {
		return nil
	}
	le := binary.LittleEndian
	headerSize := int(le.Uint32(dib))
	width, height := int(int32(le.Uint32(dib[4:]))), int(int32(le.Uint32(dib[8:])))
	bpp, compression := int(le.Uint16(dib[14:])), le.Uint32(dib[16:])
	clrUsed := int(le.Uint32(dib[32:]))
	// Default masks for BI_RGB.
	rm, gm, bm, am := uint32(0x00ff0000), uint32(0x0000ff00), uint32(0x000000ff), uint32(0)
	if bpp == 32 && headerSize > 40 {
		am = 0xff000000 // V4/V5 headers carry meaningful alpha
	}
	offset := headerSize
	if compression == biBitfields {
		if bpp != 32 {
			return nil
		}
		masks := dib[40:]
		if headerSize == 40 {
			offset += 12 // masks follow a BITMAPINFOHEADER
		}
		if len(masks) < 12 {
			return nil
		}
		rm, gm, bm = le.Uint32(masks), le.Uint32(masks[4:]), le.Uint32(masks[8:])
		if headerSize >= 56 {
			am = le.Uint32(masks[12:])
		}
	}
	offset += 4 * clrUsed
	bottomUp := height > 0
	if !bottomUp {
		if height == -1<<31 {
			return nil
		}
		height = -height
	}
	if width > maxPixels || height > maxPixels/width {
		return nil
	}
	stride := ((width*bpp + 31) / 32) * 4
	if offset > len(dib) || height > (len(dib)-offset)/stride {
		return nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	px := bpp / 8
	anyAlpha := false
	for y := range height {
		row := y
		if bottomUp {
			row = height - 1 - y
		}
		line := dib[offset+row*stride:]
		for x := range width {
			p := line[x*px:]
			var v uint32
			if px == 4 {
				v = le.Uint32(p)
			} else {
				v = uint32(p[0]) | uint32(p[1])<<8 | uint32(p[2])<<16
			}
			a := uint8(255)
			if am != 0 {
				a = channel(v, am)
				anyAlpha = anyAlpha || a != 0
			}
			img.SetNRGBA(x, y, color.NRGBA{R: channel(v, rm), G: channel(v, gm), B: channel(v, bm), A: a})
		}
	}
	if am != 0 && !anyAlpha {
		// Many programs leave the alpha byte zero; treat the bitmap as opaque.
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 255
		}
	}
	return img
}

// dibSupported reports whether the header of dib describes a bitmap that
// dibToImage may decode. It only looks at the header, so it is cheap enough
// to call while the clipboard is open.
func dibSupported(dib []byte) bool {
	if len(dib) < 40 {
		return false
	}
	le := binary.LittleEndian
	headerSize := le.Uint32(dib)
	width, height := int32(le.Uint32(dib[4:])), int32(le.Uint32(dib[8:]))
	bpp, compression := le.Uint16(dib[14:]), le.Uint32(dib[16:])
	return headerSize >= 40 && uint64(headerSize) <= uint64(len(dib)) && width > 0 && height != 0 &&
		(bpp == 24 || bpp == 32) &&
		(compression == biRGB || compression == biBitfields && bpp == 32)
}

// rawImage is image data copied from the clipboard: a PNG file or a DIB.
type rawImage struct {
	data  []byte
	isPNG bool
}

// toPNG returns the image as PNG, converting a DIB, or nil if there is no
// usable image.
func (r rawImage) toPNG() []byte {
	if len(r.data) == 0 {
		return nil
	}
	if r.isPNG {
		b, _ := trimPNG(r.data)
		return b
	}
	img := dibToImage(r.data)
	if img == nil {
		return nil
	}
	var buf bytes.Buffer
	if png.Encode(&buf, img) != nil {
		return nil
	}
	return buf.Bytes()
}

// utf16Blank reports whether NUL-terminated little-endian UTF-16 text b is
// empty or only white space. It does not allocate.
func utf16Blank(b []byte) bool {
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		if utf16.IsSurrogate(rune(c)) || !unicode.IsSpace(rune(c)) {
			return false
		}
	}
	return true
}

// channel extracts the bits of mask from v and scales them to 8 bits.
func channel(v, mask uint32) uint8 {
	if mask == 0 {
		return 0
	}
	shift := bits.TrailingZeros32(mask)
	width := bits.OnesCount32(mask)
	c := (v & mask) >> shift
	switch {
	case width == 8:
		return uint8(c)
	case width > 8:
		return uint8(c >> (width - 8))
	default:
		maxV := uint32(1)<<width - 1
		return uint8(c * 255 / maxV)
	}
}

// imageToDIB encodes img as a bottom-up 32-bit BI_RGB bitmap with a
// BITMAPINFOHEADER (CF_DIB). Transparent pixels are blended onto white since
// most CF_DIB readers ignore alpha.
func imageToDIB(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([]byte, 40+4*w*h)
	le := binary.LittleEndian
	le.PutUint32(out, 40)
	le.PutUint32(out[4:], uint32(w))
	le.PutUint32(out[8:], uint32(h))
	le.PutUint16(out[12:], 1)
	le.PutUint16(out[14:], 32)
	le.PutUint32(out[20:], uint32(4*w*h))
	blend := func(c, a uint8) uint8 { return uint8((uint32(c)*uint32(a) + 255*(255-uint32(a))) / 255) }
	i := 40
	for y := b.Max.Y - 1; y >= b.Min.Y; y-- {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			out[i], out[i+1], out[i+2], out[i+3] = blend(c.B, c.A), blend(c.G, c.A), blend(c.R, c.A), 255
			i += 4
		}
	}
	return out
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// trimPNG returns data cut after the IEND chunk; clipboard memory blocks may
// be padded. It reports false if data is not a PNG.
func trimPNG(data []byte) ([]byte, bool) {
	if !bytes.HasPrefix(data, pngSignature) {
		return nil, false
	}
	for off := len(pngSignature); off+12 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[off:]))
		if n < 0 || n > len(data)-off-12 {
			break
		}
		end := off + 12 + n
		if string(data[off+4:off+8]) == "IEND" {
			return data[:end], true
		}
		off = end
	}
	return data, true
}

// utf16Bytes encodes s as NUL-terminated little-endian UTF-16.
func utf16Bytes(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u)+2)
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[2*i:], c)
	}
	return b
}

// utf16String decodes little-endian UTF-16 up to the first NUL.
func utf16String(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
		if u[i] == 0 {
			u = u[:i]
			break
		}
	}
	return string(utf16.Decode(u))
}

// dropFiles encodes paths as a DROPFILES structure with wide strings
// (CF_HDROP).
func dropFiles(paths []string) []byte {
	const header = 20 // pFiles, pt.x, pt.y, fNC, fWide
	out := make([]byte, header, header+64*len(paths))
	le := binary.LittleEndian
	le.PutUint32(out, header)
	le.PutUint32(out[16:], 1) // fWide
	for _, p := range paths {
		out = append(out, utf16Bytes(p)...)
	}
	return append(out, 0, 0) // list terminator
}
