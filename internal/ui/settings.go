package ui

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/pkg/plugin"
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
	// ChooseExportFile asks where to save exported settings, suggesting the
	// file name name. ChooseImportFile asks for a settings file to import.
	// Both show *.json files and return at once; done runs later on the UI
	// thread with the chosen path, or "" when the user canceled.
	ChooseExportFile(name string, done func(path string, err error))
	ChooseImportFile(done func(path string, err error))
}

// ExportFileName is the file name suggested for exported settings.
const ExportFileName = "golauncher-settings.json"

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
	loadedFor string       // the plugin whose options were loaded
	services  mui.Services // redraws the window when a call finishes
}

// optionsResult is the outcome of loading the dynamic options of a preference.
type optionsResult struct {
	loading bool
	options []plugin.Option
	err     string
}

// buttonResult is the outcome of the last click on a button preference.
type buttonResult struct {
	running bool
	toast   *plugin.Toast
	err     string
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

// loadOptions asks the plugin p for the dynamic options of its preferences.
func (s *Settings) loadOptions(p core.PluginInfo) {
	s.asyncMu.Lock()
	defer s.asyncMu.Unlock()
	s.loadedFor = p.ID
	clear(s.options)
	for _, pref := range p.Preferences {
		if !pref.DynamicOptions {
			continue
		}
		key, id, name := prefKey(p.ID, pref.Name), p.ID, pref.Name
		s.options[key] = optionsResult{loading: true}
		s.async(func() {
			opts, err := s.env.Engine.PreferenceOptions(context.Background(), id, name)
			r := optionsResult{options: opts}
			if err != nil {
				r.err = err.Error()
			}
			s.asyncMu.Lock()
			if s.loadedFor == id {
				s.options[key] = r
			}
			s.asyncMu.Unlock()
		})
	}
}

// runButton runs the action of a button preference.
func (s *Settings) runButton(id, name string) {
	key := prefKey(id, name)
	s.asyncMu.Lock()
	s.buttons[key] = buttonResult{running: true}
	s.asyncMu.Unlock()
	s.async(func() {
		out, err := s.env.Engine.RunPreferenceAction(context.Background(), id, name)
		r := buttonResult{toast: out.Toast}
		if err != nil {
			r.err = err.Error()
		}
		s.asyncMu.Lock()
		s.buttons[key] = r
		s.asyncMu.Unlock()
	})
}

func prefKey(id, name string) string { return "pref/" + id + "/" + name }

// ShowPage switches to page (PageGeneral, …). UI thread only.
func (s *Settings) ShowPage(page string) {
	s.page, s.plugin = page, ""
	s.setNotice("", false)
	s.stopRecording()
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
	s.toggle(c, s.loc.T("settings.general.contextAware"), s.loc.T("settings.general.contextAware.desc"), cfg.ContextAware, func(v bool) {
		s.update(func(c *config.Config) { c.ContextAware = v })
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
					if p.Origin == core.OriginExternal {
						mui.Badge(c, s.loc.T("settings.plugins.external"))
					}
				})
				if p.Description != "" {
					mui.Text(c, p.Description).FontSize(12).TextColor(t.TextMuted)
				}
				if p.Unavailable != "" {
					mui.Text(c, s.loc.T("settings.plugins.unavailable", "reason", p.Unavailable)).FontSize(12).TextColor(t.TextMuted)
				}
				s.processState(c, p)
			})
			if p.Unavailable != "" {
				off := false
				mui.Switch(c, &off).Label(s.loc.T("settings.plugins.enabled", "name", p.Name)).Disabled(true)
				return
			}
			if button(c, s.loc.T("settings.plugins.configure")) {
				s.plugin = p.ID
				s.asyncMu.Lock()
				s.loadedFor = ""
				s.asyncMu.Unlock()
				s.setNotice("", false)
				clear(s.bufs)
				clear(s.errs)
				clear(s.stored)
			}
			on := p.Enabled
			if mui.Switch(c, &on).Label(s.loc.T("settings.plugins.enabled", "name", p.Name)).Changed() {
				s.updatePlugin(p.ID, func(pc *config.PluginConfig) { pc.Disabled = !on })
			}
		})
	}
}

// processState shows an external plugin's crash state and, when the crash
// breaker turned it off, a button to turn it on again.
func (s *Settings) processState(c *mui.Context, p core.PluginInfo) {
	t := c.Theme()
	var msg string
	switch p.State {
	case "crashed":
		msg = s.loc.T("settings.plugins.stateCrashed", "reason", p.StateReason)
	case "disabled":
		msg = s.loc.T("settings.plugins.stateDisabled", "reason", p.StateReason)
	default:
		return
	}
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.Text(c, msg).FontSize(12).TextColor(t.Danger).Shrink(1)
		if p.State == "disabled" && button(c.Key("reset:"+p.ID), s.loc.T("settings.plugins.reset")) {
			if err := s.env.Engine.ResetPlugin(p.ID); err != nil {
				s.setNotice(err.Error(), true)
			}
		}
	})
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
		sw := mui.Switch(c, &on).Label(s.loc.T("settings.plugins.enabled", "name", p.Name)).Disabled(p.Unavailable != "")
		if p.Unavailable == "" && sw.Changed() {
			s.updatePlugin(p.ID, func(pc *config.PluginConfig) { pc.Disabled = !on })
		}
	})
	mui.Text(c, s.loc.T("settings.plugins.version", "version", p.Version)).FontSize(12).TextColor(t.TextMuted)
	if p.Origin == core.OriginExternal {
		mui.Text(c, s.loc.T("settings.plugins.externalNote")).FontSize(12).TextColor(t.TextMuted)
	}
	s.processState(c, p)
	if p.Description != "" {
		mui.Text(c, p.Description)
	}
	if p.Unavailable != "" {
		mui.Text(c, s.loc.T("settings.plugins.unavailable", "reason", p.Unavailable)).TextColor(t.TextMuted)
		return
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
		s.asyncMu.Lock()
		load := s.loadedFor != p.ID
		s.asyncMu.Unlock()
		if load {
			s.loadOptions(p)
		}
		s.subheading(c, s.loc.T("settings.plugins.preferences"))
		values := cfg.Plugin(p.ID).Preferences
		// Ungrouped preferences first, then each group under its heading, in
		// the order the groups first appear.
		var groups []string
		for _, pref := range p.Preferences {
			if !slices.Contains(groups, pref.Group) {
				groups = append(groups, pref.Group)
			}
		}
		slices.SortStableFunc(groups, func(a, b string) int {
			return cmp.Compare(min(len(a), 1), min(len(b), 1))
		})
		for _, g := range groups {
			if g != "" {
				mui.Text(c, g).FontSize(13).Bold().TextColor(t.TextMuted).MarginY(6).Level(3)
			}
			for _, pref := range p.Preferences {
				if pref.Group == g && core.PrefVisible(p.Preferences, values, pref) {
					s.preference(c, p.ID, pref, values)
				}
			}
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
	if pref.Type == plugin.PrefPassword {
		s.password(c, id, pref)
		return
	}
	if pref.Type == plugin.PrefButton {
		s.prefButton(c, id, pref)
		return
	}
	options, optionsErr := pref.Options, ""
	if pref.DynamicOptions {
		s.asyncMu.Lock()
		r := s.options[key]
		s.asyncMu.Unlock()
		options = r.options
		if r.err != "" {
			optionsErr = s.loc.T("settings.plugins.optionsFailed", "err", r.err)
		}
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
			titles := make([]string, len(options))
			chosen := ""
			for i, o := range options {
				titles[i] = cmp.Or(o.Title, o.Value)
				if o.Value == current {
					chosen = titles[i]
				}
			}
			if mui.Select(c, &chosen, titles).Changed() {
				if i := slices.Index(titles, chosen); i >= 0 {
					s.setPref(id, pref.Name, options[i].Value)
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
		default: // text, path
			b := s.buf(key, decodePref[string](raw))
			if mui.TextInput(c, b).Changed() {
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
	if msg := cmp.Or(s.errs[key], optionsErr); msg != "" {
		field.Error(msg)
	}
}

// prefButton builds a button preference: its description, the button and
// the toast of its last run.
func (s *Settings) prefButton(c *mui.Context, id string, pref plugin.Preference) {
	t := c.Theme()
	key := prefKey(id, pref.Name)
	s.asyncMu.Lock()
	r := s.buttons[key]
	s.asyncMu.Unlock()
	mui.Row(c.Key(key)).Gap(16).Padding(10, 0).AlignItems(mui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		mui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			if pref.Description != "" {
				mui.Text(c, pref.Description)
			}
			switch {
			case r.err != "":
				mui.Text(c, s.loc.T("settings.plugins.buttonFailed", "err", r.err)).FontSize(12).TextColor(t.Danger)
			case r.toast != nil:
				color := t.TextMuted
				switch r.toast.Style {
				case plugin.ToastSuccess:
					color = t.Success
				case plugin.ToastFailure:
					color = t.Danger
				}
				mui.Text(c, r.toast.Title).FontSize(12).TextColor(color)
				if r.toast.Message != "" {
					mui.Text(c, r.toast.Message).FontSize(12).TextColor(t.TextMuted)
				}
			}
		})
		if r.running {
			mui.Spinner(c).Size(16, 16).Label(s.loc.T("ui.loading"))
		}
		if mui.Button(c, pref.Title).Disabled(r.running).Clicked() {
			s.runButton(id, pref.Name)
		}
	})
}

// password builds the editor of a password preference. Its value lives in
// the Windows Credential Manager, never in the configuration: the field
// starts empty, Save (or Enter) stores what was typed and Clear removes it.
func (s *Settings) password(c *mui.Context, id string, pref plugin.Preference) {
	t := c.Theme()
	key := "pref/" + id + "/" + pref.Name
	stored, ok := s.stored[key]
	if !ok {
		_, has, err := s.env.Services.Secrets.Get(id, pref.Name)
		if err != nil {
			s.errs[key] = err.Error()
		}
		stored, s.stored[key] = has, has
	}
	field := mui.Field(c.Key(key), pref.Title, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			b := s.buf(key, "")
			placeholder := ""
			if stored {
				placeholder = s.loc.T("settings.plugins.secretReplace")
			}
			in := mui.TextInput(c, b).Password().Placeholder(placeholder).Grow(1).Shrink(1)
			if (button(c, s.loc.T("settings.plugins.secretSave")) || in.Submitted()) && *b != "" {
				s.setSecret(key, id, pref.Name, *b)
				*b = ""
			}
			if stored && button(c, s.loc.T("settings.plugins.secretClear")) {
				s.setSecret(key, id, pref.Name, "")
				*b = ""
			}
		})
		status := s.loc.T("settings.plugins.secretNotSet")
		if stored {
			status = s.loc.T("settings.plugins.secretStored")
		}
		mui.Text(c, status).FontSize(12).TextColor(t.TextMuted)
	})
	field.Description(strings.TrimSpace(pref.Description + " " + s.loc.T("settings.plugins.secretHint")))
	if msg := s.errs[key]; msg != "" {
		field.Error(msg)
	}
}

// setSecret stores the secret of a password preference, or removes it when
// secret is "", and tells the plugins.
func (s *Settings) setSecret(key, id, name, secret string) {
	sec := s.env.Services.Secrets
	var err error
	if secret == "" {
		err = sec.Delete(id, name)
	} else {
		err = sec.Set(id, name, secret)
	}
	delete(s.stored, key)
	if err != nil {
		s.errs[key] = s.loc.T("settings.plugins.secretFailed", "err", err.Error())
		return
	}
	delete(s.errs, key)
	s.env.Services.Bus.Publish(plugin.EventConfigChanged)
}

// exportSettings asks for a file and writes the configuration to it.
func (s *Settings) exportSettings() {
	s.setNotice("", false)
	s.host.ChooseExportFile(ExportFileName, func(path string, err error) {
		if err == nil && path == "" {
			return // canceled
		}
		if err == nil {
			var data []byte
			if data, err = config.Export(s.env.Config.Get()); err == nil {
				err = os.WriteFile(path, data, 0o644)
			}
		}
		if err != nil {
			s.setNotice(s.loc.T("settings.advanced.exportFailed", "err", err.Error()), true)
			return
		}
		s.setNotice(s.loc.T("settings.advanced.exported", "path", path), false)
	})
}

// importSettings asks for a file exported before and replaces the whole
// configuration with it.
func (s *Settings) importSettings() {
	s.setNotice("", false)
	s.host.ChooseImportFile(func(path string, err error) {
		if err == nil && path == "" {
			return // canceled
		}
		var cfg config.Config
		if err == nil {
			var data []byte
			if data, err = os.ReadFile(path); err == nil {
				cfg, err = config.Import(data)
			}
		}
		if err == nil {
			// A file from an older version may hold passwords.
			err = s.env.MoveSecrets(&cfg)
		}
		if err == nil {
			err = s.env.Config.Update(func(c *config.Config) { *c = cfg })
		}
		if err != nil {
			s.setNotice(s.loc.T("settings.advanced.importFailed", "err", err.Error()), true)
			return
		}
		s.saveErr = ""
		clear(s.bufs)
		clear(s.errs)
		clear(s.stored)
		s.setNotice(s.loc.T("settings.advanced.imported", "path", path), false)
	})
}

func (s *Settings) advancedPage(c *mui.Context, cfg config.Config) {
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
				s.setNotice(err.Error(), true)
			} else {
				s.setNotice(s.loc.T("settings.advanced.cleared"), false)
			}
		}
	})
	s.setting(c, s.loc.T("settings.advanced.export"), s.loc.T("settings.advanced.export.desc"), func() {
		if button(c, s.loc.T("settings.advanced.exportButton")) {
			s.exportSettings()
		}
	})
	s.setting(c, s.loc.T("settings.advanced.import"), s.loc.T("settings.advanced.import.desc"), func() {
		if button(c, s.loc.T("settings.advanced.importButton")) {
			s.importSettings()
		}
	})
	s.noticeView(c)
}

func (s *Settings) open(path string) {
	if err := s.host.OpenPath(path); err != nil {
		s.setNotice(err.Error(), true)
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
