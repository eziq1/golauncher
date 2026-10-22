package core

import (
	"context"
	"encoding/json/v2"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// The Action Ring (docs/action-ring.md) shows actions of the launcher in a
// ring around the pointer: the user's fixed ones (root entries of the
// launcher) and those the context and selection providers offer for the
// window under the pointer. These are the engine's parts of it; package
// ring picks and lays out the slots.

// RingEntries returns the entries the selection providers offer for sel
// and the context providers for c (either may be nil), as the root search
// shows them before anything is typed: the selected text's first.
func (e *Engine) RingEntries(ctx context.Context, c *plugin.Context, sel *plugin.Selection) []Entry {
	cfg := e.cfg.Get()
	if sel != nil {
		cp := *sel
		sel = &cp
	}
	out := e.selectionEntries(ctx, cfg, sel, "")
	return append(out, e.contextEntries(ctx, cfg, c)...)
}

// RecentEntries returns at most n of the root entries used last, most
// recent first, as the root search lists them, leaving out those of plugins
// that are off or gone.
func (e *Engine) RecentEntries(n int) []Entry {
	if n <= 0 {
		return nil
	}
	recent, err := e.svc.DB.Recent(n * 2)
	if err != nil {
		e.log.Warn("recent entries", "err", err)
		return nil
	}
	cfg := e.cfg.Get()
	var out []Entry
	for _, b := range recent {
		var ent Entry
		if err := json.Unmarshal(b, &ent); err != nil {
			continue
		}
		if _, ok := e.lookup(ent.Plugin); !ok || cfg.Plugin(ent.Plugin).Disabled {
			continue
		}
		if name, ok := strings.CutPrefix(ent.ID, cmdPrefix); ok {
			// Rebuild commands so their titles follow the current locale.
			if ent, ok = e.commandEntry(cfg, ent.Plugin, name); !ok {
				continue
			}
		}
		ent.Rank = 0
		out = append(out, ent)
		if len(out) == n {
			break
		}
	}
	return out
}

// RingEntry finds the root entry with key ("<plugin>/<item id>") again: a
// command, or an item of the plugin's root items for text (the entry's
// title: most plugins list all their items whatever the text).
func (e *Engine) RingEntry(ctx context.Context, key, text string) (Entry, bool) {
	pluginID, id, ok := strings.Cut(key, "/")
	if !ok {
		return Entry{}, false
	}
	cfg := e.cfg.Get()
	if cfg.Plugin(pluginID).Disabled {
		return Entry{}, false
	}
	if name, ok := strings.CutPrefix(id, cmdPrefix); ok {
		return e.commandEntry(cfg, pluginID, name)
	}
	l, err := e.running(pluginID)
	if err != nil {
		return Entry{}, false
	}
	rp, ok := l.p.(plugin.RootProvider)
	if !ok {
		return Entry{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, e.rootTimeout)
	defer cancel()
	items, err := callPlugin(ctx, e, pluginID, e.rootTimeout, func(ctx context.Context) ([]plugin.Item, error) {
		return rp.RootItems(ctx, plugin.Query{Text: text, Raw: text})
	})
	if err != nil {
		e.log.Warn("ring entry", "plugin", pluginID, "err", err)
		return Entry{}, false
	}
	i := slices.IndexFunc(items, func(it plugin.Item) bool { return it.ID == id })
	if i < 0 {
		return Entry{}, false
	}
	return newEntry(pluginID, items[i]), true
}

// RunEntry runs the primary action of entry, an entry the Action Ring
// shows, from the root search as a click on it would: back to the root
// first, a destructive action waits for its confirmation and a view it
// opens is pushed. The front end then shows the launcher if anything is
// left to see (NeedsWindow).
func (c *Controller) RunEntry(ctx context.Context, entry Entry) (Outcome, error) {
	if len(entry.Actions) == 0 {
		return Outcome{}, nil
	}
	c.mu.Lock()
	navs := c.toRoot()
	c.panel = nil
	c.confirm = nil
	c.toast = nil
	c.mu.Unlock()
	c.closed(navs)
	return c.run(ctx, entry, entry.Actions[0])
}

// NeedsWindow reports whether the launcher has something to show after an
// action run while it was hidden (RunEntry): a view, a form, or a
// confirmation.
func (c *Controller) NeedsWindow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.stack) > 1 || c.confirm != nil || c.top().form != nil
}
