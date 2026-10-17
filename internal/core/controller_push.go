package core

// Pushed views: Host.Render merged into the open view instance, rate-limited.

import (
	"slices"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// push handles Host.Render: the view replaces the content of the open view
// instance, at most once per renderInterval; pushes for instances that are
// not open are dropped. Pushes are ordered by their arrival.
func (c *Controller) push(pluginID, instance string, v plugin.View) {
	c.mu.Lock()
	i := slices.IndexFunc(c.stack, func(f *frame) bool {
		return f.nav != nil && f.nav.Plugin == pluginID && f.nav.Request.Instance == instance
	})
	if i < 0 || instance == "" {
		c.mu.Unlock()
		return
	}
	f := c.stack[i]
	f.pushed = &v
	if f.pushTimer {
		c.mu.Unlock() // the scheduled flush applies the latest view
		return
	}
	if wait := renderInterval - time.Since(f.lastPush); wait > 0 {
		f.pushTimer = true
		c.pending++
		c.mu.Unlock()
		time.AfterFunc(wait, func() { c.flushPush(f) })
		return
	}
	notify := c.applyPush(f)
	c.mu.Unlock()
	if notify != nil {
		notify()
	}
}

func (c *Controller) flushPush(f *frame) {
	c.mu.Lock()
	f.pushTimer = false
	var notify func()
	if slices.Contains(c.stack, f) {
		notify = c.applyPush(f)
	} else {
		f.pushed = nil // closed meanwhile
	}
	c.mu.Unlock()
	if notify != nil {
		notify()
	}
	c.mu.Lock()
	c.pendingDone()
	c.mu.Unlock()
}

// applyPush shows the latest pushed view of f. c.mu must be held.
func (c *Controller) applyPush(f *frame) func() {
	v := f.pushed
	if v == nil {
		return nil
	}
	f.pushed, f.lastPush = nil, time.Now()
	c.setView(f, v)
	return c.onChange
}

func viewSections(pluginID string, v plugin.View) []Section {
	out := make([]Section, 0, len(v.Sections))
	for _, s := range v.Sections {
		sec := Section{Title: s.Title, Items: make([]Entry, 0, len(s.Items))}
		for _, it := range s.Items {
			sec.Items = append(sec.Items, newEntry(pluginID, it))
		}
		out = append(out, sec)
	}
	return out
}
