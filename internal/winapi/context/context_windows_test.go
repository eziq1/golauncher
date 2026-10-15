package wincontext

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// The real Explorer test opens a File Explorer window, so it only runs when
// asked for: GOLAUNCHER_CONTEXT_TESTS=1 go test ./internal/winapi/context.
const contextTestsEnv = "GOLAUNCHER_CONTEXT_TESTS"

var iidIWebBrowser2 = mustGUID("{D30C1661-CDAF-11D0-8A3E-00C04FC9E26E}")

const (
	vtWebBrowserQuit   = 32 // IWebBrowser2::Quit
	vtFolderViewSelect = 15 // IFolderView::SelectItem
	svsiSelect         = 0x1
	svsiDeselectOthers = 0x4
	svsiEnsureVisible  = 0x8
	svsiFocused        = 0x10
)

func longPath(p string) string {
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetLongPathName(u, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		return p
	}
	return windows.UTF16ToString(buf[:n])
}

// eachShellWindow calls fn with the dispatch object, the IShellBrowser and
// the window (a top-level window or a Windows 11 tab) of every shell
// window until fn returns true, and reports whether it did.
func eachShellWindow(fn func(disp, browser, w uintptr) bool) bool {
	found := false
	withCOM(func() {
		ws := shellWindows()
		if ws == 0 {
			return
		}
		defer release(ws)
		var unk uintptr
		if failed(comCall(ws, vtShellWindowsNewEnum, uintptr(unsafe.Pointer(&unk)))) || unk == 0 {
			return
		}
		enum := queryInterface(unk, &iidIEnumVARIANT)
		release(unk)
		if enum == 0 {
			return
		}
		defer release(enum)
		for !found {
			var v variant
			var fetched uint32
			if r := comCall(enum, vtEnumVariantNext, 1, uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&fetched))); r != 0 || fetched == 0 {
				return
			}
			if v.vt != vtDispatch || v.val == 0 {
				continue
			}
			func() {
				defer release(v.val)
				b := topLevelBrowser(v.val)
				if b == 0 {
					return
				}
				defer release(b)
				var w uintptr
				comCall(b, vtGetWindow, uintptr(unsafe.Pointer(&w)))
				found = fn(v.val, b, w)
			}()
		}
	})
	return found
}

// withExplorerOf finds the shell window (a window or a Windows 11 tab)
// showing dir and calls fn with its dispatch object, its IShellBrowser and
// its top-level window. Only that window is ever touched.
func withExplorerOf(t *testing.T, dir string, fn func(disp, browser, top uintptr)) bool {
	t.Helper()
	return eachShellWindow(func(disp, b, w uintptr) bool {
		folder, _, ok := browserView(b)
		if !ok || !strings.EqualFold(longPath(folder), dir) {
			return false
		}
		top, _, _ := procGetAncestor.Call(w, 2)
		fn(disp, b, top)
		return true
	})
}

// quit closes the shell window or tab of disp.
func quit(disp uintptr) {
	if wb := queryInterface(disp, &iidIWebBrowser2); wb != 0 {
		comCall(wb, vtWebBrowserQuit)
		release(wb)
	}
}

// TestExplorerActiveTabReal opens a folder, then a second tab in that same
// window (Ctrl+T's command, sent to the window's tab). The new tab (Home, a
// virtual folder) is active, so the context must not be the first tab's
// folder. Both tabs are closed again.
func TestExplorerActiveTabReal(t *testing.T) {
	if os.Getenv(contextTestsEnv) != "1" {
		t.Skipf("set %s=1 to open a real File Explorer window", contextTestsEnv)
	}
	dir := filepath.Join(longPath(t.TempDir()), "First Tab")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("explorer.exe", dir).Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { withExplorerOf(t, dir, func(disp, _, _ uintptr) { quit(disp) }) })
	var top uintptr
	for deadline := time.Now().Add(20 * time.Second); top == 0 && time.Now().Before(deadline); {
		withExplorerOf(t, dir, func(_, _, w uintptr) { top = w })
		if top == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if top == 0 {
		t.Fatal("the Explorer window did not show up")
	}
	firstTab := findChild(top, "ShellTabWindowClass")
	if firstTab == 0 {
		t.Skip("this Explorer has no tabs")
	}
	if findChild(top, "ShellTabWindowClass", firstTab) != 0 {
		// Explorer put the folder into a window of the user's: leave it alone.
		t.Skip("the folder opened as a tab of another window")
	}
	if c, _ := Detect(t.Context(), top); !strings.EqualFold(longPath(c.Folder), dir) {
		t.Fatalf("one tab: folder %q, want %q", c.Folder, dir)
	}
	const wmCommand, cmdNewTab = 0x0111, 0xA21B
	procPostMessageW := user32.NewProc("PostMessageW")
	procPostMessageW.Call(firstTab, wmCommand, cmdNewTab, 0)
	var newTab uintptr
	for deadline := time.Now().Add(10 * time.Second); newTab == 0 && time.Now().Before(deadline); {
		time.Sleep(200 * time.Millisecond)
		for tab := findChild(top, "ShellTabWindowClass"); tab != 0; tab = findChild(top, "ShellTabWindowClass", tab) {
			if r, _, _ := procIsWindowVisible.Call(tab); r != 0 && tab != firstTab {
				newTab = tab
			}
		}
	}
	if newTab == 0 {
		t.Skip("no second tab opened (tabs not supported by this Explorer)")
	}
	t.Cleanup(func() {
		// IWebBrowser2::Quit does not close the last tab of a window that
		// had several, so the window (the test's own) is closed instead.
		const wmClose = 0x0010
		// WM_CLOSE closes one tab at a time.
		for range 10 {
			if r, _, _ := procIsWindow.Call(top); r == 0 || className(top) != ClassExplorer {
				return
			}
			procPostMessageW.Call(top, wmClose, 0, 0)
			time.Sleep(300 * time.Millisecond)
		}
	})
	time.Sleep(500 * time.Millisecond) // let the new tab's view come up
	c, ok := Detect(t.Context(), top)
	t.Logf("two tabs, the new one active: %+v", c)
	if !ok || c.Kind != plugin.ContextExplorer {
		t.Fatalf("context %+v, want an explorer context", c)
	}
	if strings.EqualFold(longPath(c.Folder), dir) {
		t.Errorf("folder %q is the inactive tab's", c.Folder)
	}
}

// TestExplorerReal opens its own File Explorer window on a new folder,
// selects the file in it, detects the context and closes that window (or
// tab) again. It never touches other Explorer windows.
func TestExplorerReal(t *testing.T) {
	if os.Getenv(contextTestsEnv) != "1" {
		t.Skipf("set %s=1 to open a real File Explorer window", contextTestsEnv)
	}
	dir := longPath(t.TempDir())
	dir = filepath.Join(dir, "Context Test 上下文")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "only file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("explorer.exe", dir).Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		withExplorerOf(t, dir, func(disp, _, _ uintptr) {
			if wb := queryInterface(disp, &iidIWebBrowser2); wb != 0 {
				comCall(wb, vtWebBrowserQuit) // closes this window or tab only
				release(wb)
			}
		})
	})

	var top uintptr
	deadline := time.Now().Add(20 * time.Second)
	for top == 0 && time.Now().Before(deadline) {
		withExplorerOf(t, dir, func(_, browser, w uintptr) {
			top = w
			// Select the only file, as a user would.
			var view uintptr
			if failed(comCall(browser, vtQueryActiveShellView, uintptr(unsafe.Pointer(&view)))) || view == 0 {
				top = 0 // the view is not ready yet
				return
			}
			defer release(view)
			fv := queryInterface(view, &iidIFolderView)
			if fv == 0 {
				top = 0
				return
			}
			defer release(fv)
			if failed(comCall(fv, vtFolderViewSelect, 0, svsiSelect|svsiDeselectOthers|svsiEnsureVisible|svsiFocused)) {
				top = 0
			}
		})
		if top == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if top == 0 {
		t.Fatal("the Explorer window did not show up")
	}
	if c := className(top); c != ClassExplorer {
		t.Fatalf("window class %q, want %q", c, ClassExplorer)
	}

	var c plugin.Context
	var ok bool
	var times []time.Duration
	for range 5 {
		start := time.Now()
		c, ok = Detect(t.Context(), top)
		times = append(times, time.Since(start))
	}
	slices.Sort(times)
	t.Logf("detection took %v (median of 5; min %v, max %v)", times[2], times[0], times[4])
	if !ok || c.Kind != plugin.ContextExplorer {
		t.Fatalf("context %+v, want an explorer context", c)
	}
	if !strings.EqualFold(longPath(c.Folder), dir) {
		t.Errorf("folder %q, want %q (if this window has several tabs, the active one must be %s)", c.Folder, dir, dir)
	}
	if len(c.Selected) != 1 || !strings.EqualFold(longPath(c.Selected[0]), file) {
		t.Errorf("selected %q, want [%s]", c.Selected, file)
	}
	if !strings.EqualFold(c.App.Name, "explorer.exe") {
		t.Errorf("app %+v, want explorer.exe", c.App)
	}
}

// TestDetectSelfAndNoWindow: no context for no window or the test's own
// process (the launcher's windows).
func TestDetectSelfAndNoWindow(t *testing.T) {
	if _, ok := Detect(t.Context(), 0); ok {
		t.Error("a context for no window")
	}
	if _, ok := Detect(t.Context(), 0xdead0); ok {
		t.Error("a context for an invalid window")
	}
}

// TestExplorerTwoFoldersReal opens two folders one after the other. When
// Windows puts the second into a new tab of the same window (Windows 11),
// the context must be the active tab's folder; otherwise each window has
// its own folder.
func TestExplorerTwoFoldersReal(t *testing.T) {
	if os.Getenv(contextTestsEnv) != "1" {
		t.Skipf("set %s=1 to open real File Explorer windows", contextTestsEnv)
	}
	base := longPath(t.TempDir())
	var dirs []string
	var tops []uintptr
	for _, name := range []string{"Tab One", "Tab Two"} {
		dir := filepath.Join(base, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
		if err := exec.Command("explorer.exe", dir).Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			withExplorerOf(t, dir, func(disp, _, _ uintptr) {
				if wb := queryInterface(disp, &iidIWebBrowser2); wb != 0 {
					comCall(wb, vtWebBrowserQuit)
					release(wb)
				}
			})
		})
		var top uintptr
		for deadline := time.Now().Add(20 * time.Second); top == 0 && time.Now().Before(deadline); {
			withExplorerOf(t, dir, func(_, _, w uintptr) { top = w })
			if top == 0 {
				time.Sleep(200 * time.Millisecond)
			}
		}
		if top == 0 {
			t.Fatalf("no Explorer window for %s", dir)
		}
		tops = append(tops, top)
		time.Sleep(500 * time.Millisecond) // let the new tab become the active one
	}
	if tops[0] == tops[1] {
		t.Log("Windows opened the second folder as a tab of the first window")
		c, _ := Detect(t.Context(), tops[1])
		if !strings.EqualFold(longPath(c.Folder), dirs[1]) {
			t.Errorf("folder %q, want the active tab's %q", c.Folder, dirs[1])
		}
		return
	}
	t.Log("Windows opened two windows")
	for i, top := range tops {
		c, _ := Detect(t.Context(), top)
		if !strings.EqualFold(longPath(c.Folder), dirs[i]) {
			t.Errorf("window %d: folder %q, want %q", i, c.Folder, dirs[i])
		}
	}
}
