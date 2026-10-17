//go:build !windows

package shell

// hideCaptionColor does nothing: only Windows paints a caption color into
// a frameless window (on macOS the title bar is hidden by MyGo).
func hideCaptionColor(uintptr) {}
