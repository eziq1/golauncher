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

// Detect describes the context of the app with process identifier pid (0
// or this process: nothing).
func Detect(ctx context.Context, pid uintptr) (plugin.Context, bool) {
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
		if detectFinder(ctx, &c, axOK && w.none) {
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

// detectFinder fills in c from Finder's front window or the desktop.
func detectFinder(ctx context.Context, c *plugin.Context, desktop bool) bool {
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
	c.Kind, c.Folder, c.Selected = kind, folder, selected
	return true
}
