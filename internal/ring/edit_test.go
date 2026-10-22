package ring

import (
	"errors"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/config"
)

// fixedKeys shows f: a slot's key, a folder as name[keys], "-" for empty.
func fixedKeys(f Fixed) string {
	var out []string
	for _, s := range f {
		switch {
		case s.IsFolder():
			var ks []string
			for _, a := range s.Actions {
				ks = append(ks, a.Key)
			}
			out = append(out, s.Name+"["+strings.Join(ks, " ")+"]")
		case s.IsEmpty():
			out = append(out, "-")
		default:
			out = append(out, s.Key)
		}
	}
	return strings.Join(out, " ")
}

func fixedOf(keys ...string) Fixed {
	var slots []config.RingSlot
	for _, k := range keys {
		if k == "-" {
			slots = append(slots, config.RingSlot{})
		} else {
			slots = append(slots, config.RingSlot{Key: k})
		}
	}
	return FixedOf(slots)
}

// TestFixedEdits: the edits of the ring editor keep the rules: one level,
// at most six in a folder, a folder of one dissolves.
func TestFixedEdits(t *testing.T) {
	check := func(f Fixed, want string) {
		t.Helper()
		if got := fixedKeys(f); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
	f := fixedOf("a", "b", "-", "d")
	if err := f.Move(0, 2); err != nil {
		t.Fatal(err)
	}
	check(f, "- b a d")
	f.Move(1, 2) // two actions trade places
	check(f, "- a b d")
	if err := f.Merge(1, 2, "F"); err != nil {
		t.Fatal(err)
	}
	check(f, "- - F[b a] d")
	if !errors.Is(f.Merge(2, 3, "G"), ErrNotAllowed) || !errors.Is(f.Merge(0, 3, "G"), ErrNotAllowed) {
		t.Error("merged a folder or an empty slot")
	}
	if err := f.AddToFolder(3, 2); err != nil {
		t.Fatal(err)
	}
	check(f, "- - F[b a d] -")
	f.Reorder(2, 2, 0)
	check(f, "- - F[d b a] -")
	f.Reorder(2, 0, 9)
	check(f, "- - F[b a d] -")
	for _, k := range []string{"x", "y", "z"} {
		if err := f.Add(config.RingAction{Key: k}, 2); err != nil {
			t.Fatal(err)
		}
	}
	check(f, "- - F[b a d x y z] -")
	if !errors.Is(f.Add(config.RingAction{Key: "w"}, 2), ErrFolderFull) {
		t.Error("a seventh")
	}
	f[0] = config.RingSlot{Key: "w"}
	if !errors.Is(f.AddToFolder(0, 2), ErrFolderFull) {
		t.Error("a seventh dragged in")
	}
	if err := f.MoveOut(2, 0, -1); err != nil {
		t.Fatal(err)
	}
	check(f, "w b F[a d x y z] -")
	f[3] = config.RingSlot{Key: "v"}
	if !errors.Is(f.MoveOut(2, 0, -1), ErrNoRoom) {
		t.Error("moved out with no room")
	}
	if !f.Has("z") || f.Has("q") || f.Empties() != 0 || f.FirstEmpty() != -1 {
		t.Error("Has, Empties")
	}
	// Into another slot: an action makes a folder, then the folder of one
	// left dissolves.
	f = FixedOf([]config.RingSlot{config.FolderSlot("F", config.RingAction{Key: "a"}, config.RingAction{Key: "b"}), {Key: "c"}})
	if err := f.MoveInto(0, 0, 1, "G"); err != nil {
		t.Fatal(err)
	}
	check(f, "b G[c a] - -")
	f.RemoveItem(1, 0)
	check(f, "b a - -")
	f.Merge(1, 0, "H")
	f.Rename(0, "Tools")
	check(f, "Tools[b a] - - -")
	// Deleted: its actions to the empty slots, its own first.
	f[2] = config.RingSlot{Key: "c"}
	if dropped := f.DeleteFolder(0); dropped != 0 {
		t.Errorf("dropped %d", dropped)
	}
	check(f, "b a c -")
	f = FixedOf([]config.RingSlot{config.FolderSlot("F", config.RingAction{Key: "a"}, config.RingAction{Key: "b"}, config.RingAction{Key: "c"}), {Key: "x"}, {Key: "y"}})
	if dropped := f.DeleteFolder(0); dropped != 1 {
		t.Errorf("dropped %d", dropped)
	}
	check(f, "a x y b")
	f.Remove(1)
	check(f, "a - y b")
	if got := fixedKeys(FixedOf(f.Slots())); got != "a - y b" || len(f.Slots()) != 4 {
		t.Errorf("slots %+v", f.Slots())
	}
	if err := f.Add(config.RingAction{Key: "n"}, -1); err != nil || f[1].Key != "n" {
		t.Errorf("added %v %+v", err, f)
	}
	if !errors.Is(f.Add(config.RingAction{Key: "m"}, -1), ErrNoRoom) {
		t.Error("added with no room")
	}
}
