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
// or the drag points at is highlighted, its pill too. A folder
// (docs/action-ring-folders.md) pointed at for ArcDwell opens an arc of its
// actions outside the ring, each with its pill. The window never takes the
// focus; the shell feeds it the pointer while the middle button drags
// (Track, Release, Press) and the number keys (Key).

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
	// pills on its sides, above and below it. The ring is centred in it.
	RingWidth  = 2 * (ringDiscR + ringLabelGap + ringLabelMax + ringMargin)
	RingHeight = 2 * (ringDiscR + ringLabelGap + ringLabelH + ringMargin)

	// A folder's arc: the distance from the centre to its sub-slots', their
	// diameter, and the gap between a sub-slot and its pill.
	ringArcRadius = 170
	ringSubSlot   = 40
	ringArcGap    = 6

	// RingFolderWidth and RingFolderHeight are the window's size when the
	// ring has a folder: room for its arc and the arc's pills on every side.
	RingFolderWidth  = 2 * (ringArcRadius + ringSubSlot/2 + ringArcGap + ringLabelMax + ringMargin)
	RingFolderHeight = 2 * (ringArcRadius + ringSubSlot/2 + ringArcGap + ringLabelH + ringMargin)

	// ArcDwell is how long a folder is pointed at before its arc opens.
	ArcDwell = 150 * time.Millisecond

	ringOpen = 120 * time.Millisecond
)

// ringDisc is the diameter of the ring's background; ringBand the width
// of its band, the slots and a margin on each side: inside it, the ring is
// hollow (R129).
const (
	ringDisc = 2 * ringDiscR
	ringBand = ringSlot + 24
)

// hollowRing draws a band w wide on the inside of the circle of radius r
// around (cx, cy), hollow within (R129): a circle whose thick border is the
// band, filled with fill, and a thin line of line on each of its edges. It
// returns the band, to add a shadow to (it shows outside the circle only).
func hollowRing(c *mui.Context, key string, cx, cy, r, w float32, fill, line mui.Color) mui.Element {
	d := 2 * r
	band := mui.Box(c.Key(key)).Absolute().Left(cx-r).Top(cy-r).Size(d, d).Radius(r).Border(w, fill)
	mui.Box(c.Key(key+":outer")).Absolute().Left(cx-r).Top(cy-r).Size(d, d).Radius(r).Border(1, line)
	in := d - 2*w
	mui.Box(c.Key(key+":inner")).Absolute().Left(cx-in/2).Top(cy-in/2).Size(in, in).Radius(in/2).Border(1, line)
	return band
}

// ringArcMetrics are the sizes of a folder's arc.
var ringArcMetrics = ring.ArcMetrics{Radius: ringArcRadius, Step: math.Pi / 6, SlotR: ringSubSlot / 2, LabelGap: ringArcGap, LabelH: ringLabelH}

// RingSize returns the size of the ring's window for the slots of l: larger
// when a folder can open an arc.
func RingSize(l ring.Layout) (w, h int) {
	if l.HasFolder() {
		return RingFolderWidth, RingFolderHeight
	}
	return RingWidth, RingHeight
}

// RingHost is what the ring view asks of its window. Its methods are called
// on the UI thread and must return at once.
type RingHost interface {
	// Choose runs the slot at position i (0 at the top, clockwise).
	Choose(i int)
	// ChooseIn runs action j of the folder at position i.
	ChooseIn(i, j int)
	// Close closes the ring without running anything.
	Close()
}

// hoverCentre is Ring.hover when the close button is hovered.
const hoverCentre = -2

// Ring is the Action Ring's view.
type Ring struct {
	loc        *i18n.Localizer
	themes     *theme.Manager
	appearance atomic.Pointer[config.Appearance]
	host       RingHost

	mu        sync.Mutex
	layout    ring.Layout
	highlight int // the slot the drag points at, -1 for none
	dragSub   int // the sub-slot of the open arc the drag points at, -1 for none
	shownAt   time.Time
	opening   bool // the next frame starts the opening animation
	size      [2]float32
	bounds    ring.Rect // the part of the window on the screen, from the centre; zero for all of it

	arc     int       // the folder whose arc is open, -1 for none
	arcLay  *ring.Arc // the open arc as the last frame laid it out
	target  int       // what was pointed at last frame (dwell), -1 for none
	dwellAt time.Time // since when target is pointed at
	dwelt   bool      // target's arc opened, or closed, already: not again until it changes

	hover    int // the slot hovered last frame, -1 for none, hoverCentre (UI thread)
	hoverSub int // the sub-slot hovered last frame, -1 for none (UI thread)
}

// NewRing returns the ring view of env, shown in the window host.
func NewRing(env *app.Env, host RingHost) *Ring {
	r := &Ring{loc: env.I18n.Localizer(i18n.CoreNamespace), themes: env.Themes, host: host, highlight: -1, dragSub: -1, arc: -1, target: -1, hover: -1, hoverSub: -1}
	a := env.Config.Get().Appearance
	r.appearance.Store(&a)
	env.Config.OnChange(func(c config.Config) { r.appearance.Store(&c.Appearance) })
	return r
}

// Show prepares the view for a ring with the slots of l: nothing
// highlighted, no arc, the opening animation on the next frame. Safe from
// any goroutine.
func (r *Ring) Show(l ring.Layout) {
	r.mu.Lock()
	r.layout, r.highlight, r.dragSub, r.opening = l, -1, -1, true
	r.arc, r.arcLay, r.target, r.dwelt, r.bounds = -1, nil, -1, false, ring.Rect{}
	r.mu.Unlock()
}

// SetLayout replaces the slots (the context ones arrive after the ring
// opened). Safe from any goroutine.
func (r *Ring) SetLayout(l ring.Layout) {
	r.mu.Lock()
	r.layout = l
	if r.arc >= 0 && !l[r.arc].IsFolder() {
		r.arc, r.arcLay = -1, nil
	}
	r.mu.Unlock()
}

// SetBounds tells the part of the window on the screen, in DIPs from the
// ring's centre, where an arc must fit; zero for all of it. Safe from any
// goroutine.
func (r *Ring) SetBounds(b ring.Rect) {
	r.mu.Lock()
	r.bounds = b
	r.mu.Unlock()
}

// Layout returns the slots shown.
func (r *Ring) Layout() ring.Layout {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.layout
}

// Arc returns the position of the folder whose arc is open, or -1.
func (r *Ring) Arc() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.arc
}

// SetHighlight highlights slot i while the middle button drags toward it
// (-1 for none), and reports whether that changed. Safe from any goroutine.
func (r *Ring) SetHighlight(i int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.setHighlight(i, -1)
}

// setHighlight sets what the drag points at. r.mu is held.
func (r *Ring) setHighlight(i, sub int) bool {
	if i >= 0 && (i >= ring.Slots || r.layout[i] == nil) {
		i, sub = -1, -1
	}
	changed := r.highlight != i || r.dragSub != sub
	r.highlight, r.dragSub = i, sub
	return changed
}

// pick returns what p (DIPs from the ring's centre) points at: a slot, and
// a sub-slot of the open arc. r.mu is held.
func (r *Ring) pick(p ring.Point) (slot, sub int) {
	var arc *ring.Arc
	if r.arc >= 0 && r.arcLay != nil && r.arcLay.Folder == r.arc {
		arc = r.arcLay
	}
	slot, sub = ring.Pick(p, RingDeadZone, ringDiscR, arc)
	if slot >= 0 && r.layout[slot] == nil {
		slot, sub = -1, -1
	}
	return slot, sub
}

// Track points the drag at p, in DIPs from the ring's centre: the slot in
// its direction, or, with an arc open, the arc's sub-slot past the ring.
// Pointing elsewhere than the open arc's folder closes the arc. It reports
// whether that changed what is highlighted. Safe from any goroutine.
func (r *Ring) Track(p ring.Point) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	slot, sub := r.pick(p)
	changed := false
	if r.arc >= 0 && slot != r.arc {
		r.arc, r.arcLay, changed = -1, nil, true
	}
	return r.setHighlight(slot, sub) || changed
}

// EndDrag forgets the drag: the button went up without moving, the ring
// stays. Safe from any goroutine.
func (r *Ring) EndDrag() {
	r.mu.Lock()
	r.setHighlight(-1, -1)
	r.mu.Unlock()
}

// Release handles the middle button let go at p (DIPs from the ring's
// centre) after a drag: the action there runs; on a folder, the ring stays
// with the folder's arc open; elsewhere it closes. UI thread only.
func (r *Ring) Release(p ring.Point) { r.choose(p, 0) }

// Press handles a click of the middle button at p while the ring stays:
// as Release, with nothing chosen farther than max from the centre, but on
// the open arc. UI thread only.
func (r *Ring) Press(p ring.Point, max float64) { r.choose(p, max) }

func (r *Ring) choose(p ring.Point, max float64) {
	r.mu.Lock()
	slot, sub := r.pick(p)
	if sub < 0 && max > 0 && math.Hypot(p.X, p.Y) > max {
		slot = -1
	}
	folder := slot >= 0 && r.layout[slot].IsFolder()
	r.setHighlight(-1, -1)
	if folder && sub < 0 {
		r.openArc(slot)
	}
	r.mu.Unlock()
	switch {
	case sub >= 0:
		r.host.ChooseIn(slot, sub)
	case folder:
	case slot >= 0:
		r.host.Choose(slot)
	default:
		r.host.Close()
	}
}

// openArc opens the arc of the folder at i. r.mu is held.
func (r *Ring) openArc(i int) {
	if r.arc != i {
		r.arc, r.arcLay = i, nil
	}
	// What is pointed at now stays the dwell's target: only pointing
	// elsewhere afterwards closes the arc (a key opens it wherever the
	// pointer rests).
	r.target, r.dwelt = r.hover, true
	if r.highlight >= 0 || r.dragSub >= 0 {
		r.target = r.highlight
	}
}

// closeArc closes the arc, if any, and reports whether one was open.
func (r *Ring) closeArc() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	open := r.arc >= 0
	r.arc, r.arcLay, r.dragSub = -1, nil, -1
	return open
}

// Key handles a key the shell caught while the ring shows: Escape closes
// the open arc, else the ring; 1 to 8 choose a slot, a folder's opening
// its arc; with an arc open, 1 to 6 choose in it. It reports whether the
// key did something. UI thread only.
func (r *Ring) Key(key mui.Key) bool {
	switch {
	case key == mui.KeyEscape:
		if !r.closeArc() {
			r.host.Close()
		}
		return true
	case key >= mui.Key1 && key <= mui.Key8:
		i := int(key - mui.Key1)
		r.mu.Lock()
		l, arc := r.layout, r.arc
		if arc >= 0 {
			r.mu.Unlock()
			if i >= len(l[arc].Folder) {
				return false
			}
			r.host.ChooseIn(arc, i)
			return true
		}
		if l[i].IsFolder() {
			r.openArc(i)
			r.mu.Unlock()
			return true
		}
		r.mu.Unlock()
		if l[i] == nil {
			return false
		}
		r.host.Choose(i)
		return true
	}
	return false
}

// folderName returns the name a folder shows.
func (r *Ring) folderName(s *ring.Slot) string {
	if s.Title != "" {
		return s.Title
	}
	return r.loc.T("ring.folder")
}

// View builds the ring.
func (r *Ring) View(c *mui.Context) {
	res := resolveTheme(c, r.themes, *r.appearance.Load())
	ui := res.UI
	c.SetTheme(&ui)
	t := c.Theme()
	c.Root().Background(mui.Transparent)
	w, h := c.Size()
	cx, cy := w/2, h/2
	now := c.Now()

	r.mu.Lock()
	r.size = [2]float32{w, h}
	l, highlight, dragSub := r.layout, r.highlight, r.dragSub
	if r.opening {
		r.opening, r.shownAt = false, now
	}
	shownAt := r.shownAt
	// What is pointed at: the drag's, else the pointer's. A folder pointed
	// at for ArcDwell opens its arc; pointing at another slot or the
	// centre closes it.
	dragging := highlight >= 0 || dragSub >= 0
	target := r.hover
	if dragging {
		target = highlight
	}
	if target != r.target {
		r.target, r.dwellAt, r.dwelt = target, now, false
		if r.arc >= 0 && target != -1 && target != r.arc {
			r.arc, r.arcLay = -1, nil
		}
	}
	if r.arc < 0 && target >= 0 && l[target].IsFolder() && !r.dwelt {
		if d := now.Sub(r.dwellAt); d >= ArcDwell {
			r.openArc(target)
		} else {
			c.After(ArcDwell - d)
		}
	}
	arc := r.arc
	var lay *ring.Arc
	var subs []ring.Slot
	if arc >= 0 && l[arc].IsFolder() {
		subs = l[arc].Folder[:min(len(l[arc].Folder), config.MaxRingFolder)]
		widths := make([]float64, len(subs))
		for j, s := range subs {
			widths[j] = float64(r.labelWidth(c, j, s.Title, false))
		}
		bounds := ring.Rect{X: float64(-cx), Y: float64(-cy), W: float64(w), H: float64(h)}
		if r.bounds.W > 0 && r.bounds.H > 0 {
			bounds = bounds.Intersect(r.bounds)
		}
		a := ring.LayoutArc(arc, widths, ringArcMetrics, bounds)
		lay, r.arcLay = &a, &a
	} else {
		r.arc, r.arcLay, arc = -1, nil, -1
	}
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
		if d := now.Sub(shownAt); d < ringOpen {
			x := float64(d) / float64(ringOpen)
			p = float32(1 - (1-x)*(1-x)*(1-x)) // ease out
			c.AnimationFrame()
		}
	}

	active, activeSub := highlight, dragSub
	if !dragging {
		active, activeSub = r.hover, r.hoverSub
	}
	if arc >= 0 && active < 0 {
		active = arc // the folder of the open arc
	}
	// The arc's circles and pills, to keep the ring's pills off them.
	var arcRects []ring.Rect
	if lay != nil {
		for _, it := range lay.Items {
			arcRects = append(arcRects, grow(it.Box(lay.M.SlotR), 4), grow(it.Label, 4))
		}
	}
	hover, hoverSub := -1, -1
	mui.Box(c.Key("ring")).Size(w, h).Children(func() {
		// Outside the ring: a click closes it.
		if mui.Box(c.Key("ring:outside")).Absolute().Left(0).Top(0).Size(w, h).Clicked() {
			r.host.Close()
		}
		scale := 0.85 + 0.15*p
		mui.Box(c.Key("ring:body")).Absolute().Left(0).Top(0).Size(w, h).Opacity(p).Scale(scale, scale).PassThrough().Children(func() {
			hollowRing(c, "ring:disc", cx, cy, ringDiscR, ringBand, t.Background.Alpha(0.96), t.Border).
				Shadow(0, 6, 24, 0, mui.RGBA(0, 0, 0, 0.28))
			// Every slot's name, outside the ring (but where the arc is).
			for i, s := range l {
				if s == nil {
					continue
				}
				title, folder := s.Title, s.IsFolder()
				if folder {
					title = r.folderName(s)
				}
				lw := r.labelWidth(c, i, title, i == active)
				x, y := ringLabelAt(i, lw)
				box := ring.Rect{X: float64(x), Y: float64(y), W: float64(lw), H: ringLabelH}
				if overlapsAny(box, arcRects) {
					continue
				}
				hov, clicked := r.pill(c, "ring:label:"+strconv.Itoa(i+1), i, title, folder, cx+x, cy+y, i == active)
				if hov {
					hover = i
				}
				if clicked {
					r.chooseSlot(i)
				}
			}
			for i, s := range l {
				if s == nil {
					continue
				}
				dx, dy := ring.SlotOffset(i, RingRadius)
				x, y := cx+float32(dx)-ringSlot/2, cy+float32(dy)-ringSlot/2
				on := i == active
				bg, fg := t.Surface, t.Text
				if on {
					bg, fg = t.Accent, t.AccentText
				}
				title := s.Title
				if s.IsFolder() {
					title = r.folderName(s)
				}
				b := mui.Box(c.Key("ring:slot:"+strconv.Itoa(i))).Absolute().Left(x).Top(y).Size(ringSlot, ringSlot).Radius(ringSlot/2).
					Center().Background(bg).Border(1, t.Border).Cursor(mui.CursorPointer).Label(title)
				if b.Hovered() {
					hover = i
				}
				if b.Clicked() {
					r.chooseSlot(i)
				}
				b.Children(func() {
					if s.IsFolder() {
						r.folderIcon(c, s, fg)
					} else {
						r.slotIcon(c, s.Icon, fg, 22)
					}
				})
			}
			cb := mui.Box(c.Key("ring:close")).Absolute().Left(cx-ringCenter/2).Top(cy-ringCenter/2).Size(ringCenter, ringCenter).
				Radius(ringCenter/2).Center().Background(t.Surface).Border(1, t.Border).Cursor(mui.CursorPointer).Label(r.loc.T("ring.close"))
			if cb.Hovered() {
				cb.Background(t.SurfaceHover)
				hover = hoverCentre
			}
			if cb.Clicked() {
				r.host.Close()
			}
			cb.Children(func() { mui.Icon(c, builtinIcon("x")).FontSize(16).TextColor(t.TextMuted) })

			// The open folder's arc.
			if lay == nil {
				return
			}
			for j, it := range lay.Items {
				s := subs[j]
				on := j == activeSub
				hov, clicked := r.pill(c, "ring:sub:label:"+strconv.Itoa(j+1), j, s.Title, false,
					cx+float32(it.Label.X), cy+float32(it.Label.Y), on)
				bg, fg := t.Surface, t.Text
				if on {
					bg, fg = t.Accent, t.AccentText
				}
				const sd = ringSubSlot
				b := mui.Box(c.Key("ring:sub:"+strconv.Itoa(j))).Absolute().Left(cx+float32(it.Center.X)-sd/2).Top(cy+float32(it.Center.Y)-sd/2).
					Size(sd, sd).Radius(sd/2).Center().Background(bg).Border(1, t.Border).Shadow(0, 2, 8, 0, mui.RGBA(0, 0, 0, 0.18)).
					Cursor(mui.CursorPointer).Label(s.Title)
				if b.Hovered() || hov {
					hover, hoverSub = arc, j
				}
				if b.Clicked() || clicked {
					r.host.ChooseIn(arc, j)
				}
				b.Children(func() { r.slotIcon(c, s.Icon, fg, 18) })
			}
		})
	})
	if hover != r.hover || hoverSub != r.hoverSub {
		r.hover, r.hoverSub = hover, hoverSub
		c.Invalidate() // the highlight follows the pointer
	}
}

// chooseSlot runs slot i, clicked: a folder opens its arc.
func (r *Ring) chooseSlot(i int) {
	r.mu.Lock()
	folder := r.layout[i].IsFolder()
	if folder {
		r.openArc(i)
	}
	r.mu.Unlock()
	if !folder {
		r.host.Choose(i)
	}
}

func grow(b ring.Rect, d float64) ring.Rect {
	return ring.Rect{X: b.X - d, Y: b.Y - d, W: b.W + 2*d, H: b.H + 2*d}
}

func overlapsAny(b ring.Rect, list []ring.Rect) bool {
	for _, q := range list {
		if b.Overlaps(q) {
			return true
		}
	}
	return false
}

// slotIcon draws a slot's icon: a built-in one, or an app's image.
func (r *Ring) slotIcon(c *mui.Context, ic plugin.Icon, fg mui.Color, size float32) {
	name := ic.Name
	if name == "" {
		name = "app"
	}
	mui.Icon(c, builtinIcon(name)).FontSize(size).TextColor(fg)
}

// folderIcon draws a folder's icon: its first actions' icons, two by two.
func (r *Ring) folderIcon(c *mui.Context, s *ring.Slot, fg mui.Color) {
	items := s.Folder[:min(len(s.Folder), 4)]
	mui.Column(c).Gap(2).AlignItems(mui.Center).Children(func() {
		for row := 0; row < len(items); row += 2 {
			mui.Row(c).Gap(3).Children(func() {
				for _, it := range items[row:min(row+2, len(items))] {
					r.slotIcon(c, it.Icon, fg, 12)
				}
			})
		}
	})
}

// The name pill's content: padding on each side, the gap between the
// number key and the name, their font sizes.
const (
	ringLabelPad   = 10
	ringLabelInner = 6
	ringLabelKey   = 11
	ringLabelText  = 13
)

// labelWidth returns the width of the pill of slot (or sub-slot) i named
// title.
func (r *Ring) labelWidth(c *mui.Context, i int, title string, on bool) float32 {
	weight := 400
	if on {
		weight = 600
	}
	keyW, _ := c.MeasureText(0, mui.Span{Text: strconv.Itoa(i + 1), Size: ringLabelKey})
	textW, _ := c.MeasureText(0, mui.Span{Text: title, Size: ringLabelText, Weight: weight})
	w, _ := ringLabelWidth(keyW, textW)
	return w
}

// pill shows the name of slot (or sub-slot) i in a pill at x, y, with its
// number key; a folder's ends with "▸". On is the slot pointed at, whose
// pill stands out. A name too long for the pill ends with an ellipsis, and
// shows whole in the pill's tooltip. The pill is a part of its slot:
// pointing at it highlights the slot, a click runs it. It reports whether
// the pointer is on the pill and whether it was clicked.
func (r *Ring) pill(c *mui.Context, key string, i int, title string, folder bool, x, y float32, on bool) (hovered, clicked bool) {
	t := c.Theme()
	weight := 400
	if on {
		weight = 600
	}
	num := strconv.Itoa(i + 1)
	keyW, _ := c.MeasureText(0, mui.Span{Text: num, Size: ringLabelKey})
	textW, _ := c.MeasureText(0, mui.Span{Text: title, Size: ringLabelText, Weight: weight})
	w, cut := ringLabelWidth(keyW, textW)
	if folder {
		mw, _ := c.MeasureText(0, mui.Span{Text: "▸", Size: ringLabelText})
		w = min(w+ringLabelInner+mw+1, ringLabelMax)
	}

	bg, border, keyBg, keyFg := t.Background.Alpha(0.96), t.Border, t.Text.Alpha(0.08), t.TextMuted
	if on {
		border, keyBg, keyFg = t.Accent, t.Accent, t.AccentText
	}
	p := mui.Row(c.Key(key)).Absolute().Left(x).Top(y).Width(w).Height(ringLabelH).
		Padding(0, ringLabelPad).Gap(ringLabelInner).AlignItems(mui.Center).Radius(ringLabelH/2).
		Background(bg).Border(1, border).Shadow(0, 2, 8, 0, mui.RGBA(0, 0, 0, 0.16)).Cursor(mui.CursorPointer)
	if cut {
		p.Tooltip(title)
	}
	clicked = p.Clicked()
	p.Children(func() {
		mui.Text(c, num).FontSize(ringLabelKey).TextColor(keyFg).Padding(0, 4).Radius(4).Background(keyBg)
		mui.Text(c, title).FontSize(ringLabelText).FontWeight(weight).TextColor(t.Text).SingleLine().Shrink(1).MinWidth(0)
		if folder {
			mui.Text(c, "▸").FontSize(ringLabelText).TextColor(t.TextMuted)
		}
	})
	return p.Hovered(), clicked
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

// ringLabelAt returns the top left corner, from the ring's centre, of the
// name pill of slot i, w wide, outside the disc in the slot's direction:
// on the right, it starts next to the disc; on the left, it ends there;
// above and below, it is centred.
func ringLabelAt(i int, w float32) (x, y float32) {
	dx, dy := ring.SlotOffset(i, RingRadius)
	top := float32(dy) - ringLabelH/2 // beside the slot
	switch {
	case dx > 1:
		return ringLabelClear(top), top
	case dx < -1:
		return -ringLabelClear(top) - w, top
	case dy < 0:
		return -w / 2, -ringDiscR - ringLabelGap - ringLabelH
	default:
		return -w / 2, ringDiscR + ringLabelGap
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
