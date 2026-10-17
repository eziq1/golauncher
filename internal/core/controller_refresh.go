package core

// Refresh scheduling: coalescing of Host.Invalidate bursts, and refreshes
// when plugins are turned on or off or the context changes.

import (
	"context"
	"slices"
	"time"
)

// Coalescing of plugin notifications (docs/m5-plugin-platform.md §2, §5.4).
var (
	// invalidateDelay gathers the Host.Invalidate calls of a burst into one
	// refresh.
	invalidateDelay = 50 * time.Millisecond
	// renderInterval is the shortest time between two pushed views applied
	// to one view instance; pushes in between are coalesced to the latest.
	renderInterval = 16 * time.Millisecond
)

// invalidate handles Host.Invalidate of a plugin: when it shows in the top
// frame, the frame is refreshed after a short delay that gathers bursts.
func (c *Controller) invalidate(pluginID, viewID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.affected(invalidation{pluginID, viewID}) {
		return
	}
	c.inv = append(c.inv, invalidation{pluginID, viewID})
	if c.invTimer {
		return
	}
	c.invTimer = true
	c.pending++
	time.AfterFunc(invalidateDelay, c.flushInvalidations)
}

// affected reports whether an invalidation concerns the top frame: the
// plugin's view with that ID (any view for ""), or the root search when the
// plugin contributes to it. c.mu must be held.
func (c *Controller) affected(inv invalidation) bool {
	f := c.top()
	if inv.plugin == "" { // the context changed (contextChanged)
		return f.nav == nil
	}
	if f.nav == nil {
		return c.eng.isRootProvider(inv.plugin)
	}
	return f.nav.Plugin == inv.plugin && (inv.view == "" || inv.view == f.nav.Request.ID)
}

// pluginsChanged follows plugins turned on or off (off lists the latter):
// when a view of a plugin turned off is open, the launcher goes back to the
// root search, dropping the action panel or confirmation too; otherwise the
// root search is refreshed for the commands and results that came or went.
func (c *Controller) pluginsChanged(off []string) {
	ofOff := func(id string) bool { return slices.Contains(off, id) }
	c.mu.Lock()
	if slices.ContainsFunc(c.stack, func(f *frame) bool { return f.nav != nil && ofOff(f.nav.Plugin) }) {
		c.keep = keepState{}
		navs := c.toRoot()
		c.panel, c.confirm = nil, nil
		c.mu.Unlock()
		c.closed(navs)
		c.refresh(context.Background())
		return
	}
	if c.panel != nil && ofOff(c.panel.entry.Plugin) {
		c.panel = nil
	}
	if c.confirm != nil && ofOff(c.confirm.entry.Plugin) {
		c.confirm = nil
	}
	c.mu.Unlock()
	c.invalidate("", "") // refreshes the root search when it shows
}

// contextChanged handles a new context of the show: the root search is
// refreshed when it already ran without it (pulled); otherwise the front end
// is only told, for the context chip.
func (c *Controller) contextChanged(pulled bool) {
	c.mu.Lock()
	if c.selectionOverRestore() {
		return // unlocked
	}
	if f := c.top(); pulled && f.nav == nil {
		if !f.moved {
			// The context section appears above the results the user
			// has not touched yet: select its first row, not the old one.
			f.sel, f.selKey = 0, ""
		}
		c.mu.Unlock()
		c.invalidate("", "")
		return
	}
	notify := c.onChange
	c.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// flushInvalidations refreshes the top frame for the invalidations gathered,
// or, while a search is in flight, once it has finished.
func (c *Controller) flushInvalidations() {
	c.mu.Lock()
	invs := c.inv
	c.inv, c.invTimer = nil, false
	if !slices.ContainsFunc(invs, c.affected) {
		c.pendingDone()
		c.mu.Unlock()
		return
	}
	if c.running > 0 {
		// Refreshing now would cancel the search in flight, and a plugin
		// invalidating continuously would starve it.
		if c.invAfter {
			c.pendingDone()
		}
		c.invAfter = true // keeps the pending count until it refreshes
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.search(context.Background(), true)
	c.mu.Lock()
	c.pendingDone()
	c.mu.Unlock()
}
