package install

import (
	"strings"

	"golang.org/x/sys/windows/registry"

	"github.com/eziq1/golauncher/internal/install"
)

// uninstallRoot is where winget writes the uninstall entry of a portable
// package: <id>_<source>, with WinGetPackageIdentifier and InstallLocation
// values.
const uninstallRoot = `Software\Microsoft\Windows\CurrentVersion\Uninstall`

// WingetLocations returns the install locations of the portable packages
// eziq1.golauncher that winget recorded, per user and machine-wide
// (install.WingetManaged).
func WingetLocations() []string {
	var locs []string
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		locs = append(locs, wingetLocations(root, uninstallRoot)...)
	}
	return locs
}

// wingetLocations reads the entries under root\path.
func wingetLocations(root registry.Key, path string) []string {
	k, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var locs []string
	for _, name := range names {
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(install.WingetID)+"_") {
			continue
		}
		sub, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		id, _, _ := sub.GetStringValue("WinGetPackageIdentifier")
		loc, _, _ := sub.GetStringValue("InstallLocation")
		sub.Close()
		if strings.EqualFold(id, install.WingetID) && loc != "" {
			locs = append(locs, loc)
		}
	}
	return locs
}
