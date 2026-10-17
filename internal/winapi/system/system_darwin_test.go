package system

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeRun replaces run for the test: it records each command line and
// answers with out and err. Nothing is ever run.
func fakeRun(t *testing.T, out string, err error) *[]string {
	t.Helper()
	var calls []string
	old := run
	run = func(name string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(append([]string{name}, args...), " "))
		return []byte(out), err
	}
	t.Cleanup(func() { run = old })
	return &calls
}

func TestCommands(t *testing.T) {
	oldLock := lockScreen
	t.Cleanup(func() { lockScreen = oldLock })
	lockScreen = func() error { return nil }
	for _, tt := range []struct {
		name string
		do   func() error
		want []string
	}{
		{"lock", Lock, nil},
		{"sleep", func() error { return Suspend(false) }, []string{"/usr/bin/pmset sleepnow"}},
		{"log out", func() error { return ExitWindows(Logoff) }, []string{`/usr/bin/osascript -e tell application "System Events" to log out`}},
		{"restart", func() error { return ExitWindows(Reboot) }, []string{`/usr/bin/osascript -e tell application "System Events" to restart`}},
		{"shut down", func() error { return ExitWindows(PowerOff) }, []string{`/usr/bin/osascript -e tell application "System Events" to shut down`}},
		{"empty trash", EmptyRecycleBin, []string{`/usr/bin/osascript -e tell application "Finder" to empty trash`}},
		{"dark", func() error { return SetDarkMode(true) }, []string{`/usr/bin/osascript -e tell application "System Events" to tell appearance preferences to set dark mode to true`}},
		{"light", func() error { return SetDarkMode(false) }, []string{`/usr/bin/osascript -e tell application "System Events" to tell appearance preferences to set dark mode to false`}},
		{"mute", func() error { return PressMediaKey(VolumeMute, 1) }, []string{`/usr/bin/osascript -e set volume output muted (not (output muted of (get volume settings)))`}},
		{"up", func() error { return PressMediaKey(VolumeUp, 5) }, []string{`/usr/bin/osascript -e set volume output volume ((output volume of (get volume settings)) + 10) without output muted`}},
		{"down", func() error { return PressMediaKey(VolumeDown, 5) }, []string{`/usr/bin/osascript -e set volume output volume ((output volume of (get volume settings)) - 10) without output muted`}},
		{"display off", DisplayOff, []string{"/usr/bin/pmset displaysleepnow"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := fakeRun(t, "", nil)
			if err := tt.do(); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(*calls, tt.want) {
				t.Errorf("ran %q, want %q", *calls, tt.want)
			}
		})
	}

	t.Run("lock fallback", func(t *testing.T) {
		lockScreen = func() error { return errors.New("no login framework") }
		calls := fakeRun(t, "", nil)
		if err := Lock(); err != nil {
			t.Fatal(err)
		}
		if want := []string{"/usr/bin/pmset displaysleepnow"}; !slices.Equal(*calls, want) {
			t.Errorf("ran %q, want %q", *calls, want)
		}
	})
	t.Run("unsupported", func(t *testing.T) {
		calls := fakeRun(t, "", nil)
		if err := Suspend(true); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("hibernate: %v", err)
		}
		if ExitWindows(ExitKind(9)) == nil || PressMediaKey(MediaKey(9), 1) == nil {
			t.Error("unknown kind accepted")
		}
		if len(*calls) != 0 || HibernateAvailable() {
			t.Errorf("ran %q", *calls)
		}
	})
	t.Run("failure", func(t *testing.T) {
		fakeRun(t, "", errors.New("Not authorized to send Apple events to Finder."))
		if err := EmptyRecycleBin(); err == nil {
			t.Error("no error")
		}
	})
}

func TestParseInterfaceStyle(t *testing.T) {
	exitErr := exec.Command("/usr/bin/false").Run()
	for _, tt := range []struct {
		out     string
		err     error
		dark    bool
		wantErr bool
	}{
		{"Dark\n", nil, true, false},
		{"Light\n", nil, false, false},
		{"", exitErr, false, false}, // the key does not exist: light
		{"", errors.New("no defaults"), false, true},
	} {
		dark, err := parseInterfaceStyle([]byte(tt.out), tt.err)
		if dark != tt.dark || (err != nil) != tt.wantErr {
			t.Errorf("parseInterfaceStyle(%q, %v) = %v, %v", tt.out, tt.err, dark, err)
		}
	}
}

func TestDarkModeReads(t *testing.T) {
	calls := fakeRun(t, "Dark\n", nil)
	if dark, err := DarkMode(); !dark || err != nil {
		t.Errorf("DarkMode = %v, %v", dark, err)
	}
	if want := []string{"/usr/bin/defaults read -g AppleInterfaceStyle"}; !slices.Equal(*calls, want) {
		t.Errorf("ran %q", *calls)
	}
}

func TestQueryRecycleBin(t *testing.T) {
	home, vol := t.TempDir(), t.TempDir()
	write := func(path string, n int) {
		t.Helper()
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, "a.txt"), 100)
	write(filepath.Join(home, "folder", "b.bin"), 1000)
	write(filepath.Join(home, "folder", "sub", "c.bin"), 24)
	write(filepath.Join(home, ".DS_Store"), 6148)
	write(filepath.Join(vol, "d.mov"), 50)
	old := trashDirs
	t.Cleanup(func() { trashDirs = old })

	trashDirs = func() []string { return []string{home, vol, filepath.Join(vol, "missing")} }
	bin, err := QueryRecycleBin()
	if err != nil || bin != (RecycleBin{Items: 3, Size: 1174}) {
		t.Errorf("QueryRecycleBin = %+v, %v", bin, err)
	}

	trashDirs = func() []string { return []string{filepath.Join(home, "missing")} }
	if bin, err := QueryRecycleBin(); err != nil || bin != (RecycleBin{}) {
		t.Errorf("missing Trash = %+v, %v", bin, err)
	}
}

func TestMacOSVersion(t *testing.T) {
	calls := fakeRun(t, "15.6.1\n", nil)
	if v := MacOSVersion(); v != "15.6.1" {
		t.Errorf("MacOSVersion = %q", v)
	}
	if want := []string{"/usr/bin/sw_vers -productVersion"}; !slices.Equal(*calls, want) {
		t.Errorf("ran %q", *calls)
	}
}
