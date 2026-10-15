package winget

import (
	"errors"
	"fmt"
	"slices"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procGetPackagesByPackageFamily = kernel32.NewProc("GetPackagesByPackageFamily")
	procGetPackagePathByFullName   = kernel32.NewProc("GetPackagePathByFullName")
)

// packageFullName returns the full name of the installed package of the
// MSIX package family (the newest one if several are installed), e.g.
// "Microsoft.Winget.Source_2026.1.1.1_neutral__8wekyb3d8bbwe".
func packageFullName(family string) (string, error) {
	if err := procGetPackagesByPackageFamily.Find(); err != nil {
		return "", err
	}
	fam, err := windows.UTF16PtrFromString(family)
	if err != nil {
		return "", err
	}
	var count, bufLen uint32
	r, _, _ := procGetPackagesByPackageFamily.Call(uintptr(unsafe.Pointer(fam)), uintptr(unsafe.Pointer(&count)), 0, uintptr(unsafe.Pointer(&bufLen)), 0)
	if r != 0 && windows.Errno(r) != windows.ERROR_INSUFFICIENT_BUFFER {
		return "", fmt.Errorf("GetPackagesByPackageFamily %s: %w", family, windows.Errno(r))
	}
	if count == 0 || bufLen == 0 {
		return "", fmt.Errorf("package family %s: %w", family, errNotInstalled)
	}
	names := make([]*uint16, count)
	buf := make([]uint16, bufLen)
	r, _, _ = procGetPackagesByPackageFamily.Call(uintptr(unsafe.Pointer(fam)), uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&names[0])), uintptr(unsafe.Pointer(&bufLen)), uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return "", fmt.Errorf("GetPackagesByPackageFamily %s: %w", family, windows.Errno(r))
	}
	var full []string
	for _, n := range names[:count] {
		full = append(full, windows.UTF16PtrToString(n))
	}
	slices.Sort(full)
	return full[len(full)-1], nil
}

// packagePath returns the install folder of the package fullName.
func packagePath(fullName string) (string, error) {
	if err := procGetPackagePathByFullName.Find(); err != nil {
		return "", err
	}
	name, err := windows.UTF16PtrFromString(fullName)
	if err != nil {
		return "", err
	}
	var n uint32
	r, _, _ := procGetPackagePathByFullName.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&n)), 0)
	if r != 0 && windows.Errno(r) != windows.ERROR_INSUFFICIENT_BUFFER {
		return "", fmt.Errorf("GetPackagePathByFullName %s: %w", fullName, windows.Errno(r))
	}
	if n == 0 {
		return "", errors.New("GetPackagePathByFullName: empty path")
	}
	buf := make([]uint16, n)
	r, _, _ = procGetPackagePathByFullName.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return "", fmt.Errorf("GetPackagePathByFullName %s: %w", fullName, windows.Errno(r))
	}
	return windows.UTF16ToString(buf), nil
}
