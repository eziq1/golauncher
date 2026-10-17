// Package tcc tells which macOS privacy permissions (TCC: Transparency,
// Consent and Control) golauncher has, and opens their pages in System
// Settings (docs/macos-port.md §4.4, §10): Accessibility, Automation of
// Finder and Full Disk Access. Nothing here shows a system prompt except
// RequestAutomation and RequestAccessibility.
//
// macOS records a permission for the program responsible for this
// process: the .app, or the terminal golauncher was started from.
//
// The status mappings are plain Go and tested on every platform; on other
// platforms every permission is Unknown and OpenSettings fails.
package tcc

import (
	"errors"
	"io/fs"
	"os"
	"strings"
)

// ID names a permission.
type ID string

// The permissions golauncher uses.
const (
	// Accessibility: pasting into the previous app (⌘V), reading the
	// selected text and other apps' window titles and documents.
	Accessibility ID = "accessibility"
	// AutomationFinder: Automation (Apple Events) of Finder, for its
	// current folder and selection.
	AutomationFinder ID = "automationFinder"
	// FullDiskAccess: reading the size of the Trash. Optional.
	FullDiskAccess ID = "fullDiskAccess"
)

// All lists the permissions in the order the settings show them.
var All = []ID{Accessibility, AutomationFinder, FullDiskAccess}

// Optional reports whether golauncher works well without permission id.
func Optional(id ID) bool { return id == FullDiskAccess }

// Status is whether a permission is granted.
type Status int

// Statuses.
const (
	// Unknown: it could not be told; macOS asks on first use if needed.
	Unknown Status = iota
	// Granted: allowed.
	Granted
	// Denied: not allowed (for Accessibility and Full Disk Access also
	// "not allowed yet": macOS does not tell the difference).
	Denied
	// NotDetermined: macOS has not asked yet and will on first use.
	NotDetermined
)

func (s Status) String() string {
	switch s {
	case Granted:
		return "granted"
	case Denied:
		return "denied"
	case NotDetermined:
		return "notDetermined"
	}
	return "unknown"
}

// FinderBundleID is Finder's bundle identifier.
const FinderBundleID = "com.apple.finder"

// SettingsURL is the System Settings page of permission id.
func SettingsURL(id ID) string {
	const base = "x-apple.systempreferences:com.apple.preference.security?"
	switch id {
	case Accessibility:
		return base + "Privacy_Accessibility"
	case AutomationFinder:
		return base + "Privacy_Automation"
	case FullDiskAccess:
		return base + "Privacy_AllFiles"
	}
	return ""
}

// errUnknownID is returned for an ID that is not one of All.
var errUnknownID = errors.New("tcc: unknown permission")

// OSStatus codes of AEDeterminePermissionToAutomateTarget.
const (
	noErr                             = 0
	errAEEventNotPermitted            = -1743
	errAEEventWouldRequireUserConsent = -1744
	procNotFound                      = -600 // the target app is not running
)

// automationStatus maps the result of AEDeterminePermissionToAutomateTarget
// to a Status.
func automationStatus(code int32) Status {
	switch code {
	case noErr:
		return Granted
	case errAEEventNotPermitted:
		return Denied
	case errAEEventWouldRequireUserConsent:
		return NotDetermined
	}
	return Unknown // procNotFound and anything else
}

// diskAccessStatus maps the error of reading a folder only Full Disk
// Access opens (~/.Trash) to a Status.
func diskAccessStatus(err error) Status {
	switch {
	case err == nil:
		return Granted
	case errors.Is(err, fs.ErrPermission):
		return Denied
	}
	return Unknown // missing, or another failure
}

// accessibilityStatus maps AXIsProcessTrusted to a Status.
func accessibilityStatus(trusted bool) Status {
	if trusted {
		return Granted
	}
	return Denied
}

// Terminal returns the name of the terminal app golauncher was started
// from, which then holds its permissions, or "" when it was not started
// from a terminal (or the terminal is not known by name).
func Terminal() string {
	return terminalName(os.Getenv("TERM_PROGRAM"), os.Getenv("__CFBundleIdentifier"))
}

// terminalName names the terminal from $TERM_PROGRAM, or else the bundle
// identifier of the app that started this process (__CFBundleIdentifier).
func terminalName(program, bundleID string) string {
	switch program {
	case "Apple_Terminal":
		return "Terminal"
	case "iTerm.app":
		return "iTerm2"
	case "WarpTerminal":
		return "Warp"
	case "vscode":
		return "Visual Studio Code"
	case "ghostty":
		return "Ghostty"
	case "WezTerm":
		return "WezTerm"
	case "":
	default:
		return program
	}
	switch bundleID {
	case "com.apple.Terminal":
		return "Terminal"
	case "com.googlecode.iterm2":
		return "iTerm2"
	}
	if strings.HasPrefix(bundleID, "com.mitchellh.ghostty") {
		return "Ghostty"
	}
	return ""
}
