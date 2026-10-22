package ring

import (
	"math"
	"testing"
)

// TestSlotAt: the slot in the pointer's direction, 0 up and clockwise; none
// in the dead zone or (with a limit) too far.
func TestSlotAt(t *testing.T) {
	c := Point{100, 100}
	for _, tt := range []struct {
		p    Point
		want int
	}{
		{Point{100, 20}, 0},  // up
		{Point{160, 40}, 1},  // up right
		{Point{180, 100}, 2}, // right
		{Point{160, 160}, 3},
		{Point{100, 180}, 4}, // down
		{Point{40, 160}, 5},
		{Point{20, 100}, 6}, // left
		{Point{40, 40}, 7},
		{Point{100 + 30, 20}, 0}, // a little right of up is still up (within 22.5°)
		{Point{100 + 40, 20}, 1}, // past 22.5°: up right
		{Point{99, 20}, 0},       // a hair left of up: up, not 7 (the wrap)
		{Point{110, 105}, -1},    // in the dead zone
		{Point{100, 100}, -1},
	} {
		if got := SlotAt(c, tt.p, 20, 0); got != tt.want {
			t.Errorf("SlotAt(%v) = %d, want %d", tt.p, got, tt.want)
		}
	}
	if got := SlotAt(c, Point{100, -400}, 20, 150); got != -1 {
		t.Errorf("beyond the limit: %d", got)
	}
	if got := SlotAt(c, Point{100, -400}, 20, 0); got != 0 {
		t.Errorf("no limit: %d", got)
	}
	// Every slot's own centre is that slot.
	for i := range Slots {
		dx, dy := SlotOffset(i, 88)
		if got := SlotAt(c, Point{c.X + dx, c.Y + dy}, 28, 0); got != i {
			t.Errorf("slot %d's centre gives %d", i, got)
		}
	}
	if a := SlotAngle(2); math.Abs(a) > 1e-9 {
		t.Errorf("slot 2 points at %v, want right (0)", a)
	}
}

// TestPlace: centred on the pointer, moved back into the work area near
// its edges (and its own centre, not the pointer, picks the slots then).
func TestPlace(t *testing.T) {
	work := Rect{0, 40, 1920, 1000}
	if r := Place(Point{960, 540}, 600, 340, work); r != (Rect{660, 370, 600, 340}) {
		t.Errorf("middle: %+v", r)
	}
	r := Place(Point{10, 45}, 600, 340, work)
	if r != (Rect{0, 40, 600, 340}) {
		t.Errorf("top left: %+v", r)
	}
	if c := r.Center(); c != (Point{300, 210}) || !r.Contains(Point{10, 45}) || r.Contains(Point{700, 45}) {
		t.Errorf("centre %v", c)
	}
	if r := Place(Point{1915, 1035}, 600, 340, work); r != (Rect{1320, 700, 600, 340}) {
		t.Errorf("bottom right: %+v", r)
	}
	// Per-monitor scale: a second monitor at 150% left of the first.
	if r := Place(Point{-100, 500}, 900, 510, Rect{-1920, 0, 1920, 1040}); r != (Rect{-900, 245, 900, 510}) {
		t.Errorf("left monitor: %+v", r)
	}
	// A work area smaller than the ring: its top left corner.
	if r := Place(Point{50, 50}, 600, 340, Rect{0, 0, 300, 200}); r.X != 0 || r.Y != 0 {
		t.Errorf("small: %+v", r)
	}
}
