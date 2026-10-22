package shell

import (
	"runtime"
	"testing"

	"github.com/egoist/mygo"

	"github.com/eziq1/golauncher/internal/ui"
)

// TestRingWindowOptions: on Windows the ring's window is a flyout of the
// tray icon, which MyGo creates without a redirection bitmap (what the
// ring leaves clear shows the desktop), not focusable and without the
// shadow that would make Windows 11 round and outline it; on macOS it
// stays a clear, borderless panel without shadow.
func TestRingWindowOptions(t *testing.T) {
	if want := runtime.GOOS == "windows"; ringUsesFlyout != want {
		t.Errorf("ringUsesFlyout = %v on %s", ringUsesFlyout, runtime.GOOS)
	}
	tray := &mygo.Tray{}
	f := ringFlyoutOptions(tray, nil)
	if f.Tray != tray || f.Parent != nil {
		t.Errorf("not a tray flyout: tray %p parent %p", f.Tray, f.Parent)
	}
	if f.Focusable || f.Shadow || f.Popover || !f.Hidden {
		t.Errorf("flyout options %+v", f)
	}
	if f.Width != ui.RingWidth || f.Height != ui.RingHeight {
		t.Errorf("flyout size %dx%d", f.Width, f.Height)
	}

	p := ringPanelOptions("Action Ring", nil)
	if !p.Panel || !p.Transparent || !p.Frameless || !p.DisableShadow || !p.Hidden || !p.AlwaysOnTop || !p.SkipTaskbar {
		t.Errorf("panel options %+v", p)
	}
	if p.Width != ui.RingWidth || p.Height != ui.RingHeight {
		t.Errorf("panel size %dx%d", p.Width, p.Height)
	}
}
