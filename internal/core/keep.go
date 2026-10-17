package core

import (
	"context"
	"strings"
	"time"

	"github.com/eziq1/golauncher/internal/config"
)

// Views kept across a hide (docs/keep-view.md, R75): when the window hides
// while the top view asks for it (plugin.View.KeepOnHide), the navigation
// stack is kept as it is, views open, and the next show returns to it
// instead of the root search, unless too long has passed (the ReturnToRoot
// setting), text was selected for the new show (unless the kept view holds
// an unsent draft, R76), a plugin of the stack is
// disabled, or the top view refuses the restore (an error for a
// plugin.ViewRequest with Restore).

// keepState is the kept stack between a hide and the next show, and the
// restore of the current show.
type keepState struct {
	// kept is set when the window hid with a view asking to be kept on top,
	// at hiddenAt.
	kept     bool
	hiddenAt time.Time
	// restored is the top frame restored for this show: the footer shows
	// the hint while it is on top.
	restored *frame
	// fresh is set until the user acts on the restored view: text selected
	// for this show and read only now still goes to the root search.
	fresh bool
}

// ShowOptions describe a show of the launcher window.
type ShowOptions struct {
	// Root always starts at the root search, dropping a kept stack: the
	// selected-text hotkey, or a show that opens a command or a task.
	Root bool
}

// Hidden records that the launcher window was hidden. When the top view
// asks to be kept (plugin.View.KeepOnHide), the stack is kept for the next
// Show; nothing is closed either way (Show or Reset does that).
func (c *Controller) Hidden() {
	c.mu.Lock()
	c.keep = keepState{}
	if f := c.top(); f.nav != nil && f.view != nil && f.view.KeepOnHide {
		c.keep.kept, c.keep.hiddenAt = true, c.eng.Now()
	}
	kept := c.keep.kept
	c.mu.Unlock()
	if !kept {
		// The files handed to the root search go with the show, unless the
		// stack above them is kept (rootfiles.go).
		c.clearFiles()
	}
}

// Show prepares the launcher for being shown: the stack kept by the last
// Hidden when it may be restored, else the root search as Reset does (the
// views kept are closed then).
func (c *Controller) Show(ctx context.Context, o ShowOptions) {
	c.mu.Lock()
	k := c.keep
	c.keep = keepState{}
	if !k.kept || o.Root || !c.restorable(k.hiddenAt) {
		c.mu.Unlock()
		c.Reset(ctx)
		return
	}
	c.panel, c.confirm, c.toast = nil, nil, nil
	f := c.top()
	f.restoring = true
	c.keep.restored, c.keep.fresh = f, true
	c.mu.Unlock()
	c.refresh(ctx)
}

// restorable reports whether the kept stack, hidden at hiddenAt, may be
// shown again. c.mu must be held.
func (c *Controller) restorable(hiddenAt time.Time) bool {
	cfg := c.eng.cfg.Get()
	switch cfg.EffectiveReturnToRoot() {
	case config.ReturnToRootImmediately:
		return false
	case config.ReturnToRootAfter:
		if c.eng.Now().Sub(hiddenAt) > config.KeepViewsFor {
			return false
		}
	}
	if c.eng.Selection() != nil && !c.hasDraft() {
		// Text was selected for this show: the root shows it, unless the
		// kept view holds an unsent draft (R76).
		return false
	}
	for _, f := range c.stack {
		if f.nav == nil {
			continue
		}
		if _, ok := c.eng.lookup(f.nav.Plugin); !ok || cfg.Plugin(f.nav.Plugin).Disabled {
			return false
		}
	}
	return true
}

// selectionOverRestore goes to the root search when text selected for this
// show arrived after a kept stack was restored and before the user acted on
// it. c.mu is held on entry; it reports true after unlocking when it did.
func (c *Controller) selectionOverRestore() bool {
	if !c.keep.fresh || c.top() != c.keep.restored || c.eng.Selection() == nil || c.hasDraft() {
		return false
	}
	navs := c.toRoot()
	c.panel, c.confirm = nil, nil
	c.mu.Unlock()
	c.closed(navs)
	c.refresh(context.Background())
	return true
}

// hasDraft reports whether the top view holds text the user typed and did
// not send (e.g. an AI chat's next message), or another draft it declares
// (plugin.View.Draft: a chat's attachments): a selection then does not take
// the user away from it (R76). c.mu must be held.
func (c *Controller) hasDraft() bool {
	f := c.top()
	return f.nav != nil && (strings.TrimSpace(f.text) != "" || f.view != nil && f.view.Draft)
}

// PopToRoot closes every view and goes back to the root search, keeping
// nothing (the footer's hint on a restored view).
func (c *Controller) PopToRoot(ctx context.Context) {
	c.mu.Lock()
	navs := c.toRoot()
	c.panel, c.confirm = nil, nil
	c.mu.Unlock()
	c.closed(navs)
	c.refresh(ctx)
}

// keepHint is the footer's hint on a restored view: how Escape leaves it.
// None once the user moved on, while the view takes Escape itself (an answer
// being written) or while text is typed (Escape clears it first). c.mu must
// be held.
func (c *Controller) keepHint() string {
	f := c.top()
	if c.keep.restored == nil || f != c.keep.restored || f.text != "" || c.panel != nil {
		return ""
	}
	if f.view != nil && f.view.Escape != nil {
		return ""
	}
	if len(c.stack) == 2 {
		return c.eng.core.T("ui.keep.hintRoot")
	}
	return c.eng.core.T("ui.keep.hintBack")
}
