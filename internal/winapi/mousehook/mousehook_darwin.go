//go:build darwin

package mousehook

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/eziq1/golauncher/internal/darwin/ax"
	"github.com/eziq1/golauncher/internal/darwin/ns"
	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/internal/winapi/clip"
)

// On macOS the hook is an active event tap at the head of the session
// (kCGSessionEventTap, kCGHeadInsertEventTap) on a thread of its own running
// a run loop. It needs the Accessibility permission: Start fails without
// it, and the caller checks Trusted first, so that no system prompt shows.
// Replays are copies of the button's event posted at the HID level, marked
// in kCGEventSourceUserData. A tap macOS turned off for being slow
// (kCGEventTapDisabledByTimeout) is turned on again.
//
// Events posted by other programs are not told apart here (macOS has no
// flag like Windows' LLMHF_INJECTED): only golauncher's own are.

// CGEvent types.
const (
	evLeftMouseDown     = 1
	evRightMouseDown    = 3
	evMouseMoved        = 5
	evLeftMouseDragged  = 6
	evRightMouseDragged = 7
	evOtherMouseDown    = 25
	evOtherMouseUp      = 26
	evOtherMouseDragged = 27
	evTapDisabledByTime = 0xFFFFFFFE
	evTapDisabledByUser = 0xFFFFFFFF

	fieldButtonNumber = 3  // kCGMouseEventButtonNumber
	fieldUserData     = 42 // kCGEventSourceUserData
	middleButton      = 2

	sessionTap = 1 // kCGSessionEventTap
	hidTap     = 0 // kCGHIDEventTap
	headInsert = 0 // kCGHeadInsertEventTap
	tapDefault = 0 // kCGEventTapOptionDefault: may swallow
)

type cgPoint struct{ X, Y float64 }

type cgFuncs struct {
	tapCreate      func(tap, place, options uint32, mask uint64, callback, refcon uintptr) uintptr
	tapEnable      func(tap uintptr, enable bool)
	sourceCreate   func(alloc, port uintptr, order int) uintptr
	runLoopCurrent func() uintptr
	addSource      func(rl, src, mode uintptr)
	runLoopRun     func()
	runLoopStop    func(rl uintptr)
	invalidate     func(port uintptr)
	release        func(ref uintptr)
	getField       func(ev uintptr, field uint32) int64
	setField       func(ev uintptr, field uint32, v int64)
	location       func(ev uintptr) cgPoint
	setLocation    func(ev uintptr, p cgPoint)
	setType        func(ev uintptr, typ uint32)
	createCopy     func(ev uintptr) uintptr
	create         func(source uintptr) uintptr
	post           func(tap uint32, ev uintptr)
	commonModes    uintptr // kCFRunLoopCommonModes
}

var cg = sync.OnceValues(func() (*cgFuncs, error) {
	lib, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices",
		purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return nil, fmt.Errorf("mousehook: %w", err)
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return nil, fmt.Errorf("mousehook: %w", err)
	}
	f := &cgFuncs{}
	purego.RegisterLibFunc(&f.tapCreate, lib, "CGEventTapCreate")
	purego.RegisterLibFunc(&f.tapEnable, lib, "CGEventTapEnable")
	purego.RegisterLibFunc(&f.getField, lib, "CGEventGetIntegerValueField")
	purego.RegisterLibFunc(&f.setField, lib, "CGEventSetIntegerValueField")
	purego.RegisterLibFunc(&f.location, lib, "CGEventGetLocation")
	purego.RegisterLibFunc(&f.setLocation, lib, "CGEventSetLocation")
	purego.RegisterLibFunc(&f.setType, lib, "CGEventSetType")
	purego.RegisterLibFunc(&f.createCopy, lib, "CGEventCreateCopy")
	purego.RegisterLibFunc(&f.create, lib, "CGEventCreate")
	purego.RegisterLibFunc(&f.post, lib, "CGEventPost")
	purego.RegisterLibFunc(&f.sourceCreate, cf, "CFMachPortCreateRunLoopSource")
	purego.RegisterLibFunc(&f.runLoopCurrent, cf, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&f.addSource, cf, "CFRunLoopAddSource")
	purego.RegisterLibFunc(&f.runLoopRun, cf, "CFRunLoopRun")
	purego.RegisterLibFunc(&f.runLoopStop, cf, "CFRunLoopStop")
	purego.RegisterLibFunc(&f.invalidate, cf, "CFMachPortInvalidate")
	purego.RegisterLibFunc(&f.release, cf, "CFRelease")
	sym, err := purego.Dlsym(cf, "kCFRunLoopCommonModes")
	if err != nil {
		return nil, fmt.Errorf("mousehook: %w", err)
	}
	f.commonModes = **(**uintptr)(unsafe.Pointer(&sym))
	return f, nil
})

// Supported reports whether the hook works here: yes.
func Supported() bool { return true }

// Trusted reports whether golauncher has the Accessibility permission the
// event tap needs. It shows no prompt.
func Trusted() bool { return ax.Trusted() }

// DragThreshold is how far the pointer moves before a press counts as a
// drag, in points.
func DragThreshold() float64 { return 6 }

var (
	startMu  sync.Mutex
	running  atomic.Pointer[Handler]
	tapPort  atomic.Uintptr
	lastDown atomic.Uintptr // a copy of the last middle button down, for replays
	callback = sync.OnceValue(func() uintptr { return purego.NewCallback(tapProc) })
)

// tapProc is the event tap's callback: it returns the event to let it
// pass, 0 to swallow it.
func tapProc(_, typ, event, _ uintptr) uintptr {
	f, err := cg()
	if err != nil {
		return event
	}
	typ &= 0xFFFFFFFF // a CGEventType (uint32): the register's upper half is not defined
	switch typ {
	case evTapDisabledByTime, evTapDisabledByUser:
		if p := tapPort.Load(); p != 0 {
			f.tapEnable(p, true)
		}
		return event
	}
	hp := running.Load()
	if hp == nil {
		return event
	}
	h := *hp
	var kind ring.EventKind
	switch typ {
	case evOtherMouseDown, evOtherMouseUp:
		if f.getField(event, fieldButtonNumber) != middleButton {
			if typ == evOtherMouseDown && h.WantsButtons() {
				kind = ring.OtherDown
				break
			}
			return event
		}
		kind = ring.MiddleUp
		if typ == evOtherMouseDown {
			kind = ring.MiddleDown
		}
	case evMouseMoved, evLeftMouseDragged, evRightMouseDragged, evOtherMouseDragged:
		if !h.WantsMoves() {
			return event
		}
		kind = ring.Move
	case evLeftMouseDown, evRightMouseDown:
		if !h.WantsButtons() {
			return event
		}
		kind = ring.OtherDown
	default:
		return event
	}
	own := f.getField(event, fieldUserData) == marker
	if kind == ring.MiddleDown && !own {
		if c := f.createCopy(event); c != 0 {
			if old := lastDown.Swap(c); old != 0 {
				f.release(old)
			}
		}
	}
	p := f.location(event)
	if h.Handle(ring.Event{Kind: kind, Pt: ring.Point{X: p.X, Y: p.Y}, Own: own}) {
		return 0
	}
	return event
}

// Start installs the event tap, which reports to h until stop is called.
// It fails without the Accessibility permission.
func Start(h Handler) (stop func(), err error) {
	startMu.Lock()
	defer startMu.Unlock()
	if running.Load() != nil {
		return nil, errors.New("mousehook: already running")
	}
	if !ax.Trusted() {
		return nil, ax.ErrNotTrusted
	}
	f, err := cg()
	if err != nil {
		return nil, err
	}
	cb := callback()
	ready := make(chan error, 1)
	done := make(chan struct{})
	var rl uintptr
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		const mask = 1<<evLeftMouseDown | 1<<evRightMouseDown | 1<<evMouseMoved | 1<<evLeftMouseDragged |
			1<<evRightMouseDragged | 1<<evOtherMouseDown | 1<<evOtherMouseUp | 1<<evOtherMouseDragged
		running.Store(&h)
		port := f.tapCreate(sessionTap, headInsert, tapDefault, mask, cb, 0)
		if port == 0 {
			running.Store(nil)
			ready <- errors.New("mousehook: CGEventTapCreate failed (Accessibility permission?)")
			return
		}
		tapPort.Store(port)
		src := f.sourceCreate(0, port, 0)
		rl = f.runLoopCurrent()
		f.addSource(rl, src, f.commonModes)
		f.tapEnable(port, true)
		ready <- nil
		f.runLoopRun()
		f.tapEnable(port, false)
		f.invalidate(port)
		f.release(src)
		f.release(port)
		tapPort.Store(0)
		running.Store(nil)
	}()
	if err := <-ready; err != nil {
		<-done
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			f.runLoopStop(rl)
			<-done
		})
	}, nil
}

// replays queues the events to post, so that the tap's callback never
// waits.
var replays = sync.OnceValue(func() chan [2]bool {
	ch := make(chan [2]bool, 16)
	go func() {
		for r := range ch {
			postMiddle(r[0], r[1])
		}
	}()
	return ch
})

// Replay gives the apps the middle button: down, up, or both in order, at
// the pointer's position. It returns at once.
func Replay(down, up bool) {
	select {
	case replays() <- [2]bool{down, up}:
	default:
	}
}

func postMiddle(down, up bool) {
	f, err := cg()
	if err != nil {
		return
	}
	base := lastDown.Load()
	if base == 0 {
		return
	}
	// Where the pointer is now: an event posted elsewhere would move it.
	var at cgPoint
	if cur := f.create(0); cur != 0 {
		at = f.location(cur)
		f.release(cur)
	}
	post := func(typ uint32) {
		ev := f.createCopy(base)
		if ev == 0 {
			return
		}
		f.setType(ev, typ)
		f.setLocation(ev, at)
		f.setField(ev, fieldUserData, marker)
		f.post(hidTap, ev)
		f.release(ev)
	}
	if down {
		post(evOtherMouseDown)
	}
	if up {
		post(evOtherMouseUp)
	}
}

// WindowAt returns the frontmost app's process identifier: macOS gives the
// context of apps, not of the window under the pointer.
func WindowAt(ring.Point) uintptr { return clip.ForegroundWindow() }

// AppAt returns the frontmost app: its executable, bundle identifier and
// name. Full-screen games are not told apart on macOS.
func AppAt(ring.Point) App {
	var a App
	if ns.Load() != nil {
		return a
	}
	ns.Pool(func() {
		app := ns.Send(ns.Send(ns.Class("NSWorkspace"), "sharedWorkspace"), "frontmostApplication")
		if app == 0 {
			return
		}
		exe := ns.Send(app, "executableURL")
		a.Name = ns.GoString(ns.Send(exe, "lastPathComponent"))
		a.Path = ns.GoString(ns.Send(exe, "path"))
		a.Bundle = ns.GoString(ns.Send(app, "bundleIdentifier"))
		a.Title = ns.GoString(ns.Send(app, "localizedName"))
	})
	return a
}

// RootOwner returns h: macOS gives the context of apps, not windows.
func RootOwner(h uintptr) uintptr { return h }
