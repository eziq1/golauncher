package wincontext

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	winselection "github.com/eziq1/golauncher/internal/winapi/selection"
	"github.com/eziq1/golauncher/pkg/plugin"
)

const (
	downloads = `C:\Users\someone\Downloads`
	zipFile   = downloads + `\a.zip`
	report    = downloads + `\report.docx`
	notes     = downloads + `\notes.txt`
)

// TestPointerSelection is the table of R120: the items reported for each
// place of the pointer.
func TestPointerSelection(t *testing.T) {
	sel := []string{zipFile, notes}
	named := func(items ...viewItem) folderView {
		return folderView{Folder: downloads, Selected: sel, Named: items, NamedOK: true}
	}
	tests := []struct {
		name string
		v    folderView
		h    hit
		want []string
	}{
		{"no pointer", named(), hit{}, sel},
		{"empty space", named(), hit{Kind: hitEmpty}, nil},
		{"unselected item", named(viewItem{"report", report}), hit{Kind: hitItem, Name: "report"}, []string{report}},
		{"item of the selection, by UI Automation", named(), hit{Kind: hitItem, Name: "a.zip", Selected: true}, sel},
		{"item of the selection, by path", named(viewItem{"notes.txt", notes}), hit{Kind: hitItem, Name: "notes.txt"}, sel},
		{"item by file name", named(viewItem{"Report", report}), hit{Kind: hitItem, Name: "report.docx"}, []string{report}},
		{"unknown item", named(), hit{Kind: hitItem, Name: "gone"}, sel},
		{"view not read whole", folderView{Folder: downloads, Selected: sel, Named: []viewItem{{"report", report}}}, hit{Kind: hitItem, Name: "report"}, sel},
		{"virtual item (Recycle Bin)", named(viewItem{"Recycle Bin", ""}), hit{Kind: hitItem, Name: "Recycle Bin"}, nil},
		{"selected virtual item", named(viewItem{"Recycle Bin", ""}), hit{Kind: hitItem, Name: "Recycle Bin", Selected: true}, sel},
		{"empty space, nothing selected", folderView{Folder: downloads}, hit{Kind: hitEmpty}, nil},
		{"item, nothing selected", folderView{Folder: downloads, Named: []viewItem{{"report", report}}, NamedOK: true}, hit{Kind: hitItem, Name: "report"}, []string{report}},
		// macOS: the path comes with the hit.
		{"path in the folder", folderView{Folder: "/Users/someone/Documents", Selected: []string{"/Users/someone/Documents/a.pdf"}},
			hit{Kind: hitItem, Path: "/Users/someone/Documents/b.pdf"}, []string{"/Users/someone/Documents/b.pdf"}},
		{"path of the selection", folderView{Folder: "/Users/someone/Documents", Selected: []string{"/Users/someone/Documents/a.pdf", "/Users/someone/Documents/c.pdf"}},
			hit{Kind: hitItem, Path: "/Users/someone/Documents/a.pdf"}, []string{"/Users/someone/Documents/a.pdf", "/Users/someone/Documents/c.pdf"}},
		{"path elsewhere", folderView{Folder: "/Users/someone/Documents", Selected: []string{"/Users/someone/Documents/a.pdf"}},
			hit{Kind: hitItem, Path: "/Users/someone/Desktop/x.png"}, []string{"/Users/someone/Documents/a.pdf"}},
	}
	for _, tt := range tests {
		if got := pointerSelection(tt.v, tt.h); !slices.Equal(got, tt.want) {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestMatchItem: the item under the pointer is found by the name the view
// shows, extensions hidden and folder names localized, or by its file name;
// of two items shown alike, by its selection state.
func TestMatchItem(t *testing.T) {
	docs := `C:\Users\someone\Documents`
	jpg, txt := downloads+`\photo.jpg`, downloads+`\photo.txt`
	tests := []struct {
		name  string
		v     folderView
		h     hit
		want  string
		found bool
	}{
		{"hidden extension", folderView{Named: []viewItem{{"report", report}}, NamedOK: true}, hit{Name: "report"}, report, true},
		{"localized folder", folderView{Named: []viewItem{{"文档", docs}}, NamedOK: true}, hit{Name: "文档"}, docs, true},
		{"case", folderView{Named: []viewItem{{"Report", report}}, NamedOK: true}, hit{Name: "report"}, report, true},
		{"two shown alike", folderView{Named: []viewItem{{"photo", jpg}, {"photo", txt}}, NamedOK: true}, hit{Name: "photo"}, "", false},
		{"two shown alike, one selected", folderView{Selected: []string{jpg}, Named: []viewItem{{"photo", jpg}, {"photo", txt}}, NamedOK: true}, hit{Name: "photo"}, txt, true},
		{"two shown alike, the selected one", folderView{Selected: []string{jpg}, Named: []viewItem{{"photo", jpg}, {"photo", txt}}, NamedOK: true}, hit{Name: "photo", Selected: true}, jpg, true},
		{"incomplete", folderView{Named: []viewItem{{"report", report}}}, hit{Name: "report"}, "", false},
		{"no name", folderView{Named: []viewItem{{"", report}}, NamedOK: true}, hit{}, "", false},
	}
	for _, tt := range tests {
		it, ok := matchItem(tt.v, tt.h)
		if ok != tt.found || it.Path != tt.want {
			t.Errorf("%s: %+v, %v; want %q, %v", tt.name, it, ok, tt.want, tt.found)
		}
	}
}

func TestHitOfList(t *testing.T) {
	tests := []struct {
		l    winselection.ListHit
		want hit
	}{
		{winselection.ListHit{List: "UIItemsView", Item: true, Name: "report", Selected: true}, hit{Kind: hitItem, Name: "report", Selected: true}},
		{winselection.ListHit{List: "SysListView32", Item: true, Name: "Recycle Bin"}, hit{Kind: hitItem, Name: "Recycle Bin"}},
		{winselection.ListHit{List: "UIItemsView", Space: true}, hit{Kind: hitEmpty}},
		{winselection.ListHit{List: "SysListView32", Space: true}, hit{Kind: hitEmpty}},
		{winselection.ListHit{List: "UIItemsView"}, hit{}},                                  // a column header
		{winselection.ListHit{List: "SysTreeView32", Item: true, Name: "Documents"}, hit{}}, // another list
		{winselection.ListHit{List: "UIItemsView", Item: true}, hit{}},                      // no name
		{winselection.ListHit{}, hit{}},
	}
	for _, tt := range tests {
		if got := hitOfList(tt.l); got != tt.want {
			t.Errorf("hitOfList(%+v) = %+v, want %+v", tt.l, got, tt.want)
		}
	}
}

func TestPointerAt(t *testing.T) {
	old := pointerProbe
	t.Cleanup(func() { pointerProbe = old })
	var probes int
	result, ok := hit{Kind: hitItem, Name: "report"}, true
	pointerProbe = func(target uintptr, x, y float64, budget time.Duration) (hit, bool) {
		probes++
		if target != 7 || x != 10 || y != 20 || budget != PointerBudget {
			t.Errorf("probe(%d, %v, %v, %v)", target, x, y, budget)
		}
		return result, ok
	}
	if p := PointerAt(7, 10, 20); p == nil || p.target() != result {
		t.Errorf("PointerAt = %+v", p)
	}
	result = hit{Kind: hitEmpty}
	if p := PointerAt(7, 10, 20); p == nil || p.target().Kind != hitEmpty {
		t.Errorf("empty space: %+v", p)
	}
	result = hit{}
	if p := PointerAt(7, 10, 20); p != nil {
		t.Errorf("elsewhere: %+v, want nil", p)
	}
	result, ok = hit{Kind: hitItem, Name: "late"}, false
	if p := PointerAt(7, 10, 20); p != nil {
		t.Errorf("not read in time: %+v, want nil", p)
	}
	if PointerAt(0, 10, 20) != nil || probes != 4 {
		t.Errorf("no window: probed %d times", probes)
	}
	var none *Pointer
	if none.target() != (hit{}) {
		t.Error("a nil pointer is no hit")
	}
}

// fakeView is a shell view for the detectors: its folder, selection and
// items, recording the names looked up.
type fakeView struct {
	folder   string
	selected []string
	items    []viewItem
	lookups  []string
}

func (f *fakeView) read(name string) (folderView, bool) {
	f.lookups = append(f.lookups, name)
	v := folderView{Folder: f.folder, Selected: f.selected}
	if name != "" {
		v.NamedOK = true
		for _, it := range f.items {
			if it.Display == name {
				v.Named = append(v.Named, it)
			}
		}
	}
	return v, true
}

// TestDetectorsFollowPointer: File Explorer's and the desktop's detectors
// report the items by the pointer, looking up a name only for an unselected
// item; without a pointer (the launcher's hotkey) they report the selection
// and look nothing up.
func TestDetectorsFollowPointer(t *testing.T) {
	desk := `C:\Users\someone\Desktop`
	ex := &fakeView{folder: downloads, selected: []string{zipFile}, items: []viewItem{{"a.zip", zipFile}, {"report", report}}}
	dt := &fakeView{folder: desk, selected: []string{desk + `\todo.txt`}, items: []viewItem{{"todo", desk + `\todo.txt`}, {"Recycle Bin", ""}, {"plan", desk + `\plan.md`}}}
	withPlatform(t, nil, nil, fakeFS{})
	explorerView = func(_ context.Context, _ uintptr, name string) (folderView, bool) { return ex.read(name) }
	desktopView = func(_ context.Context, name string) (folderView, bool) { return dt.read(name) }
	tests := []struct {
		name   string
		w      Window
		want   []string
		lookup string
	}{
		{"hotkey", Window{Class: ClassExplorer}, []string{zipFile}, ""},
		{"unselected file", Window{Class: ClassExplorer, Pointer: &Pointer{hit{Kind: hitItem, Name: "report"}}}, []string{report}, "report"},
		{"selected file", Window{Class: ClassExplorer, Pointer: &Pointer{hit{Kind: hitItem, Name: "a.zip", Selected: true}}}, []string{zipFile}, ""},
		{"empty space", Window{Class: ClassExplorer, Pointer: &Pointer{hit{Kind: hitEmpty}}}, nil, ""},
		{"desktop hotkey", Window{Class: ClassProgman}, []string{desk + `\todo.txt`}, ""},
		{"desktop icon", Window{Class: ClassProgman, Pointer: &Pointer{hit{Kind: hitItem, Name: "plan"}}}, []string{desk + `\plan.md`}, "plan"},
		{"Recycle Bin", Window{Class: ClassWorkerW, Desktop: true, Pointer: &Pointer{hit{Kind: hitItem, Name: "Recycle Bin"}}}, nil, "Recycle Bin"},
		{"empty desktop", Window{Class: ClassProgman, Pointer: &Pointer{hit{Kind: hitEmpty}}}, nil, ""},
	}
	for _, tt := range tests {
		ex.lookups, dt.lookups = nil, nil
		c := plugin.Context{}
		run(t.Context(), Detectors(), tt.w, &c)
		if !slices.Equal(c.Selected, tt.want) {
			t.Errorf("%s: selected %q, want %q", tt.name, c.Selected, tt.want)
		}
		if c.Folder != ex.folder && c.Folder != desk {
			t.Errorf("%s: folder %q", tt.name, c.Folder)
		}
		if got := append(ex.lookups, dt.lookups...); len(got) != 1 || got[0] != tt.lookup {
			t.Errorf("%s: looked up %q, want %q", tt.name, got, tt.lookup)
		}
	}
}

// fakeFinderNode is an Accessibility element of a fake Finder tree.
type fakeFinderNode struct {
	r, sub, id, u, file string
	up                  *fakeFinderNode
	released            *int
}

func (n *fakeFinderNode) role() string       { return n.r }
func (n *fakeFinderNode) subrole() string    { return n.sub }
func (n *fakeFinderNode) identifier() string { return n.id }
func (n *fakeFinderNode) url() string        { return n.u }
func (n *fakeFinderNode) filename() string   { return n.file }
func (n *fakeFinderNode) release()           { *n.released++ }
func (n *fakeFinderNode) parent() finderNode {
	if n.up == nil {
		return nil
	}
	return n.up
}

func finderChain(released *int, nodes ...fakeFinderNode) *fakeFinderNode {
	var up *fakeFinderNode
	for i := len(nodes) - 1; i >= 0; i-- {
		n := nodes[i]
		n.up, n.released = up, released
		up = &n
	}
	return up
}

func TestFinderHitOf(t *testing.T) {
	win := fakeFinderNode{r: "AXWindow"}
	split := fakeFinderNode{r: "AXSplitGroup"}
	scroll := fakeFinderNode{r: "AXScrollArea"}
	outline := fakeFinderNode{r: "AXOutline"}
	list := fakeFinderNode{r: "AXList"}
	row := fakeFinderNode{r: "AXRow"}
	cell := fakeFinderNode{r: "AXCell"}
	text := fakeFinderNode{r: "AXTextField"}
	icon := fakeFinderNode{r: "AXImage", u: "file:///Users/someone/Documents/b%20c.pdf"}
	named := fakeFinderNode{r: "AXImage", file: "notes.txt"}
	sidebarOutline := fakeFinderNode{r: "AXOutline", sub: "AXSourceList"}
	sidebarByID := fakeFinderNode{r: "AXOutline", id: "SidebarOutline"}
	sideRow := fakeFinderNode{r: "AXRow", u: "file:///Users/someone/Downloads/"}
	toolbar := fakeFinderNode{r: "AXToolbar"}
	button := fakeFinderNode{r: "AXButton"}
	tests := []struct {
		name  string
		nodes []fakeFinderNode
		want  hit
	}{
		{"icon by URL", []fakeFinderNode{icon, list, scroll, split, win}, hit{Kind: hitItem, Path: "/Users/someone/Documents/b c.pdf"}},
		{"row's text", []fakeFinderNode{text, cell, {r: "AXRow", u: "file:///Users/someone/Documents/a.pdf"}, outline, scroll, win}, hit{Kind: hitItem, Path: "/Users/someone/Documents/a.pdf"}},
		{"by file name", []fakeFinderNode{named, list, scroll, win}, hit{Kind: hitItem, Name: "notes.txt"}},
		{"empty list view", []fakeFinderNode{outline, scroll, split, win}, hit{Kind: hitEmpty}},
		{"empty icon view", []fakeFinderNode{list, scroll, win}, hit{Kind: hitEmpty}},
		{"row without a file", []fakeFinderNode{text, cell, row, outline, scroll, win}, hit{}},
		{"sidebar", []fakeFinderNode{sideRow, sidebarOutline, scroll, split, win}, hit{}},
		{"sidebar by identifier", []fakeFinderNode{sidebarByID, scroll, win}, hit{}},
		{"toolbar", []fakeFinderNode{button, toolbar, win}, hit{}},
		{"no scroll area", []fakeFinderNode{list, win}, hit{}},
	}
	for _, tt := range tests {
		released := 0
		if got := finderHitOf(finderChain(&released, tt.nodes...)); got != tt.want {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
		if released == 0 {
			t.Errorf("%s: nothing released", tt.name)
		}
	}
}

func TestLocalNamed(t *testing.T) {
	old := readDirNames
	t.Cleanup(func() { readDirNames = old })
	readDirNames = func(dir string) ([]string, error) {
		if dir != "/Users/someone/Documents" {
			return nil, errors.New("no such folder")
		}
		return []string{"notes.txt", "report.pages", "report", "Plan.key", ".hidden"}, nil
	}
	got, ok := localNamed("/Users/someone/Documents", "plan")
	if !ok || len(got) != 1 || got[0].Path != "/Users/someone/Documents/Plan.key" {
		t.Errorf("hidden extension: %+v, %v", got, ok)
	}
	got, _ = localNamed("/Users/someone/Documents", "report")
	if len(got) != 2 {
		t.Errorf("two shown alike: %+v", got)
	}
	got, _ = localNamed("/Users/someone/Documents", "notes.txt")
	if len(got) != 1 || got[0].Path != "/Users/someone/Documents/notes.txt" {
		t.Errorf("file name: %+v", got)
	}
	if _, ok := localNamed("/nowhere", "x"); ok {
		t.Error("an unreadable folder is no answer")
	}
}
