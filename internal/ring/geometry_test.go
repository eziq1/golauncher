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

var testArc = ArcMetrics{Radius: 170, Step: math.Pi / 6, SlotR: 20, LabelGap: 6, LabelH: 26}

// arcRects returns the circles and the pills of a.
func arcRects(a Arc) []Rect {
	var out []Rect
	for _, it := range a.Items {
		out = append(out, it.Box(a.M.SlotR), it.Label)
	}
	return out
}

func within(r, b Rect) bool {
	return r.X >= b.X-1e-9 && r.Y >= b.Y-1e-9 && r.X+r.W <= b.X+b.W+1e-9 && r.Y+r.H <= b.Y+b.H+1e-9
}

// TestLayoutArc: at each of the four fixed positions, an arc of two to six
// sub-slots is centred on the folder's direction, its sub-slots on a circle
// outside the ring, its pills beside them away from the ring, nothing
// overlapping; it stays where it fits.
func TestLayoutArc(t *testing.T) {
	room := Rect{X: -400, Y: -260, W: 800, H: 520}
	for _, folder := range []int{0, 2, 4, 6} {
		for n := 2; n <= 6; n++ {
			widths := make([]float64, n)
			for j := range widths {
				widths[j] = 120
			}
			a := LayoutArc(folder, widths, testArc, room)
			if a.Turn != 0 || a.Folder != folder || len(a.Items) != n {
				t.Fatalf("folder %d, %d items: turn %v, %d items", folder, n, a.Turn, len(a.Items))
			}
			mid := 0.0
			for _, it := range a.Items {
				mid += it.Angle / float64(n)
				if d := math.Hypot(it.Center.X, it.Center.Y); math.Abs(d-170) > 1e-6 {
					t.Errorf("folder %d: a sub-slot %v from the centre", folder, d)
				}
				// The pill is farther from the centre than the sub-slot.
				lc := it.Label.Center()
				if math.Hypot(lc.X, lc.Y) <= 170 {
					t.Errorf("folder %d: a pill %+v towards the ring", folder, it.Label)
				}
			}
			if math.Abs(angleDiff(mid, SlotAngle(folder))) > 1e-9 {
				t.Errorf("folder %d: centred on %v", folder, mid)
			}
			rs := arcRects(a)
			for i := range rs {
				if !within(rs[i], room) {
					t.Errorf("folder %d, %d items: %+v outside", folder, n, rs[i])
				}
				for j := i + 1; j < len(rs); j++ {
					if j == i+1 && i%2 == 0 {
						continue // a sub-slot and its own pill touch
					}
					if rs[i].Overlaps(rs[j]) {
						t.Errorf("folder %d, %d items: %+v and %+v overlap", folder, n, rs[i], rs[j])
					}
				}
			}
			if b := a.Bounds(); !within(b, room) || b.W == 0 {
				t.Errorf("bounds %+v", b)
			}
		}
	}
}

// TestLayoutArcEdges: an arc that does not fit on the screen turns away
// from the edge, or flips to the other side, and then fits; one that
// cannot fit at all sticks out least.
func TestLayoutArcEdges(t *testing.T) {
	widths := []float64{120, 120, 120}
	for _, tc := range []struct {
		folder int
		bounds Rect
	}{
		{2, Rect{X: -400, Y: -260, W: 600, H: 520}}, // the right edge close
		{6, Rect{X: -200, Y: -260, W: 600, H: 520}}, // the left edge close
		{0, Rect{X: -400, Y: -160, W: 800, H: 420}}, // the top edge close
		{4, Rect{X: -400, Y: -260, W: 800, H: 420}}, // the bottom edge close
		{2, Rect{X: -400, Y: -160, W: 600, H: 420}}, // a top right corner
	} {
		a := LayoutArc(tc.folder, widths, testArc, tc.bounds)
		if a.Turn == 0 {
			t.Errorf("folder %d in %+v: not turned", tc.folder, tc.bounds)
		}
		for _, r := range arcRects(a) {
			if !within(r, tc.bounds) {
				t.Errorf("folder %d in %+v: %+v outside (turn %v)", tc.folder, tc.bounds, r, a.Turn)
			}
		}
	}
	// Far too little room: still laid out.
	if a := LayoutArc(2, widths, testArc, Rect{X: -50, Y: -50, W: 100, H: 100}); len(a.Items) != 3 {
		t.Errorf("no room: %+v", a)
	}
	// The other side when nothing nearer fits: a ring at a right edge.
	a := LayoutArc(2, []float64{120, 120, 120, 120, 120, 120}, testArc, Rect{X: -420, Y: -260, W: 560, H: 520})
	if a.Turn != math.Pi {
		t.Errorf("turn %v", a.Turn)
	}
}

// TestPick: by distance, the dead zone, the ring's slots, and with an arc
// open its sub-slots from the inner radius on, in their directions only.
func TestPick(t *testing.T) {
	at := func(angle, d float64) Point { return Point{d * math.Cos(angle), d * math.Sin(angle)} }
	if s, j := Pick(at(0, 10), 28, 126, nil); s != -1 || j != -1 {
		t.Errorf("dead zone: %d %d", s, j)
	}
	if s, j := Pick(at(SlotAngle(2), 200), 28, 126, nil); s != 2 || j != -1 {
		t.Errorf("no arc, far: %d %d", s, j)
	}
	a := LayoutArc(2, []float64{80, 80, 80}, testArc, Rect{X: -400, Y: -260, W: 800, H: 520})
	for j, it := range a.Items {
		if s, sub := Pick(at(it.Angle, 170), 28, 126, &a); s != 2 || sub != j {
			t.Errorf("sub-slot %d: %d %d", j, s, sub)
		}
		if s, sub := Pick(at(it.Angle+0.2, 140), 28, 126, &a); s != 2 || sub != j {
			t.Errorf("sub-slot %d's band: %d %d", j, s, sub)
		}
	}
	// Inside the ring: the ring's slots, the arc open or not.
	if s, j := Pick(at(SlotAngle(1), 88), 28, 126, &a); s != 1 || j != -1 {
		t.Errorf("main band: %d %d", s, j)
	}
	// Past the arc's ends: the ring's slot there.
	if s, j := Pick(at(SlotAngle(4), 170), 28, 126, &a); s != 4 || j != -1 {
		t.Errorf("past the arc: %d %d", s, j)
	}
	if got := PlaceAround(Point{X: 10, Y: 10}, 100, 100, 300, 200, Rect{W: 1000, H: 800}); got != (Rect{X: -100, Y: -50, W: 300, H: 200}) {
		t.Errorf("PlaceAround %+v", got)
	}
	if got := (Rect{X: 0, Y: 0, W: 10, H: 10}).Intersect(Rect{X: 5, Y: -5, W: 10, H: 10}); got != (Rect{X: 5, Y: 0, W: 5, H: 5}) {
		t.Errorf("Intersect %+v", got)
	}
}
