package system

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
)

// On macOS the commands run system tools (pmset, osascript, defaults).
// Those that send Apple Events to System Events or Finder (sign out,
// restart, shut down, empty the Trash, the theme) need the user's
// permission under System Settings › Privacy & Security › Automation,
// which macOS asks for the first time.

// run runs a system tool and returns its standard output; an error carries
// its standard error. Tests replace it to record the command lines.
var run = func(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			err = fmt.Errorf("%w: %s", err, msg)
		}
		return out, fmt.Errorf("system: %s: %w", filepath.Base(name), err)
	}
	return out, nil
}

// osascript runs an AppleScript.
func osascript(script string) error {
	_, err := run("/usr/bin/osascript", "-e", script)
	return err
}

// lockScreen locks the screen at once with SACLockScreenImmediate of the
// private login framework, which is what the menu bar's Lock Screen does.
// Tests replace it.
var lockScreen = func() error {
	fn, err := loadLockScreen()
	if err != nil {
		return err
	}
	if r := fn(); r != 0 {
		return fmt.Errorf("system: SACLockScreenImmediate: %d", r)
	}
	return nil
}

var loadLockScreen = sync.OnceValues(func() (func() int32, error) {
	lib, err := purego.Dlopen("/System/Library/PrivateFrameworks/login.framework/Versions/Current/login", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("system: loading login.framework: %w", err)
	}
	var fn func() int32
	purego.RegisterLibFunc(&fn, lib, "SACLockScreenImmediate")
	return fn, nil
})

// Lock locks the screen. Without the private function it turns the
// display off, which locks when "Require password after screen saver
// begins or display is turned off" is set to immediately.
func Lock() error {
	if err := lockScreen(); err == nil {
		return nil
	}
	_, err := run("/usr/bin/pmset", "displaysleepnow")
	return err
}

// Suspend puts the Mac to sleep. There is no hibernation a user can start.
func Suspend(hibernate bool) error {
	if hibernate {
		return errors.ErrUnsupported
	}
	_, err := run("/usr/bin/pmset", "sleepnow")
	return err
}

// ExitWindows logs out, restarts or shuts down through System Events, as
// the Apple menu does: apps may still refuse to quit; nothing is forced.
func ExitWindows(kind ExitKind) error {
	var verb string
	switch kind {
	case Logoff:
		verb = "log out"
	case Reboot:
		verb = "restart"
	case PowerOff:
		verb = "shut down"
	default:
		return fmt.Errorf("system: unknown exit kind %d", kind)
	}
	return osascript(`tell application "System Events" to ` + verb)
}

// trashDirs returns the user's Trash folders: ~/.Trash and the .Trashes
// folder of this user on every mounted volume. Tests replace it.
var trashDirs = func() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".Trash"))
	}
	vols, _ := filepath.Glob("/Volumes/*/.Trashes/" + strconv.Itoa(os.Getuid()))
	return append(dirs, vols...)
}

// QueryRecycleBin returns the number of items and the size of the Trash.
// It only reads. Reading ~/.Trash needs Full Disk Access (or the folder's
// own permission), without which it fails.
func QueryRecycleBin() (RecycleBin, error) {
	var bin RecycleBin
	for i, dir := range trashDirs() {
		b, err := trashContent(dir)
		if err != nil {
			if i == 0 { // the home Trash; the others are best effort
				if errors.Is(err, fs.ErrPermission) {
					return RecycleBin{}, fmt.Errorf("system: reading the Trash needs Full Disk Access: %w", err)
				}
				if !errors.Is(err, fs.ErrNotExist) {
					return RecycleBin{}, fmt.Errorf("system: reading the Trash: %w", err)
				}
			}
			continue
		}
		bin.Items += b.Items
		bin.Size += b.Size
	}
	return bin, nil
}

// trashContent counts the items directly in dir (not .DS_Store) and adds
// up the sizes of all the files below it.
func trashContent(dir string) (RecycleBin, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return RecycleBin{}, err
	}
	var bin RecycleBin
	for _, e := range entries {
		if e.Name() == ".DS_Store" {
			continue
		}
		bin.Items++
		filepath.WalkDir(filepath.Join(dir, e.Name()), func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable: skip what is below
			}
			if d.Type().IsRegular() {
				if info, err := d.Info(); err == nil {
					bin.Size += info.Size()
				}
			}
			return nil
		})
	}
	return bin, nil
}

// EmptyRecycleBin empties the Trash through Finder.
func EmptyRecycleBin() error {
	return osascript(`tell application "Finder" to empty trash`)
}

// DarkMode reports whether the dark appearance is on (AppleInterfaceStyle
// is "Dark"; the key is missing in the light appearance). It only reads.
func DarkMode() (bool, error) {
	out, err := run("/usr/bin/defaults", "read", "-g", "AppleInterfaceStyle")
	return parseInterfaceStyle(out, err)
}

// parseInterfaceStyle reads the result of "defaults read -g
// AppleInterfaceStyle": defaults fails (exit status 1) when the key does
// not exist, which means light.
func parseInterfaceStyle(out []byte, err error) (bool, error) {
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return false, nil
		}
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(string(out)), "Dark"), nil
}

// SetDarkMode switches to the dark or light appearance.
func SetDarkMode(dark bool) error {
	return osascript(`tell application "System Events" to tell appearance preferences to set dark mode to ` + strconv.FormatBool(dark))
}

// PressMediaKey changes the output volume as the volume keys do: mute
// toggles muting, and each press of up or down changes the volume by 2%
// (of 100) and unmutes.
func PressMediaKey(key MediaKey, times int) error {
	var script string
	step := 2 * max(times, 1)
	switch key {
	case VolumeMute:
		script = `set volume output muted (not (output muted of (get volume settings)))`
	case VolumeDown:
		script = fmt.Sprintf(`set volume output volume ((output volume of (get volume settings)) - %d) without output muted`, step)
	case VolumeUp:
		script = fmt.Sprintf(`set volume output volume ((output volume of (get volume settings)) + %d) without output muted`, step)
	default:
		return fmt.Errorf("system: unknown media key %d", key)
	}
	return osascript(script)
}

// DisplayOff puts the displays to sleep. Moving the mouse or pressing a
// key wakes them.
func DisplayOff() error {
	_, err := run("/usr/bin/pmset", "displaysleepnow")
	return err
}

// HibernateAvailable reports whether hibernation can be started; never on
// macOS.
func HibernateAvailable() bool { return false }

// OSBuild returns the Windows build number; 0 on macOS (unknown: no
// Windows Settings page is filtered by it).
func OSBuild() int { return 0 }

// EditionID returns the Windows edition; "" on macOS.
func EditionID() string { return "" }

// MacOSVersion returns the version of macOS, e.g. "15.6" (sw_vers); "" when
// unknown. It only reads.
func MacOSVersion() string {
	out, err := run("/usr/bin/sw_vers", "-productVersion")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
