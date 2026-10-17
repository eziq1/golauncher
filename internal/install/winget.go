package install

import (
	"path/filepath"
	"strings"
)

// WingetID is golauncher's package in the winget community repository
// (docs/winget-packaging.md).
const WingetID = "eziq1.golauncher"

// WingetUpgrade is the command that updates a copy installed with winget.
const WingetUpgrade = "winget upgrade " + WingetID

// WingetManaged reports whether exe (with links resolved) was installed by
// winget as a portable package. winget unpacks a portable package into
// <root>\WinGet\Packages\<id>_<source> (root is %LOCALAPPDATA%\Microsoft
// for a user install, %ProgramFiles% or %ProgramFiles(x86)% for a machine
// one) and puts links to its programs into WinGet\Links, so exe is in such
// a folder, or in one of its subfolders. Installed with --location, the
// package folder is the user's; locations are the InstallLocation values of
// the uninstall entries winget wrote for golauncher, and exe in one of them
// counts too.
func WingetManaged(exe string, locations []string) bool {
	if exe == "" {
		return false
	}
	exe = filepath.Clean(exe)
	for dir := filepath.Dir(exe); ; {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		if isWingetPackageDir(dir) {
			return true
		}
		dir = parent
	}
	for _, loc := range locations {
		if loc != "" && within(exe, loc) {
			return true
		}
	}
	return false
}

// isWingetPackageDir reports whether dir is ...\WinGet\Packages\eziq1.golauncher_<source>.
func isWingetPackageDir(dir string) bool {
	name := filepath.Base(dir)
	if len(name) <= len(WingetID)+1 || !strings.EqualFold(name[:len(WingetID)+1], WingetID+"_") {
		return false
	}
	packages := filepath.Dir(dir)
	return strings.EqualFold(filepath.Base(packages), "Packages") &&
		strings.EqualFold(filepath.Base(filepath.Dir(packages)), "WinGet")
}

// within reports whether path is inside dir (ignoring case, as Windows
// paths are compared).
func within(path, dir string) bool {
	dir = filepath.Clean(dir)
	rel, err := filepath.Rel(strings.ToLower(dir), strings.ToLower(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
