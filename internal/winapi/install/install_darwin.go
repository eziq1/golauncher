package install

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/eziq1/golauncher/internal/install"
)

// System is install.System on macOS (docs/self-install.md §9). There is no
// registry: an installation is golauncher.app in an install location, found
// through a small record of where it was installed
// (~/Library/Application Support/golauncher/install.json) or, without one,
// in /Applications and ~/Applications, so that a bundle the user dragged
// there counts too. Its version is read from its Info.plist. There are no
// shortcuts: the bundle is the app.
type System struct {
	// Record is the install record; Candidates are the folders searched
	// for golauncher.app when there is none.
	Record     string
	Candidates []string
}

var _ install.System = (*System)(nil)

// New returns the System of the user's Mac.
func New() *System {
	s := &System{Candidates: []string{"/Applications"}}
	if home, err := os.UserHomeDir(); err == nil {
		s.Record = filepath.Join(home, "Library", "Application Support", install.AppName, "install.json")
		s.Candidates = append(s.Candidates, filepath.Join(home, "Applications"))
	}
	return s
}

// Paths returns the install location: /Applications when the user may
// write to it (administrators may), else ~/Applications. There are no
// shortcuts. ConfigFile and DataDirs are left to the caller.
func Paths() (install.Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return install.Paths{}, err
	}
	return install.Paths{DefaultDir: install.ChooseAppsDir("/Applications", filepath.Join(home, "Applications"), writable)}, nil
}

// writable reports whether this user may create files in dir.
func writable(dir string) bool { return unix.Access(dir, unix.W_OK) == nil }

// record is the install record.
type record struct {
	Dir         string `json:"dir"`
	Version     string `json:"version,omitempty"`
	InstallDate string `json:"installDate,omitempty"`
}

// ReadEntry implements install.System: the recorded installation if its
// bundle is still there, else golauncher.app in a candidate folder.
func (s *System) ReadEntry() (install.Entry, bool, error) {
	if s.Record != "" {
		if data, err := os.ReadFile(s.Record); err == nil {
			var r record
			if json.Unmarshal(data, &r) == nil && r.Dir != "" && isApp(install.BundleIn(r.Dir)) {
				return s.entry(r.Dir, r), true, nil
			}
		}
	}
	for _, dir := range s.Candidates {
		if app := install.BundleIn(dir); isApp(app) && bundleID(app) == install.BundleID {
			return s.entry(dir, record{}), true, nil
		}
	}
	return install.Entry{}, false, nil
}

func (s *System) entry(dir string, r record) install.Entry {
	exe := install.BundleExe(install.BundleIn(dir))
	v, err := s.FileVersion(exe)
	if err != nil {
		v = r.Version
	}
	return install.Entry{Name: install.AppName, Version: v, Publisher: install.Publisher, Dir: dir, Icon: exe, URL: install.URL, InstallDate: r.InstallDate}
}

func isApp(app string) bool {
	fi, err := os.Stat(install.BundleExe(app))
	return err == nil && fi.Mode().IsRegular()
}

// bundleID reads a bundle's CFBundleIdentifier ("" when it cannot).
func bundleID(app string) string {
	info, err := readInfo(app)
	if err != nil {
		return ""
	}
	return info["CFBundleIdentifier"]
}

// WriteEntry implements install.System: it writes the install record.
func (s *System) WriteEntry(e install.Entry) error {
	if s.Record == "" {
		return nil
	}
	data, err := json.Marshal(record{Dir: e.Dir, Version: e.Version, InstallDate: e.InstallDate})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Record), 0o755); err != nil {
		return fmt.Errorf("install: writing the install record: %w", err)
	}
	if err := os.WriteFile(s.Record, data, 0o644); err != nil {
		return fmt.Errorf("install: writing the install record: %w", err)
	}
	return nil
}

// DeleteEntry implements install.System.
func (s *System) DeleteEntry() error {
	if s.Record == "" {
		return nil
	}
	if err := os.Remove(s.Record); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("install: deleting the install record: %w", err)
	}
	return nil
}

// FileVersion implements install.System: golauncher's version from the
// Info.plist of the bundle exe is in.
func (s *System) FileVersion(exe string) (string, error) {
	app, ok := install.BundleOf(exe)
	if !ok {
		return "", fmt.Errorf("install: %s is not in an app bundle", exe)
	}
	info, err := readInfo(app)
	if err != nil {
		return "", err
	}
	if v := install.PlistVersion(info); v != "" {
		return v, nil
	}
	return "", errors.New("install: the Info.plist has no version")
}

// readInfo reads the string values of a bundle's Info.plist, converting a
// binary one with plutil.
func readInfo(app string) (map[string]string, error) {
	path := filepath.Join(app, "Contents", "Info.plist")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	info, err := install.PlistStrings(data)
	if errors.Is(err, install.ErrBinaryPlist) {
		out, cerr := exec.Command("/usr/bin/plutil", "-convert", "xml1", "-o", "-", "--", path).Output()
		if cerr != nil {
			return nil, fmt.Errorf("install: converting %s: %w", path, cerr)
		}
		return install.PlistStrings(out)
	}
	return info, err
}

// CreateShortcut implements install.System: there are no shortcuts.
func (*System) CreateShortcut(string, install.Shortcut) error { return errors.ErrUnsupported }

// ShortcutTarget implements install.System.
func (*System) ShortcutTarget(string) (string, error) { return "", errors.ErrUnsupported }

// processNames are the names a running golauncher may have.
func processNames(quitter string) []string {
	names := []string{install.BundleExeName}
	if b := filepath.Base(quitter); b != names[0] {
		names = append(names, b)
	}
	return names
}

// StopInstances implements install.System.
func (s *System) StopInstances(quitter string, timeout time.Duration) error {
	names := processNames(quitter)
	if len(others(names)) == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	cmd := exec.Command(quitter, "--quit")
	cmd.Dir = filepath.Dir(quitter)
	cmd.Env = append(os.Environ(), install.EnvNoInstall+"=1")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("install: asking golauncher to quit: %w", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Until(deadline)):
		return install.ErrStillRunning
	}
	for {
		if len(others(names)) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return install.ErrStillRunning
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// others lists the processes of this user, other than this one, named one
// of names. The kernel keeps the first 16 bytes of a name.
func others(names []string) []int {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil
	}
	const szomb = 5 // a zombie has ended
	self := os.Getpid()
	var out []int
	for _, p := range procs {
		pid := int(p.Proc.P_pid)
		if pid == self || p.Proc.P_stat == szomb {
			continue
		}
		comm := unix.ByteSliceToString(p.Proc.P_comm[:])
		if slices.ContainsFunc(names, func(n string) bool { return comm == n || len(comm) >= 15 && strings.HasPrefix(n, comm) }) {
			out = append(out, pid)
		}
	}
	return out
}

// openArgs returns the arguments of /usr/bin/open that start a new
// instance of the app bundle app with args and env (VAR=value), waiting
// for it to quit when wait is set. Launch Services starts the app, so it
// is not a child of the caller and macOS attributes its permissions to it.
func openArgs(app string, args, env []string, wait bool) []string {
	out := []string{"-n"}
	if wait {
		out = append(out, "-W")
	}
	for _, e := range env {
		out = append(out, "--env", e)
	}
	out = append(out, "-a", app)
	if len(args) > 0 {
		out = append(append(out, "--args"), args...)
	}
	return out
}

// Start implements install.System: an executable in an app bundle is
// opened through Launch Services, another one started directly.
func (s *System) Start(exe string, args []string) error {
	if app, ok := install.BundleOf(exe); ok {
		if out, err := exec.Command("/usr/bin/open", openArgs(app, args, nil, false)...).CombinedOutput(); err != nil {
			return fmt.Errorf("install: opening %s: %w: %s", app, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return StartDetached(exe, args)
}

// removalScript deletes its arguments once process $1 has ended (waiting
// at most 30 seconds), then folder $2 if it is empty. Paths are
// arguments, never part of the script.
const removalScript = `pid=$1; dir=$2; shift 2; i=0
while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 30 ]; do sleep 1; i=$((i+1)); done
rm -rf -- "$@"; rmdir -- "$dir" 2>/dev/null; exit 0`

// ScheduleRemoval implements install.System: a detached shell deletes
// files (bundles are folders) and then dir, if empty, once this process
// has ended.
func (s *System) ScheduleRemoval(dir string, files []string) error {
	args, err := removalArgs(os.Getpid(), dir, files)
	if err != nil {
		return err
	}
	return StartDetached("/bin/sh", args)
}

// removalArgs returns the arguments of /bin/sh that run removalScript. It
// refuses relative paths and the folders that hold everything.
func removalArgs(pid int, dir string, files []string) ([]string, error) {
	home, _ := os.UserHomeDir()
	for _, f := range append(slices.Clone(files), dir) {
		c := filepath.Clean(f)
		if !filepath.IsAbs(f) || c == "/" || c == "/Applications" || c == filepath.Clean(home) || c == filepath.Dir(filepath.Clean(home)) {
			return nil, fmt.Errorf("install: will not remove %q", f)
		}
	}
	return append([]string{"-c", removalScript, "sh", strconv.Itoa(pid), dir}, files...), nil
}

// StartDetached starts exe with args in a session of its own, without
// waiting for it (the update helper outlives golauncher).
func StartDetached(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("install: starting %s: %w", exe, err)
	}
	return cmd.Process.Release()
}
