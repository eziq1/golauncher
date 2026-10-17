// Package attach checks and prepares a file the user attaches to a message of
// an AI chat (R91, docs/ai.md §23): it classifies the file by its content,
// refuses what a backend cannot take (with a Reason the plugin words), and
// prepares the rest: images within the limits (scaled down and re-encoded when
// needed, with a thumbnail), text files as UTF-8 without a byte order mark.
//
// The functions are pure: no logging, no state; memory is bounded by Limits.
package attach

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Limits bound what is accepted and how an image is prepared.
type Limits struct {
	// MaxImageEdge is the long edge of a prepared image, in pixels.
	MaxImageEdge int
	// MaxImageBytes is the size of a prepared image (its base64 form must
	// stay within 5 MB).
	MaxImageBytes int64
	// MaxSourceImageBytes: image files larger than this are refused.
	MaxSourceImageBytes int64
	// MaxImagePixels is a decode guard: an image whose width*height is
	// above it is refused without being decoded.
	MaxImagePixels int64
	// MaxTextBytes: text files larger than this are refused.
	MaxTextBytes int64
	// ThumbEdge is the long edge of the thumbnail, in pixels.
	ThumbEdge int
}

// DefaultLimits are the limits of this version.
func DefaultLimits() Limits {
	return Limits{
		MaxImageEdge:        2000,
		MaxImageBytes:       3_750_000,
		MaxSourceImageBytes: 50 << 20,
		MaxImagePixels:      100_000_000,
		MaxTextBytes:        200 * 1024,
		ThumbEdge:           160,
	}
}

// Reason is why a file cannot be attached ("" when it can). It is a code;
// the plugin words it.
type Reason string

const (
	ReasonFolder     Reason = "folder"
	ReasonEmpty      Reason = "empty"
	ReasonUnreadable Reason = "unreadable"
	// ReasonPDF: PDF documents come in a later version.
	ReasonPDF Reason = "pdf"
	// ReasonOffice: docx/xlsx/pptx/odt/ods/odp and legacy OLE documents
	// (doc/xls/ppt).
	ReasonOffice  Reason = "office"
	ReasonVideo   Reason = "video"
	ReasonAudio   Reason = "audio"
	ReasonArchive Reason = "archive"
	// ReasonHEIC: HEIC/HEIF/AVIF images are not converted.
	ReasonHEIC Reason = "heic"
	// ReasonBinary: neither text nor a known kind.
	ReasonBinary       Reason = "binary"
	ReasonTextTooLarge Reason = "textTooLarge"
	// ReasonImageTooLarge: the source bytes or pixels are over the limits
	// (or the image cannot be brought within MaxImageBytes).
	ReasonImageTooLarge Reason = "imageTooLarge"
	// ReasonImageInvalid: looks like an image but does not decode.
	ReasonImageInvalid Reason = "imageInvalid"
)

// Kinds returned by Sniff.
const (
	KindImage = string(backend.AttachImage)
	KindText  = string(backend.AttachText)
)

// Prepared is a checked and prepared attachment, or why there is none.
type Prepared struct {
	// Kind is backend.AttachImage or backend.AttachText; "" when Reason != "".
	Kind backend.AttachmentKind
	// MediaType of Data: image/png, image/jpeg, image/gif or image/webp for
	// images; text/plain or a better one by the extension for text.
	MediaType string
	Reason    Reason
	// Data is what to store: the image bytes (the original when within the
	// limits, else re-encoded) or the text as UTF-8 without a BOM.
	Data []byte
	// Ext is the extension of the stored copy, with the dot: ".png", ".jpg",
	// ".gif" or ".webp" for images; for text the source extension lowercased
	// (".txt" when there is none).
	Ext string
	// Width and Height of Data (images).
	Width, Height int
	// Thumb is a PNG thumbnail of an image (long edge ≤ ThumbEdge, aspect
	// kept, never upscaled).
	Thumb []byte
	// Resized: the image was scaled down.
	Resized bool
	// Converted: the image was re-encoded to another format (BMP/TIFF → PNG,
	// PNG → JPEG to fit MaxImageBytes), or re-encoded in its format without
	// being scaled down (too many bytes).
	Converted bool
	// SourceSize is the size of the source in bytes.
	SourceSize int64
}

// headSize is how much of a file too large to be text is read to classify it.
const headSize = 8 << 10

func refused(r Reason, size int64) Prepared { return Prepared{Reason: r, SourceSize: size} }

// PrepareFile checks and prepares the file at path. Its size is checked
// before anything large is read: a file over MaxTextBytes is classified by its
// head only (and, when it is an image within MaxSourceImageBytes, then read).
func PrepareFile(path string, lim Limits) Prepared {
	fi, err := os.Stat(path)
	if err != nil {
		return refused(ReasonUnreadable, 0)
	}
	if fi.IsDir() {
		return refused(ReasonFolder, 0)
	}
	if !fi.Mode().IsRegular() {
		return refused(ReasonUnreadable, 0)
	}
	size := fi.Size()
	if size == 0 {
		return refused(ReasonEmpty, 0)
	}
	f, err := os.Open(path)
	if err != nil {
		return refused(ReasonUnreadable, size)
	}
	defer f.Close()
	name := filepath.Base(path)

	if size <= lim.MaxTextBytes {
		data, err := readAtMost(f, lim.MaxTextBytes)
		if err != nil {
			return refused(ReasonUnreadable, size)
		}
		if int64(len(data)) > lim.MaxTextBytes {
			// Grew since the stat.
			return classifyLarge(f, data[:min(len(data), headSize)], name, int64(len(data)), lim)
		}
		return prepareComplete(data, name, lim)
	}

	head := make([]byte, headSize)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return refused(ReasonUnreadable, size)
	}
	return classifyLarge(f, head[:n], name, size, lim)
}

// classifyLarge handles a file over MaxTextBytes from its head; f is
// positioned after the head.
func classifyLarge(f *os.File, head []byte, name string, size int64, lim Limits) Prepared {
	kind, mt, reason := sniff(head, name, true)
	switch {
	case reason != "":
		return refused(reason, size)
	case kind == KindText:
		return refused(ReasonTextTooLarge, size)
	}
	// An image.
	if size > lim.MaxSourceImageBytes {
		return refused(ReasonImageTooLarge, size)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return refused(ReasonUnreadable, size)
	}
	data, err := readAtMost(f, lim.MaxSourceImageBytes)
	if err != nil {
		return refused(ReasonUnreadable, size)
	}
	if int64(len(data)) > lim.MaxSourceImageBytes {
		return refused(ReasonImageTooLarge, int64(len(data)))
	}
	return prepareImage(data, mt, lim)
}

// readAtMost reads r up to max+1 bytes (so that a caller sees when there is
// more than max).
func readAtMost(r io.Reader, max int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, max+1))
}

// prepareComplete prepares the whole content of a file named name.
func prepareComplete(data []byte, name string, lim Limits) Prepared {
	size := int64(len(data))
	kind, mt, reason := sniff(data, name, false)
	switch {
	case reason != "":
		return refused(reason, size)
	case kind == KindImage:
		return prepareImage(data, mt, lim)
	}
	text, _ := decodeText(data, false)
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" || ext == "." {
		ext = ".txt"
	}
	return Prepared{
		Kind:       backend.AttachText,
		MediaType:  textMediaType(ext),
		Data:       text,
		Ext:        ext,
		SourceSize: size,
	}
}

// PrepareImage checks and prepares the bytes of an image (e.g. a PNG pasted
// from the clipboard). Bytes that are not an image are ReasonImageInvalid
// (or the Reason of what they are, e.g. ReasonHEIC).
func PrepareImage(data []byte, lim Limits) Prepared {
	size := int64(len(data))
	if size == 0 {
		return refused(ReasonEmpty, 0)
	}
	if size > lim.MaxSourceImageBytes {
		return refused(ReasonImageTooLarge, size)
	}
	kind, mt, reason := sniff(data, "", false)
	switch {
	case kind == KindImage:
		return prepareImage(data, mt, lim)
	case reason != "" && reason != ReasonBinary && reason != ReasonEmpty:
		return refused(reason, size)
	}
	return refused(ReasonImageInvalid, size)
}

// textMediaType is the media type of a text file by its extension (with
// the dot, lowercased).
func textMediaType(ext string) string {
	if mt, ok := textTypes[ext]; ok {
		return mt
	}
	return "text/plain"
}

var textTypes = map[string]string{
	".md":       "text/markdown",
	".markdown": "text/markdown",
	".csv":      "text/csv",
	".tsv":      "text/tab-separated-values",
	".json":     "application/json",
	".xml":      "application/xml",
	".html":     "text/html",
	".htm":      "text/html",
	".css":      "text/css",
	".js":       "text/javascript",
	".mjs":      "text/javascript",
	".ts":       "text/x-typescript",
	".go":       "text/x-go",
	".py":       "text/x-python",
	".rs":       "text/x-rust",
	".java":     "text/x-java",
	".c":        "text/x-c",
	".h":        "text/x-c",
	".cpp":      "text/x-c++",
	".hpp":      "text/x-c++",
	".cs":       "text/x-csharp",
	".sh":       "text/x-shellscript",
	".ps1":      "text/x-powershell",
	".yaml":     "application/yaml",
	".yml":      "application/yaml",
	".toml":     "application/toml",
	".sql":      "application/sql",
}
