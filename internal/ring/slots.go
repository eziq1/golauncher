package ring

import (
	"cmp"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Slot is an action of the ring.
type Slot struct {
	// Key is a ring action's (config.RingLauncher, …) or the entry's key.
	Key   string
	Title string
	Icon  plugin.Icon
	// Fixed marks one of the user's fixed actions.
	Fixed bool
	// Entry is the root entry the slot runs (nil for the ring's own
	// actions, and for a fixed entry not found again).
	Entry *core.Entry
}

// Layout is the ring's slots by position (0 at the top, clockwise); nil
// where a position is empty.
type Layout [Slots]*Slot

// Count returns the number of slots that are filled.
func (l Layout) Count() int {
	n := 0
	for _, s := range l {
		if s != nil {
			n++
		}
	}
	return n
}

// positions is the order the slots are filled in: the fixed actions take
// the four main directions first (up, right, down, left), so that they
// stay where the hand expects them; the context ones the diagonals.
var positions = [Slots]int{0, 2, 4, 6, 1, 3, 5, 7}

// Arrange lays out list, the fixed slots first, by positions.
func Arrange(list []Slot) Layout {
	var l Layout
	for i, s := range list {
		if i == Slots {
			break
		}
		l[positions[i]] = &s
	}
	return l
}

// Keys the ring's own actions stand for: these context items are left out
// when the fixed action is there.
var sameAs = map[string][]string{
	// "Ask AI…" on the selected text.
	config.RingAskAI: {"builtin.ai/ask"},
}

// skipped are entries never shown in the ring: the selected text's
// preview row (it shows the text, it acts on nothing).
var skipped = map[string]bool{"builtin.selection/preview": true}

// Assemble picks the context slots for the fixed ones: candidates are the
// entries of the selection and context providers (core.Engine.RingEntries),
// fallback what to show when none fits (recently used entries). The most
// relevant come first (highest Score, else in the providers' order);
// duplicates of a fixed action, destructive actions and entries that need
// an argument are left out. The result has at most Slots slots, the fixed
// ones first.
func Assemble(fixed []Slot, candidates, fallback []core.Entry) []Slot {
	out := slices.Clone(fixed[:min(len(fixed), Slots)])
	seen := map[string]bool{}
	for _, s := range out {
		seen[s.Key] = true
		for _, k := range sameAs[s.Key] {
			seen[k] = true
		}
		if s.Entry != nil {
			seen[s.Entry.Key] = true
		}
	}
	pick := func(list []core.Entry) []Slot {
		list = slices.Clone(list)
		slices.SortStableFunc(list, func(a, b core.Entry) int { return cmp.Compare(b.Score, a.Score) })
		var got []Slot
		for _, e := range list {
			if seen[e.Key] || !fits(e) {
				continue
			}
			seen[e.Key] = true
			got = append(got, entrySlot(e, false))
		}
		return got
	}
	free := Slots - len(out)
	if free <= 0 {
		return out
	}
	more := pick(candidates)
	if len(more) == 0 {
		more = pick(fallback)
	}
	return append(out, more[:min(len(more), free)]...)
}

// fits reports whether entry e can be a slot: it has an action that runs
// at once (not destructive, no argument to fill in).
func fits(e core.Entry) bool {
	if skipped[e.Key] || len(e.Actions) == 0 || e.Actions[0].Style == plugin.StyleDestructive {
		return false
	}
	return !slices.ContainsFunc(e.Arguments, func(a plugin.Argument) bool { return a.Required })
}

// EntrySlot returns the slot of entry e.
func EntrySlot(e core.Entry, fixed bool) Slot { return entrySlot(e, fixed) }

func entrySlot(e core.Entry, fixed bool) Slot {
	return Slot{Key: e.Key, Title: e.Title, Icon: e.Icon, Fixed: fixed, Entry: &e}
}

// Excluded reports whether the app named by any of names (an executable
// name or path, a bundle identifier, an app name) is in list, in any case
// and with or without ".exe".
func Excluded(list []string, names ...string) bool {
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		s = s[strings.LastIndexAny(s, `\/`)+1:] // a path's file name
		return strings.TrimSuffix(s, ".exe")
	}
	for _, n := range names {
		if n = norm(n); n == "" {
			continue
		}
		if slices.ContainsFunc(list, func(x string) bool { return norm(x) == n }) {
			return true
		}
	}
	return false
}
