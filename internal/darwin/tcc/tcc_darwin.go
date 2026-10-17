//go:build darwin

package tcc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/ebitengine/purego"

	"github.com/eziq1/golauncher/internal/darwin/ax"
)

// Supported reports whether there are permissions to manage: true on macOS.
func Supported() bool { return true }

// Check returns the status of permission id without showing any prompt.
// Automation of Finder is asked with AEDeterminePermissionToAutomateTarget
// (askUserIfNeeded false); it can take a moment, so call Check off the
// main thread.
func Check(id ID) Status {
	switch id {
	case Accessibility:
		return accessibilityStatus(ax.Trusted())
	case AutomationFinder:
		return automation(FinderBundleID, false)
	case FullDiskAccess:
		return diskAccess()
	}
	return Unknown
}

// RequestAutomation asks macOS for the Automation permission of Finder:
// its prompt shows when it has not asked yet. It blocks until the user
// answered (or at once when it had asked before) and returns the status.
func RequestAutomation() Status { return automation(FinderBundleID, true) }

// RequestAccessibility asks macOS to show its prompt offering to open the
// Accessibility settings (once per run), which also lists golauncher (or
// its terminal) there, and returns the status.
func RequestAccessibility() Status { return accessibilityStatus(ax.RequestTrust()) }

// openURL opens a URL with /usr/bin/open; tests replace it.
var openURL = func(url string) error {
	if out, err := exec.Command("/usr/bin/open", url).CombinedOutput(); err != nil {
		return fmt.Errorf("tcc: open %s: %w: %s", url, err, out)
	}
	return nil
}

// OpenSettings opens the System Settings page of permission id.
func OpenSettings(id ID) error {
	u := SettingsURL(id)
	if u == "" {
		return errUnknownID
	}
	return openURL(u)
}

// diskAccess reads ~/.Trash, which only Full Disk Access opens (the Trash
// size of the system plugin needs it).
func diskAccess() Status {
	home, err := os.UserHomeDir()
	if err != nil {
		return Unknown
	}
	f, err := os.Open(filepath.Join(home, ".Trash"))
	if err == nil {
		_, err = f.Readdirnames(1)
		f.Close()
		if errors.Is(err, io.EOF) {
			err = nil // empty
		}
	}
	return diskAccessStatus(err)
}

// aeDesc is AEDesc: a descriptor type and an opaque handle.
type aeDesc struct {
	descriptorType uint32
	dataHandle     uintptr
}

type aeFuncs struct {
	createDesc          func(typeCode uint32, data *byte, size int, result *aeDesc) int16
	disposeDesc         func(desc *aeDesc) int16
	determinePermission func(target *aeDesc, class, id uint32, ask bool) int32
}

var loadAE = sync.OnceValues(func() (*aeFuncs, error) {
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreServices.framework/CoreServices", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return nil, fmt.Errorf("tcc: %w", err)
	}
	f := &aeFuncs{}
	purego.RegisterLibFunc(&f.createDesc, lib, "AECreateDesc")
	purego.RegisterLibFunc(&f.disposeDesc, lib, "AEDisposeDesc")
	purego.RegisterLibFunc(&f.determinePermission, lib, "AEDeterminePermissionToAutomateTarget")
	return f, nil
})

// Four-character codes.
const (
	typeApplicationBundleID = 0x62756e64 // 'bund'
	typeWildCard            = 0x2a2a2a2a // '****'
)

// automation asks whether golauncher may send Apple Events to the app
// bundleID; with ask, macOS shows its prompt if it has not asked yet.
func automation(bundleID string, ask bool) Status {
	f, err := loadAE()
	if err != nil {
		return Unknown
	}
	b := []byte(bundleID)
	var target aeDesc
	if f.createDesc(typeApplicationBundleID, &b[0], len(b), &target) != 0 {
		return Unknown
	}
	defer f.disposeDesc(&target)
	return automationStatus(f.determinePermission(&target, typeWildCard, typeWildCard, ask))
}
