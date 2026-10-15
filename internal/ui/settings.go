package ui

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"golauncher/internal/app"
	"golauncher/internal/config"
	"golauncher/internal/core"
	"golauncher/internal/i18n"
	"golauncher/pkg/plugin"
)

// Settings window size, in DIPs.
const (
	SettingsWidth  = 900
	SettingsHeight = 620
)

// Settings pages.
const (
	PageGeneral  = "general"
	PagePlugins  = "plugins"
	PageAdvanced = "advanced"
	PageAbout    = "about"
)

// SettingsHost is what the settings view needs from the system. Its methods
// are called on the UI thread.
type SettingsHost interface {
	// OpenPath opens a file or folder with its default app.
	OpenPath(path string) error
	// SuspendHotkeys turns the global hotkeys off while a hotkey is being
	// recorded, so that the keys reach the window, and on again.
	SuspendHotkeys(suspend bool)
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
	notice    string             // a message on the current page
	saveErr   string
	hotkeyErr string
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
	}
}

// ShowPage switches to page (PageGeneral, …). UI thread only.
func (s *Settings) ShowPage(page string) {
	s.page, s.plugin, s.notice = page, "", ""
	s.stopRecording()
}

// SetHotkeyError reports that the launcher hotkey could not be registered
// ("" when it was). UI thread only.
func (s *Settings) SetHotkeyError(hotkey string) {
	s.hotkeyErr = hotkey
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
	applyTheme(c, cfg.Theme)
	t := c.Theme()
	c.Root().Background(t.Background)
	mui.Split(c, &s.sidebar, func() {
		page := s.page
		if mui.Sidebar(c, &page, func() {
			mui.SidebarItem(c, PageGeneral, builtinIcon("settings"), s.loc.T("settings.nav.general"))
			mui.SidebarItem(c, PagePlugins, builtinIcon("app"), s.loc.T("settings.nav.plugins"))
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
				case PageAdvanced:
					s.advancedPage(c, cfg)
				case PageAbout:
					s.aboutPage(c)
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

func (s *Settings) generalPage(c *mui.Context, cfg config.Config) {
	t := c.Theme()
	s.heading(c, s.loc.T("settings.nav.general"))
	s.setting(c, s.loc.T("settings.general.hotkey"), s.loc.T("settings.general.hotkey.desc"), func() {
		s.hotkeyRecorder(c, "launcher", cfg.Hotkey, func(v string) {
			if v != "" {
				s.hotkeyErr = ""
				s.update(func(c *config.Config) { c.Hotkey = v })
			}
		})
	})
	if s.hotkeyErr != "" {
		mui.Text(c, s.loc.T("settings.general.hotkeyFailed", "hotkey", s.hotkeyErr)).FontSize(12).TextColor(t.Danger)
	}
	s.toggle(c, s.loc.T("settings.general.launchAtLogin"), "", cfg.LaunchAtLogin, func(v bool) {
		s.update(func(c *config.Config) { c.LaunchAtLogin = v })
	})
	themes := []string{"system", "light", "dark"}
	s.setting(c, s.loc.T("settings.general.theme"), "", func() {
		i := max(0, slices.Index(themes, cfg.Theme))
		if mui.Segmented(c, &i, s.loc.T("settings.theme.system"), s.loc.T("settings.theme.light"), s.loc.T("settings.theme.dark")).
			Label(s.loc.T("settings.general.theme")).Changed() {
			s.update(func(c *config.Config) { c.Theme = themes[i] })
		}
	})
	s.toggle(c, s.loc.T("settings.general.hideOnBlur"), "", cfg.HideOnBlur, func(v bool) {
		s.update(func(c *config.Config) { c.HideOnBlur = v })
	})
	if locales := s.env.I18n.Available(); len(locales) > 1 {
		s.setting(c, s.loc.T("settings.general.language"), "", func() {
			options := append([]string{s.loc.T("settings.language.system")}, locales...)
			chosen := options[0]
			if i := slices.Index(locales, cfg.Language); i >= 0 {
				chosen = locales[i]
			}
			if mui.Select(c, &chosen, options).Label(s.loc.T("settings.general.language")).Changed() {
				lang := chosen
				if chosen == options[0] {
					lang = "system"
				}
				s.update(func(c *config.Config) { c.Language = lang })
			}
		})
	}
}

func (s *Settings) pluginsPage(c *mui.Context) {
	t := c.Theme()
	s.heading(c, s.loc.T("settings.nav.plugins"))
	for _, p := range s.env.Engine.Plugins() {
		mui.Row(c.Key("plugin:"+p.ID)).Gap(12).Padding(10, 0).AlignItems(mui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			mui.Box(c).Size(32, 32).Center().Radius(8).Background(t.Text.Alpha(0.08)).Children(func() {
				mui.Icon(c, builtinIcon(cmp.Or(p.Icon.Name, "app"))).FontSize(18)
			})
			mui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
				mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
					mui.Text(c, p.Name).Bold()
					mui.Text(c, p.Version).FontSize(12).TextColor(t.TextMuted)
				})
				if p.Description != "" {
					mui.Text(c, p.Description).FontSize(12).TextColor(t.TextMuted)
				}
			})
			if button(c, s.loc.T("settings.plugins.configure")) {
				s.plugin, s.notice = p.ID, ""
				clear(s.bufs)
				clear(s.errs)
			}
			on := p.Enabled
			if mui.Switch(c, &on).Label(s.loc.T("settings.plugins.enabled", "name", p.Name)).Changed() {
				s.updatePlugin(p.ID, func(pc *config.PluginConfig) { pc.Disabled = !on })
			}
		})
	}
}

func (s *Settings) pluginPage(c *mui.Context, cfg config.Config) {
	t := c.Theme()
	plugins := s.env.Engine.Plugins()
	i := slices.IndexFunc(plugins, func(p core.PluginInfo) bool { return p.ID == s.plugin })
	if i < 0 {
		s.plugin = ""
		return
	}
	p := plugins[i]
	if mui.Button(c, "‹ "+s.loc.T("settings.plugins.back")).AlignSelf(mui.Start).Clicked() {
		s.plugin = ""
		s.stopRecording()
		return
	}
	mui.Row(c).Gap(12).AlignItems(mui.Center).Children(func() {
		mui.Text(c, p.Name).FontSize(20).Bold().Level(1).Grow(1)
		on := p.Enabled
		if mui.Switch(c, &on).Label(s.loc.T("settings.plugins.enabled", "name", p.Name)).Changed() {
			s.updatePlugin(p.ID, func(pc *config.PluginConfig) { pc.Disabled = !on })
		}
	})
	mui.Text(c, s.loc.T("settings.plugins.version", "version", p.Version)).FontSize(12).TextColor(t.TextMuted)
	if p.Description != "" {
		mui.Text(c, p.Description)
	}

	s.subheading(c, s.loc.T("settings.plugins.permissions"))
	mui.Row(c).Gap(6).Wrap().Children(func() {
		if len(p.Permissions) == 0 {
			mui.Text(c, s.loc.T("settings.plugins.noPermissions")).TextColor(t.TextMuted)
		}
		for _, perm := range p.Permissions {
			mui.Badge(c, string(perm))
		}
	})
	mui.Text(c, s.loc.T("settings.plugins.permissionsNote")).FontSize(12).TextColor(t.TextMuted)

	if len(p.Commands) > 0 {
		s.subheading(c, s.loc.T("settings.plugins.commands"))
		mui.Row(c).Gap(12).Children(func() {
			mui.Text(c, s.loc.T("settings.plugins.command")).FontSize(12).TextColor(t.TextMuted).Grow(1).Basis(0)
			mui.Text(c, s.loc.T("settings.plugins.alias")).FontSize(12).TextColor(t.TextMuted).Width(120)
			mui.Text(c, s.loc.T("settings.plugins.hotkey")).FontSize(12).TextColor(t.TextMuted).Width(200)
			mui.Text(c, s.loc.T("settings.plugins.showInRoot")).FontSize(12).TextColor(t.TextMuted).Width(110)
		})
		for _, cmd := range p.Commands {
			s.commandRow(c, p, cmd, cfg.Plugin(p.ID).Commands[cmd.Name])
		}
	}

	if len(p.Preferences) > 0 {
		s.subheading(c, s.loc.T("settings.plugins.preferences"))
		values := cfg.Plugin(p.ID).Preferences
		for _, pref := range p.Preferences {
			s.preference(c, p.ID, pref, values)
		}
	}
}

func (s *Settings) commandRow(c *mui.Context, p core.PluginInfo, cmd core.CommandInfo, cc config.CommandConfig) {
	t := c.Theme()
	mui.Row(c.Key("cmd:"+cmd.Name)).Gap(12).Padding(6, 0).AlignItems(mui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		mui.Text(c, cmd.Title).Grow(1).Basis(0).Shrink(1)
		alias := s.buf("alias/"+p.ID+"/"+cmd.Name, cmd.Alias)
		if mui.TextInput(c, alias).Width(120).Label(s.loc.T("settings.plugins.aliasOf", "command", cmd.Title)).Changed() {
			v := strings.TrimSpace(*alias)
			s.updateCommand(p.ID, cmd.Name, func(cc *config.CommandConfig) { cc.Alias = v })
		}
		mui.Row(c).Width(200).Gap(6).AlignItems(mui.Center).Children(func() {
			s.hotkeyRecorder(c, "cmd/"+p.ID+"/"+cmd.Name, cmd.Hotkey, func(v string) {
				if v == "" {
					v = config.HotkeyNone
				}
				if v == cmd.DefaultHotkey {
					v = ""
				}
				s.updateCommand(p.ID, cmd.Name, func(cc *config.CommandConfig) { cc.Hotkey = v })
			})
			if cc.Hotkey != "" && button(c, s.loc.T("settings.hotkey.reset")) {
				s.updateCommand(p.ID, cmd.Name, func(cc *config.CommandConfig) { cc.Hotkey = "" })
			}
		})
		mui.Box(c).Width(110).Children(func() {
			show := !cmd.HideFromRoot
			if mui.Switch(c, &show).Label(s.loc.T("settings.plugins.showInRootOf", "command", cmd.Title)).Changed() {
				s.updateCommand(p.ID, cmd.Name, func(cc *config.CommandConfig) { cc.HideFromRoot = !show })
			}
		})
	})
}

// prefValue returns the value of a preference: the configured one, or its default.
func prefValue(pref plugin.Preference, values map[string]jsontext.Value) jsontext.Value {
	if v, ok := values[pref.Name]; ok {
		return v
	}
	return pref.Default
}

// decodePref decodes a preference value into a T, or returns the zero T.
func decodePref[T any](v jsontext.Value) T {
	var out T
	if len(v) > 0 {
		_ = json.Unmarshal(v, &out)
	}
	return out
}

func (s *Settings) setPref(id, name string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		s.saveErr = err.Error()
		return
	}
	s.updatePlugin(id, func(p *config.PluginConfig) {
		if p.Preferences == nil {
			p.Preferences = map[string]jsontext.Value{}
		}
		p.Preferences[name] = b
	})
}

// preference builds the editor of a plugin preference, by its type.
func (s *Settings) preference(c *mui.Context, id string, pref plugin.Preference, values map[string]jsontext.Value) {
	raw := prefValue(pref, values)
	key := "pref/" + id + "/" + pref.Name
	if pref.Type == plugin.PrefBool {
		s.toggle(c, pref.Title, pref.Description, decodePref[bool](raw), func(v bool) { s.setPref(id, pref.Name, v) })
		return
	}
	if pref.Type == plugin.PrefHotkey {
		s.setting(c, pref.Title, pref.Description, func() {
			s.hotkeyRecorder(c, key, decodePref[string](raw), func(v string) { s.setPref(id, pref.Name, v) })
		})
		return
	}
	field := mui.Field(c.Key(key), pref.Title, func() {
		switch pref.Type {
		case plugin.PrefNumber:
			var n float64
			if len(raw) > 0 {
				n = decodePref[float64](raw)
			}
			b := s.buf(key, strconv.FormatFloat(n, 'f', -1, 64))
			if mui.TextInput(c, b).Width(160).Changed() {
				v, err := strconv.ParseFloat(strings.TrimSpace(*b), 64)
				if err != nil {
					s.errs[key] = s.loc.T("settings.plugins.invalidNumber")
					return
				}
				delete(s.errs, key)
				s.setPref(id, pref.Name, v)
			}
		case plugin.PrefSelect:
			current := decodePref[string](raw)
			titles := make([]string, len(pref.Options))
			chosen := ""
			for i, o := range pref.Options {
				titles[i] = cmp.Or(o.Title, o.Value)
				if o.Value == current {
					chosen = titles[i]
				}
			}
			if mui.Select(c, &chosen, titles).Changed() {
				if i := slices.Index(titles, chosen); i >= 0 {
					s.setPref(id, pref.Name, pref.Options[i].Value)
				}
			}
		case plugin.PrefStringList:
			b := s.buf(key, strings.Join(decodePref[[]string](raw), "\n"))
			if mui.TextArea(c, b).Lines(3, 8).Changed() {
				var list []string
				for line := range strings.Lines(*b) {
					if line = strings.TrimSpace(line); line != "" {
						list = append(list, line)
					}
				}
				s.setPref(id, pref.Name, list)
			}
		default: // text, password, path
			b := s.buf(key, decodePref[string](raw))
			in := mui.TextInput(c, b)
			if pref.Type == plugin.PrefPassword {
				in.Password()
			}
			if in.Changed() {
				s.setPref(id, pref.Name, *b)
			}
		}
	})
	desc := pref.Description
	if pref.Type == plugin.PrefStringList {
		desc = strings.TrimSpace(desc + " " + s.loc.T("settings.plugins.listHint"))
	}
	if desc != "" {
		field.Description(desc)
	}
	if msg := s.errs[key]; msg != "" {
		field.Error(msg)
	}
}

func (s *Settings) advancedPage(c *mui.Context, cfg config.Config) {
	t := c.Theme()
	s.heading(c, s.loc.T("settings.nav.advanced"))
	dataDir := filepath.Dir(s.env.Services.DataRoot)
	s.setting(c, s.loc.T("settings.advanced.dataDir"), dataDir, func() {
		if button(c, s.loc.T("settings.advanced.open")) {
			s.open(dataDir)
		}
	})
	if path := s.env.Config.Path(); path != "" {
		s.setting(c, s.loc.T("settings.advanced.configFile"), path, func() {
			if button(c, s.loc.T("settings.advanced.openConfig")) {
				s.open(path)
			}
		})
	}
	levels := []string{"debug", "info", "warn", "error"}
	s.setting(c, s.loc.T("settings.advanced.logLevel"), "", func() {
		i := slices.Index(levels, cmp.Or(cfg.LogLevel, "info"))
		if mui.Segmented(c, &i, s.loc.T("settings.log.debug"), s.loc.T("settings.log.info"), s.loc.T("settings.log.warn"), s.loc.T("settings.log.error")).
			Label(s.loc.T("settings.advanced.logLevel")).Changed() {
			s.update(func(c *config.Config) { c.LogLevel = levels[i] })
		}
	})
	s.setting(c, s.loc.T("settings.advanced.clearRanking"), s.loc.T("settings.advanced.clearRanking.desc"), func() {
		if button(c, s.loc.T("settings.advanced.clear")) {
			if err := s.env.Services.DB.ClearUsage(); err != nil {
				s.notice = err.Error()
			} else {
				s.notice = s.loc.T("settings.advanced.cleared")
			}
		}
	})
	if s.notice != "" {
		mui.Text(c, s.notice).FontSize(12).TextColor(t.TextMuted)
	}
}

func (s *Settings) open(path string) {
	if err := s.host.OpenPath(path); err != nil {
		s.notice = err.Error()
	}
}

func (s *Settings) aboutPage(c *mui.Context) {
	t := c.Theme()
	s.heading(c, s.loc.T("settings.nav.about"))
	mui.Row(c).Gap(14).AlignItems(mui.Center).Children(func() {
		mui.Box(c).Size(56, 56).Center().Radius(14).Background(t.Accent).Children(func() {
			mui.Icon(c, builtinIcon("command")).FontSize(30).TextColor(t.AccentText)
		})
		mui.Column(c).Gap(4).Children(func() {
			mui.Text(c, s.loc.T("app.name")).FontSize(22).Bold()
			mui.Text(c, s.loc.T("settings.about.version", "version", s.version)).TextColor(t.TextMuted)
		})
	})
	mui.Text(c, s.loc.T("settings.about.description"))
	mui.Link(c, s.loc.T("settings.about.builtWith"), "https://github.com/egoist/mygo")
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
			keyCapsView(c, value)
		}
	})
}

func (s *Settings) stopRecording() {
	if s.recording != "" {
		s.recording = ""
		s.host.SuspendHotkeys(false)
	}
}
