package ui

import (
	"errors"
	"math"
	"strconv"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/ring"
)

// The ring editor of the Action Ring's settings (docs/action-ring-folders.md
// §1): the ring drawn as it shows, its four fixed slots edited by dragging
// (an action onto an empty slot moves it, held over another action for
// MergeDwell makes a folder of the two, onto a folder goes into it) and
// a folder, clicked, opened below the ring, where its actions are dragged
// to reorder them or out to an empty slot. Buttons under it do the same
// for the slot or the action selected, without dragging.

// MergeDwell is how long an action dragged in the ring editor is held over
// another before letting go makes a folder of the two.
const MergeDwell = 500 * time.Millisecond

// The ring editor's geometry, in DIPs, in its own box.
const (
	edW      = 600
	edRingH  = 320
	edR      = 96 // from the centre to the slots' centres
	edSlotD  = 60
	edCtxD   = 44
	edDiscR  = edR + edSlotD/2 + 16
	edStripH = 104
	edItemX0 = 20 // the open folder's first action's left
	edItemDX = 92 // between two of its actions
	edItemD  = 52
	edDragAt = 4 // how far the pointer moves before a press is a drag
)

// edCenter is the ring's centre in the editor.
const edCX, edCY = edW / 2, edRingH / 2

// edHit is what a point of the editor is on: a fixed slot (slot, item -1),
// an action of the open folder (slot is the folder, item the action), or
// nothing (slot -1).
type edHit struct{ slot, item int }

var edNone = edHit{-1, -1}

// ringDrag is a press in the ring editor, and the drag it became.
type ringDrag struct {
	down, moved bool
	from        edHit
	x0, y0      float32
	x, y        float32
	over        edHit     // what the pointer is over
	overAt      time.Time // since when
}

// edSlotCenter returns the centre of fixed slot i in the editor.
func edSlotCenter(i int) (float32, float32) {
	dx, dy := ring.SlotOffset(ring.FixedPosition(i), edR)
	return edCX + float32(dx), edCY + float32(dy)
}

// edHitAt returns what (x, y) is on; open is the folder shown below the
// ring (-1 for none) and n its number of actions.
func edHitAt(x, y float32, open, n int) edHit {
	for i := range config.MaxRingFixed {
		sx, sy := edSlotCenter(i)
		if math.Hypot(float64(x-sx), float64(y-sy)) <= edSlotD/2+6 {
			return edHit{i, -1}
		}
	}
	if open >= 0 && y >= edRingH && y < edRingH+edStripH && n > 0 {
		k := int(math.Floor(float64(x-edItemX0+(edItemDX-edItemD)/2) / edItemDX))
		return edHit{open, max(0, min(k, n-1))}
	}
	return edNone
}

// ringEditorHeight returns the editor's height with the folder at open
// shown below the ring, or none.
func ringEditorHeight(open bool) float32 {
	if open {
		return edRingH + edStripH
	}
	return edRingH
}

// ringEditor draws the editor of the fixed slots f and handles its drags.
func (s *Settings) ringEditor(c *mui.Context, f ring.Fixed, save func(ring.Fixed)) {
	t := c.Theme()
	st := &s.ring
	if st.open >= 0 && !f[st.open].IsFolder() {
		st.open = -1
	}
	if st.sel >= 0 && st.item >= 0 && (st.open != st.sel || st.item >= len(f[st.sel].Actions)) {
		st.item = -1
	}
	var items []config.RingAction
	if st.open >= 0 {
		items = f[st.open].Actions
	}
	d := &st.drag
	now := c.Now()
	// What the drag is over, and whether it is held there long enough to
	// make a folder: the slot grows.
	merging := false
	if d.moved && d.from.item < 0 && d.over.item < 0 && d.over.slot >= 0 && d.over.slot != d.from.slot &&
		f[d.over.slot].Key != "" && f[d.from.slot].Key != "" {
		if el := now.Sub(d.overAt); el >= MergeDwell {
			merging = true
		} else {
			c.After(MergeDwell - el)
		}
	}

	h := ringEditorHeight(st.open >= 0)
	box := mui.Box(c.Key("ring:editor")).Width(edW).Height(h).Label(s.loc.T("settings.ring.editor"))
	box.HandleInput(func(ev mui.InputEvent) bool {
		switch ev.Kind {
		case mui.InputPointerDown:
			if ev.Button != 0 {
				return false
			}
			hit := edHitAt(ev.X, ev.Y, st.open, len(items))
			if hit.slot < 0 {
				return false
			}
			*d = ringDrag{down: true, from: hit, x0: ev.X, y0: ev.Y, x: ev.X, y: ev.Y, over: hit, overAt: ev.Time}
			return true
		case mui.InputPointerMove:
			if !d.down {
				return false
			}
			d.x, d.y = ev.X, ev.Y
			src := d.from
			if !d.moved && math.Hypot(float64(ev.X-d.x0), float64(ev.Y-d.y0)) > edDragAt && (src.item >= 0 || !f[src.slot].IsEmpty()) {
				d.moved = true
			}
			if over := edHitAt(ev.X, ev.Y, st.open, len(items)); over != d.over {
				d.over, d.overAt = over, ev.Time
			}
			return true
		case mui.InputPointerUp:
			if !d.down {
				return false
			}
			drag := *d
			*d = ringDrag{}
			to := edHitAt(ev.X, ev.Y, st.open, len(items))
			if !drag.moved {
				s.ringEditorClick(f, drag.from)
				return true
			}
			armed := to == drag.over && ev.Time.Sub(drag.overAt) >= MergeDwell
			s.ringEditorDrop(f, drag.from, to, armed, save)
			return true
		}
		return false
	})
	box.Children(func() {
		hollowRing(c, "ring-editor:disc", edCX, edCY, edDiscR, edSlotD+32, t.Surface, t.Border)
		// The diagonals: the context actions'.
		for _, p := range []int{1, 3, 5, 7} {
			dx, dy := ring.SlotOffset(p, edR)
			mui.Box(c).Absolute().Left(edCX+float32(dx)-edCtxD/2).Top(edCY+float32(dy)-edCtxD/2).Size(edCtxD, edCtxD).Radius(edCtxD/2).
				Center().Background(t.Text.Alpha(0.05)).Border(1, t.Border.Alpha(0.6)).Children(func() {
				mui.Text(c, s.loc.T("settings.ring.contextSlot")).FontSize(10).TextColor(t.TextMuted.Alpha(0.8))
			})
		}
		mui.Box(c).Absolute().Left(edCX-14).Top(edCY-14).Size(28, 28).Radius(14).Center().Border(1, t.Border).Children(func() {
			mui.Icon(c, builtinIcon("x")).FontSize(12).TextColor(t.TextMuted)
		})
		for i, slot := range f {
			s.ringEditorSlot(c, i, slot, merging && d.over.slot == i)
		}
		if st.open >= 0 {
			s.ringEditorStrip(c, st.open, items)
		}
		// What is dragged follows the pointer.
		if d.moved {
			icon, folder := "", false
			if d.from.item >= 0 {
				icon = s.ringChoiceOf(items[d.from.item]).Icon
			} else if sl := f[d.from.slot]; sl.IsFolder() {
				folder = true
			} else {
				icon = s.ringChoiceOf(sl.Action()).Icon
			}
			const g = 48
			mui.Box(c).Absolute().Left(d.x-g/2).Top(d.y-g/2).Size(g, g).Radius(g/2).Center().Opacity(0.85).
				Background(t.Accent).Shadow(0, 4, 12, 0, mui.RGBA(0, 0, 0, 0.25)).PassThrough().Children(func() {
				if folder {
					s.ringFolderThumb(c, f[d.from.slot].Actions, t.AccentText)
				} else {
					mui.Icon(c, builtinIcon(cmpOr(icon, "app"))).FontSize(20).TextColor(t.AccentText)
				}
			})
		}
	})
}

// ringSlotName returns the name fixed slot sl shows (a folder's, or the
// action's) and its accessible label.
func (s *Settings) ringSlotName(i int, sl config.RingSlot) (name, label string) {
	switch {
	case sl.IsFolder():
		n := cmpOr(sl.Name, s.loc.T("ring.folder"))
		return n, n
	case sl.IsEmpty():
		return "", s.loc.T("settings.ring.emptySlot", "pos", s.ringPosName(i))
	}
	n := s.ringChoiceOf(sl.Action()).Title
	return n, n
}

// ringPosName returns the name of fixed slot i's position.
func (s *Settings) ringPosName(i int) string {
	switch i {
	case 0:
		return s.loc.T("settings.ring.pos.top")
	case 1:
		return s.loc.T("settings.ring.pos.right")
	case 2:
		return s.loc.T("settings.ring.pos.bottom")
	}
	return s.loc.T("settings.ring.pos.left")
}

// ringEditorSlot draws fixed slot i: grown is the action a dragged one
// is held over long enough to make a folder.
func (s *Settings) ringEditorSlot(c *mui.Context, i int, sl config.RingSlot, grown bool) {
	t := c.Theme()
	st := &s.ring
	d := &st.drag
	x, y := edSlotCenter(i)
	name, label := s.ringSlotName(i, sl)
	bg, fg, border, bw := t.Background, t.Text, t.Border, float32(1)
	switch {
	case d.moved && d.over.slot == i && d.over.item < 0 && d.from != d.over:
		bg, border, bw = t.Accent.Alpha(0.18), t.Accent, 2
	case st.sel == i && st.item < 0:
		border, bw = t.Accent, 2
	}
	if sl.IsEmpty() {
		bg, fg = mui.Transparent, t.TextMuted
	}
	b := mui.Box(c.Key("ring:ed:slot:"+strconv.Itoa(i))).Absolute().Left(x-edSlotD/2).Top(y-edSlotD/2).Size(edSlotD, edSlotD).
		Radius(edSlotD/2).Center().Background(bg).Border(bw, border).Label(label)
	if grown {
		b.Scale(1.18, 1.18)
	}
	if d.moved && d.from.slot == i && d.from.item < 0 {
		b.Opacity(0.35)
	}
	b.Children(func() {
		switch {
		case sl.IsFolder():
			s.ringFolderThumb(c, sl.Actions, fg)
		case sl.IsEmpty():
			mui.Icon(c, builtinIcon("plus")).FontSize(18).TextColor(fg)
		default:
			mui.Icon(c, builtinIcon(cmpOr(s.ringChoiceOf(sl.Action()).Icon, "app"))).FontSize(22).TextColor(fg)
		}
	})
	if name != "" {
		if sl.IsFolder() {
			name += " ▸"
		}
		mui.Text(c, name).Absolute().Left(x - 60).Top(y + edSlotD/2 + 4).Width(120).FontSize(11).TextAlign(mui.Center).
			SingleLine().TextColor(t.Text)
	}
}

// ringFolderThumb draws a folder's icon: its first actions' icons, two by
// two.
func (s *Settings) ringFolderThumb(c *mui.Context, acts []config.RingAction, fg mui.Color) {
	acts = acts[:min(len(acts), 4)]
	mui.Column(c).Gap(2).AlignItems(mui.Center).Children(func() {
		for row := 0; row < len(acts); row += 2 {
			mui.Row(c).Gap(3).Children(func() {
				for _, a := range acts[row:min(row+2, len(acts))] {
					mui.Icon(c, builtinIcon(cmpOr(s.ringChoiceOf(a).Icon, "app"))).FontSize(12).TextColor(fg)
				}
			})
		}
	})
}

// ringEditorStrip draws the folder at slot i, opened below the ring.
func (s *Settings) ringEditorStrip(c *mui.Context, i int, items []config.RingAction) {
	t := c.Theme()
	st := &s.ring
	d := &st.drag
	mui.Box(c).Absolute().Left(0).Top(edRingH).Size(edW, edStripH-4).Radius(10).Background(t.Text.Alpha(0.04)).Border(1, t.Border)
	for k, a := range items {
		ch := s.ringChoiceOf(a)
		x := float32(edItemX0 + k*edItemDX)
		y := float32(edRingH + 10)
		border, bw := t.Border, float32(1)
		switch {
		case d.moved && d.over == (edHit{i, k}) && d.from != d.over:
			border, bw = t.Accent, 2
		case st.sel == i && st.item == k:
			border, bw = t.Accent, 2
		}
		b := mui.Box(c.Key("ring:ed:item:"+strconv.Itoa(k))).Absolute().Left(x).Top(y).Size(edItemD, edItemD).Radius(edItemD/2).
			Center().Background(t.Background).Border(bw, border).Label(ch.Title)
		if d.moved && d.from == (edHit{i, k}) {
			b.Opacity(0.35)
		}
		b.Children(func() { mui.Icon(c, builtinIcon(cmpOr(ch.Icon, "app"))).FontSize(20).TextColor(t.Text) })
		mui.Text(c, strconv.Itoa(k+1)+" "+ch.Title).Absolute().Left(x - 18).Top(y + edItemD + 4).Width(edItemD + 36).FontSize(11).
			TextAlign(mui.Center).SingleLine().TextColor(t.Text)
	}
}

// ringEditorClick selects what was clicked: a folder opens below the
// ring (or closes, clicked again).
func (s *Settings) ringEditorClick(f ring.Fixed, hit edHit) {
	st := &s.ring
	st.hint = ""
	if hit.item >= 0 {
		st.sel, st.item = hit.slot, hit.item
		return
	}
	st.sel, st.item = hit.slot, -1
	switch {
	case f[hit.slot].IsFolder() && st.open == hit.slot:
		st.open = -1
	case f[hit.slot].IsFolder():
		s.openRingFolder(hit.slot, false)
	default:
		st.open = -1
	}
}

// openRingFolder shows the folder at slot i below the ring, its name to
// edit (focused when rename).
func (s *Settings) openRingFolder(i int, rename bool) {
	st := &s.ring
	st.open, st.rename = i, rename
	delete(s.bufs, ringFolderNameKey)
}

// ringFolderNameKey is the edit buffer of the open folder's name.
const ringFolderNameKey = "ring/folder/name"

// ringEditorDrop handles what was dragged from let go on to: armed tells
// that it was held over to for MergeDwell.
func (s *Settings) ringEditorDrop(f ring.Fixed, from, to edHit, armed bool, save func(ring.Fixed)) {
	st := &s.ring
	st.hint = ""
	if to.slot < 0 || to == from {
		return
	}
	var err error
	switch {
	case from.item < 0 && to.item >= 0:
		// An action onto the open folder's actions: into the folder.
		if f[from.slot].IsFolder() {
			err = errNested
		} else {
			err = f.AddToFolder(from.slot, to.slot)
		}
	case from.item < 0:
		src, dst := f[from.slot], f[to.slot]
		switch {
		case dst.IsEmpty() || src.IsFolder():
			// Into an empty slot; a folder trades places (folders do not
			// go into folders).
			err = f.Move(from.slot, to.slot)
		case dst.IsFolder():
			err = f.AddToFolder(from.slot, to.slot)
		case armed:
			if err = f.Merge(from.slot, to.slot, s.loc.T("ring.folder")); err == nil {
				save(f)
				st.sel, st.item = to.slot, -1
				s.openRingFolder(to.slot, true)
				return
			}
		default:
			err = f.Move(from.slot, to.slot)
		}
	case to.item >= 0 && to.slot == from.slot:
		err = f.Reorder(from.slot, from.item, to.item)
		st.sel, st.item = from.slot, to.item
	case to.slot == from.slot:
		return
	case f[to.slot].IsEmpty():
		err = f.MoveOut(from.slot, from.item, to.slot)
	case f[to.slot].IsFolder():
		err = f.MoveInto(from.slot, from.item, to.slot, s.loc.T("ring.folder"))
	case f.FirstEmpty() < 0:
		err = ring.ErrNoRoom
	default:
		err = errDropOnEmpty
	}
	if err != nil {
		s.ringHint(err)
		return
	}
	if from.item >= 0 && !(to.item >= 0 && to.slot == from.slot) {
		st.item = -1
	}
	save(f)
}

var (
	errNested      = errors.New("nested")
	errDropOnEmpty = errors.New("drop on empty")
)

// ringHint tells why an edit of the ring editor was refused.
func (s *Settings) ringHint(err error) {
	switch {
	case errors.Is(err, ring.ErrFolderFull):
		s.ring.hint = s.loc.T("settings.ring.hint.full", "max", config.MaxRingFolder)
	case errors.Is(err, ring.ErrNoRoom):
		s.ring.hint = s.loc.T("settings.ring.hint.noRoom")
	case errors.Is(err, errNested):
		s.ring.hint = s.loc.T("settings.ring.hint.nested")
	case errors.Is(err, errDropOnEmpty):
		s.ring.hint = s.loc.T("settings.ring.hint.dropOnEmpty")
	}
}

// ringChooser is a choice of the "Move…" and "Move into Folder…" dialogs.
type ringChooser struct {
	label, detail string
	disabled      bool
	do            func(f *ring.Fixed) error
}

// ringSelectionButtons are the buttons for the slot or the folder's action
// selected: what dragging does, without dragging.
func (s *Settings) ringSelectionButtons(c *mui.Context, f ring.Fixed, save func(ring.Fixed)) {
	st := &s.ring
	if st.sel < 0 || f[st.sel].IsEmpty() {
		return
	}
	apply := func(do func(f *ring.Fixed) error) {
		g := f
		if err := do(&g); err != nil {
			s.ringHint(err)
			return
		}
		st.hint = ""
		save(g)
	}
	i, item := st.sel, st.item
	sl := f[i]
	var name string
	if item >= 0 {
		name = s.ringChoiceOf(sl.Actions[item]).Title
	} else {
		name, _ = s.ringSlotName(i, sl)
	}
	mui.Row(c.Key("ring:ed:buttons")).Gap(8).AlignItems(mui.Center).Wrap().Children(func() {
		mui.Text(c, name).FontSize(12).Bold().Shrink(1)
		mui.Spacer(c)
		if mui.Button(c.Key("ring:ed:move"), s.loc.T("settings.ring.move")).Label(s.loc.T("settings.ring.moveOf", "name", name)).Clicked() {
			st.choosing, st.chooseTitle = "move", s.loc.T("settings.ring.move.title", "name", name)
		}
		if !sl.IsFolder() || item >= 0 {
			if mui.Button(c.Key("ring:ed:into"), s.loc.T("settings.ring.moveInto")).Label(s.loc.T("settings.ring.moveIntoOf", "name", name)).Clicked() {
				st.choosing, st.chooseTitle = "into", s.loc.T("settings.ring.moveInto.title", "name", name)
			}
		}
		if item >= 0 {
			if mui.Button(c.Key("ring:ed:out"), s.loc.T("settings.ring.moveOut")).Label(s.loc.T("settings.ring.moveOutOf", "name", name)).Clicked() {
				apply(func(g *ring.Fixed) error { return g.MoveOut(i, item, -1) })
				st.item = -1
			}
			if mui.Button(c.Key("ring:ed:remove"), s.loc.T("settings.ring.remove")).Label(s.loc.T("settings.ring.removeOf", "name", name)).Clicked() {
				apply(func(g *ring.Fixed) error { g.RemoveItem(i, item); return nil })
				st.item = -1
			}
			return
		}
		if sl.IsFolder() {
			if mui.Button(c.Key("ring:ed:delete"), s.loc.T("settings.ring.deleteFolder")).Label(s.loc.T("settings.ring.deleteFolderOf", "name", name)).Clicked() {
				if len(sl.Actions) > f.Empties()+1 {
					st.deleting = true
				} else {
					apply(func(g *ring.Fixed) error { g.DeleteFolder(i); return nil })
					st.open, st.sel = -1, -1
				}
			}
			return
		}
		if mui.Button(c.Key("ring:ed:remove"), s.loc.T("settings.ring.remove")).Label(s.loc.T("settings.ring.removeOf", "name", name)).Clicked() {
			apply(func(g *ring.Fixed) error { g.Remove(i); return nil })
		}
	})

	// The choices of the dialog open.
	var choices []ringChooser
	switch st.choosing {
	case "move":
		if item >= 0 {
			for k := range sl.Actions {
				if k != item {
					choices = append(choices, ringChooser{label: s.loc.T("settings.ring.position", "n", k+1),
						detail: s.ringChoiceOf(sl.Actions[k]).Title,
						do:     func(g *ring.Fixed) error { st.item = k; return g.Reorder(i, item, k) }})
				}
			}
			break
		}
		for j := range config.MaxRingFixed {
			if j == i {
				continue
			}
			n, _ := s.ringSlotName(j, f[j])
			choices = append(choices, ringChooser{label: s.ringPosName(j), detail: cmpOr(n, s.loc.T("settings.ring.emptyName")),
				do: func(g *ring.Fixed) error { st.sel = j; return g.Move(i, j) }})
		}
	case "into":
		for j, other := range f {
			if j == i || other.IsEmpty() {
				continue
			}
			n, _ := s.ringSlotName(j, other)
			ch := ringChooser{label: s.loc.T("settings.ring.moveInto.folder", "name", n)}
			switch {
			case other.IsFolder():
				ch.disabled = len(other.Actions) >= config.MaxRingFolder
				if ch.disabled {
					ch.detail = s.loc.T("settings.ring.full")
				}
				if item >= 0 {
					ch.do = func(g *ring.Fixed) error { st.item = -1; return g.MoveInto(i, item, j, "") }
				} else {
					ch.do = func(g *ring.Fixed) error {
						st.sel = j
						s.openRingFolder(j, false)
						return g.AddToFolder(i, j)
					}
				}
			default:
				ch.label = s.loc.T("settings.ring.moveInto.new", "name", n)
				if item >= 0 {
					ch.do = func(g *ring.Fixed) error {
						st.item, st.sel = -1, j
						return g.MoveInto(i, item, j, s.loc.T("ring.folder"))
					}
				} else {
					ch.do = func(g *ring.Fixed) error {
						st.sel = j
						if err := g.Merge(i, j, s.loc.T("ring.folder")); err != nil {
							return err
						}
						s.openRingFolder(j, true)
						return nil
					}
				}
			}
			choices = append(choices, ch)
		}
	}
	if st.choosing == "" {
		return
	}
	open := true
	mui.Modal(c.Key("ring:choose"), &open, func() {
		t := c.Theme()
		mui.Column(c).Gap(12).Padding(20).Width(420).Children(func() {
			mui.Text(c, st.chooseTitle).Bold().Level(2)
			if len(choices) == 0 {
				mui.Text(c, s.loc.T("settings.ring.choose.none")).TextColor(t.TextMuted)
			}
			for k, ch := range choices {
				row := mui.Row(c.Key("ring:choice:"+strconv.Itoa(k))).Gap(8).Padding(6, 8).Radius(6).AlignItems(mui.Center).Label(ch.label)
				if !ch.disabled {
					row.Cursor(mui.CursorPointer)
					if row.Hovered() {
						row.Background(t.SurfaceHover)
					}
					if row.Clicked() {
						st.choosing = ""
						apply(ch.do)
					}
				}
				row.Children(func() {
					mui.Text(c, ch.label).Grow(1).Shrink(1)
					if ch.detail != "" {
						mui.Text(c, ch.detail).FontSize(12).TextColor(t.TextMuted)
					}
				})
			}
			mui.Row(c).Gap(8).Children(func() {
				mui.Spacer(c)
				if button(c, s.loc.T("settings.ring.cancel")) {
					st.choosing = ""
				}
			})
		})
	})
	if !open {
		st.choosing = ""
	}
}
