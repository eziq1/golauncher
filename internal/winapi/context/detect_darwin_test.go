package wincontext

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// fakeMac replaces the system lookups of Detect: app pid is bundle, its
// focused window is w (ax: Accessibility allowed) and the AppleScript
// answers out or err, recording its argument.
func fakeMac(t *testing.T, bundle string, w window, axOK bool, out string, err error) *[]string {
	t.Helper()
	oldApp, oldWin, oldRun := appOf, focusedWindow, runScript
	t.Cleanup(func() { appOf, focusedWindow, runScript = oldApp, oldWin, oldRun })
	appOf = func(pid int) (plugin.AppInfo, string) {
		return plugin.AppInfo{PID: pid, Name: "App", Path: "/Applications/App.app/Contents/MacOS/App"}, bundle
	}
	focusedWindow = func(int) (window, bool) { return w, axOK }
	var args []string
	runScript = func(_ context.Context, _ string, a ...string) (string, error) {
		args = append(args, a...)
		return out, err
	}
	return &args
}

func TestDetectFinder(t *testing.T) {
	args := fakeMac(t, FinderBundleID, window{title: "Documents"}, true, "window\n/Users/a/Documents/\n/Users/a/Documents/r.pdf\n", nil)
	c, ok := Detect(t.Context(), 4242)
	if !ok || c.Kind != plugin.ContextExplorer || c.Folder != "/Users/a/Documents" || !slices.Equal(c.Selected, []string{"/Users/a/Documents/r.pdf"}) ||
		c.Title != "Documents" || c.App.PID != 4242 || !slices.Equal(*args, []string{"window"}) {
		t.Errorf("context %+v, args %q", c, *args)
	}
}

func TestDetectFinderDesktopFocused(t *testing.T) {
	args := fakeMac(t, FinderBundleID, window{none: true}, true, "desktop\n/Users/a/Desktop/\n", nil)
	c, ok := Detect(t.Context(), 4242)
	if !ok || c.Kind != plugin.ContextDesktop || c.Folder != "/Users/a/Desktop" || !slices.Equal(*args, []string{"desktop"}) {
		t.Errorf("context %+v, args %q", c, *args)
	}
}

// Without the Automation permission the problem is reported, and the
// folder still comes from the window's document when Accessibility allows.
func TestDetectFinderNotAuthorized(t *testing.T) {
	dir := t.TempDir()
	fakeMac(t, FinderBundleID, window{document: "file://" + dir + "/"}, true, "", ErrNotAuthorized)
	var problems []error
	OnProblem(func(err error) { problems = append(problems, err) })
	defer OnProblem(nil)
	c, ok := Detect(t.Context(), 4242)
	if !ok || c.Kind != plugin.ContextExplorer || c.Folder != dir || len(c.Selected) != 0 {
		t.Errorf("context %+v", c)
	}
	if len(problems) != 1 || !errors.Is(problems[0], ErrNotAuthorized) {
		t.Errorf("problems %v", problems)
	}
}

// A special window (Recents, AirDrop) or a timed-out script: the app only.
func TestDetectFinderNothing(t *testing.T) {
	for _, tt := range []struct {
		out string
		err error
	}{{"window\n\n", nil}, {"", context.DeadlineExceeded}} {
		fakeMac(t, FinderBundleID, window{}, false, tt.out, tt.err)
		c, ok := Detect(t.Context(), 4242)
		if !ok || c.Kind != "" || c.Folder != "" || c.App.Name != "App" {
			t.Errorf("%q %v: context %+v", tt.out, tt.err, c)
		}
	}
}

func TestDetectDocument(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	args := fakeMac(t, "com.apple.TextEdit", window{title: "notes.txt", document: "file://" + file}, true, "", errors.New("not run"))
	c, ok := Detect(t.Context(), 4242)
	if !ok || c.Kind != plugin.ContextPathInTitle || c.Folder != dir || c.Extra["file"] != file || len(*args) != 0 {
		t.Errorf("context %+v, args %q", c, *args)
	}
	// A document that does not exist (anymore): the app and title only.
	fakeMac(t, "com.apple.TextEdit", window{title: "x", document: "file:///nope/x.txt"}, true, "", nil)
	if c, _ := Detect(t.Context(), 4242); c.Kind != "" || c.Title != "x" {
		t.Errorf("missing document: %+v", c)
	}
}

func TestDetectNoTarget(t *testing.T) {
	fakeMac(t, FinderBundleID, window{}, false, "window\n/x\n", nil)
	if _, ok := Detect(t.Context(), 0); ok {
		t.Error("pid 0 detected")
	}
	if _, ok := Detect(t.Context(), uintptr(os.Getpid())); ok {
		t.Error("own process detected")
	}
}

// TestDetectRealFinder asks the real Finder. It needs the Automation
// permission for Finder (macOS asks the first time); set
// GOLAUNCHER_FINDER_TESTS=1 to run it.
func TestDetectRealFinder(t *testing.T) {
	if os.Getenv("GOLAUNCHER_FINDER_TESTS") != "1" {
		t.Skip("controls Finder; set GOLAUNCHER_FINDER_TESTS=1")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second) // an unanswered Automation prompt
	defer cancel()
	out, err := runScript(ctx, finderScript, "window")
	if err != nil {
		t.Fatal(err)
	}
	kind, folder, sel, ok := finderResult(out)
	t.Logf("kind %q folder %q selected %q ok %v", kind, folder, sel, ok)
}
