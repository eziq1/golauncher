//go:build darwin

package trackpad

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// On macOS the touches come from the private MultitouchSupport framework,
// as BetterTouchTool and MiddleClick read them: MTDeviceCreateList lists
// the multitouch devices (the built-in trackpad, a Magic Trackpad, a Magic
// Mouse), MTRegisterContactFrameCallback gets each device's frames and
// MTDeviceStart starts it. The framework calls back on a thread of its own
// for every frame (about 90 a second while fingers touch): frameProc only
// decodes the touches, feeds the device's Tap and, on a tap, calls onTap,
// which must not block. A device connected later (or one removed) is
// noticed through IOKit's matching notifications for AppleMultitouchDevice
// on a thread running a run loop, and the devices are listed and started
// again.
//
// The contact frame callback, as the same sources declare it (the older
// ones with int for the device and the count, the newer ones with
// MTDeviceRef and size_t):
//
//	void callback(MTDeviceRef device, MTTouch *touches, size_t numTouches,
//	              double timestamp, size_t frame);
//
// purego's callbacks take integer and pointer arguments, and the first
// three arrive in the first three integer registers on both amd64 and
// arm64 whatever follows (the double goes in a floating-point register):
// frameProc declares only those three and ignores the timestamp and the
// frame number. The count is read as 32 bits, which is right for int and
// for size_t alike. It returns 0, which the older int-returning
// declaration expects and the void one ignores.

const (
	multitouchPath = "/System/Library/PrivateFrameworks/MultitouchSupport.framework/MultitouchSupport"

	hidSystemState = 1 // kCGEventSourceStateHIDSystemState

	// CGEvent types of the buttons going down.
	evLeftMouseDown  = 1
	evRightMouseDown = 3
	evOtherMouseDown = 25

	// IOKit's matching notifications (kIOFirstMatchNotification,
	// kIOTerminatedNotification) and the class of multitouch devices.
	firstMatch      = "IOServiceFirstMatch"
	terminated      = "IOServiceTerminate"
	multitouchClass = "AppleMultitouchDevice"

	// rescanDelay lets a device connecting settle before the devices are
	// listed again.
	rescanDelay = 500 * time.Millisecond
)

type mtFuncs struct {
	// MultitouchSupport.
	createList func() uintptr // a CFMutableArrayRef of MTDeviceRef (Create rule)
	register   func(dev, cb uintptr)
	unregister func(dev, cb uintptr)
	start      func(dev uintptr, mode int32)
	stop       func(dev uintptr)

	// CoreFoundation.
	count          func(arr uintptr) int
	at             func(arr uintptr, i int) uintptr
	release        func(ref uintptr)
	runLoopCurrent func() uintptr
	addSource      func(rl, src, mode uintptr)
	runLoopRun     func()
	runLoopStop    func(rl uintptr)
	defaultMode    uintptr // kCFRunLoopDefaultMode

	// CoreGraphics.
	sinceEvent func(state int32, typ uint32) float64

	// IOKit.
	notifyCreate  func(mainPort uint32) uintptr
	notifySource  func(port uintptr) uintptr
	notifyDestroy func(port uintptr)
	matching      func(class string) uintptr
	addNotify     func(port uintptr, typ string, matching, cb, refcon uintptr, it *uint32) int32
	iterNext      func(it uint32) uint32
	objRelease    func(obj uint32) int32
}

// bind registers fptr as the function name of lib, or fails without
// panicking when it is not there (the framework is private: a macOS
// version may lack it).
func bind(lib uintptr, name string, fptr any) error {
	sym, err := purego.Dlsym(lib, name)
	if err != nil {
		return fmt.Errorf("trackpad: %s: %w", name, err)
	}
	purego.RegisterFunc(fptr, sym)
	return nil
}

var mt = sync.OnceValues(func() (*mtFuncs, error) {
	open := func(path string) (uintptr, error) {
		lib, err := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			return 0, fmt.Errorf("trackpad: %w", err)
		}
		return lib, nil
	}
	ms, err := open(multitouchPath)
	if err != nil {
		return nil, err
	}
	cf, err := open("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
	if err != nil {
		return nil, err
	}
	cg, err := open("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices")
	if err != nil {
		return nil, err
	}
	iokit, err := open("/System/Library/Frameworks/IOKit.framework/IOKit")
	if err != nil {
		return nil, err
	}
	f := &mtFuncs{}
	err = errors.Join(
		bind(ms, "MTDeviceCreateList", &f.createList),
		bind(ms, "MTRegisterContactFrameCallback", &f.register),
		bind(ms, "MTUnregisterContactFrameCallback", &f.unregister),
		bind(ms, "MTDeviceStart", &f.start),
		bind(ms, "MTDeviceStop", &f.stop),
		bind(cf, "CFArrayGetCount", &f.count),
		bind(cf, "CFArrayGetValueAtIndex", &f.at),
		bind(cf, "CFRelease", &f.release),
		bind(cf, "CFRunLoopGetCurrent", &f.runLoopCurrent),
		bind(cf, "CFRunLoopAddSource", &f.addSource),
		bind(cf, "CFRunLoopRun", &f.runLoopRun),
		bind(cf, "CFRunLoopStop", &f.runLoopStop),
		bind(cg, "CGEventSourceSecondsSinceLastEventType", &f.sinceEvent),
		bind(iokit, "IONotificationPortCreate", &f.notifyCreate),
		bind(iokit, "IONotificationPortGetRunLoopSource", &f.notifySource),
		bind(iokit, "IONotificationPortDestroy", &f.notifyDestroy),
		bind(iokit, "IOServiceMatching", &f.matching),
		bind(iokit, "IOServiceAddMatchingNotification", &f.addNotify),
		bind(iokit, "IOIteratorNext", &f.iterNext),
		bind(iokit, "IOObjectRelease", &f.objRelease),
	)
	if err != nil {
		return nil, err
	}
	sym, err := purego.Dlsym(cf, "kCFRunLoopDefaultMode")
	if err != nil {
		return nil, fmt.Errorf("trackpad: %w", err)
	}
	f.defaultMode = **(**uintptr)(unsafe.Pointer(&sym))
	return f, nil
})

// Supported reports whether trackpad taps can be watched: the framework
// and the functions used are there.
func Supported() bool {
	_, err := mt()
	return err == nil
}

var (
	startMu sync.Mutex
	current atomic.Pointer[watcher]
	epoch   = time.Now()
	// The callbacks, created once (purego's are never freed).
	frameCB  = sync.OnceValue(func() uintptr { return purego.NewCallback(frameProc) })
	deviceCB = sync.OnceValue(func() uintptr { return purego.NewCallback(deviceProc) })
	// debugFrame, when set, sees every frame (the real test logs them).
	debugFrame atomic.Pointer[func(dev uintptr, t time.Duration, touches []Touch)]
)

// watcher is the trackpads being watched.
type watcher struct {
	f     *mtFuncs
	onTap func()

	devMu   sync.Mutex // the devices; never held while a frame is fed
	list    uintptr    // the CFArray of the devices started, 0 for none
	devices []uintptr

	mu   sync.Mutex // the taps, fed on the framework's threads
	taps []deviceTap

	changed chan struct{} // a device came or went
	done    chan struct{} // closed by stop
	rl      uintptr       // the run loop of the IOKit notifications, 0 without
	rlDone  chan struct{} // closed when that thread ends
}

// deviceTap is the Tap of a device.
type deviceTap struct {
	dev uintptr
	tap *Tap
}

// Start watches the multitouch devices and calls onTap, on the
// framework's thread, for each three-finger tap; onTap must return at
// once (post the work elsewhere). It fails when the framework or one of
// its functions is missing. With no trackpad yet, it waits for one.
func Start(onTap func()) (stop func(), err error) {
	startMu.Lock()
	defer startMu.Unlock()
	if current.Load() != nil {
		return nil, errors.New("trackpad: already running")
	}
	f, err := mt()
	if err != nil {
		return nil, err
	}
	w := &watcher{f: f, onTap: onTap, changed: make(chan struct{}, 1), done: make(chan struct{})}
	current.Store(w)
	w.startDevices()
	w.watchDevices()
	go w.rescans()
	return sync.OnceFunc(func() {
		startMu.Lock()
		defer startMu.Unlock()
		close(w.done)
		if w.rl != 0 {
			f.runLoopStop(w.rl)
			<-w.rlDone
		}
		w.stopDevices()
		current.Store(nil)
	}), nil
}

// startDevices lists the multitouch devices and starts them.
func (w *watcher) startDevices() {
	w.devMu.Lock()
	defer w.devMu.Unlock()
	f, cb := w.f, frameCB()
	list := f.createList()
	if list == 0 {
		return
	}
	w.list = list
	for i := range f.count(list) {
		dev := f.at(list, i)
		if dev == 0 {
			continue
		}
		f.register(dev, cb)
		f.start(dev, 0)
		w.devices = append(w.devices, dev)
	}
}

// stopDevices stops the devices started and lets go of their list.
func (w *watcher) stopDevices() {
	w.devMu.Lock()
	defer w.devMu.Unlock()
	f, cb := w.f, frameCB()
	for _, dev := range w.devices {
		f.unregister(dev, cb)
		f.stop(dev)
	}
	w.devices = nil
	if w.list != 0 {
		f.release(w.list)
		w.list = 0
	}
	w.mu.Lock()
	w.taps = nil
	w.mu.Unlock()
}

// watchDevices asks IOKit to tell when a multitouch device comes or goes,
// on a thread of its own running a run loop. Without it, the devices
// listed at the start are all there is.
func (w *watcher) watchDevices() {
	f, cb := w.f, deviceCB()
	ready := make(chan uintptr, 1)
	w.rlDone = make(chan struct{})
	go func() {
		defer close(w.rlDone)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		port := f.notifyCreate(0) // kIOMainPortDefault
		if port == 0 {
			ready <- 0
			return
		}
		defer f.notifyDestroy(port)
		rl := f.runLoopCurrent()
		f.addSource(rl, f.notifySource(port), f.defaultMode)
		var its []uint32
		for _, typ := range []string{firstMatch, terminated} {
			var it uint32
			// IOServiceAddMatchingNotification consumes the dictionary.
			if f.addNotify(port, typ, f.matching(multitouchClass), cb, 0, &it) == 0 && it != 0 {
				drain(f, it) // the devices there now (and arms the notification)
				its = append(its, it)
			}
		}
		if len(its) == 0 {
			ready <- 0
			return
		}
		defer func() {
			for _, it := range its {
				f.objRelease(it)
			}
		}()
		ready <- rl
		f.runLoopRun()
	}()
	if w.rl = <-ready; w.rl == 0 {
		<-w.rlDone
	}
}

// drain releases the services of iterator it, which arms its
// notification again.
func drain(f *mtFuncs, it uint32) {
	for s := f.iterNext(it); s != 0; s = f.iterNext(it) {
		f.objRelease(s)
	}
}

// deviceProc is IOKit's matching callback (refcon, io_iterator_t): a
// multitouch device came or went. It runs on the notifications' thread.
func deviceProc(_, it uintptr) {
	w := current.Load()
	if w == nil {
		return
	}
	drain(w.f, uint32(it))
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

// rescans lists and starts the devices again after one came or went.
func (w *watcher) rescans() {
	for {
		select {
		case <-w.done:
			return
		case <-w.changed:
		}
		select {
		case <-w.done:
			return
		case <-time.After(rescanDelay):
		}
		select {
		case <-w.changed: // what came meanwhile is seen now
		default:
		}
		startMu.Lock()
		select {
		case <-w.done:
		default:
			w.stopDevices()
			w.startDevices()
		}
		startMu.Unlock()
	}
}

// frameProc is the contact frame callback (see the top of the file).
func frameProc(dev uintptr, data unsafe.Pointer, n uintptr) uintptr {
	w := current.Load()
	if w == nil {
		return 0
	}
	var buf [maxTouches]Touch
	k := 0
	if count := int(int32(n)); count > 0 && data != nil {
		k = decodeTouches(buf[:], unsafe.Slice((*byte)(data), min(count, maxTouches)*touchSize))
	}
	t := time.Since(epoch)
	w.mu.Lock()
	tap := w.tapOf(dev).Feed(t, buf[:k])
	w.mu.Unlock()
	if d := debugFrame.Load(); d != nil {
		(*d)(dev, t, buf[:k])
	}
	if tap {
		w.onTap()
	}
	return 0
}

// tapOf returns the Tap of device dev, made the first time. w.mu is held.
func (w *watcher) tapOf(dev uintptr) *Tap {
	for _, d := range w.taps {
		if d.dev == dev {
			return d.tap
		}
	}
	g := &Tap{Pressed: w.pressed}
	w.taps = append(w.taps, deviceTap{dev, g})
	return g
}

// pressed reports whether a mouse button (a trackpad's click included)
// went down within the last d, as the HID system saw it: no event tap is
// needed.
func (w *watcher) pressed(d time.Duration) bool {
	for _, typ := range [...]uint32{evLeftMouseDown, evRightMouseDown, evOtherMouseDown} {
		if s := w.f.sinceEvent(hidSystemState, typ); s >= 0 && s < d.Seconds() {
			return true
		}
	}
	return false
}
