package shell

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	wincontext "github.com/eziq1/golauncher/internal/winapi/context"
	"github.com/eziq1/golauncher/internal/winapi/mousehook"
	winselection "github.com/eziq1/golauncher/internal/winapi/selection"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai"
)

// The Action Ring (docs/action-ring.md): the mouse hook feeds a
// ring.Machine; when the middle button is held, the ring's window shows
// around the pointer without taking the focus, so that the window and the
// text selected in it stay as they are; the chosen action runs once the
// ring is hidden.

// ringState is the Action Ring of a Shell. Main thread only, except where
// noted.
type ringState struct {
	machine *ring.Machine // nil where the hook is not supported
	stop    func()        // stops the hook; nil while it is off
	waiting chan struct{} // closed to stop waiting for the permission (macOS)
	win     *mygo.Window
	view    *ui.Ring

	// The ring showing: its window and centre in the hook's units, and
	// how many of them make a DIP.
	rect   ring.Rect
	center ring.Point
	scale  float64
	gen    int // a new ring, or closed: the context of an older one is dropped
	cap    ringCapture
	keys   bool // Escape and 1–8 are registered

	// Read on the hook's thread.
	exceptions atomic.Pointer[[]string]
	// The pointer while the button holds the ring open: the newest one,
	// and whether a frame is already asked for.
	trackPt      atomic.Pointer[ring.Point]
	trackPending atomic.Bool
	// shown is win, for posting to the main thread from the hook's.
	shown atomic.Pointer[mygo.Window]
}

// ringCapture is what the ring acts on: the window that was active (where
// Paste goes), the context of the window under the pointer and the text
// selected in it.
type ringCapture struct {
	fg  uintptr
	ctx *plugin.Context
	sel *plugin.Selection
}

// ringContextBudget bounds reading the context and the selection for a
// ring (Finder is asked with AppleScript on macOS).
var ringContextBudget = func() time.Duration {
	if runtime.GOOS == "darwin" {
		return time.Second
	}
	return 400 * time.Millisecond
}()

// ringKeys are the keys taken while the ring shows.
var ringKeys = []struct {
	acc string
	key mui.Key
}{
	{"Escape", mui.KeyEscape}, {"1", mui.Key1}, {"2", mui.Key2}, {"3", mui.Key3}, {"4", mui.Key4},
	{"5", mui.Key5}, {"6", mui.Key6}, {"7", mui.Key7}, {"8", mui.Key8},
}

// setUpRing creates the ring's machine and view and starts the hook if the
// ring is on. Main thread only.
func (s *Shell) setUpRing() {
	if !mousehook.Supported() {
		return
	}
	cfg := s.env.Config.Get()
	s.ring.view = ui.NewRing(s.env, ringHost{s})
	s.ring.machine = ring.NewMachine(ring.Options{
		Hold:      time.Duration(cfg.Ring.EffectiveHoldMS()) * time.Millisecond,
		Threshold: mousehook.DragThreshold(),
		Callbacks: ring.Callbacks{
			Allowed: s.ringAllowed,
			Replay:  mousehook.Replay,
			Open:    func(pt ring.Point) { s.ringPost(func() { s.openRing(pt) }) },
			Track:   s.ringTrack,
			Release: func(pt ring.Point, moved bool) { s.ringPost(func() { s.ringReleased(pt, moved) }) },
			Press:   func(pt ring.Point, middle bool) { s.ringPost(func() { s.ringPressed(pt, middle) }) },
		},
	})
	s.syncRing(cfg)
}

// syncRing applies the ring's settings: the hold, the exceptions, and the
// hook on or off. On macOS without the Accessibility permission it waits
// for it, asking nothing. Main thread only.
func (s *Shell) syncRing(cfg config.Config) {
	r := &s.ring
	if r.machine == nil {
		return
	}
	ex := cfg.Ring.Clone().Exceptions
	r.exceptions.Store(&ex)
	r.machine.SetHold(time.Duration(cfg.Ring.EffectiveHoldMS()) * time.Millisecond)
	switch {
	case cfg.Ring.Enabled && r.stop == nil:
		if !mousehook.Trusted() {
			s.waitRingPermission()
			return
		}
		s.stopWaitingRing()
		stop, err := mousehook.Start(r.machine)
		if err != nil {
			s.log.Warn("action ring: mouse hook", "err", err)
			return
		}
		r.stop = stop
		s.log.Info("action ring on")
	case !cfg.Ring.Enabled:
		s.stopWaitingRing()
		if r.stop != nil {
			r.stop()
			r.stop = nil
			r.machine.Reset()
			s.closeRing()
			s.log.Info("action ring off")
		}
	}
}

// waitRingPermission checks every few seconds whether the Accessibility
// permission was granted (macOS), and then turns the hook on.
func (s *Shell) waitRingPermission() {
	if s.ring.waiting != nil {
		return
	}
	done := make(chan struct{})
	s.ring.waiting = done
	s.log.Info("action ring: waiting for the Accessibility permission")
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-s.ctx.Done():
				return
			case <-t.C:
				if mousehook.Trusted() {
					s.post(func() {
						s.stopWaitingRing()
						s.syncRing(s.env.Config.Get())
					})
					return
				}
			}
		}
	}()
}

func (s *Shell) stopWaitingRing() {
	if s.ring.waiting != nil {
		close(s.ring.waiting)
		s.ring.waiting = nil
	}
}

// stopRing stops the hook as golauncher quits.
func (s *Shell) stopRing() {
	s.stopWaitingRing()
	if s.ring.stop != nil {
		s.ring.stop()
		s.ring.stop = nil
	}
}

// ringAllowed reports whether the ring may open over the app at pt. It runs
// on the hook's thread.
func (s *Shell) ringAllowed(pt ring.Point) bool {
	a := mousehook.AppAt(pt)
	if a.Blocked || a.FullScreen {
		return false
	}
	ex := s.ring.exceptions.Load()
	return ex == nil || !ring.Excluded(*ex, a.Name, a.Bundle, a.Title)
}

// ringWindow returns the ring's window, created the first time: a panel
// without frame or background that never takes the focus.
func (s *Shell) ringWindow() *mygo.Window {
	if s.ring.win != nil {
		return s.ring.win
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:         s.loc.T("settings.nav.ring"),
		Width:         ui.RingWidth,
		Height:        ui.RingHeight,
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		AlwaysOnTop:   true,
		SkipTaskbar:   true,
		DisableResize: true,
		DisableShadow: true,
		Content:       mui.View(s.ring.view.View),
	})
	noActivate(w.NativeHandle())
	w.OnClose(func(e *mygo.CloseEvent) {
		if s.quitting {
			return
		}
		e.PreventDefault()
		s.closeRing()
	})
	s.ring.win = w
	s.ring.shown.Store(w)
	return w
}

// ringPost runs fn on the main thread, later, without waiting: from the
// hook's thread, which must never wait. Through the ring's window once it
// exists, so that a frame of the launcher is not asked for each time.
func (s *Shell) ringPost(fn func()) {
	if w := s.ring.shown.Load(); w != nil {
		w.Update(fn)
		return
	}
	s.post(fn)
}

// openRing shows the ring around pt: the fixed actions at once, those for
// the context when they are known. Main thread only.
func (s *Shell) openRing(pt ring.Point) {
	r := &s.ring
	if r.machine == nil || !r.machine.Open() {
		return // closed meanwhile
	}
	r.gen++
	gen := r.gen
	cfg := s.env.Config.Get()
	fg := clip.ForegroundWindow()
	target := mousehook.WindowAt(pt)
	r.cap = ringCapture{fg: fg}

	// The fixed actions, as stored, until they are found again.
	var quick []ring.Slot
	own := ui.RingOwn(s.loc)
	for _, a := range cfg.Ring.Fixed[:min(len(cfg.Ring.Fixed), ring.Slots)] {
		if title, icon, ok := own(a.Key); ok {
			quick = append(quick, ring.Slot{Key: a.Key, Title: title, Icon: plugin.Icon{Name: icon}, Fixed: true})
		} else if !ring.IsOwn(a.Key) {
			quick = append(quick, ring.Slot{Key: a.Key, Title: a.Title, Icon: plugin.Icon{Name: a.Icon}, Fixed: true})
		}
	}
	r.view.Show(ring.Arrange(quick))
	w := s.ringWindow()
	r.rect, r.scale = placeRing(w, pt)
	r.center = r.rect.Center()
	w.ShowInactive()
	if rect, scale := placeRing(w, pt); rect != r.rect {
		// Shown on a display of another scale: placed again in its pixels.
		r.rect, r.scale, r.center = rect, scale, rect.Center()
	}
	w.Invalidate()
	s.registerRingKeys()

	// The selected text: of the window under the pointer only (where the
	// focus is); UI Automation or Accessibility only, never a simulated
	// copy while the button is held.
	var sel *winselection.Pending
	if cfg.ContextAware && cfg.Ring.Context && fg != 0 && (target == mousehook.RootOwner(fg) || runtime.GOOS == "darwin") {
		sc := cfg.Selection
		sel = winselection.Begin(fg, winselection.Options{MaxChars: sc.EffectiveMaxChars(), ExcludeApps: sc.ExcludeApps, Log: s.log})
	}
	go s.ringContent(gen, cfg, target, sel)
}

// ringContent reads the context and the selected text for the ring gen,
// finds its actions, and shows them if it still shows. Off the main thread.
func (s *Shell) ringContent(gen int, cfg config.Config, target uintptr, pending *winselection.Pending) {
	ctx, cancel := context.WithTimeout(s.ctx, ringContextBudget)
	defer cancel()
	var c *plugin.Context
	var sel *plugin.Selection
	if cfg.ContextAware && cfg.Ring.Context {
		var wg sync.WaitGroup
		if target != 0 {
			wg.Go(func() {
				if v, ok := wincontext.Detect(ctx, target); ok {
					c = &v
				}
			})
		}
		if pending != nil && pending.Err() == nil {
			wg.Go(func() {
				if v, ok := pending.Read(ctx); ok && strings.TrimSpace(v.Text) != "" && !ring.Excluded(cfg.Selection.ExcludeApps, v.App.Name) {
					sel = &v
				}
			})
		}
		wg.Wait()
	}
	fixed := ring.FixedSlots(s.ctx, s.env.Engine, cfg.Ring.Fixed, ui.RingOwn(s.loc))
	slots := ring.Content(s.ctx, s.env.Engine, cfg.Ring, fixed, c, sel)
	s.post(func() {
		if s.ring.gen != gen || s.ring.machine == nil || !s.ring.machine.Open() {
			return
		}
		s.ring.cap.ctx, s.ring.cap.sel = c, sel
		s.ring.view.SetLayout(ring.Arrange(slots))
		if s.ring.win != nil {
			s.ring.win.Invalidate()
		}
	})
}

// ringTrack follows the pointer while the button holds the ring open, on
// the hook's thread: one frame at a time is asked of the main thread.
func (s *Shell) ringTrack(pt ring.Point) {
	s.ring.trackPt.Store(&pt)
	if !s.ring.trackPending.CompareAndSwap(false, true) {
		return
	}
	s.ringPost(func() {
		s.ring.trackPending.Store(false)
		p := s.ring.trackPt.Load()
		if p == nil || s.ring.win == nil || !s.ring.win.IsVisible() {
			return
		}
		if s.ring.view.SetHighlight(ring.SlotAt(s.ring.center, *p, ui.RingDeadZone*s.ring.scale, 0)) {
			s.ring.win.Invalidate()
		}
	})
}

// ringReleased handles the button let go while it held the ring open:
// without moving, the ring stays; after a drag, the slot in its direction
// runs (none in the middle: the ring closes). Main thread only.
func (s *Shell) ringReleased(pt ring.Point, moved bool) {
	if s.ring.win == nil || !s.ring.win.IsVisible() {
		return
	}
	if !moved {
		s.ring.view.SetHighlight(-1)
		s.ring.win.Invalidate()
		return
	}
	s.chooseRingAt(pt, 0)
}

// ringPressed handles a button going down while the ring stays open: the
// middle button chooses the slot in its direction (or closes the ring away
// from it); another button outside the window closes the ring (inside, the
// window has the click). Main thread only.
func (s *Shell) ringPressed(pt ring.Point, middle bool) {
	if s.ring.win == nil || !s.ring.win.IsVisible() {
		return
	}
	switch {
	case middle:
		s.chooseRingAt(pt, (ui.RingRadius+2*ui.RingDeadZone)*s.ring.scale)
	case !s.ring.rect.Contains(pt):
		s.closeRing()
	}
}

// chooseRingAt runs the slot in the direction of pt, or closes the ring.
func (s *Shell) chooseRingAt(pt ring.Point, max float64) {
	if i := ring.SlotAt(s.ring.center, pt, ui.RingDeadZone*s.ring.scale, max); i >= 0 && s.ring.view.Layout()[i] != nil {
		s.chooseRing(i)
		return
	}
	s.closeRing()
}

// chooseRing hides the ring, then runs slot i. Main thread only.
func (s *Shell) chooseRing(i int) {
	slot := s.ring.view.Layout()[i]
	cap := s.ring.cap
	s.closeRing()
	if slot == nil {
		return
	}
	s.log.Info("action ring", "slot", i, "key", slot.Key)
	// After the ring's window is gone: the action runs in the window
	// that was active.
	s.post(func() { s.runRingSlot(*slot, cap) })
}

// closeRing hides the ring. Main thread only.
func (s *Shell) closeRing() {
	r := &s.ring
	r.gen++
	if r.machine != nil {
		r.machine.SetOpen(false)
	}
	s.unregisterRingKeys()
	if r.view != nil {
		r.view.SetHighlight(-1)
	}
	if r.win != nil && r.win.IsVisible() {
		r.win.Hide()
	}
}

// registerRingKeys takes Escape and 1–8 while the ring shows: its window
// never has the keyboard.
func (s *Shell) registerRingKeys() {
	if s.ring.keys {
		return
	}
	s.ring.keys = true
	for _, k := range ringKeys {
		if err := mygo.GlobalShortcut.Register(k.acc, func() { s.ring.view.Key(k.key) }); err != nil {
			s.log.Debug("action ring key", "key", k.acc, "err", err)
		}
	}
}

func (s *Shell) unregisterRingKeys() {
	if !s.ring.keys {
		return
	}
	s.ring.keys = false
	for _, k := range ringKeys {
		mygo.GlobalShortcut.Unregister(k.acc)
	}
}

// ringHost is the ring view's window.
type ringHost struct{ s *Shell }

func (h ringHost) Choose(i int) { h.s.chooseRing(i) }
func (h ringHost) Close()       { h.s.closeRing() }

// runRingSlot runs a slot of the ring in the window that was active.
// Main thread only.
func (s *Shell) runRingSlot(slot ring.Slot, cap ringCapture) {
	s.prevForeground.Store(cap.fg) // where Paste goes
	switch slot.Key {
	case config.RingLauncher:
		s.showLauncher()
		return
	case config.RingScreenshot:
		go func() {
			if err := screenshot(); err != nil {
				s.log.Warn("action ring: screenshot", "err", err)
				s.post(func() { s.ringFailed(err) })
			}
		}()
		return
	case config.RingAskAI:
		if cap.sel == nil {
			s.showLauncher()
			s.launcher.OpenCommand(ai.ID, ai.CommandChat)
			return
		}
		// A new chat about the selected text.
		item := plugin.Item{ID: "ring-ask", Transient: true, Sensitive: true, Actions: []plugin.Action{{ID: "ask",
			Do: plugin.ActionDo{Kind: plugin.ActionAskAI, Payload: plugin.MustPayload(plugin.AskAI{Text: cap.sel.Text})}}}}
		s.runRingEntry(core.Entry{Item: item, Plugin: ai.ID, Key: ai.ID + "/ring-ask", Context: true, Selection: true}, cap)
		return
	}
	if slot.Entry == nil {
		key, title := slot.Key, slot.Title
		go func() {
			e, ok := s.env.Engine.RingEntry(s.ctx, key, title)
			s.post(func() {
				if !ok {
					s.ringFailed(errors.New(title))
					return
				}
				slot.Entry = &e
				s.runRingSlot(slot, cap)
			})
		}()
		return
	}
	e := *slot.Entry
	if name, ok := strings.CutPrefix(e.ID, "cmd:"); ok && !slot.Entry.Context {
		// A command, as its hotkey runs it.
		if len(e.Actions) == 0 || e.Actions[0].Do.Kind != plugin.ActionRunCommand {
			s.showLauncher()
		}
		s.launcher.OpenCommand(e.Plugin, name)
		return
	}
	s.runRingEntry(e, cap)
}

// runRingEntry runs a root entry of the ring with the launcher hidden; the
// launcher shows if the action has more to show (a view, a confirmation).
func (s *Shell) runRingEntry(e core.Entry, cap ringCapture) {
	// What the launcher shows if it opens: the ring's context.
	s.env.Engine.SetContext(cap.ctx)
	if cap.sel != nil {
		s.env.Engine.SetSelection(cap.sel)
	}
	s.launcher.RunEntry(e, func(out core.Outcome, err error, show bool) {
		s.post(func() {
			if !show {
				s.env.Engine.ClearSelection() // the selected text lives for one action only
			}
			switch {
			case show:
				s.showCurrent()
			case err != nil:
				s.ringFailed(err)
			case out.Toast != nil && out.Toast.Style == plugin.ToastFailure:
				s.notify(core.Notice{Toast: *out.Toast})
			}
		})
	})
}

// showCurrent shows the launcher at what an action of the ring opened.
// Main thread only.
func (s *Shell) showCurrent() {
	if s.panel == nil {
		return
	}
	s.previewing = false
	if !s.panel.IsVisible() {
		d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
		w, h := s.panel.Size()
		s.panel.SetPosition(d.WorkArea.X+(d.WorkArea.Width-w)/2, d.WorkArea.Y+(d.WorkArea.Height-h)/3)
		s.launcher.ShownWith(core.ShowOptions{Current: true})
	}
	s.reveal()
}

// ringFailed tells that an action of the ring failed.
func (s *Shell) ringFailed(err error) {
	s.notify(core.Notice{Toast: plugin.Toast{Style: plugin.ToastFailure, Title: s.loc.T("ring.failed"), Message: err.Error()}})
}
