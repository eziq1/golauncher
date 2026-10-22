package ring

import (
	"errors"
	"slices"

	"github.com/eziq1/golauncher/internal/config"
)

// Errors of the edits of the fixed slots.
var (
	// ErrFolderFull: the folder holds config.MaxRingFolder actions already.
	ErrFolderFull = errors.New("ring: the folder is full")
	// ErrNoRoom: no fixed slot is empty.
	ErrNoRoom = errors.New("ring: no empty slot")
	// ErrNotAllowed: the edit makes no sense there (a folder into a folder,
	// an action onto itself).
	ErrNotAllowed = errors.New("ring: not allowed")
)

// Fixed is the fixed slots by position, as the settings' ring editor edits
// them: the top, the right, the bottom and the left (FixedPosition). Its
// edits keep the rules of config.NormalizeRingFixed: folders hold actions
// only, at most config.MaxRingFolder, and a folder left with one action
// becomes that action.
type Fixed [config.MaxRingFixed]config.RingSlot

// FixedOf returns the fixed slots of a configuration.
func FixedOf(slots []config.RingSlot) Fixed {
	var f Fixed
	for i, s := range config.NormalizeRingFixed(slots) {
		f[i] = s
	}
	return f
}

// Slots returns f as it is kept in the configuration.
func (f Fixed) Slots() []config.RingSlot { return config.NormalizeRingFixed(f[:]) }

// FirstEmpty returns the first empty slot, or -1.
func (f Fixed) FirstEmpty() int {
	return slices.IndexFunc(f[:], func(s config.RingSlot) bool { return s.IsEmpty() })
}

// Empties returns how many slots are empty.
func (f Fixed) Empties() int {
	n := 0
	for _, s := range f {
		if s.IsEmpty() {
			n++
		}
	}
	return n
}

// Has reports whether action key is in f, in a slot or a folder.
func (f Fixed) Has(key string) bool {
	for _, s := range f {
		if s.Key == key || slices.ContainsFunc(s.Actions, func(a config.RingAction) bool { return a.Key == key }) {
			return true
		}
	}
	return false
}

// valid reports whether i is a slot.
func valid(i int) bool { return i >= 0 && i < config.MaxRingFixed }

// dissolve turns a folder of one action into that action, and one of none
// into an empty slot.
func (f *Fixed) dissolve(i int) {
	switch s := &f[i]; {
	case len(s.Actions) == 1:
		*s = config.ActionSlot(s.Actions[0])
	case s.IsEmpty():
		*s = config.RingSlot{}
	}
}

// Move moves slot from to slot to: into it when it is empty, else the two
// trade places.
func (f *Fixed) Move(from, to int) error {
	if !valid(from) || !valid(to) || from == to {
		return ErrNotAllowed
	}
	f[from], f[to] = f[to], f[from]
	return nil
}

// Merge puts the action of slot src and that of slot dst together into a
// new folder named name at dst (dst's action first); src is left empty.
func (f *Fixed) Merge(src, dst int, name string) error {
	if !valid(src) || !valid(dst) || src == dst || f[src].Key == "" || f[dst].Key == "" || f[src].IsFolder() || f[dst].IsFolder() {
		return ErrNotAllowed
	}
	f[dst] = config.FolderSlot(name, f[dst].Action(), f[src].Action())
	f[src] = config.RingSlot{}
	return nil
}

// AddToFolder moves the action of slot src to the end of the folder dst.
func (f *Fixed) AddToFolder(src, dst int) error {
	if !valid(src) || !valid(dst) || src == dst || !f[dst].IsFolder() || f[src].IsFolder() || f[src].Key == "" {
		return ErrNotAllowed
	}
	if len(f[dst].Actions) >= config.MaxRingFolder {
		return ErrFolderFull
	}
	f[dst].Actions = append(slices.Clone(f[dst].Actions), f[src].Action())
	f[src] = config.RingSlot{}
	return nil
}

// Reorder moves action from of the folder at slot i to index to.
func (f *Fixed) Reorder(i, from, to int) error {
	if !valid(i) || !f[i].IsFolder() {
		return ErrNotAllowed
	}
	acts := slices.Clone(f[i].Actions)
	if from < 0 || from >= len(acts) {
		return ErrNotAllowed
	}
	to = max(0, min(to, len(acts)-1))
	a := acts[from]
	acts = slices.Insert(slices.Delete(acts, from, from+1), to, a)
	f[i].Actions = acts
	return nil
}

// MoveOut moves action item of the folder at slot i to the empty slot to
// (-1: the first empty one); the folder dissolves when one action is left.
func (f *Fixed) MoveOut(i, item, to int) error {
	if !valid(i) || !f[i].IsFolder() || item < 0 || item >= len(f[i].Actions) {
		return ErrNotAllowed
	}
	if to < 0 {
		to = f.FirstEmpty()
	}
	if to < 0 {
		return ErrNoRoom
	}
	if !f[to].IsEmpty() {
		return ErrNotAllowed
	}
	a := f[i].Actions[item]
	f[i].Actions = slices.Delete(slices.Clone(f[i].Actions), item, item+1)
	f[to] = config.ActionSlot(a)
	f.dissolve(i)
	return nil
}

// MoveInto moves action item of the folder at slot i into slot dst: a
// folder (at its end), or an action, with which it makes a new folder
// named name.
func (f *Fixed) MoveInto(i, item, dst int, name string) error {
	if !valid(i) || !valid(dst) || i == dst || !f[i].IsFolder() || item < 0 || item >= len(f[i].Actions) {
		return ErrNotAllowed
	}
	a := f[i].Actions[item]
	switch d := &f[dst]; {
	case d.IsFolder():
		if len(d.Actions) >= config.MaxRingFolder {
			return ErrFolderFull
		}
		d.Actions = append(slices.Clone(d.Actions), a)
	case d.Key != "":
		*d = config.FolderSlot(name, d.Action(), a)
	default:
		*d = config.ActionSlot(a)
	}
	f[i].Actions = slices.Delete(slices.Clone(f[i].Actions), item, item+1)
	f.dissolve(i)
	return nil
}

// Remove empties slot i (an action or a whole folder).
func (f *Fixed) Remove(i int) {
	if valid(i) {
		f[i] = config.RingSlot{}
	}
}

// RemoveItem removes action item of the folder at slot i.
func (f *Fixed) RemoveItem(i, item int) {
	if !valid(i) || !f[i].IsFolder() || item < 0 || item >= len(f[i].Actions) {
		return
	}
	f[i].Actions = slices.Delete(slices.Clone(f[i].Actions), item, item+1)
	f.dissolve(i)
}

// Rename names the folder at slot i.
func (f *Fixed) Rename(i int, name string) {
	if valid(i) && f[i].IsFolder() {
		f[i].Name = name
	}
}

// DeleteFolder removes the folder at slot i and puts its actions in the
// slots then empty, its own first; it returns how many did not fit (and
// are gone).
func (f *Fixed) DeleteFolder(i int) (dropped int) {
	if !valid(i) || !f[i].IsFolder() {
		return 0
	}
	acts := f[i].Actions
	f[i] = config.RingSlot{}
	for _, a := range acts {
		to := i
		if !f[to].IsEmpty() {
			to = f.FirstEmpty()
		}
		if to < 0 {
			dropped++
			continue
		}
		f[to] = config.ActionSlot(a)
	}
	return dropped
}

// Add puts action a in slot to: an empty slot, or a folder (at its end);
// -1 is the first empty slot.
func (f *Fixed) Add(a config.RingAction, to int) error {
	if to < 0 {
		to = f.FirstEmpty()
	}
	if to < 0 {
		return ErrNoRoom
	}
	if !valid(to) {
		return ErrNotAllowed
	}
	switch s := &f[to]; {
	case s.IsFolder():
		if len(s.Actions) >= config.MaxRingFolder {
			return ErrFolderFull
		}
		s.Actions = append(slices.Clone(s.Actions), a)
	case s.IsEmpty():
		*s = config.ActionSlot(a)
	default:
		return ErrNotAllowed
	}
	return nil
}
