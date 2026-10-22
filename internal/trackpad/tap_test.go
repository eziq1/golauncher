package trackpad

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

const ms = time.Millisecond

// frame is a synthetic frame: its time and its touches.
type frame struct {
	t       time.Duration
	touches []Touch
}

// down returns a finger id touching at x, y.
func down(id int32, x, y float32) Touch { return Touch{ID: id, State: touchStateTouching, X: x, Y: y} }

// feed feeds frames to g and returns at which frames it reported a tap.
func feed(g *Tap, frames []frame) []int {
	var taps []int
	for i, f := range frames {
		if g.Feed(f.t, f.touches) {
			taps = append(taps, i)
		}
	}
	return taps
}

// tapFrames is a clean three-finger tap starting at t0: the fingers touch
// at t0, +20ms and +40ms, stay still and lift at +150ms.
func tapFrames(t0 time.Duration) []frame {
	a, b, c := down(1, 0.3, 0.5), down(2, 0.5, 0.5), down(3, 0.7, 0.5)
	make3 := Touch{ID: 3, State: touchStateMakeTouch, X: 0.7, Y: 0.5}
	return []frame{
		{t0, []Touch{a}},
		{t0 + 20*ms, []Touch{a, b}},
		{t0 + 40*ms, []Touch{a, b, make3}},
		{t0 + 100*ms, []Touch{a, b, c}},
		{t0 + 140*ms, []Touch{{ID: 1, State: touchStateBreakTouch}, b, c}},
		{t0 + 150*ms, []Touch{{ID: 2, State: touchStateBreakTouch}, {ID: 3, State: touchStateLinger}}},
		{t0 + 160*ms, nil},
	}
}

// TestTap: a three-finger tap fires once, as its last finger lifts, and
// again for the next tap.
func TestTap(t *testing.T) {
	var g Tap
	frames := append(tapFrames(0), tapFrames(time.Second)...)
	if got := feed(&g, frames); len(got) != 2 || got[0] != 5 || got[1] != 12 {
		t.Errorf("taps at %v", got)
	}
}

// TestTapRejects: what is not a three-finger tap never fires.
func TestTapRejects(t *testing.T) {
	a, b, c, d := down(1, 0.3, 0.5), down(2, 0.5, 0.5), down(3, 0.7, 0.5), down(4, 0.8, 0.5)
	moved := func(tc Touch, dx, dy float32) Touch { tc.X += dx; tc.Y += dy; return tc }
	cases := map[string][]frame{
		"two fingers": {{0, []Touch{a, b}}, {100 * ms, nil}},
		"four fingers": {
			{0, []Touch{a, b, c}}, {30 * ms, []Touch{a, b, c, d}}, {100 * ms, nil},
		},
		"a fourth after one lifted": {
			{0, []Touch{a, b, c}}, {30 * ms, []Touch{a, b}}, {50 * ms, []Touch{a, b, d}}, {100 * ms, nil},
		},
		"third finger late": {
			{0, []Touch{a, b}}, {100 * ms, []Touch{a, b, c}}, {150 * ms, nil},
		},
		"held too long": {
			{0, []Touch{a, b, c}}, {200 * ms, []Touch{a, b, c}}, {320 * ms, []Touch{a, b, c}}, {330 * ms, nil},
		},
		"lifted too late": {
			{0, []Touch{a, b, c}}, {310 * ms, nil},
		},
		"swipe up (Mission Control)": {
			{0, []Touch{a, b, c}},
			{30 * ms, []Touch{moved(a, 0, 0.03), moved(b, 0, 0.03), moved(c, 0, 0.03)}},
			{60 * ms, []Touch{moved(a, 0, 0.1), moved(b, 0, 0.1), moved(c, 0, 0.1)}},
			{90 * ms, nil},
		},
		"swipe sideways (Spaces)": {
			{0, []Touch{a, b, c}},
			{40 * ms, []Touch{moved(a, 0.2, 0), moved(b, 0.2, 0), moved(c, 0.2, 0)}},
			{80 * ms, nil},
		},
		"three-finger drag": {
			{0, []Touch{a, b, c}},
			{100 * ms, []Touch{moved(a, 0.05, 0.05), moved(b, 0.05, 0.05), moved(c, 0.05, 0.05)}},
			{600 * ms, []Touch{moved(a, 0.3, 0.2), moved(b, 0.3, 0.2), moved(c, 0.3, 0.2)}},
			{700 * ms, nil},
		},
		"one finger slid": {
			{0, []Touch{a, b, c}}, {50 * ms, []Touch{a, b, moved(c, 0.05, 0)}}, {100 * ms, nil},
		},
		"one after another (never three at once)": {
			{0, []Touch{a}}, {20 * ms, []Touch{b}}, {40 * ms, []Touch{c}}, {100 * ms, nil},
		},
		"hovering only": {
			{0, []Touch{{ID: 1, State: touchStateHoverInRange}, {ID: 2, State: touchStateStartInRange}, {ID: 3, State: touchStateHoverInRange}}},
			{50 * ms, nil},
		},
	}
	for name, frames := range cases {
		var g Tap
		if got := feed(&g, frames); len(got) != 0 {
			t.Errorf("%s: fired at %v", name, got)
		}
		// Whatever it saw, the next tap fires.
		if got := feed(&g, tapFrames(2*time.Second)); len(got) != 1 {
			t.Errorf("%s: then a tap fired at %v", name, got)
		}
	}
}

// TestTapSmallMove: a finger that moves a little (fingers wobble) still
// taps.
func TestTapSmallMove(t *testing.T) {
	a, b, c := down(1, 0.3, 0.5), down(2, 0.5, 0.5), down(3, 0.7, 0.5)
	b2 := b
	b2.X, b2.Y = b.X+0.02, b.Y-0.02
	var g Tap
	if got := feed(&g, []frame{{0, []Touch{a, b, c}}, {50 * ms, []Touch{a, b2, c}}, {120 * ms, nil}}); len(got) != 1 {
		t.Errorf("taps at %v", got)
	}
}

// TestTapPressed: a button pressed during the gesture (a three-finger
// click) is not a tap; the window asked covers the gesture.
func TestTapPressed(t *testing.T) {
	var asked time.Duration
	pressed := true
	g := Tap{Pressed: func(d time.Duration) bool { asked = d; return pressed }}
	if got := feed(&g, tapFrames(0)); len(got) != 0 {
		t.Errorf("a click fired at %v", got)
	}
	if asked < 150*ms {
		t.Errorf("asked for presses within %v", asked)
	}
	pressed = false
	if got := feed(&g, tapFrames(time.Second)); len(got) != 1 {
		t.Errorf("taps at %v", got)
	}
}

// TestTapAllocs: feeding frames does not allocate (it runs on the
// framework's thread for every frame).
func TestTapAllocs(t *testing.T) {
	var g Tap
	frames := tapFrames(0)
	if n := testing.AllocsPerRun(100, func() {
		for _, f := range frames {
			g.Feed(f.t, f.touches)
		}
	}); n != 0 {
		t.Errorf("%v allocations", n)
	}
}

// putTouch writes tc into b at index i, laid out as touch.go documents,
// with the other fields set to values that must not be read as these.
func putTouch(b []byte, i int, frameNo int32, ts float64, tc Touch) {
	o := b[i*touchSize : (i+1)*touchSize]
	for j := range o {
		o[j] = 0xAB
	}
	le := binary.LittleEndian
	le.PutUint32(o[0:], uint32(frameNo))
	le.PutUint64(o[8:], math.Float64bits(ts))
	le.PutUint32(o[16:], uint32(tc.ID))
	le.PutUint32(o[20:], uint32(tc.State))
	le.PutUint32(o[24:], 2) // finger id
	le.PutUint32(o[28:], 1) // hand id
	le.PutUint32(o[32:], math.Float32bits(tc.X))
	le.PutUint32(o[36:], math.Float32bits(tc.Y))
	le.PutUint32(o[40:], math.Float32bits(0.5)) // velocity
	le.PutUint32(o[44:], math.Float32bits(-0.5))
	le.PutUint32(o[48:], math.Float32bits(1.25)) // total
	le.PutUint32(o[68:], math.Float32bits(40))   // millimetres
	le.PutUint32(o[72:], math.Float32bits(30))
}

// TestDecodeTouches: touches are read at their documented offsets with the
// 96-byte stride; no more than fit in the destination or the bytes.
func TestDecodeTouches(t *testing.T) {
	want := []Touch{
		{ID: 7, State: touchStateMakeTouch, X: 0.25, Y: 0.75},
		{ID: 8, State: touchStateTouching, X: 0.5, Y: 0.125},
		{ID: -1, State: touchStateBreakTouch, X: 1, Y: 0},
	}
	b := make([]byte, len(want)*touchSize)
	for i, tc := range want {
		putTouch(b, i, 1234, 5678.25, tc)
	}
	dst := make([]Touch, maxTouches)
	if n := decodeTouches(dst, b); n != len(want) {
		t.Fatalf("read %d", n)
	}
	for i, tc := range want {
		if dst[i] != tc {
			t.Errorf("touch %d: %+v, want %+v", i, dst[i], tc)
		}
	}
	if !dst[0].Down() || !dst[1].Down() || dst[2].Down() {
		t.Error("Down")
	}
	if n := decodeTouches(dst[:2], b); n != 2 {
		t.Errorf("into 2: read %d", n)
	}
	if n := decodeTouches(dst, b[:touchSize*2+50]); n != 2 {
		t.Errorf("from a partial touch: read %d", n)
	}
}
