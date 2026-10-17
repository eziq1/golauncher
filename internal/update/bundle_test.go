package update_test

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/install"
	"github.com/eziq1/golauncher/internal/update"
	"github.com/eziq1/golauncher/internal/update/fakegithub"
)

// The macOS updates: the archive with golauncher.app (one per architecture,
// or a universal one), which is extracted and replaced as a whole.
// Temporary folders only.

func TestSelectAssetsBundle(t *testing.T) {
	r := update.Release{Tag: "v0.7.0", Assets: []update.Asset{
		{Name: update.ArchiveName("0.7.0", "arm64")},
		{Name: "golauncher_0.7.0_darwin_arm64.zip"},
		{Name: "golauncher_0.7.0_darwin_amd64.zip"},
		{Name: update.ChecksumsName},
	}}
	// The archive of the architecture, as releases publish them.
	for _, arch := range []string{"arm64", "amd64"} {
		if a, err := update.SelectAssetsFor(r, arch, true); err != nil || a.Archive.Name != "golauncher_0.7.0_darwin_"+arch+".zip" {
			t.Fatalf("%s: %+v, %v", arch, a, err)
		}
	}
	// Without it, a universal archive.
	r.Assets = slices.Delete(r.Assets, 2, 3)
	r.Assets = append(r.Assets, update.Asset{Name: "golauncher_0.7.0_darwin_universal.zip"})
	if a, err := update.SelectAssetsFor(r, "amd64", true); err != nil || a.Archive.Name != "golauncher_0.7.0_darwin_universal.zip" {
		t.Fatalf("universal: %+v, %v", a, err)
	}
	// Neither.
	r.Assets = slices.DeleteFunc(r.Assets, func(a update.Asset) bool { return strings.Contains(a.Name, "universal") })
	if _, err := update.SelectAssetsFor(r, "amd64", true); !errors.Is(err, update.ErrNoAsset) || !strings.Contains(err.Error(), "darwin_amd64") {
		t.Fatalf("amd64: %v", err)
	}
	// Windows still gets its own.
	if a, err := update.SelectAssets(r, "arm64"); err != nil || a.Archive.Name != update.ArchiveName("0.7.0", "arm64") {
		t.Fatalf("windows: %+v, %v", a, err)
	}
	if got := update.BundleArchiveName("1.2.3", update.Universal); got != "golauncher_1.2.3_darwin_universal.zip" {
		t.Fatalf("BundleArchiveName = %q", got)
	}
}

func TestPrepareBundle(t *testing.T) {
	seed, pub, _ := update.GenerateKey(rand.Reader)
	fake := fakegithub.New(fakegithub.Options{Version: "0.7.0", Darwin: true, SigningKey: seed})
	defer fake.Close()
	dir := t.TempDir()
	res, err := client(fake).Prepare(t.Context(), fake.Release, update.PrepareOptions{Arch: "arm64", Dir: dir, PublicKey: pub, Bundle: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Archive != "golauncher_0.7.0_darwin_universal.zip" || !res.Signed || !res.Staged.Bundle {
		t.Fatalf("result = %+v", res)
	}
	exe := res.Staged.Exe()
	if exe != filepath.Join(dir, "files", "golauncher.app", "Contents", "MacOS", "golauncher") {
		t.Fatalf("exe = %q", exe)
	}
	if data, err := os.ReadFile(exe); err != nil || string(data) != "fake golauncher 0.7.0 universal" {
		t.Fatalf("exe = %q, %v", data, err)
	}
	if fi, err := os.Stat(exe); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("exe mode = %v, %v", fi.Mode(), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "files", "LICENSE")); err == nil {
		t.Fatal("a file beside the bundle was extracted")
	}
	info, err := os.ReadFile(filepath.Join(dir, "files", "golauncher.app", "Contents", "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := install.PlistStrings(info); install.PlistVersion(m) != "0.7.0" {
		t.Fatalf("Info.plist version = %q", install.PlistVersion(m))
	}
	// A bundle archive changed after checksums.txt was made is refused.
	name := update.BundleArchiveName("0.7.0", update.Universal)
	fake.SetFile(name, fakegithub.BundleArchive("0.7.0", []byte("evil")))
	if _, err := client(fake).Prepare(t.Context(), fake.Release, update.PrepareOptions{Dir: t.TempDir(), PublicKey: pub, Bundle: true}); !errors.Is(err, update.ErrChecksum) {
		t.Fatalf("changed archive: %v", err)
	}
}

// bundleZip writes a zip of entries (name → content; a name ending in "/"
// is a folder) with the modes given, "l:" content making a link.
func bundleZip(t *testing.T, entries [][2]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e[0], Method: zip.Deflate, Modified: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
		data := e[1]
		switch {
		case strings.HasSuffix(e[0], "/"):
			h.SetMode(os.ModeDir | 0o755)
		case strings.HasPrefix(data, "l:"):
			h.SetMode(os.ModeSymlink | 0o777)
			data = strings.TrimPrefix(data, "l:")
		case strings.Contains(e[0], "/MacOS/"):
			h.SetMode(0o755)
		default:
			h.SetMode(0o644)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(data))
	}
	zw.Close()
	path := filepath.Join(t.TempDir(), "a.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractBundleRefuses(t *testing.T) {
	good := [][2]string{
		{"golauncher.app/Contents/Info.plist", "<plist/>"},
		{"golauncher.app/Contents/MacOS/golauncher", "exe"},
	}
	if _, err := update.ExtractBundle(bundleZip(t, good), t.TempDir()); err != nil {
		t.Fatalf("good archive: %v", err)
	}
	for name, entries := range map[string][][2]string{
		"traversal":  append(slices.Clone(good), [2]string{"golauncher.app/../../evil", "x"}),
		"link":       append(slices.Clone(good), [2]string{"golauncher.app/Contents/link", "l:/etc/passwd"}),
		"no exe":     {{"golauncher.app/Contents/Info.plist", "<plist/>"}},
		"no plist":   {{"golauncher.app/Contents/MacOS/golauncher", "exe"}},
		"twice":      append(slices.Clone(good), good[1]),
		"not a .app": {{"golauncher/Contents/MacOS/golauncher", "exe"}},
	} {
		dir := t.TempDir()
		if _, err := update.ExtractBundle(bundleZip(t, entries), dir); err == nil {
			t.Errorf("%s: extracted", name)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
			t.Errorf("%s: wrote outside", name)
		}
	}
}

// bundleInstallation is installation on macOS: golauncher.app installed
// in a temporary Applications folder and a staged new golauncher.app,
// whose programs are the fake app.
type bundleInstallation struct {
	apps, updateDir, stagedDir string
	sys                        *fakeSystem
	hs                         *helperSystem
	helper                     *update.Helper
	plan                       update.Plan
}

func writeBundle(t *testing.T, dir, version string, exe []byte) {
	t.Helper()
	app := install.BundleIn(dir)
	for name, data := range map[string][]byte{
		"Contents/Info.plist":       install.InfoPlist(install.BundleInfo{Version: version}),
		"Contents/MacOS/golauncher": exe,
		"Contents/MacOS/glctl":      []byte("glctl " + version),
	} {
		p := filepath.Join(app, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func newBundleInstallation(t *testing.T, newMode string) *bundleInstallation {
	t.Helper()
	root := t.TempDir()
	in := &bundleInstallation{
		apps:      filepath.Join(root, "Applications"),
		updateDir: filepath.Join(root, "update"),
		stagedDir: filepath.Join(root, "update", "downloads", "0.5.0", "files"),
		sys:       &fakeSystem{},
		hs:        &helperSystem{},
	}
	writeBundle(t, in.apps, "0.4.0", fakeApp(t, "healthy"))
	writeBundle(t, in.stagedDir, "0.5.0", fakeApp(t, newMode))
	in.sys.entry = &install.Entry{Name: "golauncher", Version: "0.4.0", Dir: in.apps}
	in.helper = &update.Helper{
		Sys: in.hs,
		// Bundle is left unset: the plan's staged bundle sets it.
		NewInstaller: func(self, version string) *install.Installer {
			return &install.Installer{Sys: in.sys, Self: self, Version: version}
		},
		WaitOld: time.Second,
		Health:  60 * time.Second, // macOS checks each new program on its first run
		Poll:    20 * time.Millisecond,
	}
	in.plan = update.Plan{
		ID: "b1", InstallDir: in.apps, From: "0.4.0", To: "0.5.0",
		Staged:    update.Staged{Version: "0.5.0", Dir: in.stagedDir, Bundle: true},
		UpdateDir: in.updateDir, PID: 1,
	}
	t.Cleanup(in.hs.killAll)
	return in
}

func (in *bundleInstallation) glctl(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(install.BundleIn(dir), "Contents", "MacOS", "glctl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("app bundles are macOS's")
	}
}

func TestBundleHelperUpdates(t *testing.T) {
	skipOnWindows(t)
	in := newBundleInstallation(t, "healthy")
	r := in.helper.Apply(t.Context(), in.plan)
	if r.Outcome != update.OutcomeUpdated || r.Error != "" {
		t.Fatalf("result = %+v", r)
	}
	if got := in.glctl(t, in.apps); got != "glctl 0.5.0" {
		t.Fatalf("installed glctl = %q", got)
	}
	if got := in.glctl(t, filepath.Join(in.updateDir, "previous")); got != "glctl 0.4.0" {
		t.Fatalf("kept glctl = %q", got)
	}
	exe := install.BundleExe(install.BundleIn(in.apps))
	if len(in.hs.started) != 1 || in.hs.started[0].exe != exe || len(in.hs.started[0].env) != 1 {
		t.Fatalf("started = %+v", in.hs.started)
	}
	if entries, _ := os.ReadDir(in.apps); len(entries) != 1 {
		t.Fatalf("Applications holds %v", entries)
	}
	if in.sys.entry.Version != "0.5.0" {
		t.Fatalf("entry = %+v", in.sys.entry)
	}
}

func TestBundleHelperRollsBack(t *testing.T) {
	skipOnWindows(t)
	in := newBundleInstallation(t, "crash")
	r := in.helper.Apply(t.Context(), in.plan)
	if r.Outcome != update.OutcomeRolledBack {
		t.Fatalf("result = %+v", r)
	}
	if got := in.glctl(t, in.apps); got != "glctl 0.4.0" {
		t.Fatalf("after rolling back, glctl = %q", got)
	}
	if len(in.hs.started) != 2 || in.sys.entry.Version != "0.4.0" {
		t.Fatalf("started %d, entry %+v", len(in.hs.started), in.sys.entry)
	}
	if entries, _ := os.ReadDir(in.apps); len(entries) != 1 {
		t.Fatalf("Applications holds %v", entries)
	}
}
