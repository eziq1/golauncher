package ui

import (
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/internal/theme"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// The Action Ring's window (docs/action-ring.md): icon circles around the
// pointer, each with its name in a pill outside the ring (as Logitech
// Options+ shows them), a close button in the middle. The slot the pointer
// or the drag points at is highlighted, its pill too. The window never
// takes the focus; the shell feeds it the pointer while the middle button
// drags (SetHighlight) and the number keys (Key).

// Ring window size and geometry, in DIPs.
const (
	// RingRadius is the distance from the centre to the slots' centres.
	RingRadius = 88
	// RingDeadZone is the distance from the centre within which a drag
	// chooses nothing.
	RingDeadZone = 28
	ringSlot     = 52 // a slot's diameter
	ringCenter   = 36 // the close button's diameter
	// ringDiscR is the radius of the ring's background.
	ringDiscR = RingRadius + ringSlot/2 + 12
	// The name pills: their height, the widest they get (longer names end
	// with an ellipsis), and their distance from the disc.
	ringLabelH   = 26
	ringLabelMax = 180
	ringLabelGap = 8
	// ringMargin keeps the disc's and the pills' shadows in the window.
	ringMargin = 12

	// RingWidth and RingHeight are the window's size: the disc, and the
	// pills on its sides, above and below it.
	RingWidth  = 2 * (ringDiscR + ringLabelGap + ringLabelMax + ringMargin)
	RingHeight = 2 * (ringDiscR + ringLabelGap + ringLabelH + ringMargin)

	ringOpen = 120 * time.Millisecond
)

// ringDisc is the diameter of the ring's background.
const ringDisc = 2 * ringDiscR

// RingHost is what the ring view asks of its window. Its methods are called
// on the UI thread and must return at once.
type RingHost interface {
	// Choose runs the slot at position i (0 at the top, clockwise).
	Choose(i int)
	// Close closes the ring without running anything.
	Close()
}

// Ring is the Action Ring's view.
type Ring struct {
	loc        *i18n.Localizer
	themes     *theme.Manager
	appearance atomic.Pointer[config.Appearance]
	host       RingHost

	mu        sync.Mutex
	layout    ring.Layout
	highlight int // the slot the drag points at, -1 for none
	shownAt   time.Time
	opening   bool // the next frame starts the opening animation

	hover int // the slot hovered last frame, -1 for none (UI thread)
}

// NewRing returns the ring view of env, shown in the window host.
func NewRing(env *app.Env, host RingHost) *Ring {
	r := &Ring{loc: env.I18n.Localizer(i18n.CoreNamespace), themes: env.Themes, host: host, highlight: -1, hover: -1}
	a := env.Config.Get().Appearance
	r.appearance.Store(&a)
	env.Config.OnChange(func(c config.Config) { r.appearance.Store(&c.Appearance) })
	return r
}

// Show prepares the view for a ring with the slots of l: nothing
// highlighted, the opening animation on the next frame. Safe from any
// goroutine.
func (r *Ring) Show(l ring.Layout) {
	r.mu.Lock()
	r.layout, r.highlight, r.opening = l, -1, true
	r.mu.Unlock()
}

// SetLayout replaces the slots (the context ones arrive after the ring
// opened). Safe from any goroutine.
func (r *Ring) SetLayout(l ring.Layout) {
	r.mu.Lock()
	r.layout = l
	r.mu.Unlock()
}

// Layout returns the slots shown.
func (r *Ring) Layout() ring.Layout {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.layout
}

// SetHighlight highlights slot i while the middle button drags toward it
// (-1 for none), and reports whether that changed. Safe from any goroutine.
func (r *Ring) SetHighlight(i int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if i >= 0 && (i >= ring.Slots || r.layout[i] == nil) {
		i = -1
	}
	changed := r.highlight != i
	r.highlight = i
	return changed
}

// Key handles a key the shell caught while the ring shows: Escape closes
// it, 1 to 8 choose a slot. It reports whether the key did something. UI
// thread only.
func (r *Ring) Key(key mui.Key) bool {
	switch {
	case key == mui.KeyEscape:
		r.host.Close()
		return true
	case key >= mui.Key1 && key <= mui.Key8:
		i := int(key - mui.Key1)
		if r.Layout()[i] == nil {
			return false
		}
		r.host.Choose(i)
		return true
	}
	return false
}

// View builds the ring.
func (r *Ring) View(c *mui.Context) {
	res := resolveTheme(c, r.themes, *r.appearance.Load())
	ui := res.UI
	c.SetTheme(&ui)
	t := c.Theme()
	c.Root().Background(mui.Transparent)

	r.mu.Lock()
	l, highlight := r.layout, r.highlight
	if r.opening {
		r.opening, r.shownAt = false, c.Now()
	}
	shownAt := r.shownAt
	r.mu.Unlock()

	// Keys reach the view when the window has the keyboard after all (a
	// click on macOS can give it to the panel).
	if c.Shortcut(0, mui.KeyEscape) {
		r.Key(mui.KeyEscape)
	}
	for k := mui.Key1; k <= mui.Key8; k++ {
		if c.Shortcut(0, k) {
			r.Key(k)
		}
	}

	// Opening: the ring grows and fades in, unless the system asks for
	// less motion.
	p := float32(1)
	if !c.Preferences().ReduceMotion {
		if d := c.Now().Sub(shownAt); d < ringOpen {
			x := float64(d) / float64(ringOpen)
			p = float32(1 - (1-x)*(1-x)*(1-x)) // ease out
			c.AnimationFrame()
		}
	}

	const cx, cy = RingWidth / 2, RingHeight / 2
	active := highlight
	if active < 0 {
		active = r.hover
	}
	hover := -1
	mui.Box(c.Key("ring")).Size(RingWidth, RingHeight).Children(func() {
		// Outside the ring: a click closes it.
		if mui.Box(c.Key("ring:outside")).Absolute().Left(0).Top(0).Size(RingWidth, RingHeight).Clicked() {
			r.host.Close()
		}
		scale := 0.85 + 0.15*p
		mui.Box(c.Key("ring:body")).Absolute().Left(0).Top(0).Size(RingWidth, RingHeight).Opacity(p).Scale(scale, scale).PassThrough().Children(func() {
			d := float32(ringDisc)
			mui.Box(c.Key("ring:disc")).Absolute().Left(cx-d/2).Top(cy-d/2).Size(d, d).Radius(d/2).
				Background(t.Background.Alpha(0.96)).Border(1, t.Border).Shadow(0, 6, 24, 0, mui.RGBA(0, 0, 0, 0.28))
			// Every slot's name, outside the ring.
			for i, s := range l {
				if s != nil && r.label(c, i, s.Title, i == active) {
					hover = i
				}
			}
			for i, s := range l {
				if s == nil {
					continue
				}
				dx, dy := ring.SlotOffset(i, RingRadius)
				x, y := float32(cx+dx)-ringSlot/2, float32(cy+dy)-ringSlot/2
				on := i == active
				bg, fg := t.Surface, t.Text
				if on {
					bg, fg = t.Accent, t.AccentText
				}
				b := mui.Box(c.Key("ring:slot:"+strconv.Itoa(i))).Absolute().Left(x).Top(y).Size(ringSlot, ringSlot).Radius(ringSlot/2).
					Center().Background(bg).Border(1, t.Border).Cursor(mui.CursorPointer).Label(s.Title)
				if b.Hovered() {
					hover = i
				}
				if b.Clicked() {
					r.host.Choose(i)
				}
				b.Children(func() { r.slotIcon(c, s.Icon, fg) })
			}
			cb := mui.Box(c.Key("ring:close")).Absolute().Left(cx-ringCenter/2).Top(cy-ringCenter/2).Size(ringCenter, ringCenter).
				Radius(ringCenter/2).Center().Background(t.Surface).Border(1, t.Border).Cursor(mui.CursorPointer).Label(r.loc.T("ring.close"))
			if cb.Hovered() {
				cb.Background(t.SurfaceHover)
			}
			if cb.Clicked() {
				r.host.Close()
			}
			cb.Children(func() { mui.Icon(c, builtinIcon("x")).FontSize(16).TextColor(t.TextMuted) })
		})
	})
	if hover != r.hover {
		r.hover = hover
		c.Invalidate() // the highlight follows the pointer
	}
}

// slotIcon draws a slot's icon: a built-in one, or an app's image.
func (r *Ring) slotIcon(c *mui.Context, ic plugin.Icon, fg mui.Color) {
	name := ic.Name
	if name == "" {
		name = "app"
	}
	mui.Icon(c, builtinIcon(name)).FontSize(22).TextColor(fg)
}

// The name pill's content: padding on each side, the gap between the
// number key and the name, their font sizes.
const (
	ringLabelPad   = 10
	ringLabelInner = 6
	ringLabelKey   = 11
	ringLabelText  = 13
)

// label shows the name of slot i in a pill outside the ring, in the slot's
// direction (ringLabelAt), with its number key: on is the slot pointed at,
// whose pill stands out. A name too long for the pill ends with an
// ellipsis, and shows whole in the pill's tooltip. The pill is a part of
// its slot: pointing at it highlights the slot, a click runs it. It
// reports whether the pointer is on the pill.
func (r *Ring) label(c *mui.Context, i int, title string, on bool) bool {
	t := c.Theme()
	weight := 400
	if on {
		weight = 600
	}
	key := strconv.Itoa(i + 1)
	keyW, _ := c.MeasureText(0, mui.Span{Text: key, Size: ringLabelKey})
	textW, _ := c.MeasureText(0, mui.Span{Text: title, Size: ringLabelText, Weight: weight})
	w, cut := ringLabelWidth(keyW, textW)
	x, y := ringLabelAt(i, w)

	bg, border, keyBg, keyFg := t.Background.Alpha(0.96), t.Border, t.Text.Alpha(0.08), t.TextMuted
	if on {
		border, keyBg, keyFg = t.Accent, t.Accent, t.AccentText
	}
	p := mui.Row(c.Key("ring:label:"+key)).Absolute().Left(x).Top(y).Width(w).Height(ringLabelH).
		Padding(0, ringLabelPad).Gap(ringLabelInner).AlignItems(mui.Center).Radius(ringLabelH/2).
		Background(bg).Border(1, border).Shadow(0, 2, 8, 0, mui.RGBA(0, 0, 0, 0.16)).Cursor(mui.CursorPointer)
	if cut {
		p.Tooltip(title)
	}
	if p.Clicked() {
		r.host.Choose(i)
	}
	p.Children(func() {
		mui.Text(c, key).FontSize(ringLabelKey).TextColor(keyFg).Padding(0, 4).Radius(4).Background(keyBg)
		mui.Text(c, title).FontSize(ringLabelText).FontWeight(weight).TextColor(t.Text).SingleLine().Shrink(1).MinWidth(0)
	})
	return p.Hovered()
}

// ringLabelWidth returns the width of a name pill whose number key and
// name measure keyW and textW, and whether the name is cut to fit
// ringLabelMax.
func ringLabelWidth(keyW, textW float32) (float32, bool) {
	w := 2 + 2*ringLabelPad + (keyW + 8) + ringLabelInner + float32(math.Ceil(float64(textW))) + 1
	if w > ringLabelMax {
		return ringLabelMax, true
	}
	return w, false
}

// ringLabelAt returns the top left corner, in the ring's window, of the
// name pill of slot i, w wide, outside the disc in the slot's direction:
// on the right, it starts next to the disc; on the left, it ends there;
// above and below, it is centred.
func ringLabelAt(i int, w float32) (x, y float32) {
	const cx, cy = RingWidth / 2, RingHeight / 2
	dx, dy := ring.SlotOffset(i, RingRadius)
	top := float32(dy) - ringLabelH/2 // beside the slot
	switch {
	case dx > 1:
		return cx + ringLabelClear(top), cy + top
	case dx < -1:
		return cx - ringLabelClear(top) - w, cy + top
	case dy < 0:
		return cx - w/2, cy - ringDiscR - ringLabelGap - ringLabelH
	default:
		return cx - w/2, cy + ringDiscR + ringLabelGap
	}
}

// ringLabelClear returns how far sideways from the centre a pill going
// from top to top+ringLabelH (from the centre) starts, to clear the disc
// by ringLabelGap.
func ringLabelClear(top float32) float32 {
	var near float32 // the pill's nearest height to the centre's
	switch {
	case top > 0:
		near = top
	case top+ringLabelH < 0:
		near = -(top + ringLabelH)
	}
	if near >= ringDiscR {
		return ringLabelGap
	}
	return float32(math.Sqrt(float64(ringDiscR*ringDiscR-near*near))) + ringLabelGap
}

// RingOwn returns the title and icon of the ring's own actions in the
// current language (ring.Own).
func RingOwn(loc *i18n.Localizer) ring.Own {
	return func(key string) (string, string, bool) {
		switch key {
		case config.RingLauncher:
			return loc.T("ring.action.launcher"), "command", true
		case config.RingAskAI:
			return loc.T("ring.action.askAI"), "sparkles", true
		case config.RingScreenshot:
			return loc.T("ring.action.screenshot"), "scissors", true
		}
		return "", "", false
	}
}
