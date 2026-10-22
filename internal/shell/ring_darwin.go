//go:build darwin

package shell

import (
	"os/exec"

	"github.com/egoist/mygo"

	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/internal/ui"
)

// noActivate does nothing on macOS: the ring is a panel, which shows
// without activating golauncher (Window.ShowInactive), so the app the user
// works in stays active.
func noActivate(uintptr) {}

// placeRing puts the ring's window centred on pt (points, as the event tap
// reports it, from the top left of the main display, as MyGo's screen
// coordinates are) inside the work area of the display there. Points are
// the hook's units: one per DIP.
func placeRing(w *mygo.Window, pt ring.Point) (ring.Rect, float64) {
	d := mygo.Screen.DisplayNearestPoint(mygo.Point{X: int(pt.X), Y: int(pt.Y)})
	wa := d.WorkArea
	r := ring.Place(pt, ui.RingWidth, ui.RingHeight, ring.Rect{X: float64(wa.X), Y: float64(wa.Y), W: float64(wa.Width), H: float64(wa.Height)})
	w.SetBounds(mygo.Rectangle{X: int(r.X), Y: int(r.Y), Width: ui.RingWidth, Height: ui.RingHeight})
	r = ring.Rect{X: float64(int(r.X)), Y: float64(int(r.Y)), W: ui.RingWidth, H: ui.RingHeight}
	return r, 1
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
