// Package shell runs golauncher on the desktop with MyGo: the single
// instance, the launcher panel and the settings window, global hotkeys, the
// tray icon, the clipboard watcher and the real side effects of actions.
// Everything that touches MyGo's app-level APIs or Win32 lives here, so the
// rest of the program stays testable without a desktop.
package shell

import (
	"context"
	"flag"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo"
	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/builtin"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/control"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	wshell "github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// AppName names the app to the system: its single-instance lock, its
// start-at-login entry and its notifications.
const AppName = "golauncher"

// Options configure Run.
type Options struct {
	// Args are the command line arguments without the program name.
	Args []string
	// Version is shown in the settings' About page.
	Version string
	// Plugins returns the plugins to load (default builtin.All).
	Plugins func() []plugin.Plugin

	// keepLoginItem leaves the login item alone: another copy is installed.
	keepLoginItem bool
}

// Command-line actions.
const (
	actionShow     = "show"
	actionSettings = "settings"
	actionQuit     = "quit"
	actionNone     = "" // started without arguments
)

// parseArgs returns what the command line asks for: --show, --settings or
// --quit. Unknown arguments are ignored.
func parseArgs(args []string) string {
	fs := flag.NewFlagSet(AppName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	show := fs.Bool("show", false, "show the launcher")
	settings := fs.Bool("settings", false, "open the settings")
	quit := fs.Bool("quit", false, "quit the running instance")
	// Arguments it does not know (e.g. MyGo's own) are skipped one by one.
	for rest := args; len(rest) > 0; rest = rest[1:] {
		if fs.Parse(rest) == nil {
			break
		}
	}
	switch {
	case *quit:
		return actionQuit
	case *settings:
		return actionSettings
	case *show:
		return actionShow
	}
	return actionNone
}

// Shell is the running desktop app.
type Shell struct {
	env     *app.Env
	log     *slog.Logger
	level   *slog.LevelVar
	loc     *i18n.Localizer
	version string
	ctx     context.Context
	cancel  context.CancelFunc

	work     *ui.Dispatcher
	launcher *ui.Launcher
	settings *ui.Settings

	// Main thread only.
	panel       *mygo.Window
	settingsWin *mygo.Window
	tray        *mygo.Tray
	hotkey      string // the registered launcher hotkey, "" if none
	hotkeySig   string // what the registered hotkeys were made from
	failedKey   string // the launcher hotkey last reported as failing
	suspended   bool   // hotkeys are off while one is being recorded
	quitting    bool   // windows close for good

	keepLoginItem bool // see Options

	// prevForeground is the window that was active before the launcher
	// showed, which Paste pastes into.
	prevForeground atomic.Uintptr
	stopClip       func()
}

// Run runs golauncher until it quits. A second instance hands its arguments
// to the running one and returns at once. A copy that is not installed
// first offers to install itself (docs/self-install.md).
func Run(o Options) error {
	if done, err := selfInstall(&o); done {
		return err
	}
	action := parseArgs(o.Args)
	mygo.App.SetName(AppName)
	if !mygo.App.RequestSingleInstanceLock() {
		return nil // the running instance handles the arguments
	}
	if action == actionQuit {
		return nil // nothing runs
	}

	logf, level, err := openLog()
	if err != nil {
		return err
	}
	defer logf.Close()
	log := slog.New(slog.NewTextHandler(logf, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Shell{log: log, level: level, version: o.Version, ctx: ctx, cancel: cancel, keepLoginItem: o.keepLoginItem}
	plugins := builtin.All
	if o.Plugins != nil {
		plugins = o.Plugins
	}
	s.env, err = app.Build(ctx, app.Options{
		Plugins:   plugins(),
		System:    system{s},
		Clipboard: clipboard{},
		Log:       log,
	})
	if err != nil {
		log.Error("start failed", "err", err)
		return err
	}
	defer s.env.Close()
	cfg := s.env.Config.Get()
	setLevel(level, cfg.LogLevel)
	log.Info("starting", "version", o.Version, "config", s.env.Config.Path())
	s.loc = s.env.I18n.Localizer(i18n.CoreNamespace)
	mygo.Theme.SetSource(themeSource(cfg.Theme))

	s.work = ui.NewDispatcher(ctx, log)
	s.launcher = ui.NewLauncher(s.env, launcherHost{s}, s.work)
	s.settings = ui.NewSettings(s.env, settingsHost{s}, o.Version)

	mygo.App.OnWindowAllClosed(func() {}) // live in the tray
	mygo.App.OnSecondInstance(func(args []string, _ string) {
		log.Info("second instance", "args", args)
		s.handle(parseArgs(args))
	})
	// Closing a window only hides it, except when quitting.
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { s.quitting = true })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { s.shutdown() })
	mygo.App.WhenReady(func() { s.ready(action) })
	err = mygo.App.Run()
	log.Info("quit", "err", err)
	return err
}

// ControlEnv enables the E2E control channel (glctl attach) when set to 1.
const ControlEnv = "GOLAUNCHER_CONTROL"

// ready creates the windows, the tray icon and the hotkeys.
func (s *Shell) ready(action string) {
	s.panel = mygo.NewWindow(mygo.WindowOptions{
		Title:         s.loc.T("app.name"),
		Width:         ui.LauncherWidth,
		Height:        ui.LauncherHeight,
		Panel:         true,
		Hidden:        true,
		Frameless:     true,
		Transparent:   true,
		Vibrancy:      mygo.VibrancyAcrylic,
		DisableResize: true,
		Content:       mui.View(s.launcher.View),
	})
	hideCaptionColor(s.panel.NativeHandle()) // ready runs on the main thread
	s.panel.OnClose(func(e *mygo.CloseEvent) {
		if s.quitting {
			return
		}
		e.PreventDefault()
		s.panel.Hide()
	})
	s.panel.OnBlur(func() {
		if s.env.Config.Get().HideOnBlur {
			s.panel.Hide()
		}
	})
	s.env.Controller.OnChange(s.panel.Invalidate)

	s.env.Config.OnChange(func(c config.Config) {
		setLevel(s.level, c.LogLevel)
		s.post(func() { s.configChanged(c) })
	})
	s.env.I18n.OnChange(func() {
		s.post(s.localeChanged)
		s.work.Do(func(ctx context.Context) { s.env.Controller.Refresh(ctx) })
	})

	if stop, err := clip.Watch(func() { s.env.Services.Bus.Publish(plugin.EventClipboardChanged) }); err != nil {
		s.log.Error("clipboard watcher", "err", err)
	} else {
		s.stopClip = stop
	}

	// The control channel lets any local process drive the launcher (and read
	// clipboard history through snapshots), so it is only for E2E tests.
	if os.Getenv(ControlEnv) == "1" {
		dataDir := filepath.Dir(s.env.Services.DataRoot)
		s.log.Warn("control channel enabled", "env", ControlEnv)
		go func() {
			if err := control.Serve(s.ctx, filepath.Join(dataDir, "control.sock"), s.env.Controller, controlWindow{s}); err != nil {
				s.log.Warn("control channel", "err", err)
			}
		}()
	}

	s.setUpTray()
	if err := s.env.Config.Recovered(); err != nil {
		s.configRecovered(err)
	}
	s.syncLoginItem(s.env.Config.Get())
	s.registerHotkeys()
	switch {
	case action == actionSettings:
		s.showSettings(ui.PageGeneral)
	case action == actionShow || !mygo.App.WasOpenedAtLogin():
		s.showLauncher()
	}
}

// post runs fn on the main thread, later.
func (s *Shell) post(fn func()) {
	if s.panel != nil {
		s.panel.Update(fn)
	} else {
		mygo.RunOnMain(fn)
	}
}

// handle carries out a command line action of a second instance.
func (s *Shell) handle(action string) {
	switch action {
	case actionQuit:
		mygo.App.Quit()
	case actionSettings:
		s.showSettings(ui.PageGeneral)
	default:
		s.showLauncher()
	}
}

func (s *Shell) shutdown() {
	mygo.GlobalShortcut.UnregisterAll()
	if s.stopClip != nil {
		s.stopClip()
	}
	if s.tray != nil {
		s.tray.Destroy()
	}
	s.cancel()
}

// showLauncher shows the launcher panel on the display with the pointer,
// centered, a third of the way down, ready to search.
func (s *Shell) showLauncher() {
	if s.panel == nil {
		return
	}
	if !s.panel.IsVisible() {
		s.prevForeground.Store(clip.ForegroundWindow())
		d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
		w, h := s.panel.Size()
		s.panel.SetPosition(d.WorkArea.X+(d.WorkArea.Width-w)/2, d.WorkArea.Y+(d.WorkArea.Height-h)/3)
		mygo.RunOnMain(s.launcher.Shown)
	}
	s.panel.Show()
	s.panel.Focus()
	s.env.Services.Bus.Publish(plugin.EventWindowShown)
}

func (s *Shell) hideLauncher() {
	if s.panel != nil {
		s.panel.Hide()
	}
}

func (s *Shell) toggleLauncher() {
	if s.panel.IsVisible() {
		s.panel.Hide()
	} else {
		s.showLauncher()
	}
}

// showSettings shows the settings window at page. Main thread only.
func (s *Shell) showSettings(page string) {
	s.hideLauncher()
	if s.settingsWin == nil {
		s.settingsWin = mygo.NewWindow(mygo.WindowOptions{
			Title:     s.loc.T("settings.title"),
			Width:     ui.SettingsWidth,
			Height:    ui.SettingsHeight,
			MinWidth:  640,
			MinHeight: 420,
			Hidden:    true,
			Content:   mui.View(s.settings.View),
		})
		s.settingsWin.OnClose(func(e *mygo.CloseEvent) {
			if s.quitting {
				return
			}
			e.PreventDefault()
			s.settings.ShowPage(ui.PageGeneral) // also stops recording a hotkey
			s.settingsWin.Hide()
		})
	}
	if !s.settingsWin.IsVisible() || page != ui.PageGeneral {
		s.settings.ShowPage(page)
	}
	s.settingsWin.Show()
	s.settingsWin.Focus()
	s.settingsWin.Invalidate()
}

// configChanged applies a new configuration. Main thread only.
func (s *Shell) configChanged(c config.Config) {
	mygo.Theme.SetSource(themeSource(c.Theme))
	s.syncLoginItem(c)
	s.registerHotkeys()
	s.panel.Invalidate()
	if s.settingsWin != nil {
		s.settingsWin.Invalidate()
	}
}

func (s *Shell) localeChanged() {
	s.updateTrayMenu()
	s.panel.Invalidate()
	if s.settingsWin != nil {
		s.settingsWin.SetTitle(s.loc.T("settings.title"))
		s.settingsWin.Invalidate()
	}
}

func (s *Shell) syncLoginItem(c config.Config) {
	if s.keepLoginItem || c.LaunchAtLogin == mygo.App.OpenAtLogin() {
		return
	}
	if err := mygo.App.SetOpenAtLogin(c.LaunchAtLogin); err != nil {
		s.log.Error("launch at login", "err", err)
	}
}

func themeSource(theme string) mygo.ThemeSource {
	switch theme {
	case "light":
		return mygo.ThemeLight
	case "dark":
		return mygo.ThemeDark
	}
	return mygo.ThemeSystem
}

// accelerator turns a shortcut of the configuration into MyGo's syntax.
func accelerator(hotkey string) string {
	parts := strings.Split(ui.NormalizeShortcut(hotkey), "+")
	for i, p := range parts {
		if p == "Win" {
			parts[i] = "Super"
		}
	}
	return strings.Join(parts, "+")
}

// registerHotkeys (re)registers the launcher hotkey and the commands'
// hotkeys when they changed. Main thread only.
func (s *Shell) registerHotkeys() {
	cfg := s.env.Config.Get()
	hotkeys := s.env.Engine.CommandHotkeys()
	sig := cfg.Hotkey
	for _, hk := range hotkeys {
		sig += "\n" + hk.Plugin + "/" + hk.Command + "=" + hk.Hotkey
	}
	if s.suspended {
		sig = "suspended"
	}
	if sig == s.hotkeySig {
		return
	}
	s.hotkeySig = sig
	mygo.GlobalShortcut.UnregisterAll()
	s.hotkey = ""
	if s.suspended {
		return
	}
	if err := mygo.GlobalShortcut.Register(accelerator(cfg.Hotkey), s.toggleLauncher); err != nil {
		s.log.Error("launcher hotkey", "hotkey", cfg.Hotkey, "err", err)
		s.hotkeyFailed(cfg.Hotkey)
	} else {
		s.log.Info("launcher hotkey registered", "hotkey", cfg.Hotkey)
		s.hotkey = cfg.Hotkey
		s.failedKey = ""
		s.settings.SetHotkeyError("")
	}
	noView := map[string]bool{}
	for _, p := range s.env.Engine.Plugins() {
		for _, c := range p.Commands {
			noView[p.ID+"/"+c.Name] = c.Mode == plugin.ModeNoView
		}
	}
	for _, hk := range hotkeys {
		if hk.Hotkey == cfg.Hotkey {
			continue
		}
		err := mygo.GlobalShortcut.Register(accelerator(hk.Hotkey), func() {
			if !noView[hk.Plugin+"/"+hk.Command] {
				s.showLauncher()
			}
			s.launcher.OpenCommand(hk.Plugin, hk.Command)
		})
		if err != nil {
			s.log.Warn("command hotkey", "plugin", hk.Plugin, "command", hk.Command, "hotkey", hk.Hotkey, "err", err)
		}
	}
	s.updateTrayMenu()
}

// hotkeyFailed tells the user that the launcher hotkey is taken, and opens
// the settings to choose another.
func (s *Shell) hotkeyFailed(hotkey string) {
	s.settings.SetHotkeyError(hotkey)
	if s.failedKey == hotkey {
		return
	}
	s.failedKey = hotkey
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:    "hotkey-failed",
		Title: s.loc.T("notify.hotkeyFailed.title"),
		Body:  s.loc.T("notify.hotkeyFailed.body", "hotkey", hotkey),
	})
	n.OnClick(func() { s.showSettings(ui.PageGeneral) })
	go func() {
		if err := n.Show(); err != nil {
			s.log.Warn("notification", "err", err)
		}
	}()
	s.showSettings(ui.PageGeneral)
}

// configRecovered tells the user that config.json could not be read and was
// replaced by the defaults (the old file is kept as config.json.bad).
func (s *Shell) configRecovered(err error) {
	s.log.Error("config file unusable; using defaults", "err", err)
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:    "config-recovered",
		Title: s.loc.T("notify.configRecovered.title"),
		Body:  s.loc.T("notify.configRecovered.body", "path", s.env.Config.Path()+".bad"),
	})
	n.OnClick(func() { s.showSettings(ui.PageAdvanced) })
	go func() {
		if err := n.Show(); err != nil {
			s.log.Warn("notification", "err", err)
		}
	}()
}

func (s *Shell) setUpTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{Icon: trayIcon(), ToolTip: s.loc.T("tray.tooltip")})
	if err != nil {
		s.log.Error("tray icon", "err", err)
		return
	}
	s.tray = tray
	s.updateTrayMenu()
}

func (s *Shell) updateTrayMenu() {
	if s.tray == nil {
		return
	}
	s.tray.SetToolTip(s.loc.T("tray.tooltip"))
	s.tray.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: s.loc.T("tray.open"), Accelerator: accelerator(s.hotkey), Click: func(*mygo.MenuItem, *mygo.Window) { s.showLauncher() }},
		{Label: s.loc.T("tray.settings"), Click: func(*mygo.MenuItem, *mygo.Window) { s.showSettings(ui.PageGeneral) }},
		mygo.Separator(),
		{Label: s.loc.T("tray.quit"), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	}))
}

// launcherHost is the launcher view's window.
type launcherHost struct{ s *Shell }

func (h launcherHost) Hide() { h.s.hideLauncher() }

func (h launcherHost) OpenSettings() { h.s.post(func() { h.s.showSettings(ui.PageGeneral) }) }

func (h launcherHost) Invalidate() {
	if h.s.panel != nil {
		h.s.panel.Invalidate()
	}
}

// settingsHost serves the settings view.
type settingsHost struct{ s *Shell }

func (h settingsHost) OpenPath(path string) error { return wshell.Open(path, plugin.OpenOptions{}) }

func (h settingsHost) SuspendHotkeys(suspend bool) {
	h.s.suspended = suspend
	h.s.registerHotkeys()
}

func (h settingsHost) ChooseExportFile(name string, done func(string, error)) {
	s := h.s
	if home, err := os.UserHomeDir(); err == nil {
		name = filepath.Join(home, "Documents", name) // the dialog starts in the last folder used if this one is missing
	}
	opts := mygo.SaveDialogOptions{
		Parent:      s.settingsWin,
		Title:       s.loc.T("settings.advanced.exportTitle"),
		DefaultPath: name,
		Filters:     h.jsonFilters(),
	}
	go func() {
		path, err := mygo.Dialog.Save(opts)
		if path != "" && filepath.Ext(path) == "" {
			path += ".json" // the dialog adds no extension of its own
		}
		h.finish(func() { done(path, err) })
	}()
}

func (h settingsHost) ChooseImportFile(done func(string, error)) {
	s := h.s
	opts := mygo.OpenDialogOptions{
		Parent:  s.settingsWin,
		Title:   s.loc.T("settings.advanced.importTitle"),
		Filters: h.jsonFilters(),
	}
	go func() {
		paths, err := mygo.Dialog.Open(opts)
		path := ""
		if len(paths) > 0 {
			path = paths[0]
		}
		h.finish(func() { done(path, err) })
	}()
}

func (h settingsHost) jsonFilters() []mygo.FileFilter {
	return []mygo.FileFilter{{Name: h.s.loc.T("settings.advanced.jsonFiles"), Extensions: []string{"json"}}}
}

// finish runs fn on the main thread and redraws the settings.
func (h settingsHost) finish(fn func()) {
	h.s.post(func() {
		fn()
		if h.s.settingsWin != nil {
			h.s.settingsWin.Invalidate()
		}
	})
}

// controlWindow is the launcher panel as the control channel sees it.
type controlWindow struct{ s *Shell }

func (w controlWindow) Show() {
	w.s.showLauncher()
	w.s.launcher.Wait()
}

func (w controlWindow) Hide()         { w.s.hideLauncher() }
func (w controlWindow) Visible() bool { return w.s.panel.IsVisible() }

var _ control.Window = controlWindow{}
