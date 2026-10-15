package install

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/eziq1/golauncher/internal/install"
)

// integration skips tests that touch the real registry or start processes
// unless GOLAUNCHER_INSTALL_TESTS=1.
func integration(t *testing.T) {
	t.Helper()
	if os.Getenv("GOLAUNCHER_INSTALL_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_INSTALL_TESTS=1 to touch the registry and start processes")
	}
}

func TestShortcutRoundTrip(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.exe")
	if err := os.WriteFile(target, []byte("MZ"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "sub", install.LinkName)
	s := New()
	if err := s.CreateShortcut(link, install.Shortcut{Target: target, Dir: dir, Description: "test", Icon: target, Args: "--show"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ShortcutTarget(link)
	if err != nil {
		t.Fatal(err)
	}
	if !install.SamePath(got, target) {
		t.Fatalf("target = %q, want %q", got, target)
	}
}

func TestFileVersion(t *testing.T) {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	v, err := New().FileVersion(filepath.Join(sys, "kernel32.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v, "10.") {
		t.Fatalf("kernel32.dll version = %q", v)
	}
	if _, err := New().FileVersion(filepath.Join(t.TempDir(), "missing.exe")); err == nil {
		t.Fatal("no error for a missing file")
	}
}

func TestPaths(t *testing.T) {
	p, err := Paths()
	if err != nil {
		t.Fatal(err)
	}
	local, _ := os.UserCacheDir()
	if !strings.EqualFold(p.DefaultDir, filepath.Join(local, "Programs", "golauncher")) {
		t.Errorf("default dir = %q", p.DefaultDir)
	}
	if !strings.HasSuffix(p.StartMenuLink, `\Start Menu\Programs\golauncher.lnk`) {
		t.Errorf("Start menu link = %q", p.StartMenuLink)
	}
	if filepath.Base(p.DesktopLink) != install.LinkName {
		t.Errorf("desktop link = %q", p.DesktopLink)
	}
}

func TestEntryRoundTrip(t *testing.T) {
	integration(t)
	key := fmt.Sprintf(`Software\golauncher-install-test-%d`, os.Getpid())
	s := &System{Key: key}
	t.Cleanup(func() { registry.DeleteKey(registry.CURRENT_USER, key) })
	if _, ok, err := s.ReadEntry(); ok || err != nil {
		t.Fatalf("ReadEntry before writing: %v, %v", ok, err)
	}
	want := install.Entry{Name: "golauncher", Version: "0.1.0-2-gabc", Publisher: install.Publisher, Dir: `C:\x`,
		Icon: `C:\x\golauncher.exe`, Uninstall: `"C:\x\golauncher.exe" --uninstall`, URL: install.URL, InstallDate: "20261014", SizeKB: 1234}
	if err := s.WriteEntry(want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.ReadEntry()
	if err != nil || !ok || got != want {
		t.Fatalf("ReadEntry = %+v, %v, %v", got, ok, err)
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, key, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"NoModify", "NoRepair"} {
		if v, _, err := k.GetIntegerValue(name); err != nil || v != 1 {
			t.Errorf("%s = %d, %v", name, v, err)
		}
	}
	k.Close()
	if err := s.DeleteEntry(); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteEntry(); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
	if _, ok, _ := s.ReadEntry(); ok {
		t.Fatal("entry still there")
	}
}

func TestScheduleRemoval(t *testing.T) {
	integration(t)
	dir := filepath.Join(t.TempDir(), "with space & amp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, n := range []string{"a.exe", "b.exe"} {
		f := filepath.Join(dir, n)
		os.WriteFile(f, nil, 0o644)
		files = append(files, f)
	}
	// a.exe stays in use for a while, as a program that has not exited yet.
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(files[0]), windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(3*time.Second, func() { windows.CloseHandle(h) })
	if err := New().ScheduleRemoval(dir, files); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("folder not removed")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestOthers(t *testing.T) {
	if got := processNames(`C:\x\GoLauncher_0.2.0.exe`); len(got) != 2 || got[0] != "golauncher.exe" || got[1] != "golauncher_0.2.0.exe" {
		t.Fatalf("processNames = %q", got)
	}
	if got := processNames(`C:\x\GOLAUNCHER.EXE`); len(got) != 1 {
		t.Fatalf("processNames = %q", got)
	}
	if got := others([]string{"no-such-golauncher-test.exe"}); len(got) != 0 {
		t.Fatalf("others = %v", got)
	}
	// This test process is never listed. (Other processes may share its name:
	// go test runs internal/install's install.test.exe in parallel.)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got := others([]string{strings.ToLower(filepath.Base(self))}); slices.Contains(got, uint32(os.Getpid())) {
		t.Fatalf("others lists this process: %v", got)
	}
}
