package shell

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/eziq1/golauncher/pkg/plugin"
)

var (
	ole32                           = windows.NewLazySystemDLL("ole32.dll")
	shell32                         = windows.NewLazySystemDLL("shell32.dll")
	gdi32                           = windows.NewLazySystemDLL("gdi32.dll")
	procCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	procCoUninitialize              = ole32.NewProc("CoUninitialize")
	procCoTaskMemFree               = ole32.NewProc("CoTaskMemFree")
	procSHGetKnownFolderItem        = shell32.NewProc("SHGetKnownFolderItem")
	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
	procShellExecuteExW             = shell32.NewProc("ShellExecuteExW")
	procGetObjectW                  = gdi32.NewProc("GetObjectW")
	procDeleteObject                = gdi32.NewProc("DeleteObject")
)

var (
	folderIDAppsFolder        = mustGUID("{1e87508d-89c2-42f0-8a7e-645a0f50ca58}")
	bhidEnumItems             = mustGUID("{94f60519-2850-4924-aa5a-d15e84868039}")
	iidIEnumShellItems        = mustGUID("{70629033-e363-4a28-a567-0db78006e6d7}")
	iidIShellItem2            = mustGUID("{7e9fb0d3-919f-4307-ab2e-9b1860310c93}")
	iidIShellItemImageFactory = mustGUID("{bcc18b79-ba16-442f-80c4-8a59c30c463b}")

	pkeyLinkTargetParsingPath = propertyKey{mustGUID("{b9b4b3fc-2b51-4a42-b5d8-324146afcf25}"), 2}
	pkeyLinkArguments         = propertyKey{mustGUID("{436f2667-14e2-4feb-b30a-146c53b5b674}"), 100}
	pkeyPackageFamilyName     = propertyKey{mustGUID("{9f4c2855-9f79-4b39-a8d0-e1d42de1d5f3}"), 17}
)

// propertyKey is PROPERTYKEY.
type propertyKey struct {
	fmtid windows.GUID
	pid   uint32
}

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

// Vtable indexes of IShellItem / IShellItem2 / IEnumShellItems methods.
const (
	vtRelease        = 2
	vtBindToHandler  = 3
	vtNext           = 3 // IEnumShellItems
	vtGetDisplayName = 5
	vtGetString      = 17 // IShellItem2
	vtGetImage       = 3  // IShellItemImageFactory
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

// native converts an address returned by Windows to a pointer without
// tripping vet's unsafeptr check; the memory is not managed by Go.
func native(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }

func release(obj uintptr) {
	if obj != 0 {
		comCall(obj, vtRelease)
	}
}

func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func hresultError(what string, hr uintptr) error {
	return fmt.Errorf("shell: %s failed (HRESULT %#08x)", what, uint32(hr))
}

// takeString reads a string allocated with CoTaskMemAlloc and frees it.
func takeString(p uintptr) string {
	if p == 0 {
		return ""
	}
	s := windows.UTF16PtrToString((*uint16)(native(p)))
	procCoTaskMemFree.Call(p)
	return s
}

// withCOM runs fn on a locked OS thread with COM initialized as a
// single-threaded apartment, so callers need no message loop of their own.
func withCOM(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const coinitApartmentThreaded, coinitDisableOLE1DDE = 0x2, 0x4
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	if !failed(hr) { // S_OK or S_FALSE; RPC_E_CHANGED_MODE leaves COM usable
		defer procCoUninitialize.Call()
	}
	return fn()
}

// getString reads a string property of an IShellItem2, or "".
func getString(item2 uintptr, key *propertyKey) string {
	var p uintptr
	if failed(comCall(item2, vtGetString, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&p)))) {
		return ""
	}
	return takeString(p)
}

// InstalledApps lists the Start menu's "All apps": the shell's Applications
// folder (FOLDERID_AppsFolder), desktop and packaged apps alike, resolved to
// their targets and shortcuts. Uninstallers and help, readme and website
// links are left out; the result is deduplicated and sorted by name.
func InstalledApps(ctx context.Context) ([]App, error) {
	var apps []App
	err := withCOM(func() error {
		links := scanStartMenu(ctx)
		var folder uintptr
		if r, _, _ := procSHGetKnownFolderItem.Call(uintptr(unsafe.Pointer(&folderIDAppsFolder)), 0, 0, uintptr(unsafe.Pointer(&iidIShellItem2)), uintptr(unsafe.Pointer(&folder))); failed(r) {
			return hresultError("SHGetKnownFolderItem(AppsFolder)", r)
		}
		defer release(folder)
		var items uintptr
		if r := comCall(folder, vtBindToHandler, 0, uintptr(unsafe.Pointer(&bhidEnumItems)), uintptr(unsafe.Pointer(&iidIEnumShellItems)), uintptr(unsafe.Pointer(&items))); failed(r) {
			return hresultError("enumerating the Applications folder", r)
		}
		defer release(items)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			var item uintptr
			var fetched uint32
			if r := comCall(items, vtNext, 1, uintptr(unsafe.Pointer(&item)), uintptr(unsafe.Pointer(&fetched))); r != 0 || fetched == 0 {
				break // S_FALSE at the end
			}
			a := readApp(item)
			release(item)
			if a.Name == "" || a.ID == "" {
				continue
			}
			links.resolve(&a)
			apps = append(apps, a)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	apps = clean(apps)
	winDir, _ := windows.GetWindowsDirectory()
	addKeywords(apps, winDir, func(exe string) []string {
		if ctx.Err() != nil {
			return nil
		}
		return versionNames(exe)
	})
	return apps, ctx.Err()
}

// versionNames returns the FileDescription and ProductName of every
// language of the version information of exe.
func versionNames(exe string) []string {
	n, err := windows.GetFileVersionInfoSize(exe, nil)
	if err != nil || n == 0 {
		return nil
	}
	buf := make([]byte, n)
	block := unsafe.Pointer(&buf[0])
	if windows.GetFileVersionInfo(exe, 0, n, block) != nil {
		return nil
	}
	var p unsafe.Pointer
	var size uint32
	if windows.VerQueryValue(block, `\VarFileInfo\Translation`, unsafe.Pointer(&p), &size) != nil || size < 4 {
		return nil
	}
	langs := unsafe.Slice((*uint16)(p), size/2)
	var names []string
	for i := 0; i+1 < len(langs); i += 2 {
		for _, key := range []string{"FileDescription", "ProductName"} {
			var s unsafe.Pointer
			var l uint32
			sub := fmt.Sprintf(`\StringFileInfo\%04x%04x\%s`, langs[i], langs[i+1], key)
			if windows.VerQueryValue(block, sub, unsafe.Pointer(&s), &l) == nil && l > 0 {
				names = append(names, windows.UTF16PtrToString((*uint16)(s)))
			}
		}
	}
	return names
}

// readApp reads an item of the Applications folder.
func readApp(item uintptr) App {
	const (
		sigdnNormalDisplay         = 0
		sigdnParentRelativeParsing = 0x80018001
	)
	var name, id uintptr
	comCall(item, vtGetDisplayName, sigdnNormalDisplay, uintptr(unsafe.Pointer(&name)))
	comCall(item, vtGetDisplayName, sigdnParentRelativeParsing, uintptr(unsafe.Pointer(&id)))
	a := App{Name: takeString(name), ID: takeString(id)}
	var item2 uintptr
	iidPtr := uintptr(unsafe.Pointer(&iidIShellItem2))
	if failed(comCall(item, 0, iidPtr, uintptr(unsafe.Pointer(&item2)))) || item2 == 0 { // QueryInterface
		return a
	}
	defer release(item2)
	if getString(item2, &pkeyPackageFamilyName) != "" {
		a.Packaged = true
		return a
	}
	a.Path = strings.Trim(getString(item2, &pkeyLinkTargetParsingPath), `"`)
	if strings.HasPrefix(a.Path, "::") { // a shell namespace object, e.g. Control Panel
		a.Path = ""
	}
	a.Args = getString(item2, &pkeyLinkArguments)
	if a.Path == "" && filepath.IsAbs(a.ID) {
		a.Path = a.ID
	}
	return a
}

// shortcut is a .lnk file of the Start menu.
type shortcut struct {
	path, name, target, args string
}

// startMenu indexes the Start menu shortcuts by target and by name.
type startMenu struct {
	byTarget map[string][]shortcut
	byName   map[string][]shortcut
}

func targetKey(target, args string) string {
	return strings.ToLower(strings.Trim(target, `"`)) + "\x00" + args
}

// scanStartMenu reads the shortcuts of the user's and the common Start menu.
func scanStartMenu(ctx context.Context) startMenu {
	m := startMenu{byTarget: map[string][]shortcut{}, byName: map[string][]shortcut{}}
	for _, id := range []*windows.KNOWNFOLDERID{windows.FOLDERID_StartMenu, windows.FOLDERID_CommonStartMenu} {
		root, err := windows.KnownFolderPath(id, 0)
		if err != nil {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return filepath.SkipAll
			}
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".lnk") {
				return nil
			}
			s := shortcut{path: path, name: strings.ToLower(strings.TrimSuffix(d.Name(), filepath.Ext(path)))}
			s.target, s.args = readShortcut(path)
			if s.target != "" {
				k := targetKey(s.target, s.args)
				m.byTarget[k] = append(m.byTarget[k], s)
			}
			m.byName[s.name] = append(m.byName[s.name], s)
			return nil
		})
	}
	return m
}

// readShortcut returns the target and arguments of a .lnk file.
func readShortcut(path string) (target, args string) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", ""
	}
	var item2 uintptr
	if r, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&iidIShellItem2)), uintptr(unsafe.Pointer(&item2))); failed(r) || item2 == 0 {
		return "", ""
	}
	defer release(item2)
	return strings.Trim(getString(item2, &pkeyLinkTargetParsingPath), `"`), getString(item2, &pkeyLinkArguments)
}

// resolve sets a's LinkPath from the Start menu shortcuts, and its Path to
// the shortcut when its target is unknown.
func (m startMenu) resolve(a *App) {
	if a.Packaged {
		return
	}
	name := strings.ToLower(a.Name)
	var pick *shortcut
	if a.Path != "" {
		if c := m.byTarget[targetKey(a.Path, a.Args)]; len(c) > 0 {
			pick = &c[0]
			for i := range c {
				if c[i].name == name {
					pick = &c[i]
					break
				}
			}
		}
	}
	if pick == nil {
		for i, s := range m.byName[name] {
			if a.Path == "" || s.target == "" || strings.EqualFold(s.target, a.Path) {
				pick = &m.byName[name][i]
				break
			}
		}
	}
	if pick == nil {
		return
	}
	a.LinkPath = pick.path
	if a.Path == "" {
		a.Path = cmp.Or(pick.target, pick.path)
		if pick.target != "" {
			a.Args = pick.args
		}
	}
}

// IconPNG draws the icon the Start menu shows for app as a size×size PNG,
// through IShellItemImageFactory. It tries the Applications folder item
// first, then the shortcut and the target.
func IconPNG(app App, size int) ([]byte, error) {
	if size <= 0 {
		return nil, fmt.Errorf("shell: invalid icon size %d", size)
	}
	var paths []string
	if app.ID != "" {
		paths = append(paths, app.ShellPath())
	}
	for _, p := range []string{app.LinkPath, app.Path} {
		if p != "" && !app.Packaged {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("shell: app has no ID or path")
	}
	var out []byte
	err := withCOM(func() error {
		var errs []error
		for _, p := range paths {
			b, err := iconOf(p, size)
			if err == nil {
				out = b
				return nil
			}
			errs = append(errs, err)
		}
		return errors.Join(errs...)
	})
	return out, err
}

// iconOf draws the icon of a shell item; COM must be initialized.
func iconOf(path string, size int) ([]byte, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var factory uintptr
	if r, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&iidIShellItemImageFactory)), uintptr(unsafe.Pointer(&factory))); failed(r) || factory == 0 {
		return nil, hresultError(fmt.Sprintf("finding %q", path), r)
	}
	defer release(factory)
	const siigbfIconOnly = 0x4
	var hbm uintptr
	sz := uintptr(uint32(size)) | uintptr(uint32(size))<<32 // SIZE, passed by value
	if r := comCall(factory, vtGetImage, sz, siigbfIconOnly, uintptr(unsafe.Pointer(&hbm))); failed(r) || hbm == 0 {
		return nil, hresultError(fmt.Sprintf("the icon of %q", path), r)
	}
	defer procDeleteObject.Call(hbm)
	img, err := imageOfDIB(hbm)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// dibSection is DIBSECTION, which GetObject fills for a DIB section.
type dibSection struct {
	bmType, bmWidth, bmHeight, bmWidthBytes int32
	bmPlanes, bmBitsPixel                   uint16
	bmBits                                  uintptr
	biSize                                  uint32
	biWidth, biHeight                       int32
	rest                                    [7]uint32 // rest of BITMAPINFOHEADER
	_                                       [3]uint32 // dsBitfields
	_                                       uintptr   // dshSection
	_                                       uint32    // dsOffset
}

// imageOfDIB copies a 32-bit DIB section of the shell, whose pixels are BGRA
// with straight alpha, into an NRGBA image.
func imageOfDIB(hbm uintptr) (*image.NRGBA, error) {
	var ds dibSection
	if r, _, _ := procGetObjectW.Call(hbm, unsafe.Sizeof(ds), uintptr(unsafe.Pointer(&ds))); r == 0 || ds.bmBits == 0 || ds.bmBitsPixel != 32 {
		return nil, fmt.Errorf("shell: the icon is not a 32-bit DIB section (%d bits)", ds.bmBitsPixel)
	}
	w, h, stride := int(ds.bmWidth), int(ds.bmHeight), int(ds.bmWidthBytes)
	src := unsafe.Slice((*byte)(native(ds.bmBits)), stride*h)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		row := y
		if ds.biHeight > 0 { // bottom-up
			row = h - 1 - y
		}
		s := src[row*stride:]
		d := img.Pix[y*img.Stride:]
		for x := range w {
			d[4*x], d[4*x+1], d[4*x+2], d[4*x+3] = s[4*x+2], s[4*x+1], s[4*x], s[4*x+3]
		}
	}
	return img, nil
}

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize        uint32
	mask          uint32
	hwnd          uintptr
	verb          *uint16
	file          *uint16
	parameters    *uint16
	directory     *uint16
	show          int32
	instApp       uintptr
	idList        uintptr
	class         *uint16
	keyClass      uintptr
	hotKey        uint32
	iconOrMonitor uintptr
	process       uintptr
}

// Open opens target through ShellExecuteExW: a file, a folder, a URL, an
// executable, or a packaged app as shell:AppsFolder\<AUMID>. opts.Verb
// "runas" runs it elevated; opts.Reveal selects target in File Explorer
// instead of opening it.
func Open(target string, opts plugin.OpenOptions) error {
	if target == "" {
		return errors.New("shell: empty target")
	}
	file, params, verb := target, opts.Args, opts.Verb
	if opts.Reveal {
		file, params, verb = "explorer.exe", `/select,"`+target+`"`, "open"
	}
	const (
		seeMaskNoAsync  = 0x100
		seeMaskFlagNoUI = 0x400
		swShowNormal    = 1
	)
	info := shellExecuteInfo{mask: seeMaskNoAsync | seeMaskFlagNoUI, show: swShowNormal}
	info.cbSize = uint32(unsafe.Sizeof(info))
	var err error
	if info.file, err = windows.UTF16PtrFromString(file); err != nil {
		return err
	}
	for _, f := range []struct {
		dst **uint16
		s   string
	}{{&info.verb, verb}, {&info.parameters, params}, {&info.directory, opts.Dir}} {
		if f.s == "" {
			continue
		}
		if *f.dst, err = windows.UTF16PtrFromString(f.s); err != nil {
			return err
		}
	}
	return withCOM(func() error {
		if r, _, e := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
			return fmt.Errorf("shell: cannot open %s: %w", target, e)
		}
		return nil
	})
}
