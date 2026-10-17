// Package install installs golauncher for the current user, upgrades or
// downgrades an installation and uninstalls it (docs/self-install.md).
//
// It holds the decisions and the file operations; everything else the
// system does (the registry, shortcuts, processes) goes through System, so
// the package is tested with temporary directories and a fake System. The
// Windows implementation is internal/winapi/install.
package install

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Names of the installation.
const (
	// AppName is the name of the app and of its uninstall entry.
	AppName = "golauncher"
	// ExeName and CtlName are the program files.
	ExeName = "golauncher.exe"
	CtlName = "glctl.exe"
	// Publisher is shown in Apps & features.
	Publisher = "eziq1"
	// URL is the app's home page, shown in Apps & features.
	URL = "https://github.com/eziq1/golauncher"
	// UninstallKey is the uninstall entry, under HKEY_CURRENT_USER.
	UninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\golauncher`
	// LinkName is the file name of the shortcuts.
	LinkName = "golauncher.lnk"
)

// EnvNoInstall turns the installer off when set to 1 (tests, CI, E2E).
const EnvNoInstall = "GOLAUNCHER_NO_INSTALL"

// StopTimeout is how long Install and Uninstall wait for a running
// golauncher to quit.
const StopTimeout = 5 * time.Second

// Errors that the installer reports to the user in their own words.
var (
	// ErrStillRunning: a running golauncher did not quit when asked to.
	ErrStillRunning = errors.New("install: golauncher is still running")
	// ErrInvalidDir: the install location is not an absolute folder path.
	ErrInvalidDir = errors.New("install: invalid install location")
	// ErrNotInstalled: there is nothing to uninstall.
	ErrNotInstalled = errors.New("install: golauncher is not installed")
)

// Flags are the installer's command line flags.
type Flags struct {
	Portable  bool // --portable: run this copy without installing
	Uninstall bool // --uninstall: uninstall (Apps & features runs this)
	Quit      bool // --quit: never install, only hand over to the running instance
}

// ParseArgs reads the installer's flags; other arguments are ignored.
func ParseArgs(args []string) Flags {
	var f Flags
	fs := flag.NewFlagSet(AppName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&f.Portable, "portable", false, "")
	fs.BoolVar(&f.Uninstall, "uninstall", false, "")
	fs.BoolVar(&f.Quit, "quit", false, "")
	for rest := args; len(rest) > 0; rest = rest[1:] {
		if fs.Parse(rest) == nil {
			break
		}
	}
	return f
}

// Skip reports whether the installer stays out of the way: in development
// builds (version "dev"), with --portable or --quit, or when EnvNoInstall
// is 1.
func Skip(version string, f Flags, getenv func(string) string) bool {
	return version == "" || version == "dev" || f.Portable || f.Quit || getenv(EnvNoInstall) == "1"
}

// Entry is the uninstall entry of an installation.
type Entry struct {
	Name        string // DisplayName
	Version     string // DisplayVersion
	Publisher   string
	Dir         string // InstallLocation
	Icon        string // DisplayIcon
	Uninstall   string // UninstallString
	URL         string // URLInfoAbout
	InstallDate string // YYYYMMDD
	SizeKB      uint32 // EstimatedSize
}

// Shortcut describes a .lnk file.
type Shortcut struct {
	Target      string
	Args        string
	Dir         string // working directory
	Description string
	Icon        string // icon file; its first icon is used
}

// System is what the installer needs from the operating system. Program
// files are copied by the package itself.
type System interface {
	// ReadEntry reads the uninstall entry; ok is false when there is none.
	ReadEntry() (e Entry, ok bool, err error)
	// WriteEntry creates or replaces the uninstall entry.
	WriteEntry(e Entry) error
	// DeleteEntry removes the uninstall entry; a missing one is fine.
	DeleteEntry() error
	// FileVersion reads the product version from an executable's resources.
	FileVersion(exe string) (string, error)
	// CreateShortcut writes a .lnk file, creating its folder.
	CreateShortcut(link string, s Shortcut) error
	// ShortcutTarget returns the target of a .lnk file.
	ShortcutTarget(link string) (string, error)
	// StopInstances asks a running golauncher to quit by running
	// "quitter --quit", then waits until no other golauncher process of
	// this session is left. It returns ErrStillRunning after timeout.
	StopInstances(quitter string, timeout time.Duration) error
	// Start starts exe with args and does not wait for it.
	Start(exe string, args []string) error
	// ScheduleRemoval deletes files and then dir, if empty, shortly after
	// this process exits.
	ScheduleRemoval(dir string, files []string) error
}

// Paths are the locations an installation uses.
type Paths struct {
	DefaultDir    string   // the suggested install location
	StartMenuLink string   // the Start menu shortcut
	DesktopLink   string   // the desktop shortcut
	ConfigFile    string   // config.json, where launch at login is set
	DataDirs      []string // the user's settings and data, removed on request
	// UpdateDir holds the downloads and the backup of automatic updates
	// (docs/auto-update.md); it is not the user's data and is always
	// removed.
	UpdateDir string
}

// Installer installs the running copy of golauncher.
type Installer struct {
	Sys     System
	Self    string // the running executable
	Version string // the running version
	Paths   Paths
	// RemoveLoginItem turns launching at login off; Uninstall calls it.
	RemoveLoginItem func() error
	// Now returns the time (default time.Now).
	Now func() time.Time
	// Bundle is set on macOS: the program is the app bundle golauncher.app
	// that Self is in, an install location is the folder holding the
	// bundle, and the bundle is copied and replaced as a whole.
	Bundle bool
}

// ExeIn returns the program of an installation in dir.
func (in *Installer) ExeIn(dir string) string {
	if in.Bundle {
		return BundleExe(BundleIn(dir))
	}
	return filepath.Join(dir, ExeName)
}

// Kind is what Detect decides.
type Kind int

const (
	// KindRun starts normally: this is the installed copy.
	KindRun Kind = iota
	// KindFresh offers to install: nothing is installed.
	KindFresh
	// KindSame starts the installed copy, which has the running version.
	KindSame
	// KindChange offers to upgrade or downgrade the installation.
	KindChange
)

// State is the installation as Detect finds it.
type State struct {
	Kind Kind
	// Installed is set when an installation exists, and then Dir, Exe and
	// Version describe it.
	Installed bool
	Dir       string
	Exe       string
	Version   string
	// Self is set when the running executable is the installed one.
	Self bool
	// Order compares the running version to the installed one (-1 older,
	// +1 newer) when Ordered; otherwise they differ in an unknown way.
	Order   int
	Ordered bool
}

// Portable reports whether this copy runs although another one is
// installed; it must then leave the installation's login item alone.
func (s State) Portable() bool { return s.Installed && !s.Self }

// Detect looks for an installation and compares it with the running copy.
// An uninstall entry whose program is gone counts as no installation.
func (in *Installer) Detect() (State, error) {
	e, ok, err := in.Sys.ReadEntry()
	if err != nil {
		return State{}, err
	}
	if !ok || e.Dir == "" || !isFile(in.ExeIn(e.Dir)) {
		return State{Kind: KindFresh}, nil
	}
	st := State{Installed: true, Dir: e.Dir, Exe: in.ExeIn(e.Dir), Version: e.Version}
	if st.Version == "" {
		st.Version, _ = in.Sys.FileVersion(st.Exe)
	}
	switch {
	case SamePath(st.Exe, in.Self):
		st.Kind, st.Self = KindRun, true
	case SameVersion(in.Version, st.Version):
		st.Kind = KindSame
	default:
		st.Kind = KindChange
		st.Order, st.Ordered = CompareVersions(in.Version, st.Version)
	}
	return st, nil
}

// SamePath reports whether a and b name the same file: the same path
// (ignoring case), or the same existing file reached another way (short
// names, links).
func SamePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// FolderFor returns the install location for a folder chosen in a dialog:
// the folder itself if it is named golauncher, else its golauncher
// subfolder, so that the program never mixes with other files.
func FolderFor(chosen string) string {
	if chosen == "" || strings.EqualFold(filepath.Base(chosen), AppName) {
		return chosen
	}
	return filepath.Join(chosen, AppName)
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
