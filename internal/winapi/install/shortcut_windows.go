package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/eziq1/golauncher/internal/install"
)

// The COM helpers follow internal/winapi/shell.

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
)

var (
	clsidShellLink  = mustGUID("{00021401-0000-0000-C000-000000000046}")
	iidIShellLinkW  = mustGUID("{000214F9-0000-0000-C000-000000000046}")
	iidIPersistFile = mustGUID("{0000010b-0000-0000-C000-000000000046}")
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

// Vtable indexes of IUnknown, IShellLinkW and IPersistFile methods.
const (
	vtQueryInterface  = 0
	vtRelease         = 2
	vtGetPath         = 3
	vtSetDescription  = 7
	vtSetWorkingDir   = 9
	vtSetArguments    = 11
	vtSetIconLocation = 17
	vtSetPath         = 20
	vtPersistLoad     = 5
	vtPersistSave     = 6
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
	return fmt.Errorf("install: %s failed (HRESULT %#08x)", what, uint32(hr))
}

// withCOM runs fn on a locked OS thread with COM initialized.
func withCOM(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const coinitApartmentThreaded, coinitDisableOLE1DDE = 0x2, 0x4
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	if !failed(hr) {
		defer procCoUninitialize.Call()
	}
	return fn()
}

// shellLink creates a ShellLink object and its IPersistFile; COM must be
// initialized. The caller releases both.
func shellLink() (link, file uintptr, err error) {
	const clsctxInprocServer = 0x1
	if r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIShellLinkW)), uintptr(unsafe.Pointer(&link))); failed(r) || link == 0 {
		return 0, 0, hresultError("CoCreateInstance(ShellLink)", r)
	}
	if r := comCall(link, vtQueryInterface, uintptr(unsafe.Pointer(&iidIPersistFile)), uintptr(unsafe.Pointer(&file))); failed(r) || file == 0 {
		release(link)
		return 0, 0, hresultError("IShellLink.QueryInterface(IPersistFile)", r)
	}
	return link, file, nil
}

// CreateShortcut implements install.System.
func (s *System) CreateShortcut(path string, sc install.Shortcut) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return withCOM(func() error {
		link, file, err := shellLink()
		if err != nil {
			return err
		}
		defer release(link)
		defer release(file)
		for _, set := range []struct {
			method int
			name   string
			value  string
		}{
			{vtSetPath, "SetPath", sc.Target},
			{vtSetArguments, "SetArguments", sc.Args},
			{vtSetWorkingDir, "SetWorkingDirectory", sc.Dir},
			{vtSetDescription, "SetDescription", sc.Description},
		} {
			if set.value == "" {
				continue
			}
			p, err := windows.UTF16PtrFromString(set.value)
			if err != nil {
				return err
			}
			if r := comCall(link, set.method, uintptr(unsafe.Pointer(p))); failed(r) {
				return hresultError("IShellLink."+set.name, r)
			}
		}
		if sc.Icon != "" {
			p, err := windows.UTF16PtrFromString(sc.Icon)
			if err != nil {
				return err
			}
			if r := comCall(link, vtSetIconLocation, uintptr(unsafe.Pointer(p)), 0); failed(r) {
				return hresultError("IShellLink.SetIconLocation", r)
			}
		}
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		if r := comCall(file, vtPersistSave, uintptr(unsafe.Pointer(p)), 1); failed(r) {
			return hresultError(fmt.Sprintf("saving %s", path), r)
		}
		return nil
	})
}

// ShortcutTarget implements install.System.
func (s *System) ShortcutTarget(path string) (string, error) {
	var target string
	err := withCOM(func() error {
		link, file, err := shellLink()
		if err != nil {
			return err
		}
		defer release(link)
		defer release(file)
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return err
		}
		const stgmRead = 0
		if r := comCall(file, vtPersistLoad, uintptr(unsafe.Pointer(p)), stgmRead); failed(r) {
			return hresultError(fmt.Sprintf("loading %s", path), r)
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		if r := comCall(link, vtGetPath, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0); failed(r) {
			return hresultError("IShellLink.GetPath", r)
		}
		target = windows.UTF16ToString(buf)
		return nil
	})
	return target, err
}
