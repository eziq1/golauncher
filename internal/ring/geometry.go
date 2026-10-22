package ring

import "math"

// Slots is the number of slots of the ring.
const Slots = 8

// SlotAngle returns the direction of slot i, in radians on the screen (y
// grows downward): slot 0 points up, the next ones follow clockwise. The
// number keys 1 to 8 choose the slots in this order.
func SlotAngle(i int) float64 { return -math.Pi/2 + float64(i)*2*math.Pi/Slots }

// SlotOffset returns where the centre of slot i is, r away from the ring's
// centre.
func SlotOffset(i int, r float64) (dx, dy float64) {
	a := SlotAngle(i)
	return r * math.Cos(a), r * math.Sin(a)
}

// SlotAt returns the slot in the direction of p from the ring's centre c,
// or -1 when p is within dead of the centre (the dead zone), or farther
// than max from it (max > 0).
func SlotAt(c, p Point, dead, max float64) int {
	d := c.Dist(p)
	if d <= dead || max > 0 && d > max {
		return -1
	}
	a := math.Atan2(p.Y-c.Y, p.X-c.X) + math.Pi/2 // up is 0, clockwise
	step := 2 * math.Pi / Slots
	i := int(math.Floor(a/step+0.5)) % Slots
	if i < 0 {
		i += Slots
	}
	return i
}

// Rect is a rectangle on the screen.
type Rect struct{ X, Y, W, H float64 }

// Contains reports whether p is in r.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X && p.X < r.X+r.W && p.Y >= r.Y && p.Y < r.Y+r.H
}

// Center returns the centre of r.
func (r Rect) Center() Point { return Point{r.X + r.W/2, r.Y + r.H/2} }

// Place returns where a ring window of size w×h goes: centred on the
// pointer at p, moved back into the work area of its display when it
// would stick out (it is then not centred on the pointer: the slots are
// chosen from its own centre).
func Place(p Point, w, h float64, work Rect) Rect {
	r := Rect{X: p.X - w/2, Y: p.Y - h/2, W: w, H: h}
	r.X = clamp(r.X, work.X, work.X+work.W-w)
	r.Y = clamp(r.Y, work.Y, work.Y+work.H-h)
	return r
}

// clamp keeps v in [lo, hi]; lo wins when the range is empty (a window
// larger than the work area starts at its edge).
func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(v, hi))
}
