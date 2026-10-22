//go:build windows

package mousehook

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/eziq1/golauncher/internal/ring"
)

// On Windows the hook is a low-level mouse hook on a thread of its own,
// pumping its messages. Its callback only reads the event, asks the
// Handler and returns: no other call happens there except, on a middle
// button going down, finding the app under the pointer (WindowFromPoint and
// a cached process name), which the Handler's Allowed does. Replays are
// sent with SendInput from another goroutine, marked with dwExtraInfo.
//
// Events another program injected (LLMHF_INJECTED without our marker) are
// left alone: a tool's or a remote session's middle click is never held
// back nor turned into the ring.
//
// UIPI: a window of a program running as administrator does not take
// input injected by golauncher running without; the hook may still see the
// button over it. AppAt reports such an app as Blocked and the ring leaves
// the button alone there.

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	shell32                      = windows.NewLazySystemDLL("shell32.dll")
	procSetWindowsHookExW        = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx      = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx           = user32.NewProc("CallNextHookEx")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procPeekMessageW             = user32.NewProc("PeekMessageW")
	procPostThreadMessageW       = user32.NewProc("PostThreadMessageW")
	procSendInput                = user32.NewProc("SendInput")
	procWindowFromPoint          = user32.NewProc("WindowFromPoint")
	procGetAncestor              = user32.NewProc("GetAncestor")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procSHQueryUserNotification  = shell32.NewProc("SHQueryUserNotificationState")
	procGetModuleHandleW         = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")
)

const (
	whMouseLL = 14
	hcAction  = 0
	wmQuit    = 0x0012

	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmRButtonDown = 0x0204
	wmMButtonDown = 0x0207
	wmMButtonUp   = 0x0208
	wmXButtonDown = 0x020B

	llmhfInjected        = 0x01
	llmhfLowerILInjected = 0x02

	inputMouse          = 0
	mouseeventfMiddleDn = 0x0020
	mouseeventfMiddleUp = 0x0040

	gaRootOwner = 3
	smCXDrag    = 68
	pmNoRemove  = 0x0000

	qunsRunningD3DFullScreen = 3
)

// msllhookstruct mirrors MSLLHOOKSTRUCT.
type msllhookstruct struct {
	X, Y      int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// mouseInput mirrors MOUSEINPUT; input an INPUT holding one.
type mouseInput struct {
	Dx, Dy    int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type input struct {
	Type uint32
	Mi   mouseInput
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      [2]int32
	_       uint32
}

// Supported reports whether the hook works here: yes.
func Supported() bool { return true }

// Trusted reports whether the hook may run: Windows needs no permission.
func Trusted() bool { return true }

// DragThreshold is how far the pointer moves before a press counts as a
// drag, in physical pixels: twice the system's drag distance.
func DragThreshold() float64 {
	n, _, _ := procGetSystemMetrics.Call(smCXDrag)
	return max(2*float64(int32(n)), 8)
}

var (
	running  atomic.Pointer[Handler]
	startMu  sync.Mutex
	callback = sync.OnceValue(func() uintptr { return windows.NewCallback(hookProc) })
)

// hookProc is the hook's callback.
func hookProc(code, wParam, lParam uintptr) uintptr {
	if int32(code) == hcAction {
		if hp := running.Load(); hp != nil && handle(*hp, wParam, lParam) {
			return 1 // kept from the apps
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, code, wParam, lParam)
	return r
}

// handle passes one event to h and reports whether it is swallowed.
func handle(h Handler, wParam, lParam uintptr) bool {
	var kind ring.EventKind
	switch wParam {
	case wmMButtonDown:
		kind = ring.MiddleDown
	case wmMButtonUp:
		kind = ring.MiddleUp
	case wmMouseMove:
		if !h.WantsMoves() {
			return false
		}
		kind = ring.Move
	case wmLButtonDown, wmRButtonDown, wmXButtonDown:
		if !h.WantsButtons() {
			return false
		}
		kind = ring.OtherDown
	default:
		return false
	}
	info := *(**msllhookstruct)(unsafe.Pointer(&lParam))
	own := info.ExtraInfo == marker
	return h.Handle(ring.Event{
		Kind:     kind,
		Pt:       ring.Point{X: float64(info.X), Y: float64(info.Y)},
		Own:      own,
		Injected: !own && info.Flags&(llmhfInjected|llmhfLowerILInjected) != 0,
	})
}

// Start installs the hook, which reports to h until stop is called. One
// hook runs at a time.
func Start(h Handler) (stop func(), err error) {
	startMu.Lock()
	defer startMu.Unlock()
	if running.Load() != nil {
		return nil, errors.New("mousehook: already running")
	}
	cb := callback()
	ready := make(chan error, 1)
	done := make(chan struct{})
	var tid uint32
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tid = windows.GetCurrentThreadId()
		// The thread's message queue, for PostThreadMessage.
		var m msg
		procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmNoRemove)
		running.Store(&h)
		mod, _, _ := procGetModuleHandleW.Call(0)
		hk, _, err := procSetWindowsHookExW.Call(whMouseLL, cb, mod, 0)
		if hk == 0 {
			running.Store(nil)
			ready <- fmt.Errorf("mousehook: SetWindowsHookEx: %w", err)
			return
		}
		ready <- nil
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
		}
		procUnhookWindowsHookEx.Call(hk)
		running.Store(nil)
	}()
	if err := <-ready; err != nil {
		<-done
		return nil, err
	}
	stopFull := startFullScreenWatch()
	var once sync.Once
	return func() {
		once.Do(func() {
			stopFull()
			procPostThreadMessageW.Call(uintptr(tid), wmQuit, 0, 0)
			<-done
		})
	}, nil
}

// replays queues the middle button events to inject, so that the hook's
// callback never waits for SendInput.
var replays = sync.OnceValue(func() chan [2]bool {
	ch := make(chan [2]bool, 16)
	go func() {
		for r := range ch {
			sendMiddle(r[0], r[1])
		}
	}()
	return ch
})

// Replay gives the apps the middle button: down, up, or both in order. It
// returns at once; the events are injected marked as golauncher's.
func Replay(down, up bool) {
	select {
	case replays() <- [2]bool{down, up}:
	default: // a burst nobody can make by hand: drop
	}
}

func sendMiddle(down, up bool) {
	var in []input
	if down {
		in = append(in, input{Type: inputMouse, Mi: mouseInput{Flags: mouseeventfMiddleDn, ExtraInfo: marker}})
	}
	if up {
		in = append(in, input{Type: inputMouse, Mi: mouseInput{Flags: mouseeventfMiddleUp, ExtraInfo: marker}})
	}
	if len(in) > 0 {
		procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(input{}))
	}
}

// WindowAt returns the top-level window at pt (the root owner of the
// window under the pointer), or 0.
func WindowAt(pt ring.Point) uintptr {
	h := windowFromPoint(pt)
	if h == 0 {
		return 0
	}
	r, _, _ := procGetAncestor.Call(h, gaRootOwner)
	if r == 0 {
		return h
	}
	return r
}

// RootOwner returns the root owner of window h (the window an owned dialog
// belongs to), as WindowAt does for the window under the pointer.
func RootOwner(h uintptr) uintptr {
	if h == 0 {
		return 0
	}
	if r, _, _ := procGetAncestor.Call(h, gaRootOwner); r != 0 {
		return r
	}
	return h
}

func windowFromPoint(pt ring.Point) uintptr {
	// POINT goes by value: in one register on amd64 and arm64.
	x, y := int32(pt.X), int32(pt.Y)
	h, _, _ := procWindowFromPoint.Call(uintptr(uint32(x)) | uintptr(uint32(y))<<32)
	return h
}

// AppAt returns the app whose window is at pt. It is called on the hook's
// thread when the middle button goes down: process names are cached.
func AppAt(pt ring.Point) App {
	var a App
	a.FullScreen = fullScreen.Load()
	w := WindowAt(pt)
	if w == 0 {
		return a
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(w, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return a
	}
	p := processInfo(pid)
	a.Name, a.Path, a.Blocked = p.name, p.path, p.blocked
	return a
}

type process struct {
	name, path string
	blocked    bool
	at         time.Time
}

var (
	procMu    sync.Mutex
	processes = map[uint32]process{}
	// elevated tells whether golauncher itself runs as administrator: then
	// nothing is blocked.
	elevated = sync.OnceValue(func() bool { return windows.GetCurrentProcessToken().IsElevated() })
)

// processTTL is how long a process's name is kept: a process identifier
// may be reused after the process ended.
const processTTL = 30 * time.Second

func processInfo(pid uint32) process {
	now := time.Now()
	procMu.Lock()
	p, ok := processes[pid]
	procMu.Unlock()
	if ok && now.Sub(p.at) < processTTL {
		return p
	}
	p = readProcess(pid)
	p.at = now
	procMu.Lock()
	if len(processes) > 256 {
		clear(processes)
	}
	processes[pid] = p
	procMu.Unlock()
	return p
}

func readProcess(pid uint32) process {
	if int(pid) == os.Getpid() {
		return process{name: "golauncher"}
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		// Protected or gone: the button is not given back reliably.
		return process{blocked: !elevated()}
	}
	defer windows.CloseHandle(h)
	var p process
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
		p.path = windows.UTF16ToString(buf[:n])
		p.name = p.path[lastSep(p.path)+1:]
	}
	if !elevated() {
		var tok windows.Token
		if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
			p.blocked = true // an administrator's process: its token is closed to us
		} else {
			p.blocked = tok.IsElevated()
			tok.Close()
		}
	}
	return p
}

func lastSep(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\\' || s[i] == '/' {
			return i
		}
	}
	return -1
}

// fullScreen is whether a full-screen exclusive (Direct3D) app has the
// screen, checked every few seconds while the hook runs: the hook's callback
// does not ask the shell.
var fullScreen atomic.Bool

func startFullScreenWatch() (stop func()) {
	done := make(chan struct{})
	check := func() {
		var state int32
		if r, _, _ := procSHQueryUserNotification.Call(uintptr(unsafe.Pointer(&state))); r == 0 {
			fullScreen.Store(state == qunsRunningD3DFullScreen)
		}
	}
	check()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				fullScreen.Store(false)
				return
			case <-t.C:
				check()
			}
		}
	}()
	return func() { close(done) }
}
