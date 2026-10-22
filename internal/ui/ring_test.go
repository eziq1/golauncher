package ui_test

import (
	"image/color"
	"image/png"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// ringHost records what the ring view asks of its window.
type ringHost struct{ calls []string }

func (h *ringHost) Choose(i int) { h.calls = append(h.calls, "choose "+string(rune('0'+i))) }
func (h *ringHost) Close()       { h.calls = append(h.calls, "close") }

func (h *ringHost) take() []string {
	c := h.calls
	h.calls = nil
	return c
}

// ringSlots are four fixed slots and one context slot.
var ringSlots = []ring.Slot{
	{Key: "ring:launcher", Title: "Open golauncher", Icon: plugin.Icon{Name: "command"}, Fixed: true},
	{Key: "ring:askai", Title: "Ask AI", Icon: plugin.Icon{Name: "sparkles"}, Fixed: true},
	{Key: "ring:screenshot", Title: "Screenshot", Icon: plugin.Icon{Name: "scissors"}, Fixed: true},
	{Key: "builtin.system/system:lock", Title: "Lock", Icon: plugin.Icon{Name: "lock"}, Fixed: true},
	{Key: "builtin.files/terminal", Title: "Open Terminal Here", Icon: plugin.Icon{Name: "terminal"}},
}

// newRingView is the ring with slots, past its opening.
func newRingView(t *testing.T, locale string, slots []ring.Slot) (*ui.Ring, *mui.Tester, *ringHost) {
	t.Helper()
	env, _ := newEnv(t, locale)
	host := &ringHost{}
	r := ui.NewRing(env, host)
	r.Show(ring.Arrange(slots))
	tt := mui.NewTester(r.View, ui.RingWidth, ui.RingHeight)
	tt.SetLocale(locale)
	tt.Frame()
	tt.Advance(time.Second) // past the opening
	tt.Frame()
	return r, tt, host
}

// count is how many texts (and labels) of the frame are s.
func count(tt *mui.Tester, s string) int {
	return len(slices.DeleteFunc(tt.Texts(), func(x string) bool { return x != s }))
}

// slotCenter is the centre of slot i in the ring's window.
func slotCenter(i int) (float32, float32) {
	dx, dy := ring.SlotOffset(i, ui.RingRadius)
	return float32(ui.RingWidth/2 + dx), float32(ui.RingHeight/2 + dy)
}

// slotFill is the colour of slot i's circle, beside its icon.
func slotFill(tt *mui.Tester, i int) color.RGBA {
	x, y := slotCenter(i)
	return tt.Image().RGBAAt(int(x)+18, int(y))
}

// keyFill is the colour of the number key in the name pill of slot i.
func keyFill(t *testing.T, tt *mui.Tester, i int) color.RGBA {
	t.Helper()
	k, ok := tt.Find(strconv.Itoa(i + 1))
	if !ok {
		t.Fatalf("no number key %d in %q", i+1, tt.Texts())
	}
	return tt.Image().RGBAAt(int(k.X)+1, int(k.Y+k.H/2))
}

// highlighted returns the slots of l drawn highlighted: their circle's
// colour, and their pill's number key's, differ from the others'.
func highlighted(t *testing.T, tt *mui.Tester, l ring.Layout) []int {
	t.Helper()
	fills, keys := map[color.RGBA]int{}, map[color.RGBA]int{}
	for i, s := range l {
		if s != nil {
			fills[slotFill(tt, i)]++
			keys[keyFill(t, tt, i)]++
		}
	}
	var out []int
	for i, s := range l {
		if s == nil {
			continue
		}
		f, k := fills[slotFill(tt, i)] == 1 && len(fills) > 1, keys[keyFill(t, tt, i)] == 1 && len(keys) > 1
		if f != k {
			t.Errorf("slot %d: circle highlighted %v, its pill %v", i, f, k)
		}
		if f {
			out = append(out, i)
		}
	}
	return out
}

// TestRingView: every slot shows its name in a pill outside the ring, with
// its number key, without hovering; the slot hovered, or dragged to, is
// highlighted with its pill; a click on a slot or its pill runs it, the
// number keys choose a slot, Escape, the × and a click outside close.
func TestRingView(t *testing.T) {
	r, tt, host := newRingView(t, "en", ringSlots)
	l := r.Layout()
	for i, s := range l {
		if s == nil {
			if _, ok := tt.Find(strconv.Itoa(i + 1)); ok {
				t.Errorf("an empty slot %d has a pill", i)
			}
			continue
		}
		// Its circle's name, and its pill.
		if count(tt, s.Title) != 2 {
			t.Errorf("%q shows %d times in %q", s.Title, count(tt, s.Title), tt.Texts())
		}
		if _, ok := tt.Find(strconv.Itoa(i + 1)); !ok {
			t.Errorf("slot %d has no number key", i)
		}
	}
	if count(tt, "Close") != 1 {
		t.Fatalf("texts %q", tt.Texts())
	}
	if got := highlighted(t, tt, l); len(got) != 0 {
		t.Errorf("highlighted before any hover: %v", got)
	}

	// Hovered: the slot and its pill stand out.
	tt.Move(slotCenter(2)) // right: Ask AI
	tt.Frame()
	if got := highlighted(t, tt, l); !slices.Equal(got, []int{2}) {
		t.Errorf("hovered 2: %v", got)
	}
	tt.Move(slotCenter(6)) // left: Lock
	tt.Frame()
	if got := highlighted(t, tt, l); !slices.Equal(got, []int{6}) {
		t.Errorf("hovered 6: %v", got)
	}
	// The pill is a part of its slot.
	lbl, _ := tt.Find("Screenshot")
	tt.Move(lbl.X+lbl.W/2, lbl.Y+lbl.H/2)
	tt.Frame()
	if got := highlighted(t, tt, l); !slices.Equal(got, []int{4}) {
		t.Errorf("hovered the pill of 4: %v", got)
	}
	tt.Move(5, 5) // the empty corner
	tt.Frame()
	if got := highlighted(t, tt, l); len(got) != 0 {
		t.Errorf("not hovered: %v", got)
	}

	// A drag highlights through the shell.
	if !r.SetHighlight(1) || r.SetHighlight(1) {
		t.Error("SetHighlight did not report the change once")
	}
	tt.Frame()
	if got := highlighted(t, tt, l); !slices.Equal(got, []int{1}) {
		t.Errorf("dragged to 1: %v", got)
	}
	r.SetHighlight(5) // an empty slot: none
	tt.Frame()
	if got := highlighted(t, tt, l); len(got) != 0 {
		t.Errorf("dragged to an empty slot: %v", got)
	}
	r.SetHighlight(-1)
	tt.Frame()

	// A click on a slot's circle, and on a pill.
	tt.ClickAt(slotCenter(4))
	tt.Frame()
	if err := tt.Click("Lock"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := host.take(); !slices.Equal(got, []string{"choose 4", "choose 6"}) {
		t.Errorf("click: %q", got)
	}
	// The number keys and Escape.
	tt.Key(0, mui.Key2)
	tt.Frame()
	tt.Key(0, mui.Key6) // an empty slot: nothing
	tt.Frame()
	tt.Key(0, mui.KeyEscape)
	tt.Frame()
	if got := host.take(); !slices.Equal(got, []string{"choose 1", "close"}) {
		t.Errorf("keys: %q", got)
	}
	// The keys the shell catches globally.
	if !r.Key(mui.Key1) || r.Key(mui.Key8) || r.Key(mui.KeyA) {
		t.Error("Key reported wrong")
	}
	if got := host.take(); !slices.Equal(got, []string{"choose 0"}) {
		t.Errorf("global keys: %q", got)
	}
	// The ×, and a click outside the ring.
	if err := tt.Click("Close"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.ClickAt(5, 5)
	tt.Frame()
	tt.ClickAt(ui.RingWidth/2+ui.RingRadius*0.7, ui.RingHeight/2) // on the disc between slots: nothing
	tt.Frame()
	if got := host.take(); !slices.Equal(got, []string{"close", "close"}) {
		t.Errorf("close: %q", got)
	}
}

// pillOf is the box of slot i's pill content: from its number key to the
// end of its name.
func pillOf(t *testing.T, tt *mui.Tester, i int, title string) mui.Rect {
	t.Helper()
	k, ok := tt.Find(strconv.Itoa(i + 1))
	n, ok2 := tt.Find(title)
	if !ok || !ok2 {
		t.Fatalf("slot %d: no pill in %q", i, tt.Texts())
	}
	return mui.Rect{X: k.X, Y: min(k.Y, n.Y), W: n.X + n.W - k.X, H: max(k.Y+k.H, n.Y+n.H) - min(k.Y, n.Y)}
}

// TestRingLabelLayout: with all eight slots, the pills are in the window,
// clear of the ring and of one another; on the right they start beside
// the ring, on the left they end there, above and below they are centred.
// A long name ends with an ellipsis in a pill of at most 180 DIPs, and
// shows whole in its tooltip.
func TestRingLabelLayout(t *testing.T) {
	titles := []string{"Top", "Top right", "Right side action", "Bottom right", "Bottom", "Bottom left one", "Left", "Top left"}
	var slots []ring.Slot
	for i, s := range titles {
		slots = append(slots, ring.Slot{Key: "k" + strconv.Itoa(i), Title: s, Icon: plugin.Icon{Name: "app"}, Fixed: i < 4})
	}
	r, tt, _ := newRingView(t, "en", slots)
	l := r.Layout()
	const cx, cy = ui.RingWidth / 2, ui.RingHeight / 2
	const disc = ui.RingRadius + 26 + 12 // the ring's background
	var boxes []mui.Rect
	for i, s := range l {
		if s == nil {
			t.Fatalf("slot %d empty: %v", i, l)
		}
		b := pillOf(t, tt, i, s.Title)
		boxes = append(boxes, b)
		if b.X < 0 || b.Y < 0 || b.X+b.W > ui.RingWidth || b.Y+b.H > ui.RingHeight {
			t.Errorf("slot %d (%q): pill %+v outside the window", i, s.Title, b)
		}
		// The nearest point of the box to the centre is off the disc.
		nx := math.Max(float64(b.X), math.Min(cx, float64(b.X+b.W)))
		ny := math.Max(float64(b.Y), math.Min(cy, float64(b.Y+b.H)))
		if d := math.Hypot(nx-cx, ny-cy); d <= disc {
			t.Errorf("slot %d (%q): pill %+v over the ring (%.0f from its centre)", i, s.Title, b, d)
		}
		dx, dy := ring.SlotOffset(i, ui.RingRadius)
		switch {
		case dx > 1:
			if b.X < cx || b.X > cx+disc+30 {
				t.Errorf("slot %d on the right: pill %+v does not start beside the ring", i, b)
			}
		case dx < -1:
			if e := b.X + b.W; e > cx || e < cx-disc-30 {
				t.Errorf("slot %d on the left: pill %+v does not end beside the ring", i, b)
			}
		default:
			if m := b.X + b.W/2; math.Abs(float64(m)-cx) > 2 {
				t.Errorf("slot %d: pill %+v not centred (%.1f)", i, b, m)
			}
			if dy < 0 && b.Y+b.H > cy-disc || dy > 0 && b.Y < cy+disc {
				t.Errorf("slot %d: pill %+v not beyond the ring", i, b)
			}
		}
	}
	// The pills on a side line up along the ring: the diagonal ones alike.
	if a, b := boxes[1].X, boxes[3].X; math.Abs(float64(a-b)) > 0.5 {
		t.Errorf("right pills start at %.1f and %.1f", a, b)
	}
	if a, b := boxes[5].X+boxes[5].W, boxes[7].X+boxes[7].W; math.Abs(float64(a-b)) > 2 {
		t.Errorf("left pills end at %.1f and %.1f", a, b)
	}
	for i := range boxes {
		for j := i + 1; j < len(boxes); j++ {
			a, b := boxes[i], boxes[j]
			if a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
				t.Errorf("pills %d %+v and %d %+v overlap", i, a, j, b)
			}
		}
	}

	if dir := os.Getenv("GOLAUNCHER_RING_PNG"); dir != "" { // a look for the author, never committed
		tt.Move(slotCenter(2))
		tt.Frame()
		if f, err := os.Create(dir); err == nil {
			png.Encode(f, tt.Image())
			f.Close()
		}
	}

	// A long name: cut, whole in the tooltip.
	long := strings.Repeat("A very long action name ", 6)
	for _, i := range []int{2, 6, 0} {
		l := r.Layout()
		orig := l[i]
		s := *orig
		s.Title = long
		l[i] = &s
		r.SetLayout(l)
		tt.Frame()
		b := pillOf(t, tt, i, long)
		if b.W > 180 || b.X < 0 || b.X+b.W > ui.RingWidth {
			t.Errorf("slot %d: the long name's pill %+v", i, b)
		}
		if count(tt, long) != 2 {
			t.Fatalf("slot %d: %d texts of the long name", i, count(tt, long))
		}
		tt.Move(b.X+b.W/2, b.Y+b.H/2)
		tt.Frame()
		tt.Advance(time.Second)
		tt.Frame()
		if count(tt, long) != 3 {
			t.Errorf("slot %d: no tooltip with the whole name: %q", i, tt.Texts())
		}
		tt.Move(5, 5)
		l[i] = orig
		r.SetLayout(l)
		tt.Frame()
	}
	// A short name has no tooltip.
	short := r.Layout()[4].Title
	b := pillOf(t, tt, 4, short)
	tt.Move(b.X+b.W/2, b.Y+b.H/2)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if count(tt, short) != 2 {
		t.Errorf("a tooltip for a short name: %q", tt.Texts())
	}
}

// TestRingViewChinese: the close button's name and a long slot name.
func TestRingViewChinese(t *testing.T) {
	r, tt, _ := newRingView(t, "zh-CN", ringSlots)
	if !tt.HasText("关闭") {
		t.Fatalf("texts %q", tt.Texts())
	}
	l := r.Layout()
	long := *l[0]
	long.Title = strings.Repeat("很长的名称", 10)
	l[0] = &long
	r.SetLayout(l)
	r.SetHighlight(0)
	tt.Frame()
	if lbl, ok := tt.Find(long.Title); !ok || lbl.Y < 0 || lbl.X < 0 || lbl.X+lbl.W > ui.RingWidth {
		t.Errorf("the long name's pill: %+v %v", lbl, ok)
	}
}

// TestRingOwn: the ring's own actions by name, in both languages.
func TestRingOwn(t *testing.T) {
	for locale, want := range map[string][]string{"en": {"Open golauncher", "Ask AI", "Screenshot"}, "zh-CN": {"打开 golauncher", "问 AI", "截图"}} {
		env, _ := newEnv(t, locale)
		own := ui.RingOwn(env.I18n.Localizer("core"))
		for i, key := range []string{"ring:launcher", "ring:askai", "ring:screenshot"} {
			if title, icon, ok := own(key); !ok || title != want[i] || icon == "" {
				t.Errorf("%s %s: %q %q %v", locale, key, title, icon, ok)
			}
		}
		if _, _, ok := own("builtin.system/system:lock"); ok {
			t.Error("an entry taken for the ring's own")
		}
	}
}
