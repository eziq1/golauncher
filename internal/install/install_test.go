package install

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/config"
)

// fakeSys is an in-memory System: a fake registry entry, shortcuts as text
// files holding their target, and recorded process calls.
type fakeSys struct {
	entry    *Entry
	versions map[string]string // FileVersion by exe

	stops    []string // quitters StopInstances ran
	stopErr  error
	started  [][]string // exe and args of Start
	removals [][]string // dir and files of ScheduleRemoval

	writeErr    error // WriteEntry fails
	shortcutErr error // CreateShortcut fails
}

func (f *fakeSys) ReadEntry() (Entry, bool, error) {
	if f.entry == nil {
		return Entry{}, false, nil
	}
	return *f.entry, true, nil
}

func (f *fakeSys) WriteEntry(e Entry) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.entry = &e
	return nil
}

func (f *fakeSys) DeleteEntry() error { f.entry = nil; return nil }

func (f *fakeSys) FileVersion(exe string) (string, error) {
	if v, ok := f.versions[exe]; ok {
		return v, nil
	}
	return "", errors.New("no version resource")
}

func (f *fakeSys) CreateShortcut(link string, s Shortcut) error {
	if f.shortcutErr != nil {
		return f.shortcutErr
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	return os.WriteFile(link, []byte(s.Target), 0o644)
}

func (f *fakeSys) ShortcutTarget(link string) (string, error) {
	b, err := os.ReadFile(link)
	return string(b), err
}

func (f *fakeSys) StopInstances(quitter string, _ time.Duration) error {
	f.stops = append(f.stops, quitter)
	return f.stopErr
}

func (f *fakeSys) Start(exe string, args []string) error {
	f.started = append(f.started, append([]string{exe}, args...))
	return nil
}

func (f *fakeSys) ScheduleRemoval(dir string, files []string) error {
	f.removals = append(f.removals, append([]string{dir}, files...))
	return nil
}

// world is a temporary user profile with a downloaded golauncher.
type world struct {
	t    *testing.T
	root string
	sys  *fakeSys
	in   *Installer
}

func newWorld(t *testing.T, version string) *world {
	t.Helper()
	root := t.TempDir()
	w := &world{t: t, root: root, sys: &fakeSys{versions: map[string]string{}}}
	dl := filepath.Join(root, "Downloads", "golauncher_"+version)
	w.write(filepath.Join(dl, ExeName), "golauncher "+version)
	w.write(filepath.Join(dl, CtlName), "glctl "+version)
	w.in = &Installer{
		Sys:     w.sys,
		Self:    filepath.Join(dl, ExeName),
		Version: version,
		Paths: Paths{
			DefaultDir:    filepath.Join(root, "Programs", AppName),
			StartMenuLink: filepath.Join(root, "Start Menu", LinkName),
			DesktopLink:   filepath.Join(root, "Desktop", LinkName),
			ConfigFile:    filepath.Join(root, "Roaming", AppName, "config.json"),
			DataDirs:      []string{filepath.Join(root, "Roaming", AppName), filepath.Join(root, "Local", AppName)},
		},
		Now: func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
	}
	return w
}

func (w *world) write(path, content string) {
	w.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) read(path string) string {
	w.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

// as switches to another downloaded version sharing the same system.
func (w *world) as(version string) *world {
	w.t.Helper()
	dl := filepath.Join(w.root, "Downloads", "golauncher_"+version)
	w.write(filepath.Join(dl, ExeName), "golauncher "+version)
	in := *w.in
	in.Self, in.Version = filepath.Join(dl, ExeName), version
	return &world{t: w.t, root: w.root, sys: w.sys, in: &in}
}

func (w *world) detect() State {
	w.t.Helper()
	st, err := w.in.Detect()
	if err != nil {
		w.t.Fatal(err)
	}
	return st
}

func TestFreshInstall(t *testing.T) {
	w := newWorld(t, "0.1.0")
	if st := w.detect(); st.Kind != KindFresh || st.Installed || st.Portable() {
		t.Fatalf("state = %+v", st)
	}
	var steps []Step
	dir := w.in.Paths.DefaultDir
	exe, err := w.in.Install(Options{Dir: dir, LaunchAtLogin: true, DesktopShortcut: true, Description: "desc"}, func(s Step) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	if exe != filepath.Join(dir, ExeName) {
		t.Fatalf("exe = %q", exe)
	}
	if !slices.Equal(steps, []Step{StepStopping, StepCopying, StepShortcuts, StepRegistering}) {
		t.Fatalf("steps = %v", steps)
	}
	if got := w.read(exe); got != "golauncher 0.1.0" {
		t.Fatalf("installed exe = %q", got)
	}
	if got := w.read(filepath.Join(dir, CtlName)); got != "glctl 0.1.0" {
		t.Fatalf("installed glctl = %q", got)
	}
	for _, link := range []string{w.in.Paths.StartMenuLink, w.in.Paths.DesktopLink} {
		if got := w.read(link); got != exe {
			t.Fatalf("%s -> %q", link, got)
		}
	}
	e := w.sys.entry
	if e == nil || e.Version != "0.1.0" || e.Dir != dir || e.Uninstall != `"`+exe+`" --uninstall` ||
		e.Publisher != Publisher || e.Icon != exe || e.InstallDate != "20261014" || e.SizeKB == 0 {
		t.Fatalf("entry = %+v", e)
	}
	cfg, err := config.Load(w.in.Paths.ConfigFile)
	if err != nil || !cfg.LaunchAtLogin {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	if len(w.sys.stops) != 1 || w.sys.stops[0] != w.in.Self {
		t.Fatalf("stops = %q", w.sys.stops)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.new")); len(matches) > 0 {
		t.Fatalf("left over: %q", matches)
	}

	// Now the installed copy runs normally.
	inst := *w.in
	inst.Self = exe
	st, err := inst.Detect()
	if err != nil || st.Kind != KindRun || !st.Self || st.Portable() {
		t.Fatalf("installed copy: %+v, %v", st, err)
	}
	// A path differing in case is the same file.
	inst.Self = strings.ToUpper(exe)
	if st, _ := inst.Detect(); st.Kind != KindRun {
		t.Fatalf("upper-case path: %+v", st)
	}
}

func TestInstallOptionsOff(t *testing.T) {
	w := newWorld(t, "0.1.0")
	w.write(w.in.Paths.ConfigFile, `{"schemaVersion":1,"launchAtLogin":true}`)
	if _, err := w.in.Install(Options{Dir: w.in.Paths.DefaultDir}, nil); err != nil {
		t.Fatal(err)
	}
	if exists(w.in.Paths.DesktopLink) {
		t.Fatal("desktop shortcut created")
	}
	if cfg, err := config.Load(w.in.Paths.ConfigFile); err != nil || cfg.LaunchAtLogin {
		t.Fatalf("launch at login not turned off: %+v, %v", cfg, err)
	}
}

func TestInstallWithoutGlctl(t *testing.T) {
	w := newWorld(t, "0.1.0")
	os.Remove(filepath.Join(filepath.Dir(w.in.Self), CtlName))
	if _, err := w.in.Install(Options{Dir: w.in.Paths.DefaultDir}, nil); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(w.in.Paths.DefaultDir, CtlName)) {
		t.Fatal("glctl.exe appeared")
	}
}

func TestInstallIntoOwnFolder(t *testing.T) {
	w := newWorld(t, "0.1.0")
	dir := filepath.Dir(w.in.Self)
	exe, err := w.in.Install(Options{Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !SamePath(exe, w.in.Self) || w.read(exe) != "golauncher 0.1.0" {
		t.Fatalf("exe = %q", exe)
	}
	if st := w.detect(); st.Kind != KindRun || !st.Self {
		t.Fatalf("state = %+v", st)
	}
}

func TestInstallInvalidDir(t *testing.T) {
	w := newWorld(t, "0.1.0")
	file := filepath.Join(w.root, "file")
	w.write(file, "x")
	for _, dir := range []string{"", "relative\\dir", file} {
		if _, err := w.in.Install(Options{Dir: dir}, nil); !errors.Is(err, ErrInvalidDir) {
			t.Errorf("Install(%q) = %v", dir, err)
		}
	}
	if len(w.sys.stops) != 0 {
		t.Fatal("stopped instances for an invalid folder")
	}
}

func TestInstallStillRunning(t *testing.T) {
	w := newWorld(t, "0.1.0")
	w.sys.stopErr = ErrStillRunning
	if _, err := w.in.Install(Options{Dir: w.in.Paths.DefaultDir}, nil); !errors.Is(err, ErrStillRunning) {
		t.Fatalf("err = %v", err)
	}
	if exists(w.in.Paths.DefaultDir) || w.sys.entry != nil {
		t.Fatal("installed although golauncher still runs")
	}
}

func TestDetectVersions(t *testing.T) {
	w := newWorld(t, "0.2.0")
	dir := w.in.Paths.DefaultDir
	if _, err := w.in.Install(Options{Dir: dir}, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		running string
		kind    Kind
		order   int
		ordered bool
	}{
		{"0.2.0", KindSame, 0, false},
		{"v0.2.0", KindSame, 0, false},
		{"0.3.0", KindChange, 1, true},
		{"0.2.0-1-gabcdef0", KindChange, 1, true},
		{"0.1.9", KindChange, -1, true},
		{"0.2.0-rc.1", KindChange, -1, true},
		{"0.0.0-abcdef0", KindChange, 0, false},
		{"0.2.0-dirty", KindChange, 0, false},
		{"garbage", KindChange, 0, false},
	} {
		st := w.as(tc.running).detect()
		if st.Kind != tc.kind || st.Version != "0.2.0" || st.Dir != dir || !st.Portable() {
			t.Errorf("%s: %+v", tc.running, st)
			continue
		}
		if tc.kind == KindChange && (st.Order != tc.order || st.Ordered != tc.ordered) {
			t.Errorf("%s: order %d, %v; want %d, %v", tc.running, st.Order, st.Ordered, tc.order, tc.ordered)
		}
	}
}

func TestDetectVersionFromResource(t *testing.T) {
	w := newWorld(t, "0.2.0")
	exe, err := w.in.Install(Options{Dir: w.in.Paths.DefaultDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.sys.entry.Version = ""
	w.sys.versions[exe] = "0.1.0"
	if st := w.detect(); st.Kind != KindChange || st.Version != "0.1.0" || st.Order != 1 {
		t.Fatalf("state = %+v", st)
	}
}

func TestDetectMissingProgram(t *testing.T) {
	w := newWorld(t, "0.2.0")
	exe, err := w.in.Install(Options{Dir: w.in.Paths.DefaultDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(exe)
	if st := w.detect(); st.Kind != KindFresh || st.Installed {
		t.Fatalf("state = %+v", st)
	}
}

func TestUpgradeAndDowngrade(t *testing.T) {
	w := newWorld(t, "0.1.0")
	dir := w.in.Paths.DefaultDir
	if _, err := w.in.Install(Options{Dir: dir, DesktopShortcut: false, LaunchAtLogin: true}, nil); err != nil {
		t.Fatal(err)
	}
	// The user turns launch at login off; an upgrade keeps that.
	cfg, _ := config.Load(w.in.Paths.ConfigFile)
	cfg.LaunchAtLogin = false
	if err := config.Save(w.in.Paths.ConfigFile, cfg); err != nil {
		t.Fatal(err)
	}

	for _, v := range []string{"0.2.0", "0.1.5"} {
		nw := w.as(v)
		st := nw.detect()
		if st.Kind != KindChange {
			t.Fatalf("%s: %+v", v, st)
		}
		exe, err := nw.in.Install(Options{Dir: st.Dir, Upgrade: true, DesktopShortcut: true, LaunchAtLogin: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := w.read(exe); got != "golauncher "+v {
			t.Fatalf("installed %q", got)
		}
		if w.sys.entry.Version != v {
			t.Fatalf("entry version %q", w.sys.entry.Version)
		}
		// glctl.exe of the first installation is kept: v has none.
		if got := w.read(filepath.Join(dir, CtlName)); got != "glctl 0.1.0" {
			t.Fatalf("glctl = %q", got)
		}
		if exists(exe+".old") || exists(exe+".new") {
			t.Fatal("backup left behind")
		}
		if exists(w.in.Paths.DesktopLink) {
			t.Fatal("an upgrade created a desktop shortcut")
		}
		if cfg, _ := config.Load(w.in.Paths.ConfigFile); cfg.LaunchAtLogin {
			t.Fatal("an upgrade changed launch at login")
		}
		if st := nw.detect(); st.Kind != KindSame {
			t.Fatalf("after installing %s: %+v", v, st)
		}
	}
}

func TestUpgradeRollsBack(t *testing.T) {
	w := newWorld(t, "0.1.0")
	dir := w.in.Paths.DefaultDir
	exe, err := w.in.Install(Options{Dir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nw := w.as("0.2.0")
	w.sys.writeErr = errors.New("registry is read-only")
	if _, err := nw.in.Install(Options{Dir: dir, Upgrade: true}, nil); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err = %v", err)
	}
	if got := w.read(exe); got != "golauncher 0.1.0" {
		t.Fatalf("not rolled back: %q", got)
	}
	if exists(exe+".old") || exists(exe+".new") {
		t.Fatal("backup left behind")
	}
	if w.sys.entry.Version != "0.1.0" {
		t.Fatalf("entry = %+v", w.sys.entry)
	}
	if !exists(w.in.Paths.StartMenuLink) {
		t.Fatal("the existing Start menu shortcut was removed")
	}
}

func TestFreshInstallRollsBack(t *testing.T) {
	w := newWorld(t, "0.1.0")
	w.sys.shortcutErr = errors.New("no Start menu")
	dir := w.in.Paths.DefaultDir
	if _, err := w.in.Install(Options{Dir: dir}, nil); err == nil {
		t.Fatal("no error")
	}
	if exists(dir) {
		entries, _ := os.ReadDir(dir)
		t.Fatalf("install folder left behind: %v", entries)
	}
	if w.sys.entry != nil {
		t.Fatal("entry written")
	}
}

func TestCleanup(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{ExeName + ".old", CtlName + ".old", ExeName, "other.old"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	Cleanup(dir)
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{ExeName, "other.old"}) {
		t.Fatalf("left %q", names)
	}
}

func TestUninstall(t *testing.T) {
	for _, purge := range []bool{false, true} {
		w := newWorld(t, "0.1.0")
		dir := w.in.Paths.DefaultDir
		exe, err := w.in.Install(Options{Dir: dir, DesktopShortcut: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range w.in.Paths.DataDirs {
			w.write(filepath.Join(d, "data.txt"), "x")
		}
		w.write(exe+".old", "stale")
		// A desktop shortcut of the same name to something else stays.
		w.write(w.in.Paths.DesktopLink, `C:\elsewhere\golauncher.exe`)
		loginRemoved := false
		inst := *w.in
		inst.Self = exe
		inst.RemoveLoginItem = func() error { loginRemoved = true; return nil }
		if err := inst.Uninstall(UninstallOptions{PurgeData: purge}); err != nil {
			t.Fatal(err)
		}
		if exists(w.in.Paths.StartMenuLink) || !exists(w.in.Paths.DesktopLink) {
			t.Fatal("wrong shortcuts removed")
		}
		if w.sys.entry != nil || !loginRemoved {
			t.Fatal("entry or login item left")
		}
		for _, d := range w.in.Paths.DataDirs {
			if exists(d) == purge {
				t.Fatalf("purge %v: data dir %s exists: %v", purge, d, exists(d))
			}
		}
		want := []string{dir, exe, exe + ".old", filepath.Join(dir, CtlName)}
		if len(w.sys.removals) != 1 || !slices.Equal(w.sys.removals[0], want) {
			t.Fatalf("removals = %q, want %q", w.sys.removals, want)
		}
		if len(w.sys.stops) != 2 || w.sys.stops[1] != exe {
			t.Fatalf("stops = %q", w.sys.stops)
		}
	}
}

func TestUninstallNotInstalled(t *testing.T) {
	w := newWorld(t, "0.1.0")
	if err := w.in.Uninstall(UninstallOptions{}); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v", err)
	}
}

func TestUninstallStillRunning(t *testing.T) {
	w := newWorld(t, "0.1.0")
	if _, err := w.in.Install(Options{Dir: w.in.Paths.DefaultDir}, nil); err != nil {
		t.Fatal(err)
	}
	w.sys.stopErr = ErrStillRunning
	if err := w.in.Uninstall(UninstallOptions{}); !errors.Is(err, ErrStillRunning) {
		t.Fatalf("err = %v", err)
	}
	if w.sys.entry == nil || len(w.sys.removals) != 0 {
		t.Fatal("uninstalled although golauncher still runs")
	}
}

func TestParseArgsAndSkip(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == EnvNoInstall {
				return v
			}
			return ""
		}
	}
	for _, tc := range []struct {
		version string
		args    []string
		env     string
		skip    bool
	}{
		{"0.1.0", nil, "", false},
		{"0.1.0", []string{"--show"}, "", false},
		{"0.1.0", []string{"--settings", "--unknown"}, "", false},
		{"dev", nil, "", true},
		{"", nil, "", true},
		{"0.1.0", []string{"--portable"}, "", true},
		{"0.1.0", []string{"--unknown", "--portable"}, "", true},
		{"0.1.0", []string{"-quit"}, "", true},
		{"0.1.0", nil, "1", true},
		{"0.1.0", nil, "0", false},
	} {
		if got := Skip(tc.version, ParseArgs(tc.args), env(tc.env)); got != tc.skip {
			t.Errorf("Skip(%q, %q, env %q) = %v", tc.version, tc.args, tc.env, got)
		}
	}
	if f := ParseArgs([]string{"--uninstall"}); !f.Uninstall || f.Portable {
		t.Fatalf("flags = %+v", f)
	}
}

func TestFolderFor(t *testing.T) {
	for in, want := range map[string]string{
		"":                          "",
		filepath.Join("C:", "Apps"): filepath.Join("C:", "Apps", AppName),
		filepath.Join("C:", "Apps", "golauncher"): filepath.Join("C:", "Apps", "golauncher"),
		filepath.Join("C:", "Apps", "GoLauncher"): filepath.Join("C:", "Apps", "GoLauncher"),
	} {
		if got := FolderFor(in); got != want {
			t.Errorf("FolderFor(%q) = %q, want %q", in, got, want)
		}
	}
}
