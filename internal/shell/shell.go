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
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/builtin"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/control"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/theme"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/internal/update"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	wincontext "github.com/eziq1/golauncher/internal/winapi/context"
	winstall "github.com/eziq1/golauncher/internal/winapi/install"
	"github.com/eziq1/golauncher/internal/winapi/process"
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
	// installDir is the installation when this is the installed copy, the
	// only one that updates itself ("" otherwise).
	installDir string
	// managedBy is the package manager that installed this copy and updates
	// it (update.DisabledWinget, update.DisabledHomebrew; "" for none): it
	// offers no installation and leaves updates to that manager
	// (docs/winget-packaging.md, docs/homebrew-packaging.md).
	managedBy string
}

// Command-line actions.
const (
	actionShow     = "show"
	actionSettings = "settings"
	actionQuit     = "quit"
	// actionBackground starts without showing the launcher (golauncher mcp
	// starts it so).
	actionBackground = "background"
	actionNone       = "" // started without arguments
)

// parseArgs returns what the command line asks for: --show, --settings or
// --quit. Unknown arguments are ignored.
func parseArgs(args []string) string {
	fs := flag.NewFlagSet(AppName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	show := fs.Bool("show", false, "show the launcher")
	settings := fs.Bool("settings", false, "open the settings")
	quit := fs.Bool("quit", false, "quit the running instance")
	background := fs.Bool("background", false, "start without showing the launcher")
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
	case *background:
		return actionBackground
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
	hints       hints // the permission hints of this run (permission.go)
	tray        *mygo.Tray
	hotkey      string // the registered launcher hotkey, "" if none
	hotkeySig   string // what the registered hotkeys were made from
	failedKey   string // the launcher hotkey last reported as failing
	suspended   bool   // hotkeys are off while one is being recorded
	quitting    bool   // windows close for good
	// quitConfirmed is set once the user agreed to stop the running
	// background tasks (or the quit came from another instance);
	// askingQuit while the question shows.
	quitConfirmed bool
	askingQuit    bool

	keepLoginItem bool // see Options

	trim *trimmer // gives freed memory back once idle (R125)

	// updates keeps golauncher up to date (docs/auto-update.md); plugins
	// are the loaded plugins, asked whether they are busy before an update
	// restarts golauncher.
	updates *update.Service
	plugins []plugin.Plugin

	// prevForeground is the window that was active before the launcher
	// showed, which Paste pastes into.
	prevForeground atomic.Uintptr
	stopClip       func()
	// sel reads the selected text, for the launcher and the Action Ring.
	sel *selectionReader
	// copying is set while the selected text is copied before the launcher
	// shows (main thread only); hotkeys are ignored meanwhile.
	copying bool
	// choosing is set while the launcher's open dialog is shown
	// (launcherHost.ChooseFiles): losing the focus to it does not hide it.
	choosing atomic.Bool

	// The appearance applied to the windows last (main thread only).
	source        mygo.ThemeSource
	material      theme.Material
	windowOpacity float64
	// previewing is set while the launcher shows beside the settings as
	// a preview of the appearance (previewLauncher).
	previewing bool

	// ring is the Action Ring (ring.go).
	ring ringState
}

// Run runs golauncher until it quits. A second instance hands its arguments
// to the running one and returns at once. A copy that is not installed
// first offers to install itself (docs/self-install.md).
func Run(o Options) error {
	if len(o.Args) == 2 && o.Args[0] == update.FlagApply {
		return runUpdateHelper(o.Args[1]) // golauncher-update.exe
	}
	// Before anything starts a program, which must not inherit it.
	healthMarker := update.HealthMarker()
	if len(o.Args) > 0 && o.Args[0] == commandMCP {
		return runBridge(o)
	}
	// The hidden elevated helper that ends one process (or tree) and exits
	// with a code telling the outcome: no window, no log, no single-instance
	// lock (docs/process-manager.md §5.5).
	if len(o.Args) > 0 && o.Args[0] == process.HelperCommand {
		os.Exit(process.HelperMain(o.Args))
	}
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
	fixPath(log) // before the plugins look up programs

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Shell{log: log, level: level, version: o.Version, ctx: ctx, cancel: cancel, keepLoginItem: o.keepLoginItem}
	builtin.HostVersion = o.Version
	devToolsVersion = o.Version
	plugins := builtin.All
	if o.Plugins != nil {
		plugins = o.Plugins
	}
	s.plugins = plugins()
	s.updates = s.newUpdates(o)
	updatePlugin := update.NewPlugin(s.updates, func() update.Mode { return s.updateSettings().Mode })
	s.env, err = app.Build(ctx, app.Options{
		Plugins:   append(slices.Clone(s.plugins), updatePlugin),
		System:    system{s},
		Clipboard: clipboard{},
		Log:       log,
		Version:   o.Version,
		Platform:  messagePlatform(),
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
	s.sel = newSelectionReader(s.env.Config.Get, s.post, log, func() {
		s.env.Engine.Notify(core.Notice{Toast: plugin.Toast{Style: plugin.ToastFailure, Title: s.loc.T("toast.clipboardNotRestored")}})
	})
	// Before the windows exist, the appearance of the theme the
	// configuration fixes, if any; ready applies the rest.
	if a := cfg.Appearance; !a.FollowSystem {
		if t, ok := s.env.Themes.Library().Get(a.Theme); ok {
			s.source = themeSource(t.Dark)
			mygo.Theme.SetSource(s.source)
		}
	}

	s.work = ui.NewDispatcher(ctx, log)
	s.launcher = ui.NewLauncher(s.env, launcherHost{s}, s.work)
	s.settings = ui.NewSettings(s.env, settingsHost{s}, o.Version)
	s.settings.SetUpdater(s.updates)

	mygo.App.OnWindowAllClosed(func() {}) // live in the tray
	mygo.App.OnSecondInstance(func(args []string, _ string) {
		log.Info("second instance", "args", args)
		s.handle(parseArgs(args))
	})
	// macOS: opening the app while it runs (double-clicking it, open -a)
	// reopens it instead of starting a second instance; that shows the
	// launcher, as --show does.
	mygo.App.OnActivate(func(bool) {
		if s.panel != nil {
			log.Info("reopen")
			s.handle(actionShow)
		}
	})
	// Closing a window only hides it, except when quitting.
	// Quitting while background tasks run asks first.
	mygo.App.OnBeforeQuit(s.beforeQuit)
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { s.shutdown() })
	mygo.App.WhenReady(func() {
		s.ready(action)
		s.startUpdates(healthMarker)
	})
	if runtime.GOOS == "darwin" {
		// A menu bar app: no Dock icon and no menu bar of its own; the
		// panel still takes the keyboard (docs/macos-port.md §3.1).
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
	err = mygo.App.Run()
	log.Info("quit", "err", err)
	return err
}

// messagePlatform is the platform whose message variants the shell shows:
// "darwin" on macOS (i18n.Manager.SetPlatform).
func messagePlatform() string {
	if runtime.GOOS == "darwin" {
		return "darwin"
	}
	return ""
}

// ControlEnv enables the E2E control channel (glctl attach) when set to 1.
const ControlEnv = "GOLAUNCHER_CONTROL"

// ready creates the windows, the tray icon and the hotkeys.
func (s *Shell) ready(action string) {
	// MyGo's own controls and the text engine follow the UI language, which
	// also picks Chinese rather than Japanese glyphs for Han characters.
	mygo.App.SetLanguages(s.env.I18n.Available()...)
	mygo.App.SetLocale(s.env.I18n.Locale())
	s.panel = mygo.NewWindow(mygo.WindowOptions{
		Page:          mygo.PageOptions{DevTools: devTools()},
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
	s.trim = newTrimmer(func(done func(bool)) {
		s.post(func() { done(s.panel.IsVisible()) })
	})
	hideCaptionColor(s.panel.NativeHandle()) // ready runs on the main thread
	s.watchPermissions()
	s.panel.OnClose(func(e *mygo.CloseEvent) {
		if s.quitting {
			return
		}
		e.PreventDefault()
		s.panel.Hide()
	})
	s.panel.OnBlur(func() {
		// The open dialog of an attachment (launcherHost.ChooseFiles) takes
		// the focus: the launcher stays.
		if s.env.Config.Get().HideOnBlur && !s.choosing.Load() {
			s.panel.Hide()
		}
	})
	// Files dropped onto the launcher go to the current view (R91).
	s.panel.OnFileDrop(func(e *mygo.FileDropEvent) { s.launcher.DropFiles(e.Paths) })
	// Every way of hiding the panel ends in Window.Hide, which fires OnHide
	// once per hide; plugins stop their refresh timers on this event.
	s.panel.OnHide(func() {
		touched()
		s.env.Engine.ClearSelection() // the selected text lives for one show only
		s.launcher.Hidden()           // the top view may ask to be kept (docs/keep-view.md)
		s.env.Services.Bus.Publish(plugin.EventWindowHidden)
		s.trim.schedule()
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
	s.watchAppearance()

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

	s.env.Controller.OnExternalConfirm(s.externalConfirm)
	s.serveAgent()
	s.setUpTray()
	s.setUpTasks()
	if err := s.env.Config.Recovered(); err != nil {
		s.configRecovered(err)
	}
	s.syncLoginItem(s.env.Config.Get())
	s.registerHotkeys()
	s.setUpRing()
	s.prepareOnboarding()
	s.trim.schedule() // the startup's garbage, once its background work is done
	switch {
	case action == actionSettings:
		s.showSettings(ui.PageGeneral)
	case action == actionBackground:
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
		// From another instance (e.g. the installer): no question.
		s.quitConfirmed = true
		mygo.App.Quit()
	case actionSettings:
		s.showSettings(ui.PageGeneral)
	case actionBackground: // golauncher mcp found it running after all
	default:
		s.showLauncher()
	}
}

func (s *Shell) shutdown() {
	if s.trim != nil {
		s.trim.stop()
	}
	s.stopRing()
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
func (s *Shell) showLauncher() { s.showLauncherThen(nil) }

// showLauncherThen is showLauncher; then (if not nil) runs on the main
// thread once the launcher was reset for showing.
func (s *Shell) showLauncherThen(then func()) { s.show(then, false, true) }

// showLauncherFor shows the launcher at what then opens (a command), without
// reading the selected text: nothing of the show's own (the root with the
// selection) comes after it.
func (s *Shell) showLauncherFor(then func()) { s.show(then, false, false) }

// showForSelection shows the launcher for the selected text (the
// selection hotkey, docs/selected-text.md §8): as showLauncher, telling the
// user when no selected text was read.
func (s *Shell) showForSelection() { s.show(nil, true, true) }

// show shows the launcher. Main thread only. When it was hidden, the window
// that was active is recorded and its context and selected text captured
// before the launcher takes the focus; when the selection must be copied
// (a simulated Ctrl+C), that happens first, off the main thread, and the
// launcher shows once it is done (hotkeys pressed meanwhile are ignored).
// Without readSel, no selected text is read.
func (s *Shell) show(then func(), forSelection, readSel bool) {
	if s.panel == nil || s.copying {
		return
	}
	s.previewing = false
	if s.panel.IsVisible() {
		if then != nil {
			then()
		}
		s.reveal()
		return
	}
	fg := clip.ForegroundWindow()
	s.prevForeground.Store(fg)
	// Before the root search of this show starts (Shown resets it): the
	// context arrives in the background and refreshes the root.
	s.env.Engine.DetectContext(func(ctx context.Context) (plugin.Context, bool) { return wincontext.Detect(ctx, fg) })
	if !readSel {
		s.present(nil, then, forSelection)
		return
	}
	// The focused element, before we take the focus; a copy, if the
	// settings ask for one, before the launcher shows.
	s.copying = s.launcherSelection(fg, func(sel selCapture) {
		s.copying = false
		s.present(sel, then, forSelection)
	})
}

// launcherSelection starts capturing the text selected in window fg as the
// launcher shows (selectionReader.Capture, with the copy fallback the
// settings allow); done runs on the main thread, it reports whether later.
func (s *Shell) launcherSelection(fg uintptr, done func(selCapture)) bool {
	return s.sel.Capture(fg, true, done)
}

// present shows the hidden launcher for a new show whose selection capture
// began with sel (nil: none). Main thread only.
func (s *Shell) present(sel selCapture, then func(), forSelection bool) {
	var read chan bool // the outcome of reading the selection, for forSelection
	if forSelection {
		read = make(chan bool, 1)
	}
	// Nothing can be read without a permission (macOS): say so instead of
	// "no selected text".
	noPermission := sel != nil && sel.Err() != nil
	if noPermission {
		read = nil
	}
	if sel != nil {
		s.env.Engine.DetectSelection(func(ctx context.Context) (plugin.Selection, bool) {
			v, ok := sel.Read(ctx)
			if read != nil {
				read <- ok
			}
			return v, ok
		})
	}
	d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
	w, h := s.panel.Size()
	s.panel.SetPosition(d.WorkArea.X+(d.WorkArea.Width-w)/2, d.WorkArea.Y+(d.WorkArea.Height-h)/3)
	mygo.RunOnMain(func() {
		// Views kept by the last hide come back, except for the selected-text
		// hotkey and a show that opens a command or task (docs/keep-view.md §3).
		s.launcher.ShownWith(core.ShowOptions{Root: forSelection || then != nil})
		if noPermission {
			s.permissionHint(sel.Err()) // after ShownWith's reset, which clears toasts
		} else {
			s.onboardingHint()
		}
		if read != nil {
			// After Shown's reset, which clears toasts.
			s.work.Do(func(context.Context) {
				go func() {
					ok := false
					select {
					case ok = <-read:
					case <-time.After(time.Second):
					}
					if !ok {
						s.env.Controller.ShowToast(plugin.Toast{Style: plugin.ToastInfo, Title: s.loc.T("toast.noSelection")})
					}
				}()
			})
		}
		if then != nil {
			then()
		}
	})
	s.reveal()
}

// reveal shows and focuses the launcher panel.
func (s *Shell) reveal() {
	touched()
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
			Page:      mygo.PageOptions{DevTools: devTools()},
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
			touched()
			s.settings.ShowPage(ui.PageGeneral) // also stops recording a hotkey
			s.settingsWin.Hide()
			if s.previewing {
				s.hideLauncher()
			}
		})
		// Back from System Settings: the permissions may have changed.
		s.settingsWin.OnFocus(s.settings.RefreshPermissions)
	}
	if !s.settingsWin.IsVisible() || page != ui.PageGeneral {
		s.settings.ShowPage(page)
	}
	touched()
	s.settingsWin.Show()
	s.settingsWin.Focus()
	s.settingsWin.Invalidate()
}

// configChanged applies a new configuration. Main thread only.
func (s *Shell) configChanged(c config.Config) {
	s.applyAppearance()
	s.syncLoginItem(c)
	s.registerHotkeys()
	s.syncRing(c)
	s.panel.Invalidate()
	if s.settingsWin != nil {
		s.settingsWin.Invalidate()
	}
}

func (s *Shell) localeChanged() {
	mygo.App.SetLocale(s.env.I18n.Locale())
	s.updateTrayMenu()
	s.panel.Invalidate()
	if s.settingsWin != nil {
		s.settingsWin.SetTitle(s.loc.T("settings.title"))
		s.settingsWin.Invalidate()
	}
}

func (s *Shell) syncLoginItem(c config.Config) {
	if s.keepLoginItem || c.LaunchAtLogin == winstall.LoginItemOn(mygo.App.SetOpenAtLogin, mygo.App.OpenAtLogin) {
		return
	}
	if err := winstall.SetLoginItem(c.LaunchAtLogin, mygo.App.SetOpenAtLogin, mygo.App.OpenAtLogin); err != nil {
		s.log.Error("launch at login", "err", err)
	}
}

// accelerator turns a shortcut of the configuration into MyGo's syntax. On
// macOS "Ctrl" is ⌘ (Cmd) and "Control" the Control key (core.MacKeys).
func accelerator(hotkey string) string {
	parts := strings.Split(ui.NormalizeShortcut(hotkey), "+")
	for i, p := range parts {
		switch {
		case p == "Win":
			parts[i] = "Super"
		case p == "Ctrl" && core.MacKeys:
			parts[i] = "Cmd"
		case p == "Control":
			parts[i] = "Ctrl"
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
	selKey := selectionHotkey(cfg)
	sig += "\nselection=" + selKey
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
	if selKey != "" && selKey != cfg.Hotkey {
		if err := mygo.GlobalShortcut.Register(accelerator(selKey), s.showForSelection); err != nil {
			s.log.Warn("selection hotkey", "hotkey", selKey, "err", err)
		}
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

// selectionHotkey is the hotkey opening the launcher for the selected
// text, or "" when there is none or context awareness is off.
func selectionHotkey(cfg config.Config) string {
	if !cfg.ContextAware {
		return ""
	}
	return cfg.Selection.Hotkey
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
	tray, err := mygo.NewTray(mygo.TrayOptions{Icon: trayIcon(), IconIsTemplate: trayIconTemplate, ToolTip: trayToolTip(s.loc, s.env.Tasks.Active())})
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
	s.updateTrayToolTip()
	s.tray.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: s.loc.T("tray.open"), Accelerator: accelerator(s.hotkey), Click: func(*mygo.MenuItem, *mygo.Window) { s.showLauncher() }},
		{Label: s.loc.T("tray.settings"), Click: func(*mygo.MenuItem, *mygo.Window) { s.showSettings(ui.PageGeneral) }},
		{Label: s.loc.T("tray.checkUpdates"), Click: func(*mygo.MenuItem, *mygo.Window) { s.checkForUpdates() }},
		mygo.Separator(),
		{Label: s.loc.T("tray.quit"), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	}))
}

// launcherHost is the launcher view's window.
type launcherHost struct{ s *Shell }

func (h launcherHost) Hide() { h.s.hideLauncher() }

func (h launcherHost) OpenSettings() { h.s.post(func() { h.s.showSettings(ui.PageGeneral) }) }

func (h settingsHost) PreviewLauncher() { h.s.previewLauncher() }

func (h settingsHost) ChooseThemeFile(save bool, name string, done func(string, error)) {
	s := h.s
	filters := []mygo.FileFilter{{Name: s.loc.T("settings.appearance.themeFiles"), Extensions: []string{"json"}}}
	if save {
		if home, err := os.UserHomeDir(); err == nil {
			name = filepath.Join(home, "Documents", name)
		}
		opts := mygo.SaveDialogOptions{Parent: s.settingsWin, Title: s.loc.T("settings.appearance.exportTitle"), DefaultPath: name, Filters: filters}
		go func() {
			path, err := mygo.Dialog.Save(opts)
			if path != "" && filepath.Ext(path) == "" {
				path += ".json"
			}
			h.finish(func() { done(path, err) })
		}()
		return
	}
	opts := mygo.OpenDialogOptions{Parent: s.settingsWin, Title: s.loc.T("settings.appearance.importTitle"), Filters: filters}
	go func() {
		paths, err := mygo.Dialog.Open(opts)
		path := ""
		if len(paths) > 0 {
			path = paths[0]
		}
		h.finish(func() { done(path, err) })
	}()
}

// ChooseFiles shows the open dialog over the launcher, which does not hide
// while it is open, and gives the launcher the focus back afterwards. It
// runs on the launcher's Dispatcher, never on the main thread.
func (h launcherHost) ChooseFiles(o plugin.ChooseFilesOptions) ([]string, error) {
	s := h.s
	if s.panel == nil {
		return nil, nil
	}
	s.choosing.Store(true)
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: s.panel, Title: o.Title, Multiple: o.Multiple})
	s.post(func() {
		s.choosing.Store(false)
		if s.panel.IsVisible() {
			s.panel.Focus()
		}
	})
	return paths, err
}

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
