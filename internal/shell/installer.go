package shell

import (
	"errors"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"
	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/install"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/internal/update"
	winstall "github.com/eziq1/golauncher/internal/winapi/install"
)

// selfInstall installs, upgrades or uninstalls golauncher when it should
// (docs/self-install.md). Run calls it first, before the single-instance
// lock: that lock is shared by every copy, and which copy runs matters
// here. done reports that Run should return err without starting.
func selfInstall(o *Options) (done bool, err error) {
	f := install.ParseArgs(o.Args)
	self, err := os.Executable()
	if err != nil {
		return false, nil
	}
	if real, err := filepath.EvalSymlinks(self); err == nil {
		self = real // e.g. winget's WinGet\Links\golauncher.exe
	}
	// A package manager installs, updates and removes this copy.
	o.managedBy = packageManager(self)
	in, err := newInstaller(self, o.Version)
	if err != nil {
		return false, nil // the folders are unknown: just run
	}
	if f.Uninstall {
		return true, runUninstaller(in)
	}
	st, err := in.Detect()
	if err != nil {
		return false, nil
	}
	// Another copy is installed: its login item is not ours to change.
	o.keepLoginItem = st.Portable()
	if st.Self {
		install.Cleanup(st.Dir)
		o.installDir = st.Dir // the installed copy updates itself
	}
	if o.managedBy != "" || install.Skip(o.Version, f, os.Getenv) {
		return false, nil
	}
	switch st.Kind {
	case install.KindSame:
		if in.Sys.Start(st.Exe, o.Args) == nil {
			return true, nil
		}
		return false, nil // run this copy instead
	case install.KindFresh, install.KindChange:
		return true, runInstaller(in, st, o.Args)
	}
	return false, nil
}

// packageManager returns the package manager that installed self
// (Options.managedBy): winget on Windows (docs/winget-packaging.md),
// Homebrew on macOS (docs/homebrew-packaging.md).
func packageManager(self string) string {
	switch {
	case install.WingetManaged(self, winstall.WingetLocations()):
		return update.DisabledWinget
	case winstall.Bundled:
		home, _ := os.UserHomeDir()
		if install.HomebrewManaged(self, install.HomebrewPrefixes(os.Getenv), home) {
			return update.DisabledHomebrew
		}
	}
	return ""
}

// newInstaller returns the installer of the running copy. On macOS that
// is an app bundle; a bare executable (a development build) does not
// install itself.
func newInstaller(self, version string) (*install.Installer, error) {
	if _, ok := install.BundleOf(self); winstall.Bundled && !ok {
		return nil, errors.New("shell: not in an app bundle")
	}
	paths, err := winstall.Paths()
	if err != nil {
		return nil, err
	}
	if dir, err := app.DefaultConfigDir(); err == nil {
		paths.ConfigFile = filepath.Join(dir, "config.json")
		paths.DataDirs = append(paths.DataDirs, dir)
	}
	if dir, err := app.DefaultCacheDir(); err == nil {
		paths.DataDirs = append(paths.DataDirs, dir)
	}
	if dir, err := updateDir(); err == nil {
		paths.UpdateDir = dir
	}
	return &install.Installer{
		Sys:     winstall.New(),
		Self:    self,
		Version: version,
		Paths:   paths,
		RemoveLoginItem: func() error {
			return winstall.SetLoginItem(false, mygo.App.SetOpenAtLogin, mygo.App.OpenAtLogin)
		},
		Bundle: winstall.Bundled,
	}, nil
}

// installerLocalizer returns the core messages in the configured language,
// with the platform's message variants (id@darwin).
func installerLocalizer() *i18n.Localizer {
	lang := "system"
	if dir, err := app.DefaultConfigDir(); err == nil {
		if cfg, err := config.Load(filepath.Join(dir, "config.json")); err == nil {
			lang = cfg.Language
		}
	}
	m := i18n.NewManager(i18n.Source, nil)
	m.SetPlatform(messagePlatform())
	if err := m.Register(i18n.CoreNamespace, i18n.CoreLocales()); err == nil {
		m.SetLocale(lang)
	}
	return m.Localizer(i18n.CoreNamespace)
}

// runInstaller shows the installer window until it is done.
func runInstaller(in *install.Installer, st install.State, args []string) error {
	loc := installerLocalizer()
	mygo.App.SetName(AppName)
	h := &installerHost{in: in, args: args, loc: loc}
	view := ui.NewInstaller(loc, h, ui.InstallerInfo{Version: in.Version, State: st, Dir: in.Paths.DefaultDir, Bundle: in.Bundle})
	mygo.App.WhenReady(func() {
		h.win = mygo.NewWindow(mygo.WindowOptions{
			Page:            mygo.PageOptions{DevTools: devTools()},
			Title:           loc.T("install.windowTitle"),
			Width:           ui.InstallerWidth,
			Height:          ui.InstallerHeight,
			DisableResize:   true,
			DisableMaximize: true,
			Content:         mui.View(view.View),
		})
		h.win.OnClose(func(e *mygo.CloseEvent) {
			if view.Busy() && !h.quitting {
				e.PreventDefault() // not halfway through
			}
		})
	})
	err := mygo.App.Run()
	if h.after != nil {
		err = errors.Join(err, h.after())
	}
	return err
}

// installerHost serves the installer view. Its fields are used on the main
// thread.
type installerHost struct {
	in       *install.Installer
	args     []string
	loc      *i18n.Localizer
	win      *mygo.Window
	quitting bool
	after    func() error // runs once the event loop has ended
}

var _ ui.InstallerHost = (*installerHost)(nil)

func (h *installerHost) ChooseFolder(start string, done func(string, error)) {
	for start != "" && !isDir(start) && filepath.Dir(start) != start {
		start = filepath.Dir(start) // the dialog needs an existing folder
	}
	opts := mygo.OpenDialogOptions{
		Parent:      h.win,
		Title:       h.loc.T("install.chooseFolder"),
		DefaultPath: start,
		Directory:   true,
	}
	go func() {
		paths, err := mygo.Dialog.Open(opts)
		dir := ""
		if len(paths) > 0 {
			dir = paths[0]
		}
		h.win.Update(func() { done(dir, err) })
	}()
}

func (h *installerHost) Install(o install.Options, progress func(install.Step), done func(error)) {
	go func() {
		exe, err := h.in.Install(o, func(s install.Step) { h.win.Update(func() { progress(s) }) })
		if err == nil {
			err = h.in.Sys.Start(exe, h.args)
		}
		h.win.Update(func() {
			done(err)
			if err == nil {
				h.quit()
			}
		})
	}()
}

func (h *installerHost) RunPortable() {
	self, args := h.in.Self, append(slices.Clone(h.args), "--portable")
	h.after = func() error { return h.in.Sys.Start(self, args) }
	h.quit()
}

func (h *installerHost) Cancel() { h.quit() }

func (h *installerHost) quit() {
	h.quitting = true
	mygo.App.Quit()
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// runUninstaller asks for confirmation and uninstalls golauncher
// (golauncher --uninstall, as Apps & features runs it).
func runUninstaller(in *install.Installer) error {
	loc := installerLocalizer()
	mygo.App.SetName(AppName)
	result := make(chan error, 1)
	mygo.App.WhenReady(func() {
		go func() {
			result <- uninstall(in, loc)
			mygo.App.Quit()
		}()
	})
	err := mygo.App.Run()
	select {
	case e := <-result:
		err = errors.Join(err, e)
	default:
	}
	return err
}

// uninstall runs the dialogs and the uninstallation; it blocks.
func uninstall(in *install.Installer, loc *i18n.Localizer) error {
	title := loc.T("install.uninstall.title")
	message := func(typ mygo.MessageType, text string) {
		_, _ = mygo.Dialog.Message(mygo.MessageOptions{Type: typ, Title: title, Message: text})
	}
	dir, ok, err := in.Installed()
	if err != nil {
		message(mygo.MessageError, loc.T("install.uninstall.failed", "err", err.Error()))
		return err
	}
	if !ok {
		message(mygo.MessageInfo, loc.T("install.uninstall.notInstalled"))
		return nil
	}
	r, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type:          mygo.MessageQuestion,
		Title:         title,
		Message:       loc.T("install.uninstall.confirm"),
		Detail:        loc.T("install.uninstall.detail", "dir", dir),
		Buttons:       []string{loc.T("install.uninstall.button"), loc.T("install.cancel")},
		CancelButton:  1,
		CheckboxLabel: loc.T("install.uninstall.purge"),
	})
	if err != nil || r.Button != 0 {
		return err
	}
	err = in.Uninstall(install.UninstallOptions{PurgeData: r.CheckboxChecked})
	switch {
	case errors.Is(err, install.ErrStillRunning):
		message(mygo.MessageError, loc.T("install.errorRunning"))
	case err != nil:
		message(mygo.MessageError, loc.T("install.uninstall.failed", "err", err.Error()))
	default:
		message(mygo.MessageInfo, loc.T("install.uninstall.done"))
	}
	return err
}
