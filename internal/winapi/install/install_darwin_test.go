package install

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/install"
)

// These tests use temporary folders only: they never look at or change
// /Applications, the login items or a running golauncher.

func fakeApp(t *testing.T, dir, id, version string) string {
	t.Helper()
	app := install.BundleIn(dir)
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	info := string(install.InfoPlist(install.BundleInfo{Version: version}))
	info = strings.ReplaceAll(info, install.BundleID, id)
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(install.BundleExe(app), []byte("exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	return app
}

func TestReadEntry(t *testing.T) {
	root := t.TempDir()
	sys := &System{
		Record:     filepath.Join(root, "Support", "install.json"),
		Candidates: []string{filepath.Join(root, "Applications"), filepath.Join(root, "Home", "Applications")},
	}
	if _, ok, err := sys.ReadEntry(); ok || err != nil {
		t.Fatalf("empty Mac: %v, %v", ok, err)
	}
	// Someone else's golauncher.app does not count.
	fakeApp(t, sys.Candidates[0], "com.example.other", "9.9.9")
	if _, ok, _ := sys.ReadEntry(); ok {
		t.Fatal("another app's bundle counted")
	}
	// One dragged to ~/Applications does.
	fakeApp(t, sys.Candidates[1], install.BundleID, "0.6.0")
	e, ok, err := sys.ReadEntry()
	if err != nil || !ok || e.Dir != sys.Candidates[1] || e.Version != "0.6.0" {
		t.Fatalf("dragged: %+v, %v, %v", e, ok, err)
	}
	// The record wins, with the version of the bundle now there.
	custom := filepath.Join(root, "Tools")
	fakeApp(t, custom, install.BundleID, "0.7.0-rc.1")
	if err := sys.WriteEntry(install.Entry{Dir: custom, Version: "0.7.0", InstallDate: "20261016"}); err != nil {
		t.Fatal(err)
	}
	e, ok, _ = sys.ReadEntry()
	if !ok || e.Dir != custom || e.Version != "0.7.0-rc.1" || e.InstallDate != "20261016" {
		t.Fatalf("recorded: %+v", e)
	}
	// A record whose bundle is gone falls back to the search.
	os.RemoveAll(install.BundleIn(custom))
	if e, _, _ = sys.ReadEntry(); e.Dir != sys.Candidates[1] {
		t.Fatalf("stale record: %+v", e)
	}
	if err := sys.DeleteEntry(); err != nil {
		t.Fatal(err)
	}
	if err := sys.DeleteEntry(); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := os.Stat(sys.Record); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("record left")
	}
}

func TestFileVersion(t *testing.T) {
	app := fakeApp(t, t.TempDir(), install.BundleID, "0.6.0-3-gabc1234")
	if v, err := New().FileVersion(install.BundleExe(app)); err != nil || v != "0.6.0-3-gabc1234" {
		t.Fatalf("version = %q, %v", v, err)
	}
	if _, err := New().FileVersion("/usr/bin/true"); err == nil {
		t.Fatal("a bare executable has a version")
	}
}

func TestOpenArgs(t *testing.T) {
	got := openArgs("/Applications/golauncher.app", []string{"--background"}, []string{"GOLAUNCHER_UPDATE_HEALTH=/tmp/x"}, true)
	want := []string{"-n", "-W", "--env", "GOLAUNCHER_UPDATE_HEALTH=/tmp/x", "-a", "/Applications/golauncher.app", "--args", "--background"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
	if got := openArgs("/A.app", nil, nil, false); !slices.Equal(got, []string{"-n", "-a", "/A.app"}) {
		t.Fatalf("no args: %q", got)
	}
}

func TestRemovalArgs(t *testing.T) {
	app := "/Users/me/Applications/golauncher.app"
	args, err := removalArgs(42, app, []string{app})
	if err != nil {
		t.Fatal(err)
	}
	if args[0] != "-c" || args[2] != "sh" || !slices.Equal(args[3:], []string{"42", app, app}) {
		t.Fatalf("args = %q", args)
	}
	home, _ := os.UserHomeDir()
	for _, bad := range []string{"/", "/Applications", "relative/golauncher.app", home} {
		if _, err := removalArgs(1, bad, nil); err == nil {
			t.Errorf("removalArgs(%q) did not refuse", bad)
		}
	}
}

func TestLoginItemFallback(t *testing.T) {
	dir := t.TempDir()
	var sysOn bool
	var setErr error
	li := &LoginItem{
		Set: func(on bool) error {
			if setErr != nil && on {
				return setErr
			}
			sysOn = on
			return nil
		},
		Enabled: func() bool { return sysOn },
		Agent:   filepath.Join(dir, "LaunchAgents", install.LaunchAgentLabel+".plist"),
		App:     "/Applications/golauncher.app",
	}
	// The system's login item works: no launch agent.
	if err := li.Turn(true); err != nil || !sysOn || !li.On() {
		t.Fatalf("on: %v, %v", err, sysOn)
	}
	if _, err := os.Stat(li.Agent); err == nil {
		t.Fatal("launch agent written although the login item works")
	}
	if err := li.Turn(false); err != nil || sysOn || li.On() {
		t.Fatalf("off: %v", err)
	}
	// It fails: the launch agent stands in.
	setErr = errors.New("SMAppService refused")
	if err := li.Turn(true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(li.Agent)
	if err != nil || !strings.Contains(string(data), "<string>/Applications/golauncher.app</string>") || !strings.Contains(string(data), "--background") {
		t.Fatalf("agent = %s, %v", data, err)
	}
	if !li.On() {
		t.Fatal("not on with the launch agent")
	}
	if err := li.Turn(false); err != nil || li.On() {
		t.Fatalf("off: %v", err)
	}
	// Outside a bundle nothing can start it.
	li.App = ""
	if err := li.Turn(true); !errors.Is(err, setErr) {
		t.Fatalf("bare executable: %v", err)
	}
}

func TestOthersSkipsSelf(t *testing.T) {
	self, _ := os.Executable()
	if pids := others([]string{filepath.Base(self)}); slices.Contains(pids, os.Getpid()) {
		t.Fatal("others lists this process")
	}
	if pids := others([]string{"no-such-program-xyz"}); len(pids) != 0 {
		t.Fatalf("others = %v", pids)
	}
}

func TestProcessNames(t *testing.T) {
	if got := processNames("/Users/me/Downloads/golauncher.app/Contents/MacOS/golauncher"); !slices.Equal(got, []string{"golauncher"}) {
		t.Fatalf("got %q", got)
	}
	if got := processNames("/tmp/golauncher-0.6"); !slices.Equal(got, []string{"golauncher", "golauncher-0.6"}) {
		t.Fatalf("got %q", got)
	}
}
