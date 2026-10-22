package wincontext

import (
	"os"
	"path"
	"slices"
	"strings"
	"time"

	winselection "github.com/eziq1/golauncher/internal/winapi/selection"
)

// The item under the pointer (R120, docs/context-awareness.md §12). The
// Action Ring opens where the pointer is, and, as Windows' context menu
// does, its context follows the item under it rather than only the
// selection:
//
//	pointer on                          reported items (Context.Selected)
//	an item that is not selected        that item only
//	an item of the selection            the whole selection
//	the view's empty space              none: the folder itself
//	anything else                       the selection, as without a pointer
//
// The folder stays the view's folder. The selection itself is never
// changed: the item is read through UI Automation (Windows) or
// Accessibility (macOS) only. The launcher's hotkey passes no pointer and
// keeps the selection.

// Pointer is what was under the mouse pointer when the Action Ring opened,
// as PointerAt read it. A nil *Pointer means nothing is known: DetectAt then
// reports what Detect does.
type Pointer struct{ hit hit }

// hitKind is what the pointer is on in a folder view.
type hitKind int

const (
	hitOther hitKind = iota // not the view's items or empty space
	hitEmpty                // the view's empty space
	hitItem                 // an item
)

// hit is what the platform found under the pointer.
type hit struct {
	Kind hitKind
	// Name is the item's name as the view shows it (Windows: the UI
	// Automation name, with the extension hidden or localized as shown).
	Name string
	// Path is the item's path when the platform tells it (macOS: AXURL).
	Path string
	// Selected: the platform says the item is selected (Windows:
	// SelectionItemPattern).
	Selected bool
}

// target returns the hit of p; nil is hitOther.
func (p *Pointer) target() hit {
	if p == nil {
		return hit{}
	}
	return p.hit
}

// lookup returns the name of the item to look up among the view's items:
// only for an item known by name alone and not part of the selection.
func (h hit) lookup() string {
	if h.Kind != hitItem || h.Path != "" || h.Selected {
		return ""
	}
	return h.Name
}

// viewItem is an item of a folder view that bears the name looked up.
type viewItem struct {
	Display string // the name as shown (SIGDN_NORMALDISPLAY)
	Path    string // its file system path, "" for a virtual item (Recycle Bin)
}

// folderView is what a shell view (File Explorer, the desktop, Finder)
// shows: its folder, the selected items and, when a name was looked up, the
// items bearing it.
type folderView struct {
	Folder   string
	Selected []string
	// Named are the items whose name as shown, or file name, is the one
	// looked up; NamedOK is set when all items were looked at.
	Named   []viewItem
	NamedOK bool
}

// PointerBudget bounds PointerAt: it runs before the ring shows.
const PointerBudget = 100 * time.Millisecond

// pointerProbe reads what is under the screen point (x, y) of window target
// within budget; ok is false when target is not a folder view or nothing
// was read in time. Set per platform, replaced in tests.
var pointerProbe func(target uintptr, x, y float64, budget time.Duration) (h hit, ok bool)

// PointerAt reads what is under the screen point (x, y), in the mouse
// hook's units (Windows: physical pixels; macOS: points from the top left),
// for DetectAt of window target (on macOS the frontmost app's process). It
// looks only where the context can use it (a File Explorer window, the
// desktop, Finder) and returns nil elsewhere, when the pointer is on
// neither an item nor empty space, or after PointerBudget. It must run
// before a window of golauncher covers the point.
func PointerAt(target uintptr, x, y float64) *Pointer {
	if target == 0 || pointerProbe == nil {
		return nil
	}
	h, ok := pointerProbe(target, x, y, PointerBudget)
	if !ok || h.Kind == hitOther {
		return nil
	}
	return &Pointer{hit: h}
}

// viewListClasses are the UI Automation classes of the folder views' lists:
// File Explorer's items view and the desktop's icons.
var viewListClasses = []string{"UIItemsView", "SysListView32"}

// hitOfList describes a UI Automation list hit as a hit: an item or the
// empty space of a folder view's list, else hitOther.
func hitOfList(l winselection.ListHit) hit {
	switch {
	case !slices.Contains(viewListClasses, l.List):
		return hit{}
	case l.Item && l.Name != "":
		return hit{Kind: hitItem, Name: l.Name, Selected: l.Selected}
	case l.Space:
		return hit{Kind: hitEmpty}
	}
	return hit{}
}

// pointerSelection returns the items the context reports for view v with
// the pointer on h, by the table above. An item that cannot be told for
// sure (no item bears its name, or two do) leaves the selection.
func pointerSelection(v folderView, h hit) []string {
	switch h.Kind {
	case hitEmpty:
		return nil
	case hitItem:
	default:
		return v.Selected
	}
	if h.Selected {
		return v.Selected
	}
	p := h.Path
	if p == "" {
		it, ok := matchItem(v, h)
		if !ok {
			return v.Selected
		}
		p = it.Path
	} else if !containsPath(v.Selected, p) && !inFolder(p, v.Folder) {
		// Not an item of this view (Finder's desktop behind its window).
		return v.Selected
	}
	switch {
	case p != "" && containsPath(v.Selected, p):
		return v.Selected
	case p == "":
		return nil // a virtual item (Recycle Bin): no path to offer
	}
	return []string{p}
}

// matchItem finds the item of v the pointer is on by its name: the one
// item bearing it; of two (a.txt and a.jpg with extensions hidden), the
// one whose selection state is the pointed item's.
func matchItem(v folderView, h hit) (viewItem, bool) {
	if !v.NamedOK || h.Name == "" {
		return viewItem{}, false
	}
	var cands []viewItem
	for _, it := range v.Named {
		if strings.EqualFold(it.Display, h.Name) || it.Path != "" && strings.EqualFold(baseName(it.Path), h.Name) {
			cands = append(cands, it)
		}
	}
	if len(cands) > 1 {
		cands = slices.DeleteFunc(cands, func(it viewItem) bool {
			return (it.Path != "" && containsPath(v.Selected, it.Path)) != h.Selected
		})
	}
	if len(cands) != 1 {
		return viewItem{}, false
	}
	return cands[0], true
}

// containsPath reports whether paths holds p, ignoring case as Windows and
// macOS file systems do by default.
func containsPath(paths []string, p string) bool {
	return slices.ContainsFunc(paths, func(s string) bool { return strings.EqualFold(s, p) })
}

// baseName is the last element of a Windows or POSIX path.
func baseName(p string) string {
	p = strings.TrimRight(p, `\/`)
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// inFolder reports whether p is directly in folder.
func inFolder(p, folder string) bool {
	if folder == "" {
		return false
	}
	var dir string
	if strings.Contains(p, `\`) {
		dir = parentDir(p)
	} else {
		dir = path.Dir(p)
	}
	return strings.EqualFold(strings.TrimRight(dir, `\/`), strings.TrimRight(folder, `\/`))
}

// readDirNames lists the names in a folder; replaced in tests.
var readDirNames = func(dir string) ([]string, error) {
	es, err := os.ReadDir(dir)
	names := make([]string, len(es))
	for i, e := range es {
		names[i] = e.Name()
	}
	return names, err
}

// localNamed finds the items of POSIX folder named name as shown: its file
// name, or its name without the extension Finder hides. It is how a name
// read with Accessibility is matched on macOS.
func localNamed(folder, name string) ([]viewItem, bool) {
	names, err := readDirNames(folder)
	if err != nil {
		return nil, false
	}
	var out []viewItem
	for _, n := range names {
		stem := strings.TrimSuffix(n, path.Ext(n))
		if strings.EqualFold(n, name) || stem != "" && stem != n && strings.EqualFold(stem, name) {
			out = append(out, viewItem{Display: name, Path: path.Join(folder, n)})
		}
	}
	return out, true
}
