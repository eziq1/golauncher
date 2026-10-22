//go:build darwin

package shell

import (
	"os/exec"

	"github.com/egoist/mygo"

	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/internal/ui"
)

// ringUsesFlyout keeps the ring's window a panel on macOS: it is clear
// there already (a transparent window is not opaque, its background clear),
// borderless, without shadow, and shows on whichever Space is current,
// full-screen ones included (canJoinAllSpaces), which MyGo's flyouts do
// not ask for.
const ringUsesFlyout = false

// noActivate does nothing on macOS: the ring is a panel, which shows
// without activating golauncher (Window.ShowInactive), so the app the user
// works in stays active.
func noActivate(uintptr) {}

// placeRing puts the ring's window centred on pt (points, as the event tap
// reports it, from the top left of the main display, as MyGo's screen
// coordinates are) with its ring inside the work area of the display
// there; a window width×height, larger than the ring for the arc of a
// folder, may stick out of it (ring.PlaceAround). Points are the hook's
// units: one per DIP. It returns the window's rectangle, the scale and the
// work area.
func placeRing(w *mygo.Window, pt ring.Point, width, height int) (ring.Rect, float64, ring.Rect) {
	d := mygo.Screen.DisplayNearestPoint(mygo.Point{X: int(pt.X), Y: int(pt.Y)})
	wa := d.WorkArea
	work := ring.Rect{X: float64(wa.X), Y: float64(wa.Y), W: float64(wa.Width), H: float64(wa.Height)}
	r := ring.PlaceAround(pt, ui.RingWidth, ui.RingHeight, float64(width), float64(height), work)
	w.SetBounds(mygo.Rectangle{X: int(r.X), Y: int(r.Y), Width: width, Height: height})
	r = ring.Rect{X: float64(int(r.X)), Y: float64(int(r.Y)), W: float64(width), H: float64(height)}
	return r, 1, work
}

// screenshot opens macOS's Screenshot app: the toolbar of ⌘⇧5, which
// captures with its own permission (screencapture run by golauncher would
// need Screen Recording); screencapture -i -c (a selection to the
// clipboard) if it cannot.
func screenshot() error {
	if err := exec.Command("/usr/bin/open", "-b", "com.apple.screenshot.launcher").Run(); err == nil {
		return nil
	}
	return exec.Command("/usr/sbin/screencapture", "-i", "-c").Start()
}
