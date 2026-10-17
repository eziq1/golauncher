package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The macOS app bundle (docs/self-install.md §9). On macOS golauncher is
// installed as golauncher.app; an install location is the folder that
// holds it (/Applications or ~/Applications), and the bundle is copied and
// replaced as a whole, so that its code signature stays intact.
const (
	// BundleName is the app bundle's folder name.
	BundleName = AppName + ".app"
	// BundleID is the bundle identifier (CFBundleIdentifier): the reverse
	// DNS name of the repository, github.com/eziq1/golauncher. Privacy
	// grants (TCC), the login item and the keychain know the app by it.
	BundleID = "io.github.eziq1.golauncher"
	// BundleExeName and BundleCtlName are the programs in Contents/MacOS.
	BundleExeName = AppName
	BundleCtlName = "glctl"
	// VersionKey is the Info.plist key holding golauncher's own version
	// string, which CFBundleShortVersionString (numbers only) cannot.
	VersionKey = "GolauncherVersion"
	// MinSystemVersion is LSMinimumSystemVersion: macOS 13 Ventura, the
	// first with SMAppService (docs/macos-port.md §6.1).
	MinSystemVersion = "13.0"
)

// Names an upgrade gives the bundles beside the installed one while it
// swaps them; hidden, so that Finder does not show them meanwhile.
const (
	bundleNew = "." + BundleName + ".new"
	bundleOld = "." + BundleName + ".old"
)

// BundleIn returns the app bundle in an install location.
func BundleIn(dir string) string { return filepath.Join(dir, BundleName) }

// BundleExe returns the executable of an app bundle.
func BundleExe(app string) string { return filepath.Join(app, "Contents", "MacOS", BundleExeName) }

// BundleOf returns the app bundle an executable is in:
// "<x>.app/Contents/MacOS/<exe>" gives "<x>.app". ok is false for an
// executable outside a bundle.
func BundleOf(exe string) (app string, ok bool) {
	exe = filepath.Clean(exe)
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app = filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(app, ".app") || filepath.Base(app) == ".app" {
		return "", false
	}
	return app, true
}

// ChooseAppsDir returns the default install location on macOS: system
// (/Applications) when it is writable, else user (~/Applications), which
// needs no administrator. writable reports whether a folder may be
// written to.
func ChooseAppsDir(system, user string, writable func(string) bool) string {
	if system != "" && writable(system) {
		return system
	}
	return user
}

// Translocated reports whether path is under App Translocation, the
// read-only random location macOS runs a quarantined app from when it was
// opened where it was downloaded; such a copy must be installed (copied)
// before it can update itself.
func Translocated(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/AppTranslocation/")
}

// ReplaceBundle copies the app bundle src to dst, replacing what is
// there: the copy is made beside dst, the old bundle is renamed aside and
// the copy renamed into place, so that dst is never half written. undo puts
// the old bundle back (or removes dst when there was none); commit deletes
// the old bundle. Extended attributes are not copied, so the copy carries
// no quarantine.
func ReplaceBundle(src, dst string) (undo, commit func() error, err error) {
	dir := filepath.Dir(dst)
	tmp, old := filepath.Join(dir, bundleNew), filepath.Join(dir, bundleOld)
	if err := os.RemoveAll(tmp); err != nil {
		return nil, nil, err
	}
	if err := CopyTree(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, nil, err
	}
	hadOld := exists(dst)
	if hadOld {
		if err := os.RemoveAll(old); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, nil, err
		}
		if err := os.Rename(dst, old); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, nil, err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		if hadOld {
			_ = os.Rename(old, dst)
		}
		_ = os.RemoveAll(tmp)
		return nil, nil, err
	}
	undo = func() error {
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
		if hadOld {
			return os.Rename(old, dst)
		}
		return nil
	}
	commit = func() error { return os.RemoveAll(old) }
	return undo, commit, nil
}

// CopyTree copies the folder src to dst, which must not exist: folders,
// regular files with their permissions, and symbolic links as links. Links
// must stay inside src.
func CopyTree(src, dst string) error {
	src = filepath.Clean(src)
	if fi, err := os.Stat(src); err != nil {
		return err
	} else if !fi.IsDir() {
		return fmt.Errorf("install: %s is not a folder", src)
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch mode := info.Mode(); {
		case mode.IsDir():
			if rel == "." {
				return os.Mkdir(target, 0o755)
			}
			return os.Mkdir(target, mode.Perm()|0o700)
		case mode&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) || !filepath.IsLocal(filepath.Join(filepath.Dir(rel), link)) {
				return fmt.Errorf("install: %s links outside the bundle", path)
			}
			return os.Symlink(link, target)
		case mode.IsRegular():
			return copyMode(path, target, mode.Perm())
		default:
			return fmt.Errorf("install: %s is not a file or folder", path)
		}
	})
}

// copyMode copies a file with the permissions perm and flushes it.
func copyMode(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Sync(), out.Close())
}

// TreeSize returns the total size of the regular files under dir.
func TreeSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}
