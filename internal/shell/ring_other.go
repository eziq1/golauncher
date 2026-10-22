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
func placeRing(w *mygo.Window, pt ring.Point, width, height int) (ring.Rect, float64, ring.Rect) {
	work := ring.Rect{X: -1e6, Y: -1e6, W: 2e6, H: 2e6}
	r := ring.PlaceAround(pt, ui.RingWidth, ui.RingHeight, float64(width), float64(height), work)
	w.SetBounds(mygo.Rectangle{X: int(r.X), Y: int(r.Y), Width: width, Height: height})
	return r, 1, work
}

func screenshot() error { return errors.ErrUnsupported }
