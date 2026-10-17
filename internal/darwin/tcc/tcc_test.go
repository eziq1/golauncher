package tcc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestAutomationStatus(t *testing.T) {
	cases := []struct {
		code int32
		want Status
	}{
		{0, Granted},
		{-1743, Denied},
		{-1744, NotDetermined},
		{-600, Unknown}, // Finder is not running
		{-50, Unknown},
	}
	for _, c := range cases {
		if got := automationStatus(c.code); got != c.want {
			t.Errorf("automationStatus(%d) = %v, want %v", c.code, got, c.want)
		}
	}
}

func TestDiskAccessStatus(t *testing.T) {
	cases := []struct {
		err  error
		want Status
	}{
		{nil, Granted},
		{&fs.PathError{Op: "open", Path: "/Users/a/.Trash", Err: syscall.EPERM}, Denied},
		{&fs.PathError{Op: "open", Path: "/Users/a/.Trash", Err: syscall.EACCES}, Denied},
		{fmt.Errorf("x: %w", fs.ErrPermission), Denied},
		{&fs.PathError{Op: "open", Path: "/Users/a/.Trash", Err: syscall.ENOENT}, Unknown},
		{errors.New("other"), Unknown},
	}
	for _, c := range cases {
		if got := diskAccessStatus(c.err); got != c.want {
			t.Errorf("diskAccessStatus(%v) = %v, want %v", c.err, got, c.want)
		}
	}
	if accessibilityStatus(true) != Granted || accessibilityStatus(false) != Denied {
		t.Error("accessibilityStatus")
	}
}

func TestSettingsURL(t *testing.T) {
	want := map[ID]string{
		Accessibility:    "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility",
		AutomationFinder: "x-apple.systempreferences:com.apple.preference.security?Privacy_Automation",
		FullDiskAccess:   "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles",
	}
	for _, id := range All {
		if got := SettingsURL(id); got != want[id] {
			t.Errorf("SettingsURL(%s) = %q", id, got)
		}
	}
	if SettingsURL("camera") != "" || !errors.Is(OpenSettings("camera"), errUnknownID) {
		t.Error("an unknown permission has a page")
	}
	if !Optional(FullDiskAccess) || Optional(Accessibility) || Optional(AutomationFinder) {
		t.Error("only Full Disk Access is optional")
	}
}

func TestStatusString(t *testing.T) {
	for s, want := range map[Status]string{Unknown: "unknown", Granted: "granted", Denied: "denied", NotDetermined: "notDetermined"} {
		if s.String() != want {
			t.Errorf("%d.String() = %q", s, s.String())
		}
	}
}

func TestTerminalName(t *testing.T) {
	cases := []struct{ program, bundle, want string }{
		{"Apple_Terminal", "", "Terminal"},
		{"iTerm.app", "com.googlecode.iterm2", "iTerm2"},
		{"WarpTerminal", "", "Warp"},
		{"ghostty", "", "Ghostty"},
		{"Hyper", "", "Hyper"},
		{"", "com.apple.Terminal", "Terminal"},
		{"", "com.mitchellh.ghostty", "Ghostty"},
		{"", "", ""},
		{"", "com.example.golauncher", ""},
	}
	for _, c := range cases {
		if got := terminalName(c.program, c.bundle); got != c.want {
			t.Errorf("terminalName(%q, %q) = %q, want %q", c.program, c.bundle, got, c.want)
		}
	}
}

// TestCheckLive reads the real statuses. It never shows a prompt (Check
// does not ask), but it talks to tccd, so it only runs when asked.
func TestCheckLive(t *testing.T) {
	if os.Getenv("GOLAUNCHER_TCC_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_TCC_TESTS=1 to read the real permission statuses")
	}
	var b strings.Builder
	for _, id := range All {
		fmt.Fprintf(&b, "%s=%v ", id, Check(id))
	}
	t.Log(b.String(), "terminal="+Terminal())
}
