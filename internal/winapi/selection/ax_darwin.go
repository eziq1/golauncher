package winselection

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/darwin/ax"
	"github.com/eziq1/golauncher/internal/darwin/ns"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// On macOS the selection is read through the Accessibility API, the
// counterpart of UI Automation: the focused element of the system (the app
// that was frontmost when the hotkey was pressed, since the launcher's
// panel never activates golauncher) and its AXSelectedText. The window
// handle of Begin is the app's process identifier (clip.ForegroundWindow),
// and its "class" the app's bundle identifier. The clipboard method posts
// ⌘C (clip.CopySelection). Both need the Accessibility permission; without
// it Begin reads nothing and Pending.Err reports ax.ErrNotTrusted.

func init() {
	windowFacts = appFacts
	processFacts = processOf
	focusProbe = axFocusProbe
	permission = func() error {
		if !ax.Trusted() {
			return ax.ErrNotTrusted
		}
		return nil
	}
}

// running describes the running app pid: its executable and bundle
// identifier; ok is false when there is none.
func running(pid int) (exe, path, bundle string, ok bool) {
	if pid <= 0 || ns.Load() != nil {
		return "", "", "", false
	}
	ns.Pool(func() {
		app := ns.Send(ns.Class("NSRunningApplication"), "runningApplicationWithProcessIdentifier:", int32(pid))
		if app == 0 {
			return
		}
		ok = true
		u := ns.Send(app, "executableURL")
		exe = ns.GoString(ns.Send(u, "lastPathComponent"))
		path = ns.GoString(ns.Send(u, "path"))
		bundle = ns.GoString(ns.Send(app, "bundleIdentifier"))
		if exe == "" {
			exe = ns.GoString(ns.Send(app, "localizedName"))
		}
	})
	return exe, path, bundle, ok
}

// appFacts is windowFacts: hwnd is the app's process identifier.
func appFacts(hwnd uintptr) windowInfo {
	_, path, bundle, ok := running(int(hwnd))
	return windowInfo{Valid: ok, PID: int(hwnd), Path: path, Class: bundle}
}

// processOf is processFacts.
func processOf(pid int) plugin.AppInfo {
	exe, path, _, _ := running(pid)
	return plugin.AppInfo{Name: exe, Path: path, PID: pid}
}

// enabled are the apps already asked to build their accessibility tree.
var (
	enabledMu sync.Mutex
	enabled   = map[int]bool{}
)

// enableTree asks the app of element app (Chromium, Electron) to build its
// accessibility tree, which they only do for assistive apps that ask
// (AXManualAccessibility), once per process. It reports whether it asked.
func enableTree(app ax.Element) bool {
	pid, err := app.PID()
	if err != nil {
		return false
	}
	enabledMu.Lock()
	done := enabled[pid]
	enabled[pid] = true
	enabledMu.Unlock()
	if done {
		return false
	}
	return app.SetBool("AXManualAccessibility", true) == nil
}

// axFocusProbe is focusProbe: the system's focused element, whether it is a
// password field and whether it has the AXSelectedText attribute.
func axFocusProbe() (focusInfo, error) {
	sys, err := ax.SystemWide()
	if err != nil {
		return focusInfo{}, err
	}
	defer sys.Release()
	el, err := sys.Element("AXFocusedUIElement")
	if err != nil {
		app, aerr := sys.Element("AXFocusedApplication")
		if aerr != nil {
			return focusInfo{}, err
		}
		asked := enableTree(app)
		app.Release()
		if !asked {
			return focusInfo{}, err
		}
		time.Sleep(chromiumRetry)
		if el, err = sys.Element("AXFocusedUIElement"); err != nil {
			return focusInfo{}, err
		}
	}
	fi := focusInfo{}
	fi.PID, _ = el.PID()
	role, _ := el.String("AXRole")
	sub, _ := el.String("AXSubrole")
	names, _ := el.Names()
	fi.Password, fi.TextPattern = axKind(role, sub, names)
	if fi.TextPattern {
		fi.elem = axElement{el}
	} else {
		el.Release()
	}
	return fi, nil
}

// axKind tells from an element's role, subrole and attribute names whether
// it is a password field and whether its selection can be read. A password
// field's selection is never read.
func axKind(role, subrole string, names []string) (password, readable bool) {
	password = role == "AXSecureTextField" || subrole == "AXSecureTextField"
	return password, !password && slices.Contains(names, "AXSelectedText")
}

// axElement is the focused element kept for Read.
type axElement struct{ el ax.Element }

func (e axElement) read(ctx context.Context, units int) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	text, err := e.el.String("AXSelectedText")
	if err != nil && !ax.Missing(err) {
		return nil, false, err
	}
	return []string{text}, e.el.Settable("AXValue"), nil
}

func (e axElement) release() { e.el.Release() }
