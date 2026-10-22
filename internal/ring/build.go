package ring

import (
	"cmp"
	"context"
	"strings"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Source is what the ring needs of the launcher's engine (core.Engine).
type Source interface {
	RingEntries(ctx context.Context, c *plugin.Context, sel *plugin.Selection) []core.Entry
	RecentEntries(n int) []core.Entry
	RingEntry(ctx context.Context, key, text string) (core.Entry, bool)
}

var _ Source = (*core.Engine)(nil)

// Own returns the title and built-in icon of one of the ring's own actions
// (config.RingLauncher, …) in the current language, and false for other
// keys.
type Own func(key string) (title, icon string, ok bool)

// IsOwn reports whether key names one of the ring's own actions.
func IsOwn(key string) bool { return strings.HasPrefix(key, "ring:") }

// FixedSlots returns the slots of the fixed actions: the ring's own, and
// the root entries found again (a command, a plugin's root item). One not
// found (its plugin is off) keeps its stored title, without an entry.
func FixedSlots(ctx context.Context, src Source, actions []config.RingAction, own Own) []Slot {
	var out []Slot
	for _, a := range actions {
		if len(out) == Slots {
			break
		}
		if title, icon, ok := own(a.Key); ok {
			out = append(out, Slot{Key: a.Key, Title: title, Icon: plugin.Icon{Name: icon}, Fixed: true})
			continue
		}
		if IsOwn(a.Key) {
			continue // a ring action this version does not know
		}
		if e, ok := src.RingEntry(ctx, a.Key, a.Title); ok {
			out = append(out, entrySlot(e, true))
			continue
		}
		out = append(out, Slot{Key: a.Key, Title: cmp.Or(a.Title, a.Key), Icon: plugin.Icon{Name: a.Icon}, Fixed: true})
	}
	return out
}

// Content returns the slots of a ring for the context c and the selected
// text sel (either may be nil): the fixed ones, then, with cfg.Context,
// those for the context, or the recently used entries when nothing fits.
func Content(ctx context.Context, src Source, cfg config.RingConfig, fixed []Slot, c *plugin.Context, sel *plugin.Selection) []Slot {
	if !cfg.Context || len(fixed) >= Slots {
		return fixed
	}
	var candidates []core.Entry
	if c != nil || sel != nil {
		candidates = src.RingEntries(ctx, c, sel)
	}
	return Assemble(fixed, candidates, src.RecentEntries(Slots))
}
