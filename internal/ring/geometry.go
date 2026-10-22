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

// PlaceAround returns where a ring window of size w×h goes when its ring
// takes core (coreW×coreH, centred in the window): the core is placed as
// Place places a window, and the window centred on it, which may stick out
// of the work area where nothing shows (an arc of a folder turns away from
// there: LayoutArc).
func PlaceAround(p Point, coreW, coreH, w, h float64, work Rect) Rect {
	c := Place(p, coreW, coreH, work).Center()
	return Rect{X: c.X - w/2, Y: c.Y - h/2, W: w, H: h}
}

// Intersect returns the part of r in q (empty: W or H 0).
func (r Rect) Intersect(q Rect) Rect {
	x0, y0 := math.Max(r.X, q.X), math.Max(r.Y, q.Y)
	x1, y1 := math.Min(r.X+r.W, q.X+q.W), math.Min(r.Y+r.H, q.Y+q.H)
	return Rect{X: x0, Y: y0, W: math.Max(0, x1-x0), H: math.Max(0, y1-y0)}
}

// Overlaps reports whether r and q share some area.
func (r Rect) Overlaps(q Rect) bool {
	return r.X < q.X+q.W && q.X < r.X+r.W && r.Y < q.Y+q.H && q.Y < r.Y+r.H
}

// outside returns how far r sticks out of b, summed over its sides.
func (r Rect) outside(b Rect) float64 {
	return math.Max(0, b.X-r.X) + math.Max(0, r.X+r.W-b.X-b.W) + math.Max(0, b.Y-r.Y) + math.Max(0, r.Y+r.H-b.Y-b.H)
}

// ArcMetrics are the sizes of a folder's arc, in the units of its layout
// (DIPs from the ring's centre).
type ArcMetrics struct {
	// Radius is the distance from the ring's centre to the sub-slots'.
	Radius float64
	// Step is the angle between two sub-slots, in radians.
	Step float64
	// SlotR is a sub-slot's radius.
	SlotR float64
	// LabelGap is the distance between a sub-slot and its name pill,
	// LabelH the pill's height.
	LabelGap, LabelH float64
}

// ArcItem is a sub-slot of an arc: its direction and centre, and its name
// pill, from the ring's centre.
type ArcItem struct {
	Angle  float64
	Center Point
	Label  Rect
}

// Box returns the sub-slot's circle's bounds.
func (it ArcItem) Box(r float64) Rect {
	return Rect{X: it.Center.X - r, Y: it.Center.Y - r, W: 2 * r, H: 2 * r}
}

// Arc is the arc of sub-slots a folder opens, outside the ring.
type Arc struct {
	// Folder is the folder's position on the ring.
	Folder int
	Items  []ArcItem
	// Turn is how far the arc turned from the folder's direction to fit
	// (radians; π is the other side of the ring).
	Turn float64
	M    ArcMetrics
}

// ArcAngles returns the directions of n sub-slots step apart, centred on
// the direction at.
func ArcAngles(at float64, n int, step float64) []float64 {
	out := make([]float64, n)
	for j := range n {
		out[j] = at + (float64(j)-float64(n-1)/2)*step
	}
	return out
}

// arcItem lays out a sub-slot at angle a whose name pill is w wide: beside
// it, away from the ring, on the right of those on the right and on the
// left of those on the left; one straight above or below the ring has it
// above or below, centred.
func arcItem(a, w float64, m ArcMetrics) ArcItem {
	c := Point{m.Radius * math.Cos(a), m.Radius * math.Sin(a)}
	var l Rect
	switch {
	case c.X > 1:
		l = Rect{X: c.X + m.SlotR + m.LabelGap, Y: c.Y - m.LabelH/2, W: w, H: m.LabelH}
	case c.X < -1:
		l = Rect{X: c.X - m.SlotR - m.LabelGap - w, Y: c.Y - m.LabelH/2, W: w, H: m.LabelH}
	case c.Y < 0:
		l = Rect{X: c.X - w/2, Y: c.Y - m.SlotR - m.LabelGap - m.LabelH, W: w, H: m.LabelH}
	default:
		l = Rect{X: c.X - w/2, Y: c.Y + m.SlotR + m.LabelGap, W: w, H: m.LabelH}
	}
	return ArcItem{Angle: a, Center: c, Label: l}
}

// LayoutArc lays out the arc of the folder at position folder, whose
// sub-slots' name pills are widths wide: centred on the folder's direction
// when it fits in bounds (the part of the window on the screen, from the
// ring's centre); else turned by whole steps away from the edge, the
// nearest turn that fits first, else flipped to the other side of the
// ring; else, when nothing fits, the way that sticks out least.
func LayoutArc(folder int, widths []float64, m ArcMetrics, bounds Rect) Arc {
	base := SlotAngle(folder)
	turns := []float64{0}
	for k := 1; float64(k)*m.Step <= math.Pi/2+1e-9; k++ {
		turns = append(turns, float64(k)*m.Step, -float64(k)*m.Step)
	}
	turns = append(turns, math.Pi)
	var best Arc
	bestOut := math.Inf(1)
	for _, turn := range turns {
		a := Arc{Folder: folder, Turn: turn, M: m}
		out := 0.0
		for j, ang := range ArcAngles(base+turn, len(widths), m.Step) {
			it := arcItem(ang, widths[j], m)
			a.Items = append(a.Items, it)
			out += it.Box(m.SlotR).outside(bounds) + it.Label.outside(bounds)
		}
		if out == 0 {
			return a
		}
		if out < bestOut {
			best, bestOut = a, out
		}
	}
	return best
}

// Bounds returns the bounds of the arc's sub-slots and pills.
func (a Arc) Bounds() Rect {
	var x0, y0, x1, y1 = math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, it := range a.Items {
		for _, r := range []Rect{it.Box(a.M.SlotR), it.Label} {
			x0, y0 = math.Min(x0, r.X), math.Min(y0, r.Y)
			x1, y1 = math.Max(x1, r.X+r.W), math.Max(y1, r.Y+r.H)
		}
	}
	if len(a.Items) == 0 {
		return Rect{}
	}
	return Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// ItemAt returns the sub-slot in the direction of p (from the ring's
// centre) within half a step of its own, or -1.
func (a Arc) ItemAt(p Point) int {
	ang := math.Atan2(p.Y, p.X)
	for j, it := range a.Items {
		if math.Abs(angleDiff(ang, it.Angle)) <= a.M.Step/2 {
			return j
		}
	}
	return -1
}

// angleDiff returns a-b within (-π, π].
func angleDiff(a, b float64) float64 {
	d := math.Mod(a-b, 2*math.Pi)
	switch {
	case d > math.Pi:
		d -= 2 * math.Pi
	case d <= -math.Pi:
		d += 2 * math.Pi
	}
	return d
}

// Pick returns what a drag at p (from the ring's centre) points at, by
// bands of distance: within dead, nothing (-1, -1); from inner on, with an
// arc open, the arc's sub-slot in that direction (arc.Folder, sub); else
// the ring's slot in that direction (slot, -1).
func Pick(p Point, dead, inner float64, arc *Arc) (slot, sub int) {
	d := math.Hypot(p.X, p.Y)
	if d <= dead {
		return -1, -1
	}
	if arc != nil && d >= inner {
		if j := arc.ItemAt(p); j >= 0 {
			return arc.Folder, j
		}
	}
	return SlotAt(Point{}, p, dead, 0), -1
}
