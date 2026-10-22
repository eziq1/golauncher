package ui

import (
	"sync"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/i18n"
)

// Settings window size, in DIPs.
const (
	SettingsWidth  = 900
	SettingsHeight = 620
)

// Settings pages.
const (
	PageGeneral    = "general"
	PageAppearance = "appearance"
	PagePlugins    = "plugins"
	PageAdvanced   = "advanced"
	PageAbout      = "about"
)

// SettingsHost is what the settings view needs from the system. Its methods
// are called on the UI thread.
type SettingsHost interface {
	// OpenPath opens a file or folder with its default app.
	OpenPath(path string) error
	// SuspendHotkeys turns the global hotkeys off while a hotkey is being
	// recorded, so that the keys reach the window, and on again.
	SuspendHotkeys(suspend bool)
	// ChooseExportFile asks where to save exported settings, suggesting the
	// file name name. ChooseImportFile asks for a settings file to import.
	// Both show *.json files and return at once; done runs later on the UI
	// thread with the chosen path, or "" when the user canceled.
	ChooseExportFile(name string, done func(path string, err error))
	ChooseImportFile(done func(path string, err error))
	// ChooseThemeFile asks where to save a theme (save, suggesting name)
	// or for a theme to import, as ChooseExportFile does.
	ChooseThemeFile(save bool, name string, done func(path string, err error))
	// PreviewLauncher shows the launcher beside the settings, and keeps it
	// shown, while the appearance is adjusted.
	PreviewLauncher()
}

// Settings is the settings window's view (design §6). Every change is
// written through config.Store.Update and takes effect at once.
type Settings struct {
	env     *app.Env
	loc     *i18n.Localizer
	host    SettingsHost
	version string

	page    string
	plugin  string // the plugin whose page shows, "" for the list
	sidebar float32

	recording string             // the hotkey recorder that is recording, "" for none
	bufs      map[string]*string // text being edited, by field
	errs      map[string]string  // validation errors, by field
	stored    map[string]bool    // whether a password preference has a secret, by field
	notice    string             // a message on the current page
	noticeErr bool               // the notice reports a failure
	saveErr   string
	hotkeyErr string

	// Plugin calls of the plugin page run in the background: the dynamic
	// options of preferences, loaded when the page opens, and the buttons.
	// Their results are kept under asyncMu until a frame shows them.
	wg        sync.WaitGroup
	asyncMu   sync.Mutex
	options   map[string]optionsResult // by preference field key
	buttons   map[string]buttonResult
	loadedFor string // the plugin whose options were loaded
	// optionsStale asks for the options of loadedFor again (a choice they
	// may depend on changed, R109); the old ones show until the new arrive.
	optionsStale bool
	services     mui.Services // redraws the window when a call finishes

	agent    agentState    // the AI tools section of the AI plugin's page
	backends backendsState // its Backends section (guarded by asyncMu)
	actions  actionsState  // its AI Actions section

	appearance appearanceState // the Appearance page

	updates updatesState // the Updates section of the About page

	perms permissionsState // the Permissions page (macOS)

	ring ringSettingsState // the Action Ring page
}

// NewSettings returns the settings view of env.
func NewSettings(env *app.Env, host SettingsHost, version string) *Settings {
	return &Settings{
		env:     env,
		loc:     env.I18n.Localizer(i18n.CoreNamespace),
		host:    host,
		version: version,
		page:    PageGeneral,
		sidebar: 200,
		bufs:    map[string]*string{},
		errs:    map[string]string{},
		stored:  map[string]bool{},
		options: map[string]optionsResult{},
		buttons: map[string]buttonResult{},
	}
}

// Wait blocks until the plugin calls started by the settings have finished.
func (s *Settings) Wait() { s.wg.Wait() }

// async runs fn in the background and redraws the window when it is done.
func (s *Settings) async(fn func()) {
	s.wg.Go(func() {
		fn()
		s.asyncMu.Lock()
		svc := s.services
		s.asyncMu.Unlock()
		svc.Invalidate()
	})
}

// ShowPage switches to page (PageGeneral, …). UI thread only.
func (s *Settings) ShowPage(page string) {
	s.page, s.plugin = page, ""
	s.setNotice("", false)
	s.stopRecording()
	if s.appearance.dragging != "" {
		s.appearance.dragging = ""
		s.env.Themes.SetPreview(nil)
	}
	switch page {
	case PagePermissions:
		s.checkPermissions()
	case PageRing:
		s.openRingPage()
	}
}

// SetHotkeyError reports that the launcher hotkey could not be registered
// ("" when it was). UI thread only.
func (s *Settings) SetHotkeyError(hotkey string) {
	s.hotkeyErr = hotkey
}

// setNotice shows msg on the current page, as a failure when failed.
func (s *Settings) setNotice(msg string, failed bool) {
	s.notice, s.noticeErr = msg, failed
}

// noticeView shows the notice of the page, if any.
func (s *Settings) noticeView(c *mui.Context) {
	if s.notice == "" {
		return
	}
	t := c.Theme()
	color := t.TextMuted
	if s.noticeErr {
		color = t.Danger
	}
	mui.Text(c, s.notice).FontSize(12).TextColor(color)
}

// update changes the configuration and remembers a failure to save it.
func (s *Settings) update(fn func(*config.Config)) {
	s.saveErr = ""
	if err := s.env.Config.Update(fn); err != nil {
		s.saveErr = err.Error()
	}
}

// updatePlugin changes the configuration of plugin id.
func (s *Settings) updatePlugin(id string, fn func(*config.PluginConfig)) {
	s.update(func(c *config.Config) {
		if c.Plugins == nil {
			c.Plugins = map[string]config.PluginConfig{}
		}
		p := c.Plugins[id]
		fn(&p)
		c.Plugins[id] = p
	})
}

// updateCommand changes the configuration of a command of plugin id.
func (s *Settings) updateCommand(id, name string, fn func(*config.CommandConfig)) {
	s.updatePlugin(id, func(p *config.PluginConfig) {
		if p.Commands == nil {
			p.Commands = map[string]config.CommandConfig{}
		}
		cc := p.Commands[name]
		fn(&cc)
		p.Commands[name] = cc
	})
}

// buf returns the edit buffer of a text field, starting it at init.
func (s *Settings) buf(key, init string) *string {
	b, ok := s.bufs[key]
	if !ok {
		b = &init
		s.bufs[key] = b
	}
	return b
}

// View builds the settings window.
func (s *Settings) View(c *mui.Context) {
	cfg := s.env.Config.Get()
	s.asyncMu.Lock()
	s.services = c.Services()
	s.asyncMu.Unlock()
	// The settings keep MyGo's own look, light or dark as the launcher's
	// theme is.
	if dark := s.resolved(c, cfg).Dark; dark != c.Theme().Dark {
		if dark {
			c.SetTheme(mui.DarkTheme())
		} else {
			c.SetTheme(mui.LightTheme())
		}
	}
	t := c.Theme()
	c.Root().Background(t.Background)
	mui.Split(c, &s.sidebar, func() {
		page := s.page
		if mui.Sidebar(c, &page, func() {
			mui.SidebarItem(c, PageGeneral, builtinIcon("settings"), s.loc.T("settings.nav.general"))
			mui.SidebarItem(c, PageAppearance, builtinIcon("contrast"), s.loc.T("settings.nav.appearance"))
			mui.SidebarItem(c, PagePlugins, builtinIcon("app"), s.loc.T("settings.nav.plugins"))
			mui.SidebarItem(c, PageRing, builtinIcon("circle-dot"), s.loc.T("settings.nav.ring"))
			if _, ok := s.permissionsHost(); ok {
				mui.SidebarItem(c, PagePermissions, builtinIcon("shield"), s.loc.T("settings.nav.permissions"))
			}
			mui.SidebarItem(c, PageAdvanced, builtinIcon("wrench"), s.loc.T("settings.nav.advanced"))
			mui.SidebarItem(c, PageAbout, builtinIcon("info"), s.loc.T("settings.nav.about"))
		}).FillHeight().Padding(8).Changed() {
			s.ShowPage(page)
		}
	}, func() {
		mui.Scroll(c.Key("page:" + s.page + "/" + s.plugin)).Fill().Children(func() {
			mui.Column(c).Padding(24, 28).Gap(16).MaxWidth(760).Children(func() {
				if s.saveErr != "" {
					mui.Text(c, s.loc.T("settings.error", "err", s.saveErr)).TextColor(t.Danger)
				}
				switch s.page {
				case PagePlugins:
					if s.plugin != "" {
						s.pluginPage(c, cfg)
					} else {
						s.pluginsPage(c)
					}
				case PageAppearance:
					s.appearancePage(c, cfg)
				case PagePermissions:
					s.permissionsPage(c)
				case PageRing:
					s.ringPage(c, cfg)
				case PageAdvanced:
					s.advancedPage(c, cfg)
				case PageAbout:
					s.aboutPage(c, cfg)
				default:
					s.generalPage(c, cfg)
				}
			})
		})
	}).Fill()
}

func (s *Settings) heading(c *mui.Context, text string) {
	mui.Text(c, text).FontSize(20).Bold().Level(1)
}

func (s *Settings) subheading(c *mui.Context, text string) {
	mui.Text(c, text).FontSize(15).Bold().MarginY(8).Level(2)
}

// setting lays out a setting: its label and description on the left, its
// control on the right.
func (s *Settings) setting(c *mui.Context, label, desc string, control func()) mui.Element {
	t := c.Theme()
	return mui.Row(c).Gap(16).Padding(10, 0).AlignItems(mui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		mui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			mui.Text(c, label)
			if desc != "" {
				mui.Text(c, desc).FontSize(12).TextColor(t.TextMuted)
			}
		})
		control()
	})
}

// toggle is a setting with a switch; a click anywhere on it flips it.
func (s *Settings) toggle(c *mui.Context, label, desc string, on bool, set func(bool)) {
	v := on
	changed := false
	row := s.setting(c, label, desc, func() {
		changed = mui.Switch(c, &v).Label(label).Changed()
	})
	if row.Clicked() {
		v, changed = !on, true
	}
	if changed && v != on {
		set(v)
	}
}

// button is a plain button that reports a click.
func button(c *mui.Context, label string) bool {
	return mui.Button(c, label).Clicked()
}

// hotkeyRecorder shows a shortcut and, once clicked, records the next key
// combination pressed: Escape cancels, Backspace or Delete clears it.
func (s *Settings) hotkeyRecorder(c *mui.Context, id, value string, set func(string)) {
	t := c.Theme()
	recording := s.recording == id
	label := value
	switch {
	case recording:
		label = s.loc.T("settings.hotkey.recording")
	case value == "":
		label = s.loc.T("settings.hotkey.none")
	}
	b := mui.Row(c.Key("hotkey:"+id)).Focusable().MinWidth(120).Padding(5, 10).Radius(6).Gap(6).AlignItems(mui.Center).
		Border(1, t.Border).Background(t.Surface).Cursor(mui.CursorPointer).Label(s.loc.T("settings.hotkey.record"))
	if recording {
		b.Border(2, t.Accent).Focus()
	} else if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	if b.Clicked() {
		if recording {
			s.stopRecording()
		} else {
			s.stopRecording()
			s.recording = id
			s.host.SuspendHotkeys(true)
		}
	}
	b.HandleInput(func(ev mui.InputEvent) bool {
		if s.recording != id {
			return false
		}
		switch ev.Kind {
		case mui.InputText:
			return true
		case mui.InputKeyDown:
		default:
			return false
		}
		switch {
		case ev.Mods == 0 && ev.Key == mui.KeyEscape:
			s.stopRecording()
		case ev.Mods == 0 && (ev.Key == mui.KeyBackspace || ev.Key == mui.KeyDelete):
			s.stopRecording()
			set("")
		default:
			name := KeyString(ev.Mods, ev.Key)
			fkey := ev.Key >= mui.KeyF1 && ev.Key <= mui.KeyF12
			if name == "" || ev.Mods&^mui.Shift == 0 && !fkey {
				return true // a shortcut needs Ctrl, Alt or Win
			}
			s.stopRecording()
			set(name)
		}
		return true
	})
	b.Children(func() {
		if recording || value == "" {
			mui.Text(c, label).FontSize(12).TextColor(t.TextMuted).SingleLine()
		} else {
			keyCapsView(c, value, t.Text.Alpha(0.08))
		}
	})
}

func (s *Settings) stopRecording() {
	if s.recording != "" {
		s.recording = ""
		s.host.SuspendHotkeys(false)
	}
}
