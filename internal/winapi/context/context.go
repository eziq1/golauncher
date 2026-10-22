// Package wincontext finds out what the user was doing in the window that
// was active when the launcher opened: which app it belongs to, the folder
// it shows and the files selected in it (docs/context-awareness.md §3).
//
// Detectors are tried in order and the first that recognizes the window
// fills in the context; Register adds more (another file manager, …). Built
// in are File Explorer and the desktop (through the shell's COM interfaces)
// and a generic detector that finds an existing path in the window title.
// Without any match the context holds only the app and the window title.
//
// The package reads other windows only when Detect is called; the launcher
// does so once per show and only with context awareness turned on. On other
// platforms Detect reports nothing.
package wincontext

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Window classes the built-in detectors recognize.
const (
	ClassExplorer = "CabinetWClass" // a File Explorer window
	ClassProgman  = "Progman"       // the desktop
	ClassWorkerW  = "WorkerW"       // the desktop (behind a wallpaper slideshow), if it hosts the desktop view
)

// Window is the foreground window a detector looks at.
type Window struct {
	HWND  uintptr
	Class string
	Title string
	// Desktop is set for a WorkerW window that hosts the desktop's view.
	Desktop bool
	// Pointer is what was under the mouse pointer (the Action Ring, R120);
	// nil for the launcher's hotkey.
	Pointer *Pointer
}

// Detector recognizes a kind of window and describes its context.
type Detector interface {
	Name() string
	// Detect fills in c for window w (c.App and c.Title are already set),
	// or reports false if it does not apply.
	Detect(ctx context.Context, w Window, c *plugin.Context) bool
}

var (
	mu        sync.Mutex
	detectors = []Detector{explorerDetector{}, desktopDetector{}, titleDetector{}}
)

// Register adds d after the detectors registered so far. Add detectors for
// specific apps before the generic title detector with RegisterFirst.
func Register(d Detector) {
	mu.Lock()
	detectors = append(detectors, d)
	mu.Unlock()
}

// RegisterFirst adds d before all other detectors.
func RegisterFirst(d Detector) {
	mu.Lock()
	detectors = slices.Insert(detectors, 0, d)
	mu.Unlock()
}

// Detectors returns the registered detectors in order.
func Detectors() []Detector {
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(detectors)
}

// run tries ds in order on w and returns the name of the one that matched.
func run(ctx context.Context, ds []Detector, w Window, c *plugin.Context) string {
	for _, d := range ds {
		if ctx.Err() != nil {
			return ""
		}
		if d.Detect(ctx, w, c) {
			return d.Name()
		}
	}
	return ""
}

// explorerView and desktopView read the folder and the selection of a
// shell view (File Explorer or the desktop) and, when name is not empty,
// the items bearing that name (the item under the pointer); set per
// platform, replaced in tests.
var (
	explorerView func(ctx context.Context, hwnd uintptr, name string) (v folderView, ok bool)
	desktopView  func(ctx context.Context, name string) (v folderView, ok bool)
	// statPath reports whether path exists and is a directory.
	statPath func(path string) (isDir, ok bool)
	homeDir  func() string
)

// explorerDetector recognizes File Explorer windows (the active tab on
// Windows 11).
type explorerDetector struct{}

func (explorerDetector) Name() string { return plugin.ContextExplorer }

func (explorerDetector) Detect(ctx context.Context, w Window, c *plugin.Context) bool {
	if w.Class != ClassExplorer || explorerView == nil {
		return false
	}
	h := w.Pointer.target()
	v, ok := explorerView(ctx, w.HWND, h.lookup())
	if !ok {
		return false
	}
	// A virtual folder (This PC, Recycle Bin) has no path: Kind only.
	c.Kind, c.Folder, c.Selected = plugin.ContextExplorer, v.Folder, pointerSelection(v, h)
	return true
}

// desktopDetector recognizes the desktop.
type desktopDetector struct{}

func (desktopDetector) Name() string { return plugin.ContextDesktop }

func (desktopDetector) Detect(ctx context.Context, w Window, c *plugin.Context) bool {
	if (w.Class != ClassProgman && !(w.Class == ClassWorkerW && w.Desktop)) || desktopView == nil {
		return false
	}
	h := w.Pointer.target()
	v, ok := desktopView(ctx, h.lookup())
	if !ok {
		return false
	}
	c.Kind, c.Folder, c.Selected = plugin.ContextDesktop, v.Folder, pointerSelection(v, h)
	return true
}

// titleDetector finds an existing absolute path in the window title, which
// many editors, terminals and file managers show.
type titleDetector struct{}

func (titleDetector) Name() string { return plugin.ContextPathInTitle }

func (titleDetector) Detect(_ context.Context, w Window, c *plugin.Context) bool {
	if statPath == nil {
		return false
	}
	home := ""
	if homeDir != nil {
		home = homeDir()
	}
	p, isDir, ok := PathFromTitle(w.Title, home, statPath)
	if !ok || strings.EqualFold(p, c.App.Path) {
		// A console shows its own executable (C:\Windows\system32\cmd.exe),
		// which says nothing about where the user works.
		return false
	}
	c.Kind = plugin.ContextPathInTitle
	if isDir {
		c.Folder = p
		return true
	}
	c.Folder = parentDir(p)
	if c.Extra == nil {
		c.Extra = map[string]string{}
	}
	c.Extra["file"] = p
	return true
}

// quoted matches text in quotes: "…", '…', “…”, 「…」, 『…』.
var quoted = regexp.MustCompile(`"([^"]+)"|'([^']+)'|“([^”]+)”|「([^」]+)」|『([^』]+)』`)

// pathStart matches the start of an absolute Windows path: a drive (C:\ or
// C:/), a UNC share (\\server\share) or the home folder (~\ or ~/).
var pathStart = regexp.MustCompile(`[A-Za-z]:[\\/]|\\\\[^\\\s]+\\|~[\\/]`)

// maxStats bounds the file system lookups for one title.
const maxStats = 48

// PathFromTitle returns the longest existing absolute path in a window
// title, trying quoted text first. A path may be followed by anything (" -
// Notepad++", " [Administrator]") and preceded by anything ("Administrator:
// ", "*" for unsaved). "~" stands for home. stat reports whether a path
// exists and is a directory.
func PathFromTitle(title, home string, stat func(string) (isDir, ok bool)) (path string, isDir, ok bool) {
	var cands []string
	for _, m := range quoted.FindAllStringSubmatch(title, -1) {
		for _, g := range m[1:] {
			if g != "" {
				cands = append(cands, g)
			}
		}
	}
	cands = append(cands, title)
	stats := 0
	tried := map[string]bool{}
	for _, s := range cands {
		for _, loc := range pathStart.FindAllStringIndex(s, -1) {
			rest := s[loc[0]:]
			ends := pathEnds(rest)
			for _, end := range slices.Backward(ends) {
				p := normalize(strings.TrimRight(rest[:end], trailing), home)
				if p == "" || tried[p] {
					continue
				}
				tried[p] = true
				if stats++; stats > maxStats {
					return "", false, false
				}
				if dir, ok := stat(p); ok {
					return p, dir, true
				}
			}
		}
	}
	return "", false, false
}

// trailing are characters that end a path in a title but rarely end a real
// one: separators of the app name and unsaved-changes markers.
const trailing = " \t-–—|*•·:>,;"

// pathEnds returns the candidate end offsets of a path at the start of s,
// in increasing order: before every space or closing punctuation, and the
// end of s.
func pathEnds(s string) []int {
	var out []int
	for i, r := range s {
		if i > 0 && strings.ContainsRune(" \t\"'”」』|>*[(", r) {
			out = append(out, i)
		}
	}
	return append(out, len(s))
}

// normalize turns a path found in a title into a clean absolute path, or ""
// when it is not one.
func normalize(p, home string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	if rest, ok := strings.CutPrefix(p, `~\`); ok {
		if home == "" {
			return ""
		}
		p = strings.TrimRight(home, `\/`) + `\` + rest
	}
	if len(p) < 3 || !pathStart.MatchString(p[:min(len(p), 3)]) && !strings.HasPrefix(p, `\\`) {
		return ""
	}
	// Collapse repeated separators (not the UNC prefix) and drop a trailing
	// one except at a drive root.
	prefix := ""
	if strings.HasPrefix(p, `\\`) {
		prefix, p = `\\`, p[2:]
	}
	for strings.Contains(p, `\\`) {
		p = strings.ReplaceAll(p, `\\`, `\`)
	}
	p = prefix + p
	if len(p) > 3 {
		p = strings.TrimRight(p, `\`)
	}
	return p
}

// parentDir is the folder of a Windows path; a drive root keeps its
// backslash ("C:\").
func parentDir(p string) string {
	before, _, ok := strings.CutLast(strings.TrimRight(p, `\`), `\`)
	if !ok {
		return p
	}
	if len(before) == 2 && before[1] == ':' {
		return before + `\`
	}
	return before
}
