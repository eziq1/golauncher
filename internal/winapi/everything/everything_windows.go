package everything

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procFindWindowW                 = user32.NewProc("FindWindowW")
	procRegisterClassExW            = user32.NewProc("RegisterClassExW")
	procCreateWindowExW             = user32.NewProc("CreateWindowExW")
	procDestroyWindow               = user32.NewProc("DestroyWindow")
	procDefWindowProcW              = user32.NewProc("DefWindowProcW")
	procSendMessageTimeoutW         = user32.NewProc("SendMessageTimeoutW")
	procPeekMessageW                = user32.NewProc("PeekMessageW")
	procDispatchMessageW            = user32.NewProc("DispatchMessageW")
	procMsgWaitForMultipleObjectsEx = user32.NewProc("MsgWaitForMultipleObjectsEx")
	procChangeWindowMessageFilterEx = user32.NewProc("ChangeWindowMessageFilterEx")
	procGetModuleHandleW            = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmCopyData        = 0x004A
	hwndMessage       = ^uintptr(2) // (HWND)-3
	smtoAbortIfHung   = 0x0002
	pmRemove          = 0x0001
	qsAllInput        = 0x04FF
	mwmoInputAvail    = 0x0004
	msgfltAllow       = 1
	errClassUsed      = 1410       // ERROR_CLASS_ALREADY_EXISTS
	replyID           = 0x474C4551 // "GLEQ"; any value, echoed back in dwData
	defaultTimeout    = 10 * time.Second
	pollInterval      = 25 * time.Millisecond
	processQueryLimit = windows.PROCESS_QUERY_LIMITED_INFORMATION
)

// ipcClasses are the window classes of Everything's IPC window: 1.4, then
// the default instance of 1.5 alpha.
var ipcClasses = []string{"EVERYTHING_TASKBAR_NOTIFICATION", "EVERYTHING_TASKBAR_NOTIFICATION_(1.5a)"}

// copyDataStruct is COPYDATASTRUCT; its natural Go layout matches C on
// amd64, arm64 and 386.
type copyDataStruct struct {
	data   uintptr // dwData (ULONG_PTR)
	size   uint32  // cbData
	buffer unsafe.Pointer
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
	Private uint32
}

// findIPCWindow returns Everything's IPC window, or 0.
func findIPCWindow() uintptr {
	for _, c := range ipcClasses {
		name, _ := windows.UTF16PtrFromString(c)
		if h, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(name)), 0); h != 0 {
			return h
		}
	}
	return 0
}

// Running reports whether an Everything IPC window exists.
func Running() bool { return findIPCWindow() != 0 }

// reply collects the answer to one query. It is only touched by the thread
// owning its window.
type reply struct {
	data []byte
	done bool
}

var (
	repliesMu sync.Mutex
	replies   = map[uintptr]*reply{}
)

func wndProc(hwnd, m, wparam, lparam uintptr) uintptr {
	if m == wmCopyData {
		repliesMu.Lock()
		r := replies[hwnd]
		repliesMu.Unlock()
		cds := *(**copyDataStruct)(unsafe.Pointer(&lparam))
		if r != nil && cds != nil && cds.data == replyID {
			if !r.done {
				r.data = slices.Clone(unsafe.Slice((*byte)(cds.buffer), cds.size))
				r.done = true
			}
			return 1
		}
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, m, wparam, lparam)
	return ret
}

type windowClass struct {
	name     *uint16
	instance uintptr
}

// replyClass registers the reply window class once; callbacks are a limited
// resource, so all reply windows share one window procedure.
var replyClass = sync.OnceValues(func() (windowClass, error) {
	inst, _, _ := procGetModuleHandleW.Call(0)
	name, _ := windows.UTF16PtrFromString("golauncher.everything.reply")
	wc := wndClassEx{WndProc: windows.NewCallback(wndProc), Instance: inst, ClassName: name}
	wc.Size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		if errno, ok := errors.AsType[syscall.Errno](err); !ok || errno != errClassUsed {
			return windowClass{}, fmt.Errorf("everything: RegisterClassEx: %w", err)
		}
	}
	return windowClass{name: name, instance: inst}, nil
})

// Search runs q in Everything. It returns ErrNotRunning when Everything is
// not running, and gives up at ctx's deadline (10 s at most).
func Search(ctx context.Context, q Query) ([]Result, error) {
	target := findIPCWindow()
	if target == 0 {
		return nil, ErrNotRunning
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cls, err := replyClass()
	if err != nil {
		return nil, err
	}

	// The reply is sent to a window, which must be served by the thread
	// that created it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls.name)), 0, 0,
		0, 0, 0, 0, hwndMessage, 0, cls.instance, 0)
	if hwnd == 0 {
		return nil, fmt.Errorf("everything: CreateWindowEx: %w", err)
	}
	defer procDestroyWindow.Call(hwnd)
	r := &reply{}
	repliesMu.Lock()
	replies[hwnd] = r
	repliesMu.Unlock()
	defer func() {
		repliesMu.Lock()
		delete(replies, hwnd)
		repliesMu.Unlock()
	}()
	// Let an elevated golauncher receive replies from a non-elevated Everything.
	procChangeWindowMessageFilterEx.Call(hwnd, wmCopyData, msgfltAllow, 0)

	deadline := time.Now().Add(defaultTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	data := encodeQuery(q, uint32(hwnd), replyID)
	cds := copyDataStruct{data: copyDataQuery2W, size: uint32(len(data)), buffer: unsafe.Pointer(&data[0])}
	var accepted uintptr
	// Not SMTO_BLOCK: Everything may answer while handling the request, and
	// the answer must be dispatched meanwhile.
	ok, _, err := procSendMessageTimeoutW.Call(target, wmCopyData, hwnd, uintptr(unsafe.Pointer(&cds)),
		smtoAbortIfHung, uintptr(max(time.Until(deadline).Milliseconds(), 1)), uintptr(unsafe.Pointer(&accepted)))
	runtime.KeepAlive(data)
	switch {
	case ok == 0 && !time.Now().Before(deadline):
		return nil, fmt.Errorf("everything: sending the query: %w", context.DeadlineExceeded)
	case ok == 0:
		return nil, fmt.Errorf("everything: sending the query: %w", err)
	case accepted == 0 && !r.done:
		return nil, errors.New("everything: query rejected (Everything 1.4.1 or later is required)")
	}

	var m msg
	for !r.done {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return nil, fmt.Errorf("everything: waiting for results: %w", context.DeadlineExceeded)
		}
		procMsgWaitForMultipleObjectsEx.Call(0, 0, uintptr(min(left, pollInterval).Milliseconds()+1), qsAllInput, mwmoInputAvail)
		for {
			got, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
			if got == 0 {
				break
			}
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}
	return parseList2(r.data)
}

// Start launches Everything in the background ("-startup": no search window).
func Start(exePath string) error {
	if exePath == "" {
		return errors.New("everything: executable not known")
	}
	cmd := exec.Command(exePath, "-startup")
	cmd.Dir = filepath.Dir(exePath)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("everything: starting %s: %w", exePath, err)
	}
	return cmd.Process.Release()
}

// Installed reports whether Everything is installed and returns its
// executable. It looks, in order, at the running instance, the App Paths and
// Uninstall registry entries (HKLM, WOW6432Node and HKCU), and the usual
// install folders, including Scoop. When Everything runs but its executable
// cannot be determined, it returns "", true.
func Installed() (exePath string, ok bool) {
	if p, running := runningExe(); running {
		return p, true
	}
	if p := registryExe(); p != "" {
		return p, true
	}
	if p := firstFile(commonPaths()); p != "" {
		return p, true
	}
	return "", false
}

// runningExe returns the executable of the running Everything.
func runningExe() (string, bool) {
	hwnd := findIPCWindow()
	if hwnd == 0 {
		return "", false
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid); err != nil || pid == 0 {
		return "", true
	}
	h, err := windows.OpenProcess(processQueryLimit, false, pid)
	if err != nil {
		return "", true
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return "", true
	}
	return windows.UTF16ToString(buf[:n]), true
}

const uninstallKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`

// registryExe looks for Everything in App Paths and in the Uninstall entries.
func registryExe() string {
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		if p, ok := readString(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\Everything.exe`, ""); ok {
			if p = firstFile([]string{unquote(p)}); p != "" {
				return p
			}
		}
	}
	type key struct {
		root registry.Key
		path string
	}
	for _, k := range []key{
		{registry.LOCAL_MACHINE, uninstallKey},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.CURRENT_USER, uninstallKey},
	} {
		if p := uninstallExe(k.root, k.path); p != "" {
			return p
		}
	}
	return ""
}

// uninstallExe scans the Uninstall entries under path for Everything.
func uninstallExe(root registry.Key, path string) string {
	parent, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return ""
	}
	names, _ := parent.ReadSubKeyNames(-1)
	parent.Close()
	for _, name := range names {
		sub := path + `\` + name
		display, _ := readString(root, sub, "DisplayName")
		publisher, _ := readString(root, sub, "Publisher")
		isEverything := strings.EqualFold(name, "Everything") ||
			strings.HasPrefix(strings.ToLower(display), "everything") && strings.Contains(strings.ToLower(publisher), "voidtools")
		if !isEverything {
			continue
		}
		var candidates []string
		if loc, ok := readString(root, sub, "InstallLocation"); ok && loc != "" {
			candidates = append(candidates, exesIn(unquote(loc))...)
		}
		if icon, ok := readString(root, sub, "DisplayIcon"); ok && icon != "" {
			if i := strings.LastIndex(icon, ","); i > 0 {
				icon = icon[:i]
			}
			if icon = unquote(icon); strings.EqualFold(filepath.Ext(icon), ".exe") {
				candidates = append(candidates, icon)
			}
		}
		if un, ok := readString(root, sub, "UninstallString"); ok && un != "" {
			if exe, _, _ := strings.Cut(strings.TrimPrefix(un, `"`), `"`); exe != "" {
				candidates = append(candidates, exesIn(filepath.Dir(exe))...)
			}
		}
		if p := firstFile(candidates); p != "" {
			return p
		}
	}
	return ""
}

func readString(root registry.Key, path, name string) (string, bool) {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	if err != nil {
		return "", false
	}
	if expanded, err := registry.ExpandString(v); err == nil {
		v = expanded
	}
	return v, true
}

func unquote(s string) string { return strings.Trim(strings.TrimSpace(s), `"`) }

// exesIn lists the Everything executables that may be in dir.
func exesIn(dir string) []string {
	return []string{filepath.Join(dir, "Everything.exe"), filepath.Join(dir, "Everything64.exe")}
}

// commonPaths lists the usual locations of Everything.exe.
func commonPaths() []string {
	var dirs []string
	for _, env := range []string{"ProgramW6432", "ProgramFiles", "ProgramFiles(x86)"} {
		if base := os.Getenv(env); base != "" {
			dirs = append(dirs, filepath.Join(base, "Everything"), filepath.Join(base, "Everything 1.5a"))
		}
	}
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		dirs = append(dirs, filepath.Join(base, "Programs", "Everything"))
	}
	var scoop []string
	for _, env := range []string{"SCOOP", "SCOOP_GLOBAL"} {
		if v := os.Getenv(env); v != "" {
			scoop = append(scoop, v)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		scoop = append(scoop, filepath.Join(home, "scoop"))
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		scoop = append(scoop, filepath.Join(pd, "scoop"))
	}
	for _, s := range scoop {
		for _, app := range []string{"everything", "everything-alpha", "everything-beta", "everything-lite"} {
			dirs = append(dirs, filepath.Join(s, "apps", app, "current"))
		}
	}
	var paths []string
	for _, d := range dirs {
		paths = append(paths, exesIn(d)...)
	}
	// Scoop's shim forwards arguments to the real executable.
	for _, s := range scoop {
		paths = append(paths, filepath.Join(s, "shims", "everything.exe"))
	}
	return paths
}

// firstFile returns the first path naming an existing regular file.
func firstFile(paths []string) string {
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}
