//go:build !windows

package clip

import (
	"errors"

	"golauncher/pkg/plugin"
)

// Read returns the current clipboard content.
func Read() (plugin.ClipboardContent, error) {
	return plugin.ClipboardContent{}, errors.ErrUnsupported
}

// WriteText replaces the clipboard content with text.
func WriteText(s string) error { return errors.ErrUnsupported }

// WriteImage replaces the clipboard content with a PNG image.
func WriteImage(png []byte) error { return errors.ErrUnsupported }

// WriteFiles replaces the clipboard content with a list of files.
func WriteFiles(paths []string) error { return errors.ErrUnsupported }

// Watch calls fn after the clipboard changes.
func Watch(fn func()) (stop func(), err error) { return nil, errors.ErrUnsupported }

// ForegroundWindow returns the current foreground window, or 0.
func ForegroundWindow() uintptr { return 0 }

// PasteTo brings hwnd to the foreground and sends Ctrl+V to it.
func PasteTo(hwnd uintptr) error { return errors.ErrUnsupported }
