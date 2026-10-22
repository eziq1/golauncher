//go:build !windows && !darwin

package mousehook

import (
	"errors"

	"github.com/eziq1/golauncher/internal/ring"
)

// Supported reports whether the hook works here: no.
func Supported() bool { return false }

// Trusted reports true: there is nothing to allow.
func Trusted() bool { return true }

// DragThreshold is how far the pointer moves before a press counts as a
// drag.
func DragThreshold() float64 { return 6 }

// Start is not supported here.
func Start(Handler) (func(), error) { return nil, errors.ErrUnsupported }

// Replay does nothing here.
func Replay(down, up bool) {}

// WindowAt returns 0.
func WindowAt(ring.Point) uintptr { return 0 }

// AppAt returns no app.
func AppAt(ring.Point) App { return App{} }

// RootOwner returns h.
func RootOwner(h uintptr) uintptr { return h }
