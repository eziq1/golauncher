package plugin

import (
	"context"
	"maps"
	"slices"
)

// Context kinds set by the host's detectors (Context.Kind).
const (
	// ContextExplorer is a File Explorer window.
	ContextExplorer = "explorer"
	// ContextDesktop is the desktop.
	ContextDesktop = "desktop"
	// ContextPathInTitle is a window whose title names a file or folder
	// (editors, terminals, other file managers).
	ContextPathInTitle = "path-in-title"
	// ContextFileDialog is an Open or Save dialog (not detected yet).
	ContextFileDialog = "file-dialog"
)

// Context describes what the user was doing when the launcher opened: the
// window that was in the foreground and, when a detector recognized it, the
// folder it shows and the files selected in it (docs/context-awareness.md).
//
// The host captures it once per show, keeps it in memory only and never
// stores it. It is nil in Query.Context when nothing was captured (for
// example when the user turned context awareness off).
type Context struct {
	App AppInfo `json:"app"`
	// Kind names the detector that recognized the window (ContextExplorer,
	// ContextDesktop, ContextPathInTitle, …); "" when only App and Title are
	// known.
	Kind string `json:"kind,omitempty"`
	// Folder is the current folder, an absolute path.
	Folder string `json:"folder,omitempty"`
	// Selected are the selected files and folders, absolute paths.
	Selected []string `json:"selected,omitempty"`
	// Title is the window title.
	Title string `json:"title,omitempty"`
	// Extra holds detector-specific details, e.g. "file" for the file named
	// by a window title.
	Extra map[string]string `json:"extra,omitempty"`
}

// Clone returns a deep copy of c.
func (c Context) Clone() Context {
	c.Selected = slices.Clone(c.Selected)
	c.Extra = maps.Clone(c.Extra)
	return c
}

// AppInfo is the application that owned the foreground window.
type AppInfo struct {
	// Name is the executable name, e.g. "explorer.exe".
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	PID  int    `json:"pid,omitzero"`
	// Package is the AppUserModelID of a packaged (UWP/MSIX) app.
	Package string `json:"package,omitempty"`
}

// ContextProvider contributes items for the current context. They are
// shown in a section at the top of the empty root search and take part in
// the root search when text is typed. ContextItems is called with the
// context of the current show; return nil when nothing applies.
type ContextProvider interface {
	ContextItems(ctx context.Context, c Context) ([]Item, error)
}
