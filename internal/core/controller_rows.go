package core

// Rows of the top frame: local filtering, selection and the action panel's actions.

import (
	"cmp"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/internal/match"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// restoreSelection selects the entry selected before the results of f
// changed, by its key, or else the row at the same index. c.mu must be held.
func (c *Controller) restoreSelection(f *frame) {
	vis := c.visible(f)
	n := countItems(vis)
	if i := indexOfKey(vis, f.selKey); i >= 0 {
		f.sel = i
	} else {
		f.sel = min(f.sel, max(0, n-1))
	}
	if e, ok := itemAt(vis, f.sel); ok {
		f.selKey = e.Key
	}
}

// setSel selects row i of f. c.mu must be held.
func (c *Controller) setSel(f *frame, i int) {
	vis := c.visible(f)
	f.sel = clamp(i, countItems(vis))
	if e, ok := itemAt(vis, f.sel); ok {
		f.selKey = e.Key
	}
}

func indexOfKey(sections []Section, key string) int {
	if key == "" {
		return -1
	}
	i := 0
	for _, s := range sections {
		for _, it := range s.Items {
			if it.Key == key {
				return i
			}
			i++
		}
	}
	return -1
}

// visible returns the sections shown for f, applying local filtering.
func (c *Controller) visible(f *frame) []Section {
	if f.view == nil || !f.view.FilterLocally || strings.TrimSpace(f.text) == "" {
		return f.sections
	}
	var out []Section
	for _, s := range f.sections {
		var items []Entry
		for _, it := range s.Items {
			if m := entryMatch(f.text, it); m > 0 {
				it.Rank = m
				items = append(items, it)
			}
		}
		slices.SortStableFunc(items, func(a, b Entry) int { return cmp.Compare(b.Rank, a.Rank) })
		if len(items) > 0 {
			out = append(out, Section{Title: s.Title, Items: items})
		}
	}
	return out
}

func countItems(sections []Section) int {
	n := 0
	for _, s := range sections {
		n += len(s.Items)
	}
	return n
}

// itemAt returns the i-th entry across sections. i may be -1 (no
// selection) and sections may be empty.
func itemAt(sections []Section, i int) (Entry, bool) {
	if i < 0 {
		return Entry{}, false
	}
	for _, s := range sections {
		if i < len(s.Items) {
			return s.Items[i], true
		}
		i -= len(s.Items)
	}
	return Entry{}, false
}

// Select sets the selected row (e.g. on mouse hover or click).
func (c *Controller) Select(i int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.panel != nil {
		c.panel.sel = clamp(i, len(c.panelActions()))
		return
	}
	c.setSel(c.top(), i)
	c.top().moved = true
	c.keep.fresh = false
}

// Hover selects row i under the mouse pointer. Unlike Select (a click) it
// does not count as the user choosing a row, so results arriving above (the
// context) still move the selection to the first row.
func (c *Controller) Hover(i int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.panel != nil {
		c.panel.sel = clamp(i, len(c.panelActions()))
		return
	}
	c.setSel(c.top(), i)
}

func clamp(i, n int) int { return max(0, min(i, n-1)) }

// panelActions returns the panel's actions filtered by its text. c.mu must be held.
func (c *Controller) panelActions() []plugin.Action {
	if c.panel == nil {
		return nil
	}
	if strings.TrimSpace(c.panel.text) == "" {
		return c.panel.entry.Actions
	}
	var out []plugin.Action
	for _, a := range c.panel.entry.Actions {
		if match.Score(c.panel.text, a.Title) > 0 {
			out = append(out, a)
		}
	}
	return out
}
