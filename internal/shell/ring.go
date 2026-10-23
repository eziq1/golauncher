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
	"github.com/eziq1/golauncher/internal/trackpad"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	wincontext "github.com/eziq1/golauncher/internal/winapi/context"
	"github.com/eziq1/golauncher/internal/winapi/mousehook"
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
	machine ringMachine // nil where the hook is not supported
	remote  ringRemote  // machine, where it runs in the hook host; nil in this process
	stop    func()      // stops the hook; nil while it is off
	// restarted: the hook host ended once and was started again; the next
	// time the hook runs in this process.
	restarted bool
	waiting   chan struct{} // closed to stop waiting for the permission (macOS)
	// tapStop stops watching the trackpads for a three-finger tap (macOS,
	// R127); nil while off. tapFailed: it could not start, which was
	// logged once.
	tapStop   func()
	tapFailed bool
	win       *mygo.Window
	view      *ui.Ring

	// The ring showing: its window and centre in the hook's units, and
	// how many of them make a DIP.
	rect   ring.Rect
	center ring.Point
	scale  float64
	work   ring.Rect // the work area of its display
	gen    int       // a new ring, or closed: the context of an older one is dropped
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
	// selHere: the pointer was on the focused window, whose selected text
	// the ring's actions take; read: the selection was read again once an
	// action was chosen (ringSelection).
	selHere, read bool
}

// ringContextBudget bounds reading the context and the selection for a
// ring (Finder is asked with AppleScript on macOS; the copy fallback takes
// up to a few hundred milliseconds, R131).
var ringContextBudget = func() time.Duration {
	if runtime.GOOS == "darwin" {
		return time.Second
	}
	return 800 * time.Millisecond
}()

// ringKeys are the keys taken while the ring shows.
var ringKeys = []struct {
	acc string
	key mui.Key
}{
	{"Escape", mui.KeyEscape}, {"1", mui.Key1}, {"2", mui.Key2}, {"3", mui.Key3}, {"4", mui.Key4},
	{"5", mui.Key5}, {"6", mui.Key6}, {"7", mui.Key7}, {"8", mui.Key8},
}

// ringMachine is what the shell needs of the machine telling a held
// middle button from a click: a ring.Machine fed by the hook in this
// process, or the one in the hook host process (mousehook.Remote, Windows:
// docs/action-ring.md §9), whose callbacks come the same way.
type ringMachine interface {
	Open() bool
	ButtonDown() bool
	SetOpen(open bool)
	SetHold(d time.Duration)
	Reset()
}

// ringRemote is the machine in the hook host (mousehook.Remote): its hook
// starts with the host, and the exceptions are sent to it.
type ringRemote interface {
	ringMachine
	SetExceptions(apps []string)
	Start() (stop func(), err error)
}

// startLocalHook starts the hook in this process; replaced in tests.
var startLocalHook = mousehook.Start

// setUpRing creates the ring's machine and view and starts the hook if the
// ring is on. Main thread only.
func (s *Shell) setUpRing() {
	if !mousehook.Supported() {
		return
	}
	cfg := s.env.Config.Get()
	s.ring.view = ui.NewRing(s.env, ringHost{s})
	hold := time.Duration(cfg.Ring.EffectiveHoldMS()) * time.Millisecond
	if mousehook.HostSupported() {
		s.ring.remote = mousehook.NewRemote(hold, s.ringCallbacks(), func(err error) {
			s.post(func() { s.ringHostExited(err) })
		})
		s.ring.machine = s.ring.remote
	} else {
		s.ring.machine = s.localRingMachine(hold)
	}
	s.syncRing(cfg)
}

// ringCallbacks are how the machine opens and drives the ring: from the
// hook's thread (or the goroutine reading the hook host), posting to the
// main thread.
func (s *Shell) ringCallbacks() ring.Callbacks {
	return ring.Callbacks{
		Open:    func(pt ring.Point) { s.ringPost(func() { s.openRing(pt) }) },
		Track:   s.ringTrack,
		Release: func(pt ring.Point, moved bool) { s.ringPost(func() { s.ringReleased(pt, moved) }) },
		Press:   func(pt ring.Point, middle bool) { s.ringPost(func() { s.ringPressed(pt, middle) }) },
	}
}

// localRingMachine returns a machine fed by the hook in this process.
func (s *Shell) localRingMachine(hold time.Duration) *ring.Machine {
	cb := s.ringCallbacks()
	cb.Allowed, cb.Replay = s.ringAllowed, mousehook.Replay
	return ring.NewMachine(ring.Options{Hold: hold, Threshold: mousehook.DragThreshold(), Callbacks: cb})
}

// startRingHook starts watching the mouse: the hook host, or the hook in
// this process feeding the machine here.
func (s *Shell) startRingHook() (stop func(), err error) {
	if s.ring.remote != nil {
		return s.ring.remote.Start()
	}
	h, ok := s.ring.machine.(mousehook.Handler)
	if !ok {
		return nil, errors.New("action ring: no hook for the machine")
	}
	return startLocalHook(h)
}

// ringInProcess makes the hook run in this process from now on, with the
// settings cfg: the hook host could not start or ended again. The ring must
// be off. Main thread only.
func (s *Shell) ringInProcess(cfg config.Config) {
	r := &s.ring
	r.remote = nil
	r.machine = s.localRingMachine(time.Duration(cfg.Ring.EffectiveHoldMS()) * time.Millisecond)
}

// ringHostExited handles the hook host ending while the ring is on: it is
// started again once, then the hook runs in this process. Main thread
// only.
func (s *Shell) ringHostExited(err error) {
	r := &s.ring
	if r.remote == nil || r.stop == nil {
		return // stopped or replaced meanwhile
	}
	r.stop()
	r.stop = nil
	s.closeRing()
	cfg := s.env.Config.Get()
	if !r.restarted {
		r.restarted = true
		s.log.Warn("action ring: the mouse hook host ended; starting it again", "err", err)
	} else {
		s.log.Warn("action ring: the mouse hook host ended again; the hook runs in golauncher", "err", err)
		s.ringInProcess(cfg)
	}
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
	defer s.syncTrackpad(cfg) // with the hook as it ends up
	ex := cfg.Ring.Clone().Exceptions
	r.exceptions.Store(&ex)
	if r.remote != nil {
		r.remote.SetExceptions(ex)
	}
	r.machine.SetHold(time.Duration(cfg.Ring.EffectiveHoldMS()) * time.Millisecond)
	switch {
	case cfg.Ring.Enabled && r.stop == nil:
		if !mousehook.Trusted() {
			s.waitRingPermission()
			return
		}
		s.stopWaitingRing()
		stop, err := s.startRingHook()
		if err != nil && r.remote != nil {
			s.log.Warn("action ring: mouse hook host; the hook runs in golauncher", "err", err)
			s.ringInProcess(cfg)
			stop, err = s.startRingHook()
		}
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
	s.stopTrackpad()
	if s.ring.stop != nil {
		s.ring.stop()
		s.ring.stop = nil
	}
}

// startTrackpad starts watching the trackpads for a three-finger tap, and
// trackpadSupported reports whether it can (macOS); replaced in tests.
var (
	startTrackpad     = trackpad.Start
	trackpadSupported = trackpad.Supported
)

// syncTrackpad watches the trackpads for a three-finger tap (R127,
// docs/action-ring.md §10) while the hook runs (the ring is on and, on
// macOS, has the Accessibility permission) and the setting is on. Where
// the framework is missing, the tap stays off with one line in the log.
// Main thread only.
func (s *Shell) syncTrackpad(cfg config.Config) {
	r := &s.ring
	on := cfg.Ring.Enabled && cfg.Ring.TrackpadTap && r.stop != nil
	switch {
	case on && r.tapStop == nil && !r.tapFailed:
		if !trackpadSupported() {
			return
		}
		stop, err := startTrackpad(func() { s.ringPost(s.ringTapped) })
		if err != nil {
			r.tapFailed = true
			s.log.Info("action ring: no three-finger tap", "err", err)
			return
		}
		r.tapStop = stop
		s.log.Info("action ring: three-finger tap on")
	case !on:
		s.stopTrackpad()
	}
}

func (s *Shell) stopTrackpad() {
	if s.ring.tapStop != nil {
		s.ring.tapStop()
		s.ring.tapStop = nil
	}
}

// ringOpener is a machine the ring opens on without a press
// (ring.Machine.OpenAt): the machine of the hook in this process, which
// macOS has.
type ringOpener interface {
	OpenAt(pt ring.Point) bool
}

// ringPointerNow returns where the pointer is, in the hook's units (points on
// macOS, as MyGo's screen coordinates); replaced in tests.
var ringPointerNow = func() ring.Point {
	p := mygo.Screen.CursorScreenPoint()
	return ring.Point{X: float64(p.X), Y: float64(p.Y)}
}

// ringTapped opens the ring at the pointer for a three-finger tap on a
// trackpad: through the machine, as a held middle button does (Allowed
// checks the exceptions and full screen against the frontmost app, Open
// shows it with openRing), and it stays open as after the button was let
// go without moving. Main thread only.
func (s *Shell) ringTapped() {
	m, ok := s.ring.machine.(ringOpener)
	if !ok || s.ring.tapStop == nil {
		return // turned off meanwhile
	}
	m.OpenAt(ringPointerNow())
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

// ringFlyoutOptions are the options of the ring's window where it is a
// flyout (ringUsesFlyout): a flyout of the tray icon, which has no parent
// and floats above other apps' windows, is transparent where its content
// is (on Windows it has no redirection bitmap: what the content leaves
// clear shows the desktop), never takes the keyboard, and has no shadow,
// so that Windows 11 neither rounds nor outlines it. MyGo places it next
// to the tray icon; placeRing moves it to the pointer.
func ringFlyoutOptions(tray *mygo.Tray, content mygo.Content) mygo.FlyoutOptions {
	return mygo.FlyoutOptions{
		Tray:      tray,
		Width:     ui.RingWidth,
		Height:    ui.RingHeight,
		Focusable: false,
		Shadow:    false,
		Popover:   false,
		Hidden:    true,
		Content:   content,
	}
}

// ringPanelOptions are the options of the ring's window elsewhere: a panel
// without frame, background or shadow that never takes the focus.
func ringPanelOptions(title string, content mygo.Content) mygo.WindowOptions {
	return mygo.WindowOptions{
		Title:         title,
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
		Content:       content,
	}
}

// ringWindow returns the ring's window, created the first time: a flyout
// of the tray icon on Windows, a panel elsewhere (ringUsesFlyout), and a
// panel too where the tray icon could not be created.
func (s *Shell) ringWindow() *mygo.Window {
	if s.ring.win != nil {
		return s.ring.win
	}
	content := mui.View(s.ring.view.View)
	var w *mygo.Window
	if ringUsesFlyout && s.tray != nil {
		w = mygo.NewFlyout(ringFlyoutOptions(s.tray, content))
	} else {
		if ringUsesFlyout {
			s.log.Warn("action ring: no tray icon; the ring's window is a panel")
		}
		w = mygo.NewWindow(ringPanelOptions(s.loc.T("settings.nav.ring"), content))
	}
	noActivate(w.NativeHandle())
	// The user closing it (a flyout or a panel closes on an Escape its
	// content does not take) closes the ring as anything else does.
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
	// The selected text is the focused window's: read only when the
	// pointer is on it.
	r.cap = ringCapture{fg: fg, selHere: fg != 0 && (target == mousehook.RootOwner(fg) || runtime.GOOS == "darwin")}
	// The item under the pointer (R120), read before the ring's window
	// covers it: only in a folder view, within wincontext.PointerBudget.
	var ptr *wincontext.Pointer
	if cfg.ContextAware && cfg.Ring.Context && target != 0 {
		ptr = ringPointer(target, pt)
	}

	// The fixed actions, as stored, until they are found again.
	quick := ring.Arrange(ring.FixedSlots(s.ctx, nil, cfg.Ring.Fixed, ui.RingOwn(s.loc)), nil)
	r.view.Show(quick)
	// Larger with a folder: room for its arc.
	width, height := ui.RingSize(quick)
	w := s.ringWindow()
	r.rect, r.scale, r.work = placeRing(w, pt, width, height)
	w.ShowInactive()
	if rect, scale, work := placeRing(w, pt, width, height); rect != r.rect {
		// Shown on a display of another scale: placed again in its pixels.
		r.rect, r.scale, r.work = rect, scale, work
	}
	r.center = r.rect.Center()
	// An arc fits in the part of the window on the screen.
	vis := r.rect.Intersect(r.work)
	r.view.SetBounds(ring.Rect{X: (vis.X - r.center.X) / r.scale, Y: (vis.Y - r.center.Y) / r.scale, W: vis.W / r.scale, H: vis.H / r.scale})
	w.Invalidate()
	s.registerRingKeys()

	// The selected text, through the launcher's path (selectionReader),
	// with the user's copy fallback as the launcher's show has it (R131):
	// the ring never takes the focus and the held button never reached
	// the app, so a simulated Ctrl+C goes to the window as it would without
	// the ring. Its actions then follow the text as they do where UI
	// Automation reads it.
	var sel func(context.Context) selCapture
	if cfg.Ring.Context && r.cap.selHere {
		sel = s.ringOpenSelection(fg)
	}
	go s.ringContent(gen, cfg, target, ptr, sel)
}

// ringPointer reads what is under the pointer at pt in window target, and
// ringDetect the context of target with it: the same detection as the
// launcher's (wincontext.Detect), given the pointer. Replaced in tests.
var (
	ringPointer = func(target uintptr, pt ring.Point) *wincontext.Pointer {
		return wincontext.PointerAt(target, pt.X, pt.Y)
	}
	ringDetect = wincontext.DetectAt
)

// ringOpenSelection starts capturing the text selected in window fg as the
// ring opens, through the launcher's path with its copy fallback, and
// returns how to wait for the capture (nil when ctx ends first): a copy
// runs off the main thread and ends there.
func (s *Shell) ringOpenSelection(fg uintptr) func(context.Context) selCapture {
	ch := make(chan selCapture, 1)
	s.sel.Capture(fg, true, func(c selCapture) { ch <- c })
	return func(ctx context.Context) selCapture {
		select {
		case c := <-ch:
			return c
		case <-ctx.Done():
			return nil
		}
	}
}

// ringContent reads the context and the selected text for the ring gen,
// finds its actions, and shows them if it still shows. Off the main thread.
func (s *Shell) ringContent(gen int, cfg config.Config, target uintptr, ptr *wincontext.Pointer, pending func(context.Context) selCapture) {
	ctx, cancel := context.WithTimeout(s.ctx, ringContextBudget)
	defer cancel()
	c, sel, copied := ringRead(ctx, cfg, target, ptr, pending)
	fixed := ring.FixedSlots(s.ctx, s.env.Engine, cfg.Ring.Fixed, ui.RingOwn(s.loc))
	layout := ring.Content(s.ctx, s.env.Engine, cfg.Ring, fixed, c, sel)
	s.post(func() {
		if s.ring.gen != gen || s.ring.machine == nil || !s.ring.machine.Open() {
			return
		}
		s.ring.cap.ctx, s.ring.cap.sel = c, sel
		// Copied already: a chosen action does not copy again.
		s.ring.cap.read = s.ring.cap.read || copied
		s.ring.view.SetLayout(layout)
		if s.ring.win != nil {
			s.ring.win.Invalidate()
		}
	})
}

// ringRead reads, side by side within ctx, the context of window target
// with what was under the pointer (ptr) and the selected text pending
// captures (it waits for the capture, copy included); nil for what is not
// known or turned off. copied reports that the capture used the clipboard.
func ringRead(ctx context.Context, cfg config.Config, target uintptr, ptr *wincontext.Pointer, pending func(context.Context) selCapture) (c *plugin.Context, sel *plugin.Selection, copied bool) {
	if !cfg.ContextAware || !cfg.Ring.Context {
		return nil, nil, false
	}
	var wg sync.WaitGroup
	if target != 0 {
		wg.Go(func() {
			if v, ok := ringDetect(ctx, target, ptr); ok {
				c = &v
			}
		})
	}
	if pending != nil {
		wg.Go(func() {
			got := pending(ctx)
			if got == nil || got.Err() != nil {
				return
			}
			copied = got.NeedsCopy()
			if v, ok := got.Read(ctx); ok {
				sel = &v
			}
		})
	}
	wg.Wait()
	return c, sel, copied
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
		if s.ring.view.Track(s.ringLocal(*p)) {
			s.ring.win.Invalidate()
		}
	})
}

// ringLocal returns pt, in the hook's units, in DIPs from the ring's
// centre.
func (s *Shell) ringLocal(pt ring.Point) ring.Point {
	r := &s.ring
	return ring.Point{X: (pt.X - r.center.X) / r.scale, Y: (pt.Y - r.center.Y) / r.scale}
}

// ringReleased handles the button let go while it held the ring open:
// without moving, the ring stays; after a drag, the slot (or the open
// arc's action) in its direction runs; on a folder, the ring stays with
// its arc; in the middle, the ring closes. Main thread only.
func (s *Shell) ringReleased(pt ring.Point, moved bool) {
	if s.ring.win == nil || !s.ring.win.IsVisible() {
		return
	}
	if !moved {
		s.ring.view.EndDrag()
		s.ring.win.Invalidate()
		return
	}
	s.ring.view.Release(s.ringLocal(pt))
	if s.ring.win.IsVisible() {
		s.ring.win.Invalidate()
	}
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
		s.ring.view.Press(s.ringLocal(pt), ui.RingRadius+2*ui.RingDeadZone)
		if s.ring.win.IsVisible() {
			s.ring.win.Invalidate()
		}
	case !s.ring.rect.Contains(pt):
		s.closeRing()
	}
}

// chooseRing hides the ring, then runs slot i (j of its folder, j >= 0).
// Main thread only.
func (s *Shell) chooseRing(i, j int) {
	slot := s.ring.view.Layout()[i]
	if slot != nil && j >= 0 {
		if j >= len(slot.Folder) {
			return
		}
		slot = &slot.Folder[j]
	}
	if slot.IsFolder() {
		return // a folder opens its arc: the view does that
	}
	cap := s.ring.cap
	s.closeRing()
	if slot == nil {
		return
	}
	s.log.Info("action ring", "slot", i, "sub", j, "key", slot.Key)
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
		if err := mygo.GlobalShortcut.Register(k.acc, func() {
			if s.ring.view.Key(k.key) && s.ring.win != nil && s.ring.win.IsVisible() {
				s.ring.win.Invalidate() // an arc opened or closed
			}
		}); err != nil {
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

func (h ringHost) Choose(i int)      { h.s.chooseRing(i, -1) }
func (h ringHost) ChooseIn(i, j int) { h.s.chooseRing(i, j) }
func (h ringHost) Close()            { h.s.closeRing() }

// runRingSlot runs a slot of the ring in the window that was active.
// Main thread only.
func (s *Shell) runRingSlot(slot ring.Slot, cap ringCapture) {
	if cap.sel == nil && !cap.read && cap.selHere && ringNeedsSelection(slot) &&
		!(slot.Key == config.RingAskAI && len(ringFiles(cap.ctx)) > 0) {
		// Not read as the ring opened (UI Automation could not, and no copy
		// while the button is held): read now, as the launcher does.
		s.ringSelection(cap, func(cap ringCapture) { s.runRingSlot(slot, cap) })
		return
	}
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
		if e, ok := ringAskAIEntry(cap.sel, ringFiles(cap.ctx)); ok {
			s.runRingEntry(e, cap) // a new chat about the selected text or files
			return
		}
		// A blank chat: the launcher shows at it, without the root (and the
		// selected text) its own show would put over it.
		s.showLauncherFor(func() { s.launcher.OpenCommand(ai.ID, ai.CommandChat) })
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
		// A command, as its hotkey runs it; one with a view opens once the
		// launcher's show reset it, so that the root does not come over it.
		open := func() { s.launcher.OpenCommand(e.Plugin, name) }
		if len(e.Actions) == 0 || e.Actions[0].Do.Kind != plugin.ActionRunCommand {
			s.showLauncherThen(open)
			return
		}
		open()
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

// ringNeedsSelection reports whether slot acts on the selected text: Ask
// AI, and the entries of the selection's providers (selected-text items,
// AI actions).
func ringNeedsSelection(slot ring.Slot) bool {
	return slot.Key == config.RingAskAI || slot.Entry != nil && slot.Entry.Selection
}

// ringFiles are the files the ring acts on in a folder view (Explorer, the
// desktop, Finder): the one under the pointer, or the selected ones it was
// part of (R120).
func ringFiles(c *plugin.Context) []string {
	if c == nil {
		return nil
	}
	return c.Selected
}

// ringAskAIEntry returns the entry of Ask AI about the selected text sel
// and the files: a new chat with the text as a quote and the files attached
// (only the files: they wait in its draft for the question); false without
// either (a blank chat).
func ringAskAIEntry(sel *plugin.Selection, files []string) (core.Entry, bool) {
	if sel == nil && len(files) == 0 {
		return core.Entry{}, false
	}
	q := plugin.AskAI{Files: files}
	if sel != nil {
		q.Text = sel.Text
	}
	item := plugin.Item{ID: "ring-ask", Transient: true, Sensitive: true, Actions: []plugin.Action{{ID: "ask",
		Do: plugin.ActionDo{Kind: plugin.ActionAskAI, Payload: plugin.MustPayload(q)}}}}
	return core.Entry{Item: item, Plugin: ai.ID, Key: ai.ID + "/ring-ask", Context: true, Selection: sel != nil}, true
}

// ringSelectionWait is how often, and how many times, ringSelection looks
// whether the middle button went up.
const (
	ringSelectionPoll  = 20 * time.Millisecond
	ringSelectionPolls = 100
)

// ringSelection reads the selected text of the window the ring acted on
// once an action that needs it was chosen, with the user's copy fallback
// (selectionReader, the launcher's path), then runs then with it (cap.sel,
// nil for none; cap.read set). The ring is hidden by then and never had
// the focus, so the window still has it; a copy waits for the middle
// button to be up (a middle click chose). Main thread only.
func (s *Shell) ringSelection(cap ringCapture, then func(ringCapture)) {
	s.ringSelectionAfter(cap, then, ringSelectionPolls)
}

func (s *Shell) ringSelectionAfter(cap ringCapture, then func(ringCapture), polls int) {
	if m := s.ring.machine; m != nil && m.ButtonDown() && polls > 0 {
		time.AfterFunc(ringSelectionPoll, func() { s.sel.post(func() { s.ringSelectionAfter(cap, then, polls-1) }) })
		return
	}
	cap.read = true
	s.sel.Read(s.ctx, cap.fg, true, time.Second, func(sel *plugin.Selection) {
		cap.sel = sel
		then(cap)
	})
}
