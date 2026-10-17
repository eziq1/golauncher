package backend

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Attachments (R91, docs/ai.md §24): files the user attaches to a message of
// a chat. The plugin checks and prepares them (images within the limits,
// text files as UTF-8) and keeps a copy in the chat's directory; a backend
// sends them with the message: images as the CLI takes them, text files
// inline (InlineText).

// AttachmentKind is what an attachment is to a backend.
type AttachmentKind string

const (
	// AttachImage is a PNG, JPEG, GIF or WebP image.
	AttachImage AttachmentKind = "image"
	// AttachText is a text file, stored as UTF-8.
	AttachText AttachmentKind = "text"
)

// Attachment is a file sent with a message (Conversation.Send).
type Attachment struct {
	Kind AttachmentKind
	// Name is the file's name as the user knows it (a base name, e.g.
	// "notes.md"); it is sent to the model with text files.
	Name string
	// Path is golauncher's copy of the file (an image within the limits, a
	// text file as UTF-8 without a byte order mark), which the backend reads
	// when it sends the message.
	Path string
	// MediaType is e.g. "image/png" or "text/plain".
	MediaType string
	// Size is the size of the copy in bytes.
	Size int64
}

// AttachmentCaps is what a backend can send with a message.
type AttachmentCaps struct {
	// Images: the backend sends images of ImageTypes (media types, e.g.
	// "image/png").
	Images     bool
	ImageTypes []string
	// Text: the backend sends text files (inline, InlineText).
	Text bool
}

// Accepts reports whether the backend can send an attachment of kind with
// the media type.
func (c AttachmentCaps) Accepts(kind AttachmentKind, mediaType string) bool {
	switch kind {
	case AttachImage:
		return c.Images && slices.Contains(c.ImageTypes, mediaType)
	case AttachText:
		return c.Text
	}
	return false
}

// ImageTypes are the image media types every backend of this version takes.
var ImageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// AllAttachments is the AttachmentCaps of a backend that takes images of
// ImageTypes and text files.
func AllAttachments() AttachmentCaps {
	return AttachmentCaps{Images: true, ImageTypes: slices.Clone(ImageTypes), Text: true}
}

// AttachmentCapser is implemented by backends that can send attachments.
type AttachmentCapser interface {
	// Attachments is what the backend can send with a message. It is
	// static: it does not run the CLI.
	Attachments() AttachmentCaps
}

// AttachmentsOf returns what b can send with a message (nothing when it does
// not implement AttachmentCapser).
func AttachmentsOf(b Backend) AttachmentCaps {
	if c, ok := b.(AttachmentCapser); ok {
		return c.Attachments()
	}
	return AttachmentCaps{}
}

// ErrAttachment: the backend cannot send an attachment of the message (the
// CLI does not take that kind, e.g. an agent without the image prompt
// capability, or an older CLI); Message says which and why.
const ErrAttachment ErrorKind = "attachment"

// Images returns the image attachments of atts, in order.
func Images(atts []Attachment) []Attachment {
	var out []Attachment
	for _, a := range atts {
		if a.Kind == AttachImage {
			out = append(out, a)
		}
	}
	return out
}

// InlineText is the message text with the text attachments of atts before
// it, each clearly delimited with its name:
//
//	<attached-file name="notes.md">
//	…
//	</attached-file>
//
// Images are left out (the backend sends them its own way). Without text
// attachments it is text unchanged.
func InlineText(text string, atts []Attachment) (string, error) {
	var b strings.Builder
	for _, a := range atts {
		if a.Kind != AttachText {
			continue
		}
		data, err := os.ReadFile(a.Path)
		if err != nil {
			// Not err itself: its text has the path (never logged, docs/ai.md §9).
			reason := "it cannot be read"
			if errors.Is(err, os.ErrNotExist) {
				reason = "its copy no longer exists"
			}
			return "", &Error{Kind: ErrAttachment, Message: "a text attachment was not sent: " + reason}
		}
		b.WriteString(FileBlock(a.Name, string(data)))
		b.WriteString("\n\n")
	}
	if b.Len() == 0 {
		return text, nil
	}
	b.WriteString(text)
	return b.String(), nil
}

// FileBlock is the text of a text attachment as the model reads it: its
// content between <attached-file name="…"> and </attached-file>. A closing
// tag inside the content is broken up so that it cannot end the block.
func FileBlock(name, content string) string {
	name = strings.NewReplacer(`"`, "'", "\n", " ", "\r", " ", "<", "(", ">", ")").Replace(name)
	content = strings.ReplaceAll(content, "</attached-file>", "</attached-file​>")
	return `<attached-file name="` + name + `">` + "\n" + strings.TrimRight(content, "\r\n") + "\n</attached-file>"
}

// AttachmentError is an ErrAttachment for an attachment the backend named
// cannot send.
func AttachmentError(backendName, format string, args ...any) *Error {
	return &Error{Kind: ErrAttachment, Message: backendName + ": " + fmt.Sprintf(format, args...)}
}
