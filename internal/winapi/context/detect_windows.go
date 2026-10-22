package wincontext

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	winselection "github.com/eziq1/golauncher/internal/winapi/selection"
	"github.com/eziq1/golauncher/pkg/plugin"
)

func init() {
	explorerView = explorerFolderView
	desktopView = desktopFolderView
	pointerProbe = shellPointer
	statPath = func(p string) (bool, bool) {
		st, err := os.Stat(p)
		if err != nil {
			return false, false
		}
		return st.IsDir(), true
	}
	homeDir = func() string {
		h, _ := os.UserHomeDir()
		return h
	}
}

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetClassNameW             = user32.NewProc("GetClassNameW")
	procGetWindowTextW            = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW      = user32.NewProc("GetWindowTextLengthW")
	procFindWindowExW             = user32.NewProc("FindWindowExW")
	procIsWindowVisible           = user32.NewProc("IsWindowVisible")
	procGetAncestor               = user32.NewProc("GetAncestor")
	procIsWindow                  = user32.NewProc("IsWindow")
	procCoInitializeEx            = ole32.NewProc("CoInitializeEx")
	procCoUninitialize            = ole32.NewProc("CoUninitialize")
	procCoCreateInstance          = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree             = ole32.NewProc("CoTaskMemFree")
	procSHGetPathFromIDListEx     = shell32.NewProc("SHGetPathFromIDListEx")
	procGetApplicationUserModelId = kernel32.NewProc("GetApplicationUserModelId")
)

var (
	clsidShellWindows   = mustGUID("{9BA05972-F6A8-11CF-A442-00A0C90A8F39}")
	iidIShellWindows    = mustGUID("{85CB6900-4D95-11CF-960C-0080C7F4EE85}")
	iidIEnumVARIANT     = mustGUID("{00020404-0000-0000-C000-000000000046}")
	iidIServiceProvider = mustGUID("{6D5140C1-7436-11CE-8034-00AA006009FA}")
	sidSTopLevelBrowser = mustGUID("{4C96BE40-915C-11CF-99D3-00AA004AE837}")
	iidIShellBrowser    = mustGUID("{000214E2-0000-0000-C000-000000000046}")
	iidIFolderView      = mustGUID("{CDE725B0-CCC9-4519-917E-325D72FAB4CE}")
	iidIPersistFolder2  = mustGUID("{1AC3D9F0-175C-11D1-95BE-00609797EA4F}")
	iidIShellItemArray  = mustGUID("{B63EA76D-1F85-456F-A19C-48159EFA858B}")
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

// Vtable indexes of the COM methods used here.
const (
	vtQueryInterface = 0
	vtRelease        = 2

	vtShellWindowsNewEnum      = 9  // IShellWindows::_NewEnum
	vtShellWindowsFindWindowSW = 15 // IShellWindows::FindWindowSW
	vtEnumVariantNext          = 3  // IEnumVARIANT::Next
	vtQueryService             = 3  // IServiceProvider::QueryService
	vtGetWindow                = 3  // IOleWindow::GetWindow (IShellBrowser)
	vtQueryActiveShellView     = 15 // IShellBrowser::QueryActiveShellView
	vtFolderViewGetFolder      = 5  // IFolderView::GetFolder
	vtFolderViewItemCount      = 7  // IFolderView::ItemCount
	vtFolderViewItems          = 8  // IFolderView::Items
	vtGetCurFolder             = 5  // IPersistFolder2::GetCurFolder
	vtItemArrayGetCount        = 7  // IShellItemArray::GetCount
	vtItemArrayGetItemAt       = 8  // IShellItemArray::GetItemAt
	vtShellItemGetDisplayName  = 5  // IShellItem::GetDisplayName
)

// maxSelected bounds the selected items read from a view.
const maxSelected = 500

// maxViewItems bounds the items of a view looked through for the item under
// the pointer; a larger view keeps the selection.
const maxViewItems = 20000

// variant is VARIANT: 16 bytes on 32-bit Windows, 24 on 64-bit.
type variant struct {
	vt      uint16
	_, _, _ uint16
	val     uintptr
	_       uintptr
}

const (
	vtI4       = 3
	vtDispatch = 9
)

// comCall calls method i of the COM interface obj.
//
//go:uintptrescapes
func comCall(obj uintptr, i int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(native(obj))
	fn := *(*uintptr)(unsafe.Add(native(vtbl), i*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

// native converts an address returned by Windows to a pointer; the memory
// is not managed by Go.
func native(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

func release(obj uintptr) {
	if obj != 0 {
		comCall(obj, vtRelease)
	}
}

func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func queryInterface(obj uintptr, iid *windows.GUID) uintptr {
	var out uintptr
	if failed(comCall(obj, vtQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))) {
		return 0
	}
	return out
}

// withCOM runs fn on a locked OS thread with COM initialized as a
// single-threaded apartment.
func withCOM(fn func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const coinitApartmentThreaded, coinitDisableOLE1DDE = 0x2, 0x4
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	if !failed(hr) {
		defer procCoUninitialize.Call()
	}
	fn()
}

// Detect describes the context of window hwnd, normally the foreground
// window captured before the launcher took the focus. It reports false for
// no window and for the launcher's own windows.
func Detect(ctx context.Context, hwnd uintptr) (plugin.Context, bool) {
	return DetectAt(ctx, hwnd, nil)
}

// DetectAt is Detect with what was under the mouse pointer (PointerAt, for
// the Action Ring): in a folder view the reported items follow the item
// under it (R120). A nil pointer is Detect.
func DetectAt(ctx context.Context, hwnd uintptr, p *Pointer) (plugin.Context, bool) {
	if hwnd == 0 {
		return plugin.Context{}, false
	}
	if r, _, _ := procIsWindow.Call(hwnd); r == 0 {
		return plugin.Context{}, false
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &pid); err != nil || pid == 0 || int(pid) == os.Getpid() {
		return plugin.Context{}, false
	}
	w := Window{HWND: hwnd, Class: className(hwnd), Title: windowText(hwnd), Pointer: p}
	if w.Class == ClassWorkerW {
		w.Desktop = findChild(hwnd, "SHELLDLL_DefView") != 0
	}
	c := plugin.Context{App: appInfo(hwnd, pid), Title: w.Title}
	run(ctx, Detectors(), w, &c)
	return c, true
}

func className(hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

func windowText(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	n, _, _ = procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

// findChild returns the first child window of parent with class, after
// child after (0 for the first).
func findChild(parent uintptr, class string, after ...uintptr) uintptr {
	var a uintptr
	if len(after) > 0 {
		a = after[0]
	}
	var cls *uint16
	if class != "" {
		cls, _ = windows.UTF16PtrFromString(class)
	}
	r, _, _ := procFindWindowExW.Call(parent, a, uintptr(unsafe.Pointer(cls)), 0)
	return r
}

// appInfo describes the process owning hwnd. For a packaged app hosted by
// ApplicationFrameHost.exe, the app's own process (owning the CoreWindow
// child) is described instead.
func appInfo(hwnd uintptr, pid uint32) plugin.AppInfo {
	a := processInfo(pid)
	if strings.EqualFold(a.Name, "ApplicationFrameHost.exe") {
		for child := findChild(hwnd, ""); child != 0; child = findChild(hwnd, "", child) {
			var cpid uint32
			if _, err := windows.GetWindowThreadProcessId(windows.HWND(child), &cpid); err == nil && cpid != 0 && cpid != pid {
				return processInfo(cpid)
			}
		}
	}
	return a
}

func processInfo(pid uint32) plugin.AppInfo {
	a := plugin.AppInfo{PID: int(pid)}
	const processQueryLimitedInformation = 0x1000
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return a
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
		a.Path = windows.UTF16ToString(buf[:n])
		a.Name = filepath.Base(a.Path)
	}
	if procGetApplicationUserModelId.Find() == nil {
		aumid := make([]uint16, 256)
		size := uint32(len(aumid))
		if r, _, _ := procGetApplicationUserModelId.Call(uintptr(h), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&aumid[0]))); r == 0 {
			a.Package = windows.UTF16ToString(aumid)
		}
	}
	return a
}

// explorerFolderView reads the folder and selection of the File Explorer
// window hwnd. On Windows 11 a window holds several tabs, each a shell
// browser of its own in IShellWindows; the active tab is the first visible
// ShellTabWindowClass child of the window (the tabs are siblings ordered by
// z-order), and the browser whose IShellBrowser::GetWindow is that tab is
// the one shown. Without tabs, the browser whose window belongs to hwnd is
// taken.
func explorerFolderView(ctx context.Context, hwnd uintptr, name string) (v folderView, ok bool) {
	activeTab := uintptr(0)
	for tab := findChild(hwnd, "ShellTabWindowClass"); tab != 0; tab = findChild(hwnd, "ShellTabWindowClass", tab) {
		if r, _, _ := procIsWindowVisible.Call(tab); r != 0 {
			activeTab = tab
			break
		}
	}
	withCOM(func() {
		windowsObj := shellWindows()
		if windowsObj == 0 {
			return
		}
		defer release(windowsObj)
		var browser uintptr // the chosen IShellBrowser
		take := func(b uintptr) {
			release(browser)
			comCall(b, 1) // AddRef: kept beyond the callback
			browser = b
		}
		forEachBrowser(ctx, windowsObj, func(b, w uintptr) bool {
			if activeTab != 0 && w == activeTab {
				take(b)
				return false
			}
			if root, _, _ := procGetAncestor.Call(w, 2); browser == 0 && (w == hwnd || root == hwnd) { // GA_ROOT
				take(b) // a tab of this window: the fallback when no tab is known
			}
			return true
		})
		if browser == 0 {
			return
		}
		defer release(browser)
		v, ok = browserView(ctx, browser, name)
	})
	return v, ok
}

// desktopFolderView reads the desktop's folder and selected icons through
// IShellWindows::FindWindowSW(SWC_DESKTOP).
func desktopFolderView(ctx context.Context, name string) (v folderView, ok bool) {
	desk, err := windows.KnownFolderPath(windows.FOLDERID_Desktop, 0)
	if err != nil {
		return folderView{}, false
	}
	withCOM(func() {
		windowsObj := shellWindows()
		if windowsObj == 0 {
			return
		}
		defer release(windowsObj)
		const (
			csidlDesktop     = 0
			swcDesktop       = 8
			swfoNeedDispatch = 1
		)
		loc := variant{vt: vtI4, val: csidlDesktop}
		var empty variant
		var hwnd int32
		var disp uintptr
		if r := comCall(windowsObj, vtShellWindowsFindWindowSW, uintptr(unsafe.Pointer(&loc)), uintptr(unsafe.Pointer(&empty)),
			swcDesktop, uintptr(unsafe.Pointer(&hwnd)), swfoNeedDispatch, uintptr(unsafe.Pointer(&disp))); failed(r) || disp == 0 {
			return
		}
		defer release(disp)
		b := topLevelBrowser(disp)
		if b == 0 {
			return
		}
		defer release(b)
		v, _ = browserView(ctx, b, name)
	})
	v.Folder = desk
	return v, ctx.Err() == nil
}

// shellWindows creates the IShellWindows collection of the shell's windows.
func shellWindows() uintptr {
	const clsctxAll = 0x17
	var obj uintptr
	if r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellWindows)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIShellWindows)), uintptr(unsafe.Pointer(&obj))); failed(r) {
		return 0
	}
	return obj
}

// forEachBrowser calls fn with the IShellBrowser of every shell window and
// the window it reports, until fn returns false. The browser is released
// after fn returns.
func forEachBrowser(ctx context.Context, windowsObj uintptr, fn func(browser, hwnd uintptr) bool) {
	var unk uintptr
	if r := comCall(windowsObj, vtShellWindowsNewEnum, uintptr(unsafe.Pointer(&unk))); failed(r) || unk == 0 {
		return
	}
	enum := queryInterface(unk, &iidIEnumVARIANT)
	release(unk)
	if enum == 0 {
		return
	}
	defer release(enum)
	for ctx.Err() == nil {
		var v variant
		var fetched uint32
		if r := comCall(enum, vtEnumVariantNext, 1, uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&fetched))); r != 0 || fetched == 0 {
			return // S_FALSE at the end
		}
		if v.vt != vtDispatch || v.val == 0 {
			continue
		}
		b := topLevelBrowser(v.val)
		release(v.val)
		if b == 0 {
			continue
		}
		var w uintptr
		comCall(b, vtGetWindow, uintptr(unsafe.Pointer(&w)))
		more := fn(b, w)
		release(b)
		if !more {
			return
		}
	}
}

// topLevelBrowser returns the IShellBrowser of a shell window's dispatch
// object (IWebBrowser2), or 0.
func topLevelBrowser(disp uintptr) uintptr {
	sp := queryInterface(disp, &iidIServiceProvider)
	if sp == 0 {
		return 0
	}
	defer release(sp)
	var b uintptr
	if failed(comCall(sp, vtQueryService, uintptr(unsafe.Pointer(&sidSTopLevelBrowser)), uintptr(unsafe.Pointer(&iidIShellBrowser)), uintptr(unsafe.Pointer(&b)))) {
		return 0
	}
	return b
}

// browserView reads the folder path (empty for virtual folders) and the
// selected items' paths of a shell browser's active view and, when name is
// not empty, the items shown with that name (namedItems).
func browserView(ctx context.Context, browser uintptr, name string) (v folderView, ok bool) {
	var view uintptr
	if failed(comCall(browser, vtQueryActiveShellView, uintptr(unsafe.Pointer(&view)))) || view == 0 {
		return folderView{}, false
	}
	defer release(view)
	fv := queryInterface(view, &iidIFolderView)
	if fv == 0 {
		return folderView{}, false
	}
	defer release(fv)
	var pf uintptr
	if !failed(comCall(fv, vtFolderViewGetFolder, uintptr(unsafe.Pointer(&iidIPersistFolder2)), uintptr(unsafe.Pointer(&pf)))) && pf != 0 {
		var pidl uintptr
		if !failed(comCall(pf, vtGetCurFolder, uintptr(unsafe.Pointer(&pidl)))) && pidl != 0 {
			v.Folder = pathOfIDList(pidl)
			procCoTaskMemFree.Call(pidl)
		}
		release(pf)
	}
	const svgioSelection = 1
	// The selection is read whole, as it always was: no deadline.
	forEachViewItem(context.Background(), fv, svgioSelection, maxSelected, func(item uintptr) {
		if p := fileSysPath(item); p != "" {
			v.Selected = append(v.Selected, p)
		}
	})
	if name != "" {
		v.Named, v.NamedOK = namedItems(ctx, fv, name)
	}
	return v, true
}

// namedItems returns the items of folder view fv shown as name, with their
// exact paths. name is the one UI Automation reads for the item under the
// pointer: the display name (SIGDN_NORMALDISPLAY), with the extension
// hidden or a folder's name localized ("文档" for Documents) as the view
// shows it. complete is false when the view holds more than maxViewItems
// items or ctx ended first.
func namedItems(ctx context.Context, fv uintptr, name string) (items []viewItem, complete bool) {
	const svgioAllView = 2
	var count int32
	if failed(comCall(fv, vtFolderViewItemCount, svgioAllView, uintptr(unsafe.Pointer(&count)))) || count > maxViewItems {
		return nil, false
	}
	complete = forEachViewItem(ctx, fv, svgioAllView, maxViewItems, func(item uintptr) {
		// The path is asked only of the items bearing the name: one call
		// per item to Explorer otherwise.
		if shown := shellItemName(item, sigdnNormalDisplay); strings.EqualFold(shown, name) {
			items = append(items, viewItem{Display: shown, Path: fileSysPath(item)})
		}
	})
	if !complete {
		return nil, false
	}
	return items, true
}

// forEachViewItem calls fn with the items of folder view fv that svgio
// names (the selection, all items), at most limit of them. It reports false
// when the items could not be read, there were more than limit or ctx
// ended first.
func forEachViewItem(ctx context.Context, fv uintptr, svgio uint32, limit uint32, fn func(item uintptr)) bool {
	var arr uintptr
	if failed(comCall(fv, vtFolderViewItems, uintptr(svgio), uintptr(unsafe.Pointer(&iidIShellItemArray)), uintptr(unsafe.Pointer(&arr)))) || arr == 0 {
		return false
	}
	defer release(arr)
	var n uint32
	comCall(arr, vtItemArrayGetCount, uintptr(unsafe.Pointer(&n)))
	for i := range min(n, limit) {
		if ctx.Err() != nil {
			return false
		}
		var item uintptr
		if failed(comCall(arr, vtItemArrayGetItemAt, uintptr(i), uintptr(unsafe.Pointer(&item)))) || item == 0 {
			continue
		}
		fn(item)
		release(item)
	}
	return n <= limit
}

// pathOfIDList returns the file system path of an ID list, or "" for a
// virtual folder.
func pathOfIDList(pidl uintptr) string {
	buf := make([]uint16, windows.MAX_LONG_PATH)
	if r, _, _ := procSHGetPathFromIDListEx.Call(pidl, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0); r == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}

// IShellItem::GetDisplayName forms.
const (
	sigdnNormalDisplay = 0x00000000
	sigdnFileSysPath   = 0x80058000
)

// fileSysPath returns the file system path of an IShellItem, or "".
func fileSysPath(item uintptr) string { return shellItemName(item, sigdnFileSysPath) }

// shellItemName returns the name of an IShellItem in form sigdn, or "".
func shellItemName(item uintptr, sigdn uint32) string {
	var p uintptr
	if failed(comCall(item, vtShellItemGetDisplayName, uintptr(sigdn), uintptr(unsafe.Pointer(&p)))) || p == 0 {
		return ""
	}
	s := windows.UTF16PtrToString((*uint16)(native(p)))
	procCoTaskMemFree.Call(p)
	return s
}

// shellPointer reads what is under the screen point (x, y), in physical
// pixels, when window target is File Explorer or the desktop: the item or
// the empty space of its items view, through UI Automation on the
// selection's thread (winselection.ListItemAt).
func shellPointer(target uintptr, x, y float64, budget time.Duration) (hit, bool) {
	switch cls := className(target); {
	case cls == ClassExplorer, cls == ClassProgman:
	case cls == ClassWorkerW && findChild(target, "SHELLDLL_DefView") != 0:
	default:
		return hit{}, false
	}
	l, ok := winselection.ListItemAt(int(math.Round(x)), int(math.Round(y)), budget)
	if !ok {
		return hit{}, false
	}
	return hitOfList(l), true
}
