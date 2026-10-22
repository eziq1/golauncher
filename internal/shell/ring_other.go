//go:build !windows && !darwin

package shell

import (
	"errors"

	"github.com/egoist/mygo"

	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/internal/ui"
)

const ringUsesFlyout = false

func noActivate(uintptr) {}

// placeRing centres the ring's window on pt (the ring does not run here:
// mousehook is not supported).
func placeRing(w *mygo.Window, pt ring.Point) (ring.Rect, float64) {
	r := ring.Place(pt, ui.RingWidth, ui.RingHeight, ring.Rect{X: -1e6, Y: -1e6, W: 2e6, H: 2e6})
	w.SetBounds(mygo.Rectangle{X: int(r.X), Y: int(r.Y), Width: ui.RingWidth, Height: ui.RingHeight})
	return r, 1
}

func screenshot() error { return errors.ErrUnsupported }
