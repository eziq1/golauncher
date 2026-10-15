package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/eziq1/golauncher/internal/config"
)

// Options configure Install.
type Options struct {
	// Dir is the install location, an absolute path.
	Dir string
	// LaunchAtLogin and DesktopShortcut are the user's choices for a new
	// installation.
	LaunchAtLogin   bool
	DesktopShortcut bool
	// Upgrade replaces the installation in Dir (an upgrade or a downgrade):
	// its desktop shortcut and settings are kept as they are.
	Upgrade bool
	// Description is the shortcuts' comment.
	Description string
}

// Step is a stage of Install, reported as it begins.
type Step int

const (
	StepStopping    Step = iota // asking a running golauncher to quit
	StepCopying                 // copying the program files
	StepShortcuts               // creating the shortcuts
	StepRegistering             // writing the settings and the uninstall entry
)

// Install installs the running copy into o.Dir, or replaces the
// installation there, and returns the installed executable. A running
// golauncher is asked to quit first. When a step fails, what was done is
// undone: replaced files are restored from their backups and new shortcuts
// removed; the uninstall entry is written last.
func (in *Installer) Install(o Options, progress func(Step)) (exe string, err error) {
	report := func(s Step) {
		if progress != nil {
			progress(s)
		}
	}
	if o.Dir == "" || !filepath.IsAbs(o.Dir) {
		return "", ErrInvalidDir
	}
	dir := filepath.Clean(o.Dir)
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		return "", ErrInvalidDir
	}

	report(StepStopping)
	if err := in.Sys.StopInstances(in.Self, StopTimeout); err != nil {
		return "", err
	}

	report(StepCopying)
	t := &txn{}
	defer func() {
		if err != nil {
			err = errors.Join(err, t.rollback())
		}
	}()
	if !exists(dir) {
		if err = os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		t.dir = dir
	}
	exe = filepath.Join(dir, ExeName)
	files := [][2]string{{in.Self, exe}}
	if ctl := filepath.Join(filepath.Dir(in.Self), CtlName); isFile(ctl) {
		files = append(files, [2]string{ctl, filepath.Join(dir, CtlName)})
	}
	var size int64
	for _, f := range files {
		if err = t.place(f[0], f[1]); err != nil {
			return "", err
		}
		if fi, err := os.Stat(f[1]); err == nil {
			size += fi.Size()
		}
	}

	report(StepShortcuts)
	sc := Shortcut{Target: exe, Dir: dir, Description: o.Description, Icon: exe}
	if err = t.shortcut(in.Sys, in.Paths.StartMenuLink, sc); err != nil {
		return "", err
	}
	if !o.Upgrade && o.DesktopShortcut {
		if err = t.shortcut(in.Sys, in.Paths.DesktopLink, sc); err != nil {
			return "", err
		}
	}

	report(StepRegistering)
	if !o.Upgrade {
		if err = in.setLaunchAtLogin(o.LaunchAtLogin); err != nil {
			return "", err
		}
	}
	if err = in.Sys.WriteEntry(in.entry(dir, exe, size)); err != nil {
		return "", err
	}
	t.commit()
	return exe, nil
}

// entry returns the uninstall entry of an installation in dir.
func (in *Installer) entry(dir, exe string, size int64) Entry {
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	return Entry{
		Name:        AppName,
		Version:     in.Version,
		Publisher:   Publisher,
		Dir:         dir,
		Icon:        exe,
		Uninstall:   `"` + exe + `" --uninstall`,
		URL:         URL,
		InstallDate: now().Format("20060102"),
		SizeKB:      uint32(min((size+1023)/1024, math.MaxUint32)),
	}
}

// setLaunchAtLogin records the choice in the configuration. The installed
// program applies it when it starts, so that the login item runs the
// installed copy. A configuration that cannot be read is left alone (the
// program recovers it at start).
func (in *Installer) setLaunchAtLogin(on bool) error {
	path := in.Paths.ConfigFile
	if path == "" {
		return nil
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.LaunchAtLogin == on {
		return nil
	}
	cfg.LaunchAtLogin = on
	if err := config.Save(path, cfg); err != nil {
		return fmt.Errorf("install: saving the settings: %w", err)
	}
	return nil
}

// UninstallOptions configure Uninstall.
type UninstallOptions struct {
	// PurgeData also deletes the user's settings and data (Paths.DataDirs).
	PurgeData bool
}

// Uninstall removes the installation: it asks a running golauncher to
// quit, removes the shortcuts that point to it, the login item and the
// uninstall entry, the user's data if asked, and schedules the program
// files for deletion once this process exits. It goes on after a failed
// step and reports every failure.
func (in *Installer) Uninstall(o UninstallOptions) error {
	e, ok, err := in.Sys.ReadEntry()
	if err != nil {
		return err
	}
	if !ok || e.Dir == "" {
		return ErrNotInstalled
	}
	dir := filepath.Clean(e.Dir)
	exe := filepath.Join(dir, ExeName)
	if err := in.Sys.StopInstances(in.Self, StopTimeout); err != nil {
		return err
	}
	var errs []error
	for _, link := range []string{in.Paths.StartMenuLink, in.Paths.DesktopLink} {
		if link == "" || !exists(link) {
			continue
		}
		if target, err := in.Sys.ShortcutTarget(link); err == nil && SamePath(target, exe) {
			errs = append(errs, os.Remove(link))
		}
	}
	if in.RemoveLoginItem != nil {
		errs = append(errs, in.RemoveLoginItem())
	}
	errs = append(errs, in.Sys.DeleteEntry())
	if o.PurgeData {
		for _, d := range in.Paths.DataDirs {
			errs = append(errs, os.RemoveAll(d))
		}
	}
	var files []string
	for _, name := range []string{ExeName, CtlName} {
		for _, suffix := range []string{"", ".old", ".new"} {
			if p := filepath.Join(dir, name+suffix); exists(p) {
				files = append(files, p)
			}
		}
	}
	errs = append(errs, in.Sys.ScheduleRemoval(dir, files))
	return errors.Join(errs...)
}

// Installed reports the installation's folder, if there is one.
func (in *Installer) Installed() (dir string, ok bool, err error) {
	e, ok, err := in.Sys.ReadEntry()
	if err != nil || !ok || e.Dir == "" {
		return "", false, err
	}
	return e.Dir, true, nil
}

// Cleanup removes the backups an upgrade could not delete (the old program
// may still have been running). The installed program calls it at start.
func Cleanup(dir string) {
	for _, name := range []string{ExeName, CtlName} {
		_ = os.Remove(filepath.Join(dir, name+".old"))
	}
}

// txn undoes the changes of a failed Install.
type txn struct {
	placed []placed
	links  []string // shortcuts that did not exist before
	dir    string   // the install folder, if Install created it
}

type placed struct {
	dst    string
	backup string // the file dst replaced, "" if none
}

// place copies src to dst: through dst.new, keeping a file already at dst
// as dst.old until commit. A running program can be renamed but not
// overwritten, so this works even if it still runs.
func (t *txn) place(src, dst string) error {
	if SamePath(src, dst) {
		return nil // installing into the folder it runs from
	}
	tmp := dst + ".new"
	if err := copyFile(src, tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	p := placed{dst: dst}
	if exists(dst) {
		p.backup = dst + ".old"
		if err := os.Remove(p.backup); err != nil && !errors.Is(err, fs.ErrNotExist) {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Rename(dst, p.backup); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		if p.backup != "" {
			_ = os.Rename(p.backup, dst)
		}
		_ = os.Remove(tmp)
		return err
	}
	t.placed = append(t.placed, p)
	return nil
}

// shortcut creates a shortcut and remembers it if it is new.
func (t *txn) shortcut(sys System, link string, s Shortcut) error {
	if link == "" {
		return nil
	}
	existed := exists(link)
	if err := sys.CreateShortcut(link, s); err != nil {
		return err
	}
	if !existed {
		t.links = append(t.links, link)
	}
	return nil
}

func (t *txn) rollback() error {
	var errs []error
	for _, l := range t.links {
		errs = append(errs, os.Remove(l))
	}
	for i := len(t.placed) - 1; i >= 0; i-- {
		p := t.placed[i]
		errs = append(errs, os.Remove(p.dst))
		if p.backup != "" {
			errs = append(errs, os.Rename(p.backup, p.dst))
		}
	}
	if t.dir != "" {
		_ = os.Remove(t.dir) // only if empty
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("install: undoing the changes: %w", err)
	}
	return nil
}

func (t *txn) commit() {
	for _, p := range t.placed {
		if p.backup != "" {
			_ = os.Remove(p.backup) // in use: Cleanup removes it later
		}
	}
}

// copyFile copies src to dst and flushes it to disk.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Sync(), out.Close())
}
