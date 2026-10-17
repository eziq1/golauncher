package install

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestBundleOf(t *testing.T) {
	for _, c := range []struct {
		exe, app string
		ok       bool
	}{
		{"/Applications/golauncher.app/Contents/MacOS/golauncher", "/Applications/golauncher.app", true},
		{"/Users/me/Downloads/golauncher 2.app/Contents/MacOS/glctl", "/Users/me/Downloads/golauncher 2.app", true},
		{"/Applications/golauncher.app/Contents/MacOS/../MacOS/golauncher", "/Applications/golauncher.app", true},
		{"/usr/local/bin/golauncher", "", false},
		{"/Applications/golauncher.app/Contents/Resources/golauncher", "", false},
		{"/Applications/golauncher/Contents/MacOS/golauncher", "", false},
		{"/.app/Contents/MacOS/golauncher", "", false},
	} {
		app, ok := BundleOf(filepath.FromSlash(c.exe))
		if app != filepath.FromSlash(c.app) || ok != c.ok {
			t.Errorf("BundleOf(%q) = %q, %v; want %q, %v", c.exe, app, ok, c.app, c.ok)
		}
	}
	if got := BundleExe(BundleIn("/Applications")); got != filepath.FromSlash("/Applications/golauncher.app/Contents/MacOS/golauncher") {
		t.Errorf("BundleExe = %q", got)
	}
}

func TestChooseAppsDir(t *testing.T) {
	writable := map[string]bool{"/Applications": true}
	w := func(d string) bool { return writable[d] }
	if got := ChooseAppsDir("/Applications", "/Users/me/Applications", w); got != "/Applications" {
		t.Errorf("writable /Applications: %q", got)
	}
	writable["/Applications"] = false
	if got := ChooseAppsDir("/Applications", "/Users/me/Applications", w); got != "/Users/me/Applications" {
		t.Errorf("read-only /Applications: %q", got)
	}
	if got := ChooseAppsDir("", "/Users/me/Applications", w); got != "/Users/me/Applications" {
		t.Errorf("no /Applications: %q", got)
	}
}

func TestTranslocated(t *testing.T) {
	if !Translocated("/private/var/folders/xy/abc/T/AppTranslocation/1234-5678/d/golauncher.app/Contents/MacOS/golauncher") {
		t.Error("translocated path not recognized")
	}
	if Translocated("/Applications/golauncher.app/Contents/MacOS/golauncher") {
		t.Error("/Applications counted as translocated")
	}
}

func TestBundleVersion(t *testing.T) {
	for in, want := range map[string]string{
		"0.6.0":                  "0.6.0",
		"v1.2.3":                 "1.2.3",
		"1.0.0-rc.1":             "1.0.0",
		"0.6.0-3-gabc1234":       "0.6.0",
		"0.0.0-abc1234-dirty":    "0.0.0",
		"1.2":                    "1.2.0",
		"dev":                    "0.0.0",
		"0.6.0-3-gabc1234-debug": "0.6.0",
	} {
		if got := BundleVersion(in); got != want {
			t.Errorf("BundleVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInfoPlist(t *testing.T) {
	data := InfoPlist(BundleInfo{Version: "v0.7.0-rc.1"})
	info, err := PlistStrings(data)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"CFBundleIdentifier":         BundleID,
		"CFBundleExecutable":         "golauncher",
		"CFBundleName":               "golauncher",
		"CFBundleIconFile":           "golauncher.icns",
		"CFBundlePackageType":        "APPL",
		"CFBundleShortVersionString": "0.7.0",
		"CFBundleVersion":            "0.7.0",
		VersionKey:                   "0.7.0-rc.1",
		"LSMinimumSystemVersion":     "13.0",
		"CFBundleDevelopmentRegion":  "en",
	} {
		if info[k] != want {
			t.Errorf("%s = %q, want %q", k, info[k], want)
		}
	}
	for _, k := range []string{"NSAppleEventsUsageDescription", "NSDesktopFolderUsageDescription", "NSLocalNetworkUsageDescription"} {
		if !strings.HasPrefix(info[k], "golauncher") {
			t.Errorf("%s = %q", k, info[k])
		}
	}
	s := string(data)
	for _, frag := range []string{
		"<key>LSUIElement</key>\n\t<true/>",
		"<key>NSHighResolutionCapable</key>\n\t<true/>",
		"<key>CFBundleLocalizations</key>\n\t<array>\n\t\t<string>en</string>\n\t\t<string>zh-Hans</string>\n\t</array>",
		"Copyright © eziq1",
	} {
		if !strings.Contains(s, frag) {
			t.Errorf("Info.plist lacks %q:\n%s", frag, s)
		}
	}
	if PlistVersion(info) != "0.7.0-rc.1" {
		t.Errorf("PlistVersion = %q", PlistVersion(info))
	}
	if !slices.Equal(Localizations, []string{"en", "zh-Hans"}) {
		t.Errorf("Localizations = %q", Localizations)
	}
	lint(t, data)
}

func TestInfoPlistStrings(t *testing.T) {
	for _, lang := range Localizations {
		data, err := InfoPlistStrings(lang)
		if err != nil {
			t.Fatal(err)
		}
		m, err := PlistStrings(data)
		if err != nil {
			t.Fatal(err)
		}
		// Every purpose string of the Info.plist is translated.
		if len(m) != len(usage["en"]) || m["NSAppleEventsUsageDescription"] == "" {
			t.Errorf("%s: %d strings", lang, len(m))
		}
		lint(t, data)
	}
	if _, err := InfoPlistStrings("fr"); err == nil {
		t.Error("InfoPlistStrings(fr) did not fail")
	}
}

func TestLaunchAgentPlist(t *testing.T) {
	data := LaunchAgentPlist("/Applications/golauncher & co.app", []string{"--background"})
	want := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>io.github.eziq1.golauncher.login</string>
	<key>LimitLoadToSessionType</key>
	<array>
		<string>Aqua</string>
	</array>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/bin/open</string>
		<string>-a</string>
		<string>/Applications/golauncher &amp; co.app</string>
		<string>--args</string>
		<string>--background</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`
	if string(data) != want {
		t.Errorf("got:\n%s\nwant:\n%s", data, want)
	}
	lint(t, data)
	if s := string(LaunchAgentPlist("/A.app", nil)); strings.Contains(s, "--args") {
		t.Errorf("no args: %s", s)
	}
}

func TestPlistStrings(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
	<key>A</key><string>a &amp; b</string>
	<key>Nested</key><dict><key>A</key><string>inner</string></dict>
	<key>List</key><array><string>x</string></array>
	<key>Flag</key><true/>
	<key>N</key><integer>3</integer>
	<key>B</key><string>b</string>
</dict></plist>`)
	m, err := PlistStrings(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m["A"] != "a & b" || m["B"] != "b" {
		t.Errorf("got %q", m)
	}
	if _, err := PlistStrings([]byte("bplist00...")); !errors.Is(err, ErrBinaryPlist) {
		t.Errorf("binary: %v", err)
	}
	if _, err := PlistStrings([]byte("<plist><array/></plist>")); err == nil {
		t.Error("no dictionary: no error")
	}
}

// lint checks a property list with plutil, where there is one.
func lint(t *testing.T, data []byte) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return
	}
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		return
	}
	f := filepath.Join(t.TempDir(), "x.plist")
	if err := os.WriteFile(f, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(plutil, "-lint", f).CombinedOutput(); err != nil {
		t.Errorf("plutil -lint: %v\n%s", err, out)
	}
}

// makeBundle writes a fake golauncher.app of version under dir.
func makeBundle(t *testing.T, dir, version string) string {
	t.Helper()
	app := BundleIn(dir)
	files := map[string]string{
		"Contents/Info.plist":                   string(InfoPlist(BundleInfo{Version: version})),
		"Contents/MacOS/golauncher":             "golauncher " + version,
		"Contents/MacOS/glctl":                  "glctl " + version,
		"Contents/Resources/golauncher.icns":    "icns",
		"Contents/_CodeSignature/CodeResources": "sealed " + version,
	}
	for name, content := range files {
		p := filepath.Join(app, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.Contains(name, "/MacOS/") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReplaceBundle(t *testing.T) {
	root := t.TempDir()
	src := makeBundle(t, filepath.Join(root, "Downloads"), "0.2.0")
	apps := filepath.Join(root, "Applications")
	old := makeBundle(t, apps, "0.1.0")
	if err := os.WriteFile(filepath.Join(old, "Contents", "stale"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("MacOS/golauncher", filepath.Join(src, "Contents", "link")); err != nil {
		t.Fatal(err)
	}

	undo, commit, err := ReplaceBundle(src, old)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, BundleExe(old)); got != "golauncher 0.2.0" {
		t.Fatalf("exe = %q", got)
	}
	if exists(filepath.Join(old, "Contents", "stale")) {
		t.Error("a file of the old bundle is left in the new one")
	}
	if fi, err := os.Stat(BundleExe(old)); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("exe mode: %v, %v", fi.Mode(), err)
	}
	if l, err := os.Readlink(filepath.Join(old, "Contents", "link")); err != nil || l != "MacOS/golauncher" {
		t.Errorf("link = %q, %v", l, err)
	}
	// Until commit the old bundle waits aside; undo puts it back.
	if !exists(filepath.Join(apps, bundleOld)) {
		t.Fatal("old bundle not kept")
	}
	if err := undo(); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, BundleExe(old)); got != "golauncher 0.1.0" {
		t.Fatalf("after undo: exe = %q", got)
	}

	_, commit, err = ReplaceBundle(src, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(apps)
	if len(entries) != 1 || entries[0].Name() != BundleName {
		t.Errorf("Applications holds %v", entries)
	}

	// Into an empty folder; undo removes it again.
	fresh := filepath.Join(root, "Other", BundleName)
	os.MkdirAll(filepath.Dir(fresh), 0o755)
	undo, _, err = ReplaceBundle(src, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if err := undo(); err != nil || exists(fresh) {
		t.Fatalf("undo of a new bundle: %v, exists %v", err, exists(fresh))
	}
}

func TestCopyTreeRejectsEscapingLink(t *testing.T) {
	root := t.TempDir()
	src := makeBundle(t, filepath.Join(root, "a"), "0.1.0")
	if err := os.Symlink("../../../../etc", filepath.Join(src, "Contents", "evil")); err != nil {
		t.Fatal(err)
	}
	if err := CopyTree(src, filepath.Join(root, "b")); err == nil {
		t.Fatal("escaping link copied")
	}
}

// bundleWorld is a temporary Mac with a downloaded golauncher.app.
func bundleWorld(t *testing.T, version string) (*world, string) {
	t.Helper()
	w := newWorld(t, version)
	dl := filepath.Join(w.root, "Downloads", "golauncher_"+version)
	app := makeBundle(t, dl, version)
	w.in.Self = BundleExe(app)
	w.in.Bundle = true
	w.in.Paths = Paths{
		DefaultDir: filepath.Join(w.root, "Applications"),
		ConfigFile: filepath.Join(w.root, "Library", "Application Support", AppName, "config.json"),
		DataDirs:   []string{filepath.Join(w.root, "Library", "Application Support", AppName)},
		UpdateDir:  filepath.Join(w.root, "Library", "Caches", AppName, "update"),
	}
	return w, app
}

func TestBundleInstall(t *testing.T) {
	w, _ := bundleWorld(t, "0.1.0")
	if st := w.detect(); st.Kind != KindFresh {
		t.Fatalf("state = %+v", st)
	}
	dir := w.in.Paths.DefaultDir
	var steps []Step
	exe, err := w.in.Install(Options{Dir: dir, LaunchAtLogin: true}, func(s Step) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	if exe != BundleExe(BundleIn(dir)) || readFile(t, exe) != "golauncher 0.1.0" {
		t.Fatalf("exe = %q", exe)
	}
	if got := readFile(t, filepath.Join(BundleIn(dir), "Contents", "MacOS", "glctl")); got != "glctl 0.1.0" {
		t.Fatalf("glctl = %q", got)
	}
	if !slices.Equal(steps, []Step{StepStopping, StepCopying, StepShortcuts, StepRegistering}) {
		t.Fatalf("steps = %v", steps)
	}
	if e := w.sys.entry; e == nil || e.Dir != dir || e.Version != "0.1.0" || e.SizeKB == 0 {
		t.Fatalf("entry = %+v", e)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("Applications holds %v", entries)
	}

	// The installed copy runs normally.
	inst := *w.in
	inst.Self = exe
	if st, err := inst.Detect(); err != nil || st.Kind != KindRun || !st.Self {
		t.Fatalf("installed copy: %+v, %v", st, err)
	}

	// An upgrade replaces the bundle; a failure undoes it.
	w2 := w.as("0.2.0")
	app2 := makeBundle(t, filepath.Join(w.root, "Downloads", "v2"), "0.2.0")
	w2.in.Self = BundleExe(app2)
	if st := w2.detect(); st.Kind != KindChange || st.Order != 1 || st.Dir != dir {
		t.Fatalf("state = %+v", st)
	}
	w.sys.writeErr = errors.New("disk full")
	if _, err := w2.in.Install(Options{Dir: dir, Upgrade: true}, nil); err == nil {
		t.Fatal("no error")
	}
	if got := readFile(t, exe); got != "golauncher 0.1.0" {
		t.Fatalf("after a failed upgrade: %q", got)
	}
	w.sys.writeErr = nil
	if _, err := w2.in.Install(Options{Dir: dir, Upgrade: true}, nil); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, exe); got != "golauncher 0.2.0" {
		t.Fatalf("after the upgrade: %q", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("Applications holds %v", entries)
	}

	// Uninstalling removes the bundle, never the folder holding it.
	if err := inst.Uninstall(UninstallOptions{}); err != nil {
		t.Fatal(err)
	}
	app := BundleIn(dir)
	last := w.sys.removals[len(w.sys.removals)-1]
	if !slices.Equal(last, []string{app, app}) {
		t.Fatalf("removal = %q", last)
	}
	if w.sys.entry != nil {
		t.Fatal("entry left")
	}
}

func TestBundleInstallNeedsBundle(t *testing.T) {
	w, _ := bundleWorld(t, "0.1.0")
	w.in.Self = filepath.Join(w.root, "dist", "golauncher")
	if _, err := w.in.Install(Options{Dir: w.in.Paths.DefaultDir}, nil); err == nil || !strings.Contains(err.Error(), "app bundle") {
		t.Fatalf("err = %v", err)
	}
	if exists(w.in.Paths.DefaultDir) {
		t.Fatal("install location created")
	}
}

func TestBundleInstallInPlace(t *testing.T) {
	w, app := bundleWorld(t, "0.1.0")
	dir := filepath.Dir(app)
	exe, err := w.in.Install(Options{Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !SamePath(exe, w.in.Self) {
		t.Fatalf("exe = %q", exe)
	}
	if st := w.detect(); st.Kind != KindRun {
		t.Fatalf("state = %+v", st)
	}
}

func TestCleanupBundle(t *testing.T) {
	root := t.TempDir()
	makeBundle(t, root, "0.2.0")
	for _, n := range []string{bundleOld, bundleNew} {
		os.MkdirAll(filepath.Join(root, n, "Contents"), 0o755)
	}
	Cleanup(root)
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 || entries[0].Name() != BundleName {
		t.Fatalf("left %v", entries)
	}
}
