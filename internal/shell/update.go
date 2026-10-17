package shell

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/install"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/internal/update"
	winstall "github.com/eziq1/golauncher/internal/winapi/install"
	wshell "github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Automatic updates on the desktop (docs/auto-update.md): the update
// service, the "Check for Updates" entries, the safe moment to restart and
// the notifications, and the update helper (golauncher-update.exe
// --apply-update plan.json).

// updateDir returns %LOCALAPPDATA%\golauncher\update.
func updateDir() (string, error) {
	dir, err := app.DefaultCacheDir()
	return filepath.Join(dir, "update"), err
}

// userAgent identifies golauncher to GitHub.
func userAgent(version string) string {
	return "golauncher/" + version + " (+" + install.URL + ")"
}

// updateClient is the GitHub client of the updates; GOLAUNCHER_UPDATE_API
// replaces the API (to rehearse with a fake server).
func updateClient(version string) *update.Client {
	return &update.Client{
		HTTP:      update.NewHTTPClient(winstall.SystemProxy()),
		API:       os.Getenv(update.EnvAPI),
		UserAgent: userAgent(version),
	}
}

// newUpdates creates the update service of the shell. Its callbacks read
// the shell lazily: the environment does not exist yet.
func (s *Shell) newUpdates(o Options) *update.Service {
	dir, err := updateDir()
	if err != nil {
		dir = filepath.Join(os.TempDir(), "golauncher-update")
	}
	self, _ := os.Executable()
	disabled := ""
	if os.Getenv(update.EnvNoUpdate) == "1" {
		disabled = update.DisabledEnv
	}
	return update.NewService(update.Options{
		Client:      updateClient(o.Version),
		Dir:         dir,
		Current:     o.Version,
		Arch:        runtime.GOARCH,
		Self:        self,
		InstallDir:  o.installDir,
		Disabled:    disabled,
		PublicKey:   update.PublicKey,
		Bundle:      winstall.Bundled,
		Settings:    s.updateSettings,
		FileVersion: winstall.New().FileVersion,
		Busy:        s.updateBusy,
		Restart:     s.restartIntoUpdate,
		Notify:      func(e update.Event) { s.post(func() { s.updateNotify(e) }) },
		Log:         s.log,
	})
}

// updateSettings returns the user's update settings.
func (s *Shell) updateSettings() update.Settings {
	if s.env == nil {
		return update.ParseSettings("", "")
	}
	u := s.env.Config.Get().Updates
	return update.ParseSettings(u.EffectiveMode(), u.EffectiveFrequency())
}

// lastUsed is when the launcher or the settings last showed or hid (Unix
// nanoseconds).
var lastUsed atomic.Int64

func touched() { lastUsed.Store(time.Now().UnixNano()) }

// updateBusy returns why golauncher should not restart into an update now
// (update.SafeMoment).
func (s *Shell) updateBusy(now time.Time, user bool) string {
	ch := make(chan update.Activity, 1)
	s.post(func() {
		a := update.Activity{LastUsed: time.Unix(0, lastUsed.Load())}
		a.LauncherVisible = s.panel != nil && s.panel.IsVisible()
		a.SettingsVisible = s.settingsWin != nil && s.settingsWin.IsVisible()
		ch <- a
	})
	var a update.Activity
	select {
	case a = <-ch:
	case <-time.After(5 * time.Second):
		return update.BusyLauncher // the main thread is busy: not now
	}
	if s.env != nil {
		a.Tasks = s.env.Tasks.Active()
	}
	for _, p := range s.plugins {
		if b, ok := p.(interface{ Busy() bool }); ok && b.Busy() {
			a.AIBusy = true
		}
	}
	return update.SafeMoment(a, now, user)
}

// restartIntoUpdate starts the update helper and quits; the helper installs
// the update once this process has ended.
func (s *Shell) restartIntoUpdate(planFile string) error {
	if err := winstall.StartDetached(update.HelperPathFor(planFile, winstall.Bundled), []string{update.FlagApply, planFile}); err != nil {
		return err
	}
	s.log.Info("update: quitting for the update helper")
	s.post(func() {
		s.quitConfirmed = true
		mygo.App.Quit()
	})
	return nil
}

// updateNotify tells the user about an update. Main thread only.
func (s *Shell) updateNotify(e update.Event) {
	var title, body string
	open := func() { s.showSettings(ui.PageAbout) }
	switch e.Kind {
	case update.EventUpdated:
		title = s.loc.T("notify.updated.title", "version", e.Version)
		body = s.loc.T("notify.updated.body")
		open = func() { s.openURL(e.Page) }
	case update.EventRolledBack:
		title = s.loc.T("notify.rolledBack.title", "version", e.Version)
		body = s.loc.T("notify.rolledBack.body", "version", e.From)
	case update.EventAvailable:
		title = s.loc.T("notify.updateAvailable.title", "version", e.Version)
		body = s.loc.T("notify.updateAvailable.body")
	default:
		return
	}
	s.showNotification("update-"+string(e.Kind), title, body, open)
}

// showNotification shows a Windows notification; click runs on the main
// thread when it is clicked.
func (s *Shell) showNotification(id, title, body string, click func()) {
	n := mygo.NewNotification(mygo.NotificationOptions{ID: id, Title: title, Body: body})
	n.OnClick(func() { s.post(click) })
	go func() {
		if err := n.Show(); err != nil {
			s.log.Warn("notification", "err", err)
		}
	}()
}

func (s *Shell) openURL(u string) {
	if u == "" {
		u = update.ReleasesURL
	}
	if err := wshell.Open(u, plugin.OpenOptions{}); err != nil {
		s.log.Warn("open", "url", u, "err", err)
	}
}

// checkForUpdates checks as the user asked from the tray and tells the
// answer in a notification.
func (s *Shell) checkForUpdates() {
	go func() {
		st, err := s.updates.Check(s.ctx)
		mode := s.updateSettings().Mode
		title, body, _ := update.Summary(s.loc, st, err, mode)
		s.post(func() {
			click := func() { s.showSettings(ui.PageAbout) }
			if st.Available && st.Disabled != "" {
				click = func() { s.openURL(st.Page) }
			}
			s.showNotification("update-check", title, body, click)
		})
	}()
}

// startUpdates runs the update service; when the update helper started
// this golauncher, it reports itself healthy once it has run well.
func (s *Shell) startUpdates(marker string) {
	s.updates.Start(s.ctx)
	if marker == "" {
		return
	}
	go func() {
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(update.HealthyAfter):
		}
		if err := update.WriteMarker(marker); err != nil {
			s.log.Error("update: health marker", "err", err)
			return
		}
		s.log.Info("update: reported healthy")
		// The helper records the result right after it sees the marker.
		for range 10 {
			time.Sleep(time.Second)
			s.updates.Refresh()
			if r, ok := update.ReadResult(filepath.Dir(marker)); ok && r.Outcome != update.OutcomeApplying {
				return
			}
		}
	}()
}

// runUpdateHelper is golauncher-update.exe --apply-update plan.json: it
// installs the update and exits (docs/auto-update.md §5.2).
func runUpdateHelper(planFile string) error {
	logf, closeLog := helperLog()
	defer closeLog()
	log := slog.New(slog.NewTextHandler(logf, nil))
	p, err := update.ReadPlan(planFile)
	if err != nil {
		log.Error("update helper: reading the plan", "err", err)
		return err
	}
	log.Info("update helper: start", "from", p.From, "to", p.To, "dir", p.InstallDir)
	h := &update.Helper{
		Sys: winstall.HelperSystem{},
		NewInstaller: func(self, version string) *install.Installer {
			in, err := newInstaller(self, version)
			if err != nil {
				log.Error("update helper: installer", "err", err)
				return &install.Installer{Sys: winstall.New(), Self: self, Version: version, Bundle: winstall.Bundled}
			}
			return in
		},
		Log: log,
	}
	r := h.Apply(context.Background(), p)
	log.Info("update helper: done", "outcome", r.Outcome, "err", r.Error)
	return nil
}

// helperLog opens logs\update.log for appending (or discards).
func helperLog() (io.Writer, func()) {
	cache, err := app.DefaultCacheDir()
	if err == nil {
		dir := filepath.Join(cache, "logs")
		if err = os.MkdirAll(dir, 0o755); err == nil {
			path := filepath.Join(dir, "update.log")
			if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogSize {
				_ = os.Rename(path, path+".old")
			}
			if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
				return f, func() { f.Close() }
			}
		}
	}
	return io.Discard, func() {}
}
