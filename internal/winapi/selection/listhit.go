package winselection

import (
	"errors"
	"time"
)

// ListHit describes what lies under a point of the screen when it is in a
// list of items, such as File Explorer's items view or the desktop's icons
// (R120, docs/context-awareness.md §12). It is read through UI Automation
// without side effects: nothing is clicked, focused or selected.
type ListHit struct {
	// List is the UI Automation class name of the list the point is in
	// ("UIItemsView", "SysListView32"), or "" when it is in none.
	List string
	// Item: the point is on an item of the list. Name is its UI Automation
	// name, the name as the list shows it; Selected is its selection state
	// (SelectionItemPattern).
	Item     bool
	Name     string
	Selected bool
	// Space: the point is on the list itself, between its items.
	Space bool
}

// UI Automation control types (UIAutomationClient.h).
const (
	uiaListControlType     = 50008
	uiaListItemControlType = 50007
	uiaDataGridControlType = 50028
	uiaDataItemControlType = 50029
	uiaGroupControlType    = 50026
	uiaPaneControlType     = 50033
	uiaWindowControlType   = 50032
)

// maxListDepth bounds the walk from the element at the point up to its list.
const maxListDepth = 12

// listNode is a UI Automation element seen while walking up from the
// element at a point; the UI Automation one is uiaNode, tests use fakes.
type listNode interface {
	controlType() int
	className() string
	name() string
	selected() bool
	// parent returns the parent in the control view, nil at the top.
	parent() listNode
	release()
}

// listHitOf walks up from n, the element at a point, to the list it is in
// and describes the point: on an item (the first list item or data item
// passed), on the list between items (reached through groups and panes
// only), or neither. Every node is released.
func listHitOf(n listNode) ListHit {
	var h ListHit
	gap := true // only groups and panes between the point and the list
	for depth := 0; n != nil; depth++ {
		if depth == maxListDepth {
			n.release()
			return ListHit{}
		}
		switch ct := n.controlType(); ct {
		case uiaListItemControlType, uiaDataItemControlType:
			if !h.Item {
				h.Item, h.Name, h.Selected = true, n.name(), n.selected()
			}
		case uiaListControlType, uiaDataGridControlType:
			h.List = n.className()
			h.Space = !h.Item && gap
			n.release()
			return h
		case uiaWindowControlType:
			n.release()
			return ListHit{} // a window before any list: not in one
		case uiaGroupControlType, uiaPaneControlType:
		default:
			gap = false // a header, a scroll bar, an item's text…
		}
		p := n.parent()
		n.release()
		n = p
	}
	return ListHit{}
}

// listProbe reads the list hit at a point on the UI Automation thread; set
// per platform, replaced in tests.
var listProbe = func(x, y int) (ListHit, error) { return ListHit{}, errors.ErrUnsupported }

// ListItemAt describes the list item, or the list, at the screen point (x,
// y) in physical pixels, read on the UI Automation thread the selection is
// read on; it waits at most budget (a late answer is dropped). ok is false
// when nothing could be read in time; outside Windows it never is. The
// point must not be covered by a window of this process: its own elements
// are never looked at.
func ListItemAt(x, y int, budget time.Duration) (ListHit, bool) {
	var (
		h   ListHit
		err error
	)
	probe := listProbe
	done := uia.do(func() { h, err = probe(x, y) })
	t := time.NewTimer(budget)
	defer t.Stop()
	select {
	case <-done:
		return h, err == nil
	case <-t.C:
		return ListHit{}, false
	}
}
