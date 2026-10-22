package ring

import (
	"cmp"
	"context"
	"strconv"
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

// FixedSlots returns the fixed slots by position (top, right, bottom,
// left; an empty slot is a zero Slot): the ring's own actions, the root
// entries found again (a command, a plugin's root item), and folders of
// them. One not found (its plugin is off) keeps its stored title, without
// an entry. With src nil, nothing is looked up: the stored titles show
// until the entries are found. An unknown ring action is left out; a
// folder left with one action is that action, one left with none empty.
func FixedSlots(ctx context.Context, src Source, slots []config.RingSlot, own Own) []Slot {
	action := func(a config.RingAction) (Slot, bool) {
		if title, icon, ok := own(a.Key); ok {
			return Slot{Key: a.Key, Title: title, Icon: plugin.Icon{Name: icon}, Fixed: true}, true
		}
		if IsOwn(a.Key) || a.Key == "" {
			return Slot{}, false // a ring action this version does not know
		}
		if src != nil {
			if e, ok := src.RingEntry(ctx, a.Key, a.Title); ok {
				return entrySlot(e, true), true
			}
		}
		return Slot{Key: a.Key, Title: cmp.Or(a.Title, a.Key), Icon: plugin.Icon{Name: a.Icon}, Fixed: true}, true
	}
	out := make([]Slot, 0, config.MaxRingFixed)
	for i, s := range slots[:min(len(slots), config.MaxRingFixed)] {
		var slot Slot
		if s.IsFolder() {
			var items []Slot
			for _, a := range s.Actions[:min(len(s.Actions), config.MaxRingFolder)] {
				if it, ok := action(a); ok {
					items = append(items, it)
				}
			}
			switch len(items) {
			case 0:
			case 1:
				slot = items[0]
			default:
				slot = Slot{Key: FolderKey + strconv.Itoa(i), Title: s.Name, Icon: plugin.Icon{Name: "folder"}, Fixed: true, Folder: items}
			}
		} else if a, ok := action(s.Action()); ok {
			slot = a
		}
		out = append(out, slot)
	}
	return out
}

// Content returns the slots of a ring for the context c and the selected
// text sel (either may be nil): the fixed ones (FixedSlots, by position),
// then, with cfg.Context, those for the context, or the recently used
// entries when nothing fits.
func Content(ctx context.Context, src Source, cfg config.RingConfig, fixed []Slot, c *plugin.Context, sel *plugin.Selection) Layout {
	if !cfg.Context {
		return Arrange(fixed, nil)
	}
	var candidates []core.Entry
	if c != nil || sel != nil {
		candidates = src.RingEntries(ctx, c, sel)
	}
	return Assemble(fixed, candidates, src.RecentEntries(Slots))
}
