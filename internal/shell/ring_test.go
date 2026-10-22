package shell

import (
	"context"
	"runtime"
	"testing"

	"github.com/egoist/mygo"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/ui"
	wincontext "github.com/eziq1/golauncher/internal/winapi/context"
	"github.com/eziq1/golauncher/pkg/plugin"
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

// TestRingReadPassesPointer: the ring's context is read by the launcher's
// detection, given what was under the pointer (R120); with context
// awareness or the ring's context off, nothing is read.
func TestRingReadPassesPointer(t *testing.T) {
	old := ringDetect
	t.Cleanup(func() { ringDetect = old })
	var (
		calls     int
		gotTarget uintptr
		gotPtr    *wincontext.Pointer
	)
	ringDetect = func(_ context.Context, target uintptr, p *wincontext.Pointer) (plugin.Context, bool) {
		calls++
		gotTarget, gotPtr = target, p
		return plugin.Context{Kind: plugin.ContextExplorer, Folder: `C:\Users\someone\Downloads`, Selected: []string{`C:\Users\someone\Downloads\a.zip`}}, true
	}
	cfg := config.Default()
	cfg.ContextAware, cfg.Ring.Context = true, true
	ptr := &wincontext.Pointer{}
	c, sel := ringRead(t.Context(), cfg, 42, ptr, nil)
	if calls != 1 || gotTarget != 42 || gotPtr != ptr {
		t.Fatalf("detect called %d times with %d, %p; want once with 42, %p", calls, gotTarget, gotPtr, ptr)
	}
	if c == nil || c.Folder != `C:\Users\someone\Downloads` || len(c.Selected) != 1 || sel != nil {
		t.Errorf("context %+v, selection %v", c, sel)
	}

	ringRead(t.Context(), cfg, 0, ptr, nil)
	cfg.Ring.Context = false
	ringRead(t.Context(), cfg, 42, ptr, nil)
	cfg.Ring.Context, cfg.ContextAware = true, false
	ringRead(t.Context(), cfg, 42, ptr, nil)
	if calls != 1 {
		t.Errorf("detect called %d times; want no call without a window or with the context off", calls)
	}
}
