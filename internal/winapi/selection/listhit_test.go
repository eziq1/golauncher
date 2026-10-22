package winselection

import (
	"testing"
	"time"
)

// fakeNode is a UI Automation element of a fake tree; released counts the
// nodes freed.
type fakeNode struct {
	ct       int
	class    string
	label    string
	sel      bool
	up       *fakeNode
	released *int
}

func (n *fakeNode) controlType() int  { return n.ct }
func (n *fakeNode) className() string { return n.class }
func (n *fakeNode) name() string      { return n.label }
func (n *fakeNode) selected() bool    { return n.sel }
func (n *fakeNode) release()          { *n.released++ }
func (n *fakeNode) parent() listNode {
	if n.up == nil {
		return nil
	}
	return n.up
}

// chain builds the elements from the one at the point (first) up to the
// top (last).
func chain(released *int, nodes ...fakeNode) *fakeNode {
	var up *fakeNode
	for i := len(nodes) - 1; i >= 0; i-- {
		n := nodes[i]
		n.up, n.released = up, released
		up = &n
	}
	return up
}

func TestListHitOf(t *testing.T) {
	window := fakeNode{ct: uiaWindowControlType, class: "CabinetWClass"}
	view := fakeNode{ct: uiaListControlType, class: "UIItemsView"}
	desktop := fakeNode{ct: uiaListControlType, class: "SysListView32"}
	pane := fakeNode{ct: uiaPaneControlType, class: "DirectUIHWND"}
	group := fakeNode{ct: uiaGroupControlType, label: "Today"}
	item := fakeNode{ct: uiaListItemControlType, class: "UIItem", label: "report", sel: true}
	plain := fakeNode{ct: uiaListItemControlType, label: "notes.txt"}
	cell := fakeNode{ct: 50004, label: "2026-10-22"} // an edit: a column of the details view
	header := fakeNode{ct: 50034}
	tree := fakeNode{ct: 50023}
	treeItem := fakeNode{ct: 50024, label: "Documents"}
	tests := []struct {
		name  string
		nodes []fakeNode
		want  ListHit
	}{
		{"item", []fakeNode{item, view, pane, window}, ListHit{List: "UIItemsView", Item: true, Name: "report", Selected: true}},
		{"item's column", []fakeNode{cell, plain, view, window}, ListHit{List: "UIItemsView", Item: true, Name: "notes.txt"}},
		{"grouped item", []fakeNode{plain, group, view}, ListHit{List: "UIItemsView", Item: true, Name: "notes.txt"}},
		{"desktop icon", []fakeNode{plain, desktop, pane}, ListHit{List: "SysListView32", Item: true, Name: "notes.txt"}},
		{"empty space", []fakeNode{view, pane, window}, ListHit{List: "UIItemsView", Space: true}},
		{"empty space in a group", []fakeNode{group, view}, ListHit{List: "UIItemsView", Space: true}},
		{"empty desktop", []fakeNode{desktop, pane}, ListHit{List: "SysListView32", Space: true}},
		{"column header", []fakeNode{header, view, window}, ListHit{List: "UIItemsView"}},
		{"navigation pane", []fakeNode{treeItem, tree, pane, window}, ListHit{}},
		{"no list at all", []fakeNode{cell}, ListHit{}},
	}
	for _, tt := range tests {
		released := 0
		got := listHitOf(chain(&released, tt.nodes...))
		if got != tt.want {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
		if released == 0 {
			t.Errorf("%s: no element released", tt.name)
		}
	}
}

// TestListHitOfReleasesAll: every element walked is released, also when the
// walk gives up at its depth limit.
func TestListHitOfReleasesAll(t *testing.T) {
	released := 0
	nodes := make([]fakeNode, maxListDepth+5)
	for i := range nodes {
		nodes[i] = fakeNode{ct: uiaPaneControlType}
	}
	if got := listHitOf(chain(&released, nodes...)); got != (ListHit{}) {
		t.Errorf("deep tree: %+v", got)
	}
	if released != maxListDepth+1 {
		t.Errorf("released %d, want %d (the walked ones)", released, maxListDepth+1)
	}
	released = 0
	listHitOf(chain(&released, fakeNode{ct: 50004}, fakeNode{ct: uiaListItemControlType}, fakeNode{ct: uiaListControlType}))
	if released != 3 {
		t.Errorf("released %d of 3", released)
	}
}

func TestListItemAtBudget(t *testing.T) {
	old := listProbe
	t.Cleanup(func() { listProbe = old })
	listProbe = func(x, y int) (ListHit, error) {
		return ListHit{List: "UIItemsView", Item: true, Name: "a"}, nil
	}
	if h, ok := ListItemAt(1, 2, time.Second); !ok || h.Name != "a" {
		t.Errorf("ListItemAt = %+v, %v", h, ok)
	}
	release := make(chan struct{})
	listProbe = func(x, y int) (ListHit, error) {
		<-release
		return ListHit{Item: true}, nil
	}
	if _, ok := ListItemAt(1, 2, 10*time.Millisecond); ok {
		t.Error("a late answer must be dropped")
	}
	close(release)
}
