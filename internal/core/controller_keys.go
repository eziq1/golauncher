package core

// Keyboard and navigation: key presses in the launcher and in the action panel.

import (
	"context"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// KeyTasks opens Background Tasks from anywhere in the launcher; it takes
// precedence over action shortcuts.
const KeyTasks = "Ctrl+J"

// Key handles a key press such as "Down", "Enter", "Ctrl+K" or an action
// shortcut, and returns what the front end should do.
func (c *Controller) Key(ctx context.Context, key string) (Outcome, error) {
	key = normalizeShortcut(key)
	// Act on the results of what has been typed so far.
	if key != "Up" && key != "Down" {
		c.waitSearches()
	}
	c.mu.Lock()
	c.keep.fresh = false // the user acts on the restored view (keep.go)
	if len(c.external) > 0 {
		c.externalKey(key) // unlocks
		return Outcome{}, nil
	}
	if p := c.confirm; p != nil {
		// While confirming, only Enter (confirm) and Escape (cancel) count.
		switch key {
		case "Enter":
			c.confirm = nil
			c.mu.Unlock()
			return c.exec(ctx, p.entry, p.action)
		case "Escape":
			c.confirm = nil
		}
		c.mu.Unlock()
		return Outcome{}, nil
	}
	if key == KeyTasks && c.eng.tasks != nil {
		c.mu.Unlock()
		return c.OpenTasks(ctx, "")
	}
	if c.panel != nil {
		return c.panelKey(ctx, key) // unlocks
	}
	f := c.top()
	if f.form != nil {
		return c.formKey(ctx, key) // unlocks
	}
	if c.argumentKey(f, key) {
		c.mu.Unlock()
		return Outcome{}, nil
	}
	if key == "Tab" && f.nav == nil && strings.TrimSpace(f.text) != "" {
		// Tab with text in the root search, where the selected entry takes
		// no arguments: the plugin.TabProvider's action (e.g. ask AI).
		text := f.text
		c.mu.Unlock()
		entry, action, ok := c.eng.tabAction(ctx, text)
		if !ok {
			return Outcome{}, nil
		}
		return c.run(ctx, entry, action)
	}
	if key == "Escape" && f.view != nil && f.view.Escape != nil {
		// The view takes Escape (e.g. to stop an answer being written).
		entry, action := Entry{Plugin: f.nav.Plugin}, *f.view.Escape
		c.mu.Unlock()
		return c.run(ctx, entry, action)
	}
	n := countItems(c.visible(f))
	nav := key
	if f.view != nil && f.view.Kind == plugin.ViewDetail {
		// A detail view lists no rows: these keys are free for its actions
		// (Ctrl+N for "New Chat").
		switch key {
		case "Down", "Up", "Ctrl+N", "Ctrl+P":
			nav = ""
		}
	}
	switch nav {
	case "Down", "Ctrl+N":
		if n > 0 {
			c.setSel(f, (f.sel+1)%n)
			f.moved = true
		}
		c.mu.Unlock()
		return Outcome{}, nil
	case "Up", "Ctrl+P":
		if n > 0 {
			c.setSel(f, (f.sel-1+n)%n)
			f.moved = true
		}
		c.mu.Unlock()
		return Outcome{}, nil
	case "Escape":
		switch {
		case f.text != "":
			c.mu.Unlock()
			c.SetText(ctx, "")
			return Outcome{}, nil
		case len(c.stack) > 1:
			c.back() // unlocks
			c.refresh(ctx)
			return Outcome{}, nil
		default:
			c.mu.Unlock()
			return Outcome{Hide: true}, nil
		}
	case "Backspace":
		// Backspace in an empty search field goes back, as in Raycast.
		if f.text == "" && len(c.stack) > 1 {
			c.back() // unlocks
			c.refresh(ctx)
			return Outcome{}, nil
		}
		c.mu.Unlock()
		return Outcome{}, nil
	}
	entry, ok := itemAt(c.visible(f), f.sel)
	if !ok {
		c.mu.Unlock()
		return Outcome{}, nil
	}
	if key == "Ctrl+K" {
		c.panel = &panel{entry: entry}
		c.mu.Unlock()
		return Outcome{}, nil
	}
	var action plugin.Action
	switch key {
	case "Enter":
		if len(entry.Actions) == 0 {
			c.mu.Unlock()
			return Outcome{}, nil
		}
		action = entry.Actions[0]
	case "Ctrl+Enter":
		if len(entry.Actions) < 2 {
			c.mu.Unlock()
			return Outcome{}, nil
		}
		action = entry.Actions[1]
	default:
		i := slices.IndexFunc(entry.Actions, func(a plugin.Action) bool {
			return a.Shortcut != "" && normalizeShortcut(a.Shortcut) == key
		})
		if i < 0 {
			c.mu.Unlock()
			return Outcome{}, nil
		}
		action = entry.Actions[i]
	}
	c.mu.Unlock()
	return c.run(ctx, entry, action)
}

// back pops the top view. c.mu is held on entry and released before
// returning.
func (c *Controller) back() {
	navs := c.truncate(len(c.stack) - 1)
	c.mu.Unlock()
	c.closed(navs)
}

// panelKey handles a key while the action panel is open. c.mu is held on
// entry and released before returning.
func (c *Controller) panelKey(ctx context.Context, key string) (Outcome, error) {
	actions := c.panelActions()
	n := len(actions)
	switch key {
	case "Down", "Ctrl+N":
		if n > 0 {
			c.panel.sel = (c.panel.sel + 1) % n
		}
	case "Up", "Ctrl+P":
		if n > 0 {
			c.panel.sel = (c.panel.sel - 1 + n) % n
		}
	case "Escape", "Ctrl+K":
		c.panel = nil
	case "Enter":
		if n == 0 {
			break
		}
		entry, action := c.panel.entry, actions[clamp(c.panel.sel, n)]
		c.panel = nil
		c.mu.Unlock()
		return c.run(ctx, entry, action)
	default:
		i := slices.IndexFunc(c.panel.entry.Actions, func(a plugin.Action) bool {
			return a.Shortcut != "" && normalizeShortcut(a.Shortcut) == key
		})
		if i >= 0 {
			entry := c.panel.entry
			c.panel = nil
			c.mu.Unlock()
			return c.run(ctx, entry, entry.Actions[i])
		}
	}
	c.mu.Unlock()
	return Outcome{}, nil
}
