package plugin

import "context"

// PermFiles lets a plugin see the files the user handed to the root search
// (dropped, pasted or chosen; docs/root-files.md), through
// FileActionsProvider. Only built-in plugins get them for now: plugins in
// their own process are never called.
const PermFiles Permission = "files"

// FileKind is what a file is, told by its extension (FileInfo.Kind).
type FileKind string

const (
	FileImage    FileKind = "image"
	FileText     FileKind = "text"     // plain text, Markdown, code, CSV, JSON…
	FileDocument FileKind = "document" // PDF and office documents
	FileArchive  FileKind = "archive"
	FileAudio    FileKind = "audio"
	FileVideo    FileKind = "video"
	FileFolder   FileKind = "folder"
	FileOther    FileKind = "other"
)

// FileInfo describes a file handed to the root search. The host fills it
// in from the file system's metadata and the name: it never reads the
// file's content.
type FileInfo struct {
	// Path is the file's absolute path. For a Pasted image it is a
	// temporary file the host wrote, deleted soon after the launcher hides.
	Path string `json:"path"`
	// Name is the name shown to the user (the base name of Path, or "Pasted
	// Image.png" in the user's language for a pasted image).
	Name string `json:"name"`
	// Size is the size in bytes, 0 for a folder.
	Size int64    `json:"size"`
	Dir  bool     `json:"dir,omitzero"`
	Kind FileKind `json:"kind"`
	// MediaType is the media type of the extension ("image/png"), "" when
	// unknown.
	MediaType string `json:"mediaType,omitempty"`
	// Pasted marks an image pasted from the clipboard.
	Pasted bool `json:"pasted,omitzero"`
}

// FilesQuery is passed to FileActionsProvider.
type FilesQuery struct {
	Files  []FileInfo  `json:"files"`
	Source FilesSource `json:"source"`
	// Text is what the user typed in the root search, "" when nothing.
	Text string `json:"text"`
}

// FileActionsProvider contributes items for the files handed to the root
// search. The host shows them in the "Files" section at the top of the root
// search, above its other results, and keeps those matching the text typed.
// Only plugins declaring PermFiles are called; return nil when nothing
// applies.
//
// The host marks the items as Transient and Sensitive (never recorded as
// recently used, never shown to AI), so an action must carry the paths it
// needs in its Payload or Target. Item.Score works as for
// SelectionProvider: the order while nothing is typed (highest first), and
// with text typed an item with a Score is taken as matching it with that
// score.
type FileActionsProvider interface {
	FileItems(ctx context.Context, q FilesQuery) ([]Item, error)
}
