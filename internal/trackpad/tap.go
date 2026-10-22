package trackpad

import (
	"math"
	"time"
)

// What a three-finger tap is (docs/action-ring.md §10). A swipe (Mission
// Control, switching Spaces) or a drag moves too far or lasts too long; a
// fourth finger, or a finger coming late, is another gesture.
const (
	// TapFingers is how many fingers tap.
	TapFingers = 3
	// TapDownWindow is how soon after the first finger the others touch.
	TapDownWindow = 80 * time.Millisecond
	// TapMaxDuration is the longest from the first finger touching to the
	// last one lifted.
	TapMaxDuration = 300 * time.Millisecond
	// TapMaxMove is how far each finger may move from where it touched,
	// in normalized units (1 is the width or the height of the trackpad):
	// about 4–6 mm.
	TapMaxMove = 0.04
)

// Tap tells a three-finger tap from the frames of one trackpad. Feed it
// every frame in order; it does not allocate. Not safe for concurrent use:
// each device has its own.
type Tap struct {
	// Pressed reports whether a mouse button (the trackpad's click
	// included) went down within the last d: a tap is not a click. Nil
	// for never.
	Pressed func(d time.Duration) bool

	active   bool // fingers touch since start
	rejected bool // not a tap: wait for all fingers to lift
	start    time.Duration
	most     int // the most fingers down at once
	n        int // how many fingers touched: the first n of fingers
	fingers  [TapFingers]finger
}

// finger is a finger of the gesture: where it touched.
type finger struct {
	id   int32
	x, y float32
}

// Feed takes the frame at t (any monotonic clock) with its touches, and
// reports whether it ends a three-finger tap.
func (g *Tap) Feed(t time.Duration, touches []Touch) bool {
	down := 0
	for _, tc := range touches {
		if tc.Down() {
			down++
		}
	}
	if !g.active {
		if down == 0 {
			return false
		}
		*g = Tap{Pressed: g.Pressed, active: true, start: t}
	}
	if down == 0 {
		// All lifted: the gesture ends; a button pressed during it, or
		// just before the first finger touched, makes it a click.
		tap := !g.rejected && g.n == TapFingers && g.most == TapFingers && t-g.start <= TapMaxDuration &&
			(g.Pressed == nil || !g.Pressed(t-g.start+TapDownWindow))
		g.active = false
		return tap
	}
	if g.rejected {
		return false
	}
	if t-g.start > TapMaxDuration {
		g.rejected = true // held, or a slow swipe
		return false
	}
	g.most = max(g.most, down)
	for _, tc := range touches {
		if !tc.Down() {
			continue
		}
		f := g.find(tc.ID)
		if f == nil {
			if g.n == TapFingers || t-g.start > TapDownWindow {
				g.rejected = true // a fourth finger, or one too late
				return false
			}
			g.fingers[g.n] = finger{id: tc.ID, x: tc.X, y: tc.Y}
			g.n++
			continue
		}
		if math.Hypot(float64(tc.X-f.x), float64(tc.Y-f.y)) > TapMaxMove {
			g.rejected = true // a swipe or a drag
			return false
		}
	}
	return false
}

// find returns the finger of the gesture with identifier id, or nil.
func (g *Tap) find(id int32) *finger {
	for i := range g.n {
		if g.fingers[i].id == id {
			return &g.fingers[i]
		}
	}
	return nil
}
