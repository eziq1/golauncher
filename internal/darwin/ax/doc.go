// Package ax is a minimal wrapper of the macOS Accessibility API
// (AXUIElement) for reading other apps: the permission check and prompt,
// the focused element, the element at a point, string, boolean and URL
// attributes. It uses purego,
// no cgo (docs/macos-port.md §10). On other platforms only ErrNotTrusted,
// Trusted and RequestTrust exist, and report no permission.
//
// Every call that reads another app needs the Accessibility permission
// (System Settings › Privacy & Security › Accessibility) of the program
// that is responsible for this process: the .app, or the terminal it was
// started from.
package ax

import "errors"

// ErrNotTrusted means that this process lacks the Accessibility permission.
var ErrNotTrusted = errors.New("ax: golauncher needs the Accessibility permission (System Settings › Privacy & Security › Accessibility)")
