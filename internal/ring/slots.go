package ring

import (
	"cmp"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Slot is an action of the ring, or a folder of actions.
type Slot struct {
	// Key is a ring action's (config.RingLauncher, …) or the entry's key;
	// a folder's is FolderKey and its position.
	Key string
	// Title is the action's name, or the folder's ("" for the default
	// one, which the view shows in its language).
	Title string
	Icon  plugin.Icon
	// Fixed marks one of the user's fixed actions or folders.
	Fixed bool
	// Entry is the root entry the slot runs (nil for the ring's own
	// actions, for a fixed entry not found again, and for a folder).
	Entry *core.Entry
	// Folder holds a folder's actions (nil for an action): pointed at, the
	// folder opens an arc of them.
	Folder []Slot
}

// FolderKey starts the key of a folder slot.
const FolderKey = "folder:"

// IsFolder reports whether s is a folder.
func (s *Slot) IsFolder() bool { return s != nil && s.Folder != nil }

// empty reports whether s is an empty fixed slot.
func (s Slot) empty() bool { return s.Key == "" && s.Folder == nil }

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

// HasFolder reports whether a slot of l is a folder.
func (l Layout) HasFolder() bool {
	return slices.ContainsFunc(l[:], func(s *Slot) bool { return s.IsFolder() })
}

// FixedPosition returns the position of fixed slot i: the fixed slots take
// the four main directions (up, right, down, left), so that they stay where
// the hand expects them.
func FixedPosition(i int) int { return 2 * i }

// contextPositions is the order the context slots are filled in: the
// diagonals, then the main directions no fixed slot takes.
var contextPositions = [Slots]int{1, 3, 5, 7, 0, 2, 4, 6}

// Arrange lays out the fixed slots by position (fixed[i] at
// FixedPosition(i); an empty one leaves its place) and the context slots
// around them.
func Arrange(fixed, context []Slot) Layout {
	var l Layout
	for i, s := range fixed[:min(len(fixed), config.MaxRingFixed)] {
		if !s.empty() {
			l[FixedPosition(i)] = &s
		}
	}
	next := 0
	for _, s := range context {
		for next < Slots && l[contextPositions[next]] != nil {
			next++
		}
		if next == Slots {
			break
		}
		l[contextPositions[next]] = &s
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

// Assemble picks the context slots for the fixed ones (by position, as
// FixedSlots returns them) and lays them all out: candidates are the
// entries of the selection and context providers (core.Engine.RingEntries),
// fallback what to show when none fits (recently used entries). The most
// relevant come first (highest Score, else in the providers' order);
// duplicates of a fixed action (also one in a folder), destructive actions
// and entries that need an argument are left out.
func Assemble(fixed []Slot, candidates, fallback []core.Entry) Layout {
	fixed = fixed[:min(len(fixed), config.MaxRingFixed)]
	seen := map[string]bool{}
	mark := func(s Slot) {
		seen[s.Key] = true
		for _, k := range sameAs[s.Key] {
			seen[k] = true
		}
		if s.Entry != nil {
			seen[s.Entry.Key] = true
		}
	}
	taken := 0
	for _, s := range fixed {
		if s.empty() {
			continue
		}
		taken++
		mark(s)
		for _, a := range s.Folder {
			mark(a)
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
	free := Slots - taken
	more := pick(candidates)
	if len(more) == 0 {
		more = pick(fallback)
	}
	return Arrange(fixed, more[:min(len(more), free)])
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
