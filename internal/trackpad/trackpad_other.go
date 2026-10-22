//go:build !darwin

package trackpad

import "errors"

// Supported reports whether trackpad taps are watched here: only on macOS.
func Supported() bool { return false }

// Start fails: only macOS has the touches of its trackpads.
func Start(onTap func()) (stop func(), err error) {
	return nil, errors.New("trackpad: not supported")
}
