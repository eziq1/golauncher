//go:build windows

package shell

import (
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"

	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/internal/ui"
	wshell "github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
)

var (
	ringUser32            = windows.NewLazySystemDLL("user32.dll")
	procGetWindowLongPtrW = ringUser32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW = ringUser32.NewProc("SetWindowLongPtrW")
	procSetWindowPos      = ringUser32.NewProc("SetWindowPos")
	procMonitorFromPoint  = ringUser32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW   = ringUser32.NewProc("GetMonitorInfoW")
	procGetDpiForMonitor  = windows.NewLazySystemDLL("shcore.dll").NewProc("GetDpiForMonitor")
)

const (
	gwlExStyle             = -20
	wsExNoActivate         = 0x08000000
	swpNoActivate          = 0x0010
	swpNoOwnerZOrder       = 0x0200
	hwndTopmost            = ^uintptr(0) // (HWND)-1
	monitorDefaultToNearst = 2
)

type monitorInfo struct {
	Size    uint32
	Monitor [4]int32
	Work    [4]int32
	Flags   uint32
}

// ringUsesFlyout makes the ring's window a flyout of the tray icon
// (ringFlyoutOptions): MyGo creates flyouts without a redirection bitmap,
// which a transparent panel keeps, so that what the ring leaves clear
// would show black, in a box Windows 11 rounds as it rounds tool windows.
const ringUsesFlyout = true

// noActivate keeps the ring's window from ever being activated, also by a
// click (WS_EX_NOACTIVATE): the window the user works in keeps the focus
// and its selection.
func noActivate(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	i := int32(gwlExStyle)
	idx := uintptr(i) // a negative index, sign-extended
	ex, _, _ := procGetWindowLongPtrW.Call(hwnd, idx)
	procSetWindowLongPtrW.Call(hwnd, idx, ex|wsExNoActivate)
}

// placeRing puts the ring's window centred on pt (physical pixels, as the
// hook reports it) on the monitor there, at that monitor's scale, inside
// its work area, and returns its rectangle in physical pixels and the
// pixels per DIP. It places the window itself: MyGo's coordinates follow
// the DPI of the monitor the window was on.
func placeRing(w *mygo.Window, pt ring.Point) (ring.Rect, float64) {
	hwnd := w.NativeHandle()
	x, y := int32(pt.X), int32(pt.Y)
	mon, _, _ := procMonitorFromPoint.Call(uintptr(uint32(x))|uintptr(uint32(y))<<32, monitorDefaultToNearst)
	scale := 1.0
	var dx, dy uint32
	if r, _, _ := procGetDpiForMonitor.Call(mon, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx != 0 {
		scale = float64(dx) / 96
	}
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	work := ring.Rect{X: float64(mi.Work[0]), Y: float64(mi.Work[1]), W: float64(mi.Work[2] - mi.Work[0]), H: float64(mi.Work[3] - mi.Work[1])}
	r := ring.Place(pt, float64(ui.RingWidth)*scale, float64(ui.RingHeight)*scale, work)
	r = ring.Rect{X: float64(int32(r.X)), Y: float64(int32(r.Y)), W: float64(int32(r.W + 0.5)), H: float64(int32(r.H + 0.5))}
	procSetWindowPos.Call(hwnd, hwndTopmost, uintptr(int32(r.X)), uintptr(int32(r.Y)), uintptr(int32(r.W)), uintptr(int32(r.H)),
		swpNoActivate|swpNoOwnerZOrder)
	return r, scale
}

// screenshot opens the system's snipping overlay (Snipping Tool's
// ms-screenclip: URI, the tool Win+Shift+S opens).
func screenshot() error { return wshell.Open("ms-screenclip:", plugin.OpenOptions{}) }
