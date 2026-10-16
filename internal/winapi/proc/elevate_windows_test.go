package proc

import (
	"errors"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestElevatedCommand(t *testing.T) {
	const comspec = `C:\Windows\system32\cmd.exe`
	cases := []struct {
		argv         []string
		file, params string
	}{
		{[]string{"ipconfig", "/flushdns"}, "ipconfig", "/flushdns"},
		{[]string{"cmd.exe", "/d", "/c", "ipconfig /release && ipconfig /renew"}, "cmd.exe", `/d /c "ipconfig /release && ipconfig /renew"`},
		{[]string{`C:\Program Files\Tool\tool.exe`, "a b", `say "hi"`, ""}, `C:\Program Files\Tool\tool.exe`, `"a b" "say \"hi\"" ""`},
		{[]string{`C:\My Scripts\fix.cmd`, "a&b"}, comspec, `/d /s /c ""C:\My Scripts\fix.cmd" "a&b""`},
		{[]string{"netsh"}, "netsh", ""},
	}
	for _, c := range cases {
		file, params := ElevatedCommand(c.argv, comspec)
		if file != c.file || params != c.params {
			t.Errorf("ElevatedCommand(%q) = %q %q, want %q %q", c.argv, file, params, c.file, c.params)
		}
		// The parameters are what os/exec puts after the program for the
		// same argv (batch files aside).
		if !IsBatch(c.argv[0]) {
			cmd := exec.Command(c.argv[0], c.argv[1:]...)
			want := windows.ComposeCommandLine(cmd.Args[1:])
			if params != want {
				t.Errorf("%q: params %q, os/exec %q", c.argv, params, want)
			}
		}
	}
}

// TestStartElevatedRefusedInTests: StartElevated never shows a UAC prompt
// from a test binary.
func TestStartElevatedRefusedInTests(t *testing.T) {
	p, err := StartElevated([]string{"ipconfig", "/flushdns"}, "")
	if p != nil || !errors.Is(err, errElevateInTest) {
		t.Fatalf("StartElevated in a test: %v %v", p, err)
	}
	if _, err := StartElevated(nil, ""); err == nil {
		t.Error("no program accepted")
	}
}

func TestElevationRequiredErrno(t *testing.T) {
	if !errors.Is(&exec.Error{Name: "x", Err: windows.ERROR_ELEVATION_REQUIRED}, ErrorElevationRequired) {
		t.Error("ERROR_ELEVATION_REQUIRED is not ErrorElevationRequired")
	}
}
