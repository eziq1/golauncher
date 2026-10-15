package shell

import (
	"bytes"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func TestInstalledAppsAndIcon(t *testing.T) {
	if testing.Short() {
		t.Skip("enumerates the Start menu")
	}
	apps, err := InstalledApps(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) == 0 {
		t.Fatal("no apps found")
	}
	seen := map[string]bool{}
	var packaged, win32 *App
	for i, a := range apps {
		if a.Name == "" || a.ID == "" {
			t.Errorf("app %d has an empty name or ID: %+v", i, a)
		}
		if seen[a.ID] {
			t.Errorf("duplicate ID %q", a.ID)
		}
		seen[a.ID] = true
		if a.Packaged && packaged == nil {
			packaged = &apps[i]
		}
		if !a.Packaged && a.Path != "" && win32 == nil {
			win32 = &apps[i]
		}
	}
	t.Logf("%d apps; first: %+v", len(apps), apps[0])
	for _, a := range []*App{win32, packaged} {
		if a == nil {
			continue
		}
		t.Logf("icon of %+v", *a)
		b, err := IconPNG(*a, 64)
		if err != nil {
			t.Fatalf("IconPNG(%q): %v", a.Name, err)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("IconPNG(%q) is not a PNG: %v", a.Name, err)
		}
		if s := img.Bounds().Size(); s.X == 0 || s.Y == 0 || s.X > 256 {
			t.Errorf("IconPNG(%q) size = %v", a.Name, s)
		}
	}
}

func TestOpenErrors(t *testing.T) {
	if err := Open("", plugin.OpenOptions{}); err == nil {
		t.Error("Open(\"\") succeeded")
	}
	if err := Open(`C:\definitely\missing\file-7b1e.exe`, plugin.OpenOptions{}); err == nil {
		t.Error("Open of a missing file succeeded")
	}
}

// TestRealAppKeywords measures the enumeration on this machine and how many
// apps get keywords. It is read-only and logs counts and timings only, never
// app names. Run with GOLAUNCHER_APPS_TESTS=1.
func TestRealAppKeywords(t *testing.T) {
	if os.Getenv("GOLAUNCHER_APPS_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_APPS_TESTS=1 to enumerate the real Start menu")
	}
	start := time.Now()
	apps, err := InstalledApps(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	total := time.Since(start)
	start = time.Now()
	exes, withNames := 0, 0
	for _, a := range apps {
		if !a.Packaged && strings.HasSuffix(strings.ToLower(a.Path), ".exe") {
			exes++
			if len(versionNames(a.Path)) > 0 {
				withNames++
			}
		}
	}
	version := time.Since(start)
	withKeywords := 0
	for _, a := range apps {
		if len(a.Keywords) > 0 {
			withKeywords++
		}
		for _, k := range a.Keywords {
			if k == "" || strings.EqualFold(k, a.Name) {
				t.Errorf("bad keyword %q of an app", k)
			}
		}
	}
	t.Logf("%d apps in %v; %d executables, %d with version names, read again in %v; %d apps with keywords",
		len(apps), total, exes, withNames, version, withKeywords)
}
