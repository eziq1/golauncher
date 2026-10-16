package winselection

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The helper process of the real tests: a top-level window of its own with
// an EDIT, a RICHEDIT50W and a password EDIT holding fixed made-up text. The
// test drives it through stdin, one command per line:
//
//	sel <control> <start> <end>   select a range, focus the control and take the foreground
//	fg                            take the foreground again
//	quit
//
// and it answers each with one line on stdout ("ok <foreground>" or "err …").
// It never touches any window but its own.

const helperEnv = "GOLAUNCHER_SELECTION_HELPER"

// Fixed fake contents of the helper's controls.
const (
	helperEditText     = "The quick brown fox jumps over the lazy dog"
	helperRichText     = "Lorem ipsum 你好世界 😀 dolor sit amet"
	helperPasswordText = "not-a-real-password"
)

// Controls of the helper window.
var helperControls = []string{"edit", "rich", "password"}

var (
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procSetFocus            = user32.NewProc("SetFocus")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procBringWindowToTop    = user32.NewProc("BringWindowToTop")
	procShowWindow          = user32.NewProc("ShowWindow")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procAllowSetForeground  = user32.NewProc("AllowSetForegroundWindow")
)

const (
	wmDestroy = 0x0002
	wmApp     = 0x8000
	emSetSel  = 0x00B1

	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000
	wsChild            = 0x40000000
	wsBorder           = 0x00800000
	wsTabStop          = 0x00010000
	esAutoHScroll      = 0x0080
	esPassword         = 0x0020
	esNoHideSel        = 0x0100

	swMinimize = 6
	swRestore  = 9
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
	private uint32
}

func utf16Ptr(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

// helper state, owned by the helper's window thread.
var helper struct {
	top      uintptr
	controls map[string]uintptr
	cmds     chan string
}

// runHelper is the helper process's main.
func runHelper() {
	runtime.LockOSThread()
	windows.NewLazySystemDLL("Msftedit.dll").Load() // registers RICHEDIT50W
	inst, _, _ := procGetModuleHandleW.Call(0)
	wc := wndClassEx{
		wndProc:    syscall.NewCallback(helperProc),
		instance:   inst,
		background: 5 + 1, // COLOR_WINDOW+1
		className:  utf16Ptr("GolauncherSelectionTestWindow"),
	}
	wc.size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		fmt.Println("err register", err)
		return
	}
	top, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(wc.className)), uintptr(unsafe.Pointer(utf16Ptr("golauncher selection test"))),
		wsOverlappedWindow|wsVisible, 120, 120, 640, 260, 0, 0, inst, 0)
	if top == 0 {
		fmt.Println("err window", err)
		return
	}
	helper.top = top
	helper.controls = map[string]uintptr{}
	child := func(name, class, text string, style uintptr, y int) {
		h, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(utf16Ptr(class))), uintptr(unsafe.Pointer(utf16Ptr(text))),
			wsChild|wsVisible|wsBorder|wsTabStop|esAutoHScroll|esNoHideSel|style, 10, uintptr(y), 600, 30, top, uintptr(len(helper.controls)+1), inst, 0)
		if h == 0 {
			fmt.Println("err control", name, err)
			os.Exit(1)
		}
		helper.controls[name] = h
	}
	child("edit", "EDIT", helperEditText, 0, 10)
	child("rich", "RICHEDIT50W", helperRichText, 0, 60)
	child("password", "EDIT", helperPasswordText, esPassword, 110)

	helper.cmds = make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			helper.cmds <- sc.Text()
			procPostMessageW.Call(top, wmApp, 0, 0)
		}
		helper.cmds <- "quit"
		procPostMessageW.Call(top, wmApp, 0, 0)
	}()
	takeForeground()
	fmt.Printf("ready %d\n", top)
	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// takeForeground brings the helper's own window to the foreground. A newly
// started process may do so; minimizing and restoring its own window is the
// fallback when Windows refuses.
func takeForeground() bool {
	top := helper.top
	procBringWindowToTop.Call(top)
	procSetForegroundWindow.Call(top)
	if fg, _, _ := procGetForegroundWindow.Call(); fg == top {
		return true
	}
	procShowWindow.Call(top, swMinimize)
	procShowWindow.Call(top, swRestore)
	procSetForegroundWindow.Call(top)
	fg, _, _ := procGetForegroundWindow.Call()
	return fg == top
}

func helperProc(hwnd, m, wparam, lparam uintptr) uintptr {
	switch m {
	case wmApp:
		for {
			select {
			case c := <-helper.cmds:
				helperCommand(c)
			default:
				return 0
			}
		}
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, m, wparam, lparam)
	return r
}

func helperCommand(c string) {
	f := strings.Fields(c)
	if len(f) == 0 {
		return
	}
	switch f[0] {
	case "sel":
		if len(f) != 4 {
			fmt.Println("err usage")
			return
		}
		ctl, ok := helper.controls[f[1]]
		start, err1 := strconv.Atoi(f[2])
		end, err2 := strconv.Atoi(f[3])
		if !ok || err1 != nil || err2 != nil {
			fmt.Println("err args")
			return
		}
		fg := takeForeground()
		procSetFocus.Call(ctl)
		procSendMessageW.Call(ctl, emSetSel, uintptr(start), uintptr(end))
		fmt.Println("ok", fg)
	case "fg":
		fmt.Println("ok", takeForeground())
	case "quit":
		procDestroyWindow.Call(helper.top)
	default:
		fmt.Println("err command")
	}
}
