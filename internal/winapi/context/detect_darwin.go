package wincontext

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/eziq1/golauncher/internal/darwin/ax"
	"github.com/eziq1/golauncher/internal/darwin/ns"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// On macOS the context is that of the app that was frontmost when the
// hotkey was pressed (Detect gets its process identifier, see
// clip.ForegroundWindow). The launcher's panel never activates golauncher,
// so that app stays frontmost and its windows stay in front while Detect
// runs. Finder is read with AppleScript (finderScript); with the
// Accessibility permission the focused window's title and document (a file
// or folder URL, which Finder, Terminal, Preview and most document apps
// set) are read as well.

// finderTimeout bounds the AppleScript; the first one may wait for the
// user's answer to macOS's Automation prompt, which then stays up.
const finderTimeout = 900 * time.Millisecond

// runScript runs AppleScript script with args and returns its output.
// Tests replace it.
var runScript = func(ctx context.Context, script string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", append([]string{"-e", script}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", scriptError(stderr.String())
	}
	return string(out), nil
}

// window describes the focused window of an app, read with Accessibility.
type window struct {
	title, document string
	// none: the app has no focused window (Finder: the desktop has the
	// focus).
	none bool
}

// focusedWindow reads the focused window of app pid; ok is false without
// the Accessibility permission or on errors. Tests replace it.
var focusedWindow = func(pid int) (w window, ok bool) {
	if !ax.Trusted() {
		return w, false
	}
	app, err := ax.Application(pid)
	if err != nil {
		return w, false
	}
	defer app.Release()
	win, err := app.Element("AXFocusedWindow")
	if err != nil {
		w.none = ax.Missing(err)
		return w, w.none
	}
	defer win.Release()
	w.title, _ = win.String("AXTitle")
	w.document, _ = win.URL("AXDocument")
	return w, true
}

// appOf describes the running app pid and returns its bundle identifier.
// Tests replace it.
var appOf = func(pid int) (plugin.AppInfo, string) {
	var a plugin.AppInfo
	var bundle string
	if ns.Load() != nil {
		return a, ""
	}
	ns.Pool(func() {
		app := ns.Send(ns.Class("NSRunningApplication"), "runningApplicationWithProcessIdentifier:", int32(pid))
		if app == 0 {
			return
		}
		exe := ns.Send(app, "executableURL")
		a = plugin.AppInfo{PID: pid, Name: ns.GoString(ns.Send(exe, "lastPathComponent")), Path: ns.GoString(ns.Send(exe, "path"))}
		if a.Name == "" {
			a.Name = ns.GoString(ns.Send(app, "localizedName"))
		}
		bundle = ns.GoString(ns.Send(app, "bundleIdentifier"))
	})
	return a, bundle
}

func init() { pointerProbe = finderPointer }

// Detect describes the context of the app with process identifier pid (0
// or this process: nothing).
func Detect(ctx context.Context, pid uintptr) (plugin.Context, bool) {
	return DetectAt(ctx, pid, nil)
}

// DetectAt is Detect with what was under the mouse pointer (PointerAt, for
// the Action Ring): in Finder the reported items follow the item under it
// (R120). A nil pointer is Detect.
func DetectAt(ctx context.Context, pid uintptr, ptr *Pointer) (plugin.Context, bool) {
	if pid == 0 || int(pid) == os.Getpid() {
		return plugin.Context{}, false
	}
	app, bundle := appOf(int(pid))
	if app.Name == "" {
		return plugin.Context{}, false
	}
	c := plugin.Context{App: app}
	w, axOK := focusedWindow(int(pid))
	c.Title = w.title
	if bundle == FinderBundleID {
		if detectFinder(ctx, &c, axOK && w.none, ptr.target()) {
			return c, true
		}
	}
	// A window that names its document: Finder without AppleScript,
	// Terminal (its working directory), Preview, TextEdit, Xcode, …
	if p, ok := fileURLPath(w.document); ok {
		if st, err := os.Stat(p); err == nil {
			c.Kind = plugin.ContextPathInTitle
			if bundle == FinderBundleID {
				c.Kind = plugin.ContextExplorer
			}
			if st.IsDir() {
				c.Folder = p
			} else {
				c.Folder = filepath.Dir(p)
				c.Extra = map[string]string{"file": p}
			}
		}
	}
	return c, true
}

// detectFinder fills in c from Finder's front window or the desktop, the
// items following h, the pointer's.
func detectFinder(ctx context.Context, c *plugin.Context, desktop bool, h hit) bool {
	ctx, cancel := context.WithTimeout(ctx, finderTimeout)
	defer cancel()
	arg := "window"
	if desktop {
		arg = "desktop"
	}
	out, err := runScript(ctx, finderScript, arg)
	if err != nil {
		if errors.Is(err, ErrNotAuthorized) {
			problem(err)
		}
		return false
	}
	kind, folder, selected, ok := finderResult(out)
	if !ok {
		return false
	}
	v := folderView{Folder: folder, Selected: selected}
	if name := h.lookup(); name != "" && folder != "" {
		v.Named, v.NamedOK = localNamed(folder, name)
	}
	c.Kind, c.Folder, c.Selected = kind, folder, pointerSelection(v, h)
	return true
}

// finderPointer reads what is under the screen point (x, y) when the
// frontmost app pid is Finder (finderHitAt), within budget.
func finderPointer(pid uintptr, x, y float64, budget time.Duration) (hit, bool) {
	if pid == 0 || int(pid) == os.Getpid() {
		return hit{}, false
	}
	if _, bundle := appOf(int(pid)); bundle != FinderBundleID {
		return hit{}, false
	}
	type result struct {
		h  hit
		ok bool
	}
	at := finderHitAt
	ch := make(chan result, 1) // a late answer is dropped
	go func() {
		h, ok := at(int(pid), x, y)
		ch <- result{h, ok}
	}()
	t := time.NewTimer(budget)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.h, r.ok
	case <-t.C:
		return hit{}, false
	}
}

// finderHitAt reads the Accessibility element at (x, y) and, when it is
// Finder's (process pid), walks up from it (finderHitOf). Tests replace
// it.
var finderHitAt = func(pid int, x, y float64) (hit, bool) {
	sys, err := ax.SystemWide()
	if err != nil {
		return hit{}, false
	}
	defer sys.Release()
	el, err := sys.ElementAt(x, y)
	if err != nil {
		return hit{}, false
	}
	if p, err := el.PID(); err != nil || p != pid {
		el.Release()
		return hit{}, false // another app's window is under the pointer
	}
	return finderHitOf(axNode{el}), true
}

// axNode is an Accessibility element walked by finderHitOf.
type axNode struct{ e ax.Element }

func (n axNode) str(attr string) string {
	s, _ := n.e.String(attr)
	return s
}

func (n axNode) role() string       { return n.str("AXRole") }
func (n axNode) subrole() string    { return n.str("AXSubrole") }
func (n axNode) identifier() string { return n.str("AXIdentifier") }
func (n axNode) filename() string   { return n.str("AXFilename") }
func (n axNode) release()           { n.e.Release() }

func (n axNode) url() string {
	s, _ := n.e.URL("AXURL")
	return s
}

func (n axNode) parent() finderNode {
	p, err := n.e.Element("AXParent")
	if err != nil || !p.Valid() {
		return nil
	}
	return axNode{p}
}
