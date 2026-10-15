package shell

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procDwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")

const (
	dwmwaCaptionColor = 35         // DWMWA_CAPTION_COLOR, Windows 11
	dwmColorNone      = 0xFFFFFFFE // DWMWA_COLOR_NONE
)

// hideCaptionColor stops DWM from painting the title bar color into a
// frameless window. MyGo extends the frame over the whole client area so the
// Acrylic material shows; with "Show accent color on title bars" enabled,
// DWM then paints the accent color where the caption would be, a colored
// strip across the top of the panel. Must run on the window's thread.
func hideCaptionColor(hwnd uintptr) {
	color := uint32(dwmColorNone)
	// Fails harmlessly on Windows 10, which has no caption color attribute
	// (and does not paint the strip).
	procDwmSetWindowAttribute.Call(hwnd, dwmwaCaptionColor, uintptr(unsafe.Pointer(&color)), unsafe.Sizeof(color))
}
