package update_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/install"
	"github.com/eziq1/golauncher/internal/update"
)

// The fake golauncher.exe programs (testdata/fakeapp), built once.
var (
	buildOnce sync.Once
	buildErr  error
	fakeApps  = map[string]string{} // mode -> executable
)

func fakeApp(t *testing.T, mode string) []byte {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "golauncher-fakeapp-")
		if err != nil {
			buildErr = err
			return
		}
		for _, m := range []string{"healthy", "crash", "hang"} {
			exe := filepath.Join(dir, m+".exe")
			cmd := exec.Command("go", "build", "-o", exe, "-ldflags", "-X main.mode="+m, "./testdata/fakeapp")
			if out, err := cmd.CombinedOutput(); err != nil {
				buildErr = errors.Join(err, errors.New(string(out)))
				return
			}
			fakeApps[m] = exe
		}
	})
	if buildErr != nil {
		t.Fatalf("building the fake app: %v", buildErr)
	}
	data, err := os.ReadFile(fakeApps[mode])
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMain(m *testing.M) {
	code := m.Run()
	for _, exe := range fakeApps {
		os.RemoveAll(filepath.Dir(exe))
		break
	}
	os.Exit(code)
}

// fakeSystem is install.System in memory: the uninstall entry, shortcuts
// written as files holding their target.
type fakeSystem struct {
	mu    sync.Mutex
	entry *install.Entry
}

func (f *fakeSystem) ReadEntry() (install.Entry, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.entry == nil {
		return install.Entry{}, false, nil
	}
	return *f.entry, true, nil
}

func (f *fakeSystem) WriteEntry(e install.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entry = &e
	return nil
}

func (f *fakeSystem) DeleteEntry() error { f.entry = nil; return nil }
func (f *fakeSystem) FileVersion(string) (string, error) {
	return "", errors.New("no version resource")
}
func (f *fakeSystem) CreateShortcut(link string, s install.Shortcut) error {
	os.MkdirAll(filepath.Dir(link), 0o755)
	return os.WriteFile(link, []byte(s.Target), 0o644)
}
func (f *fakeSystem) ShortcutTarget(link string) (string, error) {
	b, err := os.ReadFile(link)
	return string(b), err
}
func (f *fakeSystem) StopInstances(string, time.Duration) error { return nil }
func (f *fakeSystem) Start(string, []string) error              { return nil }
func (f *fakeSystem) ScheduleRemoval(string, []string) error    { return nil }

// helperSystem starts real processes and records them.
type helperSystem struct {
	mu       sync.Mutex
	started  []started
	procs    []*proc
	waitErr  error
	startErr error
}

type started struct {
	exe  string
	args []string
	env  []string
	data []byte // the program file when it started
}

type proc struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (p *proc) Exited() <-chan struct{} { return p.done }
func (p *proc) Kill() error             { return p.cmd.Process.Kill() }

func (h *helperSystem) WaitExit(int, time.Duration) error { return h.waitErr }

func (h *helperSystem) Start(exe string, args, env []string) (update.Process, error) {
	if h.startErr != nil {
		return nil, h.startErr
	}
	data, _ := os.ReadFile(exe)
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &proc{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	h.mu.Lock()
	h.started = append(h.started, started{exe, args, env, data})
	h.procs = append(h.procs, p)
	h.mu.Unlock()
	return p, nil
}

// killAll ends the processes a test started.
func (h *helperSystem) killAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, p := range h.procs {
		// The done channel, not cmd.ProcessState: Wait writes that on
		// another goroutine.
		select {
		case <-p.done:
		default:
			_ = p.Kill()
		}
	}
}

// installation is a temporary installation of an "old" golauncher and a
// staged "new" one.
type installation struct {
	dir, updateDir, stagedDir string
	old, newExe               []byte
	sys                       *fakeSystem
	hs                        *helperSystem
	helper                    *update.Helper
	plan                      update.Plan
	links                     install.Paths
}

func newInstallation(t *testing.T, newMode string) *installation {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("the fake programs are Windows executables")
	}
	root := t.TempDir()
	in := &installation{
		dir:       filepath.Join(root, "Programs", "golauncher"),
		updateDir: filepath.Join(root, "update"),
		stagedDir: filepath.Join(root, "update", "downloads", "0.5.0", "files"),
		old:       fakeApp(t, "healthy"),
		sys:       &fakeSystem{},
		hs:        &helperSystem{},
	}
	in.newExe = append(fakeApp(t, newMode), "\x00new"...) // differs from the old even when healthy
	in.links = install.Paths{StartMenuLink: filepath.Join(root, "Start Menu", "golauncher.lnk")}
	for _, d := range []string{in.dir, in.stagedDir} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(in.dir, install.ExeName), in.old, 0o755)
	os.WriteFile(filepath.Join(in.dir, install.CtlName), []byte("old glctl"), 0o755)
	os.WriteFile(filepath.Join(in.stagedDir, install.ExeName), in.newExe, 0o755)
	os.WriteFile(filepath.Join(in.stagedDir, install.CtlName), []byte("new glctl"), 0o755)
	in.sys.entry = &install.Entry{Name: "golauncher", Version: "0.4.0", Dir: in.dir}
	in.helper = &update.Helper{
		Sys: in.hs,
		NewInstaller: func(self, version string) *install.Installer {
			return &install.Installer{Sys: in.sys, Self: self, Version: version, Paths: in.links}
		},
		WaitOld: time.Second,
		Health:  5 * time.Second,
		Poll:    20 * time.Millisecond,
	}
	in.plan = update.Plan{
		ID: "t1", InstallDir: in.dir, From: "0.4.0", To: "0.5.0",
		Staged:    update.Staged{Version: "0.5.0", Dir: in.stagedDir, Page: "https://example.test/v0.5.0"},
		UpdateDir: in.updateDir, PID: 1,
	}
	t.Cleanup(in.hs.killAll)
	return in
}

func (in *installation) installed(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(in.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestHelperUpdates(t *testing.T) {
	in := newInstallation(t, "healthy")
	r := in.helper.Apply(t.Context(), in.plan)
	if r.Outcome != update.OutcomeUpdated || r.Error != "" {
		t.Fatalf("result = %+v", r)
	}
	if !bytes.Equal(in.installed(t, install.ExeName), in.newExe) || string(in.installed(t, install.CtlName)) != "new glctl" {
		t.Fatal("the new version is not installed")
	}
	if in.sys.entry.Version != "0.5.0" {
		t.Fatalf("uninstall entry = %+v", in.sys.entry)
	}
	prev, _ := os.ReadFile(filepath.Join(in.updateDir, "previous", install.ExeName))
	if !bytes.Equal(prev, in.old) {
		t.Fatal("the previous version was not kept")
	}
	if len(in.hs.started) != 1 || !slices.Equal(in.hs.started[0].args, []string{"--background"}) || len(in.hs.started[0].env) != 1 {
		t.Fatalf("started = %+v", in.hs.started)
	}
	if saved, ok := update.ReadResult(in.updateDir); !ok || saved.Outcome != update.OutcomeUpdated || saved.Page != in.plan.Staged.Page {
		t.Fatalf("result.json = %+v", saved)
	}
	for _, leftover := range []string{install.ExeName + ".old", install.ExeName + ".new"} {
		if _, err := os.Stat(filepath.Join(in.dir, leftover)); err == nil {
			t.Errorf("%s left in the installation", leftover)
		}
	}
}

func TestHelperRollsBackACrash(t *testing.T) {
	in := newInstallation(t, "crash")
	r := in.helper.Apply(t.Context(), in.plan)
	if r.Outcome != update.OutcomeRolledBack || r.Error == "" {
		t.Fatalf("result = %+v", r)
	}
	in.assertRolledBack(t)
}

func TestHelperRollsBackAHang(t *testing.T) {
	in := newInstallation(t, "hang")
	in.helper.Health = time.Second
	start := time.Now()
	r := in.helper.Apply(t.Context(), in.plan)
	if r.Outcome != update.OutcomeRolledBack {
		t.Fatalf("result = %+v", r)
	}
	if time.Since(start) > 30*time.Second {
		t.Fatal("waited too long")
	}
	in.assertRolledBack(t)
}

func (in *installation) assertRolledBack(t *testing.T) {
	t.Helper()
	if !bytes.Equal(in.installed(t, install.ExeName), in.old) || string(in.installed(t, install.CtlName)) != "old glctl" {
		t.Fatal("the old version was not restored")
	}
	if in.sys.entry.Version != "0.4.0" {
		t.Fatalf("uninstall entry = %+v", in.sys.entry)
	}
	// The new version ran once; then the old one was started again.
	if len(in.hs.started) != 2 || !bytes.Equal(in.hs.started[0].data, in.newExe) || !bytes.Equal(in.hs.started[1].data, in.old) {
		t.Fatalf("started %d programs", len(in.hs.started))
	}
	if saved, _ := update.ReadResult(in.updateDir); saved.Outcome != update.OutcomeRolledBack {
		t.Fatalf("result.json = %+v", saved)
	}
}

func TestHelperInstallFails(t *testing.T) {
	in := newInstallation(t, "healthy")
	os.Remove(filepath.Join(in.stagedDir, install.ExeName))
	r := in.helper.Apply(t.Context(), in.plan)
	if r.Outcome != update.OutcomeFailed {
		t.Fatalf("result = %+v", r)
	}
	if !bytes.Equal(in.installed(t, install.ExeName), in.old) || in.sys.entry.Version != "0.4.0" {
		t.Fatal("the installation changed")
	}
	if len(in.hs.started) != 1 || !bytes.Equal(in.hs.started[0].data, in.old) {
		t.Fatal("the old version was not started again")
	}
}

func TestHelperOldStillRunning(t *testing.T) {
	in := newInstallation(t, "healthy")
	in.hs.waitErr = errors.New("still runs")
	r := in.helper.Apply(t.Context(), in.plan)
	if r.Outcome != update.OutcomeFailed {
		t.Fatalf("result = %+v", r)
	}
	if !bytes.Equal(in.installed(t, install.ExeName), in.old) || len(in.hs.started) != 0 {
		t.Fatal("the helper went on while golauncher still ran")
	}
}

func TestReadPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	os.WriteFile(path, []byte(`{"id":"x"}`), 0o644)
	if _, err := update.ReadPlan(path); err == nil {
		t.Fatal("an incomplete plan was accepted")
	}
	if _, err := update.ReadPlan(filepath.Join(t.TempDir(), "none.json")); err == nil {
		t.Fatal("a missing plan was accepted")
	}
}

func TestHealthMarker(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "healthy-x")
	t.Setenv(update.EnvHealth, marker)
	got := update.HealthMarker()
	if got != marker || os.Getenv(update.EnvHealth) != "" {
		t.Fatalf("HealthMarker = %q, env %q", got, os.Getenv(update.EnvHealth))
	}
	if err := update.WriteMarker(got); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	if update.HealthMarker() != "" || update.WriteMarker("") != nil {
		t.Fatal("without the variable")
	}
}
