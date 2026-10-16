//go:build windows

package clip

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procEnumClipboardFormats        = user32.NewProc("EnumClipboardFormats")
	procPeekMessageW                = user32.NewProc("PeekMessageW")
	procMsgWaitForMultipleObjectsEx = user32.NewProc("MsgWaitForMultipleObjectsEx")
	procCreateWaitableTimerExW      = kernel32.NewProc("CreateWaitableTimerExW")
	procSetWaitableTimer            = kernel32.NewProc("SetWaitableTimer")
)

const (
	pmRemove      = 0x0001
	qsPostMessage = 0x0008
	qsSendMessage = 0x0040

	createWaitableTimerHighResolution = 0x00000002
	timerAllAccess                    = 0x1F0003
)

// winCopyAPI is copyAPI on the real clipboard and keyboard.
type winCopyAPI struct{}

func systemCopyAPI() (copyAPI, error) { return winCopyAPI{}, nil }

func (winCopyAPI) foreground(hwnd uintptr) bool { return isWindow(hwnd) && isForeground(hwnd) }

func (winCopyAPI) open() error {
	runtime.LockOSThread()
	if err := openClipboard(); err != nil {
		runtime.UnlockOSThread()
		return err
	}
	return nil
}

func (winCopyAPI) close() {
	procCloseClipboard.Call()
	runtime.UnlockOSThread()
}

func (winCopyAPI) enumFormats() []uint32 {
	var out []uint32
	for f := uintptr(0); ; {
		r, _, _ := procEnumClipboardFormats.Call(f)
		if r == 0 {
			return out
		}
		f = r
		out = append(out, uint32(f))
	}
}

func (winCopyAPI) get(f uint32, limit int) ([]byte, error) { return getDataLimit(uintptr(f), limit) }

func (winCopyAPI) empty() error {
	if r, _, err := procEmptyClipboard.Call(); r == 0 {
		return fmt.Errorf("clip: EmptyClipboard: %w", err)
	}
	return nil
}

func (winCopyAPI) set(f uint32, data []byte) error { return setData(uintptr(f), data) }

func (winCopyAPI) seq() uint32 { return sequenceNumber() }

func (winCopyAPI) markers() []uint32 {
	f := formats()
	return []uint32{uint32(f.exclude), uint32(f.history), uint32(f.cloud)}
}

func (winCopyAPI) keyHeld(vk uint16) bool { return keyHeld(vk) }

func (winCopyAPI) send(keys []keyEvent) (int, error) { return sendKeys(keys) }

func (winCopyAPI) now() time.Time { return time.Now() }

func (winCopyAPI) sleep(d time.Duration) { time.Sleep(d) }

// listenerClass is the window class of the temporary listener windows. Its
// window procedure is DefWindowProc: unlike the watcher class it must not
// post WM_QUIT when the window is destroyed, as the thread goes back to
// Go's pool afterwards.
var listenerClass = sync.OnceValues(func() (windowClass, error) {
	inst, _, _ := procGetModuleHandleW.Call(0)
	name, _ := windows.UTF16PtrFromString("golauncher.clip.copy")
	wc := wndClassEx{
		WndProc:   procDefWindowProcW.Addr(),
		Instance:  inst,
		ClassName: name,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		if errno, ok := err.(windows.Errno); !ok || errno != errClassUsed {
			return windowClass{}, fmt.Errorf("clip: RegisterClassEx: %w", err)
		}
	}
	return windowClass{name: name, instance: inst}, nil
})

// winListener is a message-only window on the clipboard format listener
// list. Its messages are pumped by wait, on the thread that created it.
type winListener struct {
	hwnd  uintptr
	timer windows.Handle // high-resolution timer for precise waits, or 0
}

func (winCopyAPI) listen() (listener, error) {
	cls, err := listenerClass()
	if err != nil {
		return nil, err
	}
	runtime.LockOSThread()
	h, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls.name)), 0, 0,
		0, 0, 0, 0, hwndMessage, 0, cls.instance, 0)
	if h == 0 {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("clip: CreateWindowEx: %w", err)
	}
	if r, _, err := procAddClipboardFormatListener.Call(h); r == 0 {
		procDestroyWindow.Call(h)
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("clip: AddClipboardFormatListener: %w", err)
	}
	// Without a high-resolution timer waits are rounded up to the system
	// tick (about 16ms), which would stretch the 20ms settle time.
	t, _, _ := procCreateWaitableTimerExW.Call(0, 0, createWaitableTimerHighResolution, timerAllAccess)
	return &winListener{hwnd: h, timer: windows.Handle(t)}, nil
}

func (l *winListener) wait(d time.Duration) bool {
	deadline := time.Now().Add(d)
	var m msg
	for {
		got := false
		for {
			r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), l.hwnd, 0, 0, pmRemove)
			if r == 0 {
				break
			}
			if m.Message == wmClipboardUpdate {
				got = true
			} else {
				procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
			}
		}
		if got {
			return true
		}
		rem := time.Until(deadline)
		if rem <= 0 {
			return false
		}
		ms := uint32((rem + time.Millisecond - 1) / time.Millisecond)
		if l.timer != 0 {
			due := -int64(rem / 100) // relative, in 100ns units
			if r, _, _ := procSetWaitableTimer.Call(uintptr(l.timer), uintptr(unsafe.Pointer(&due)), 0, 0, 0, 0); r != 0 {
				h := l.timer
				procMsgWaitForMultipleObjectsEx.Call(1, uintptr(unsafe.Pointer(&h)), uintptr(ms+16), qsPostMessage|qsSendMessage, 0)
				continue
			}
		}
		procMsgWaitForMultipleObjectsEx.Call(0, 0, uintptr(ms), qsPostMessage|qsSendMessage, 0)
	}
}

func (l *winListener) close() {
	procRemoveClipboardFormatListener.Call(l.hwnd)
	procDestroyWindow.Call(l.hwnd)
	if l.timer != 0 {
		windows.CloseHandle(l.timer)
	}
	runtime.UnlockOSThread()
}
