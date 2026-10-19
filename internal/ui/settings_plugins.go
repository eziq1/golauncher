package ui

// The Plugins page and a plugin's page: process state and commands.

import (
	"cmp"
	"slices"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
)

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
		load := s.loadedFor != p.ID || s.optionsStale
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
	if p.ID == aiPluginID {
		s.backendsSection(c)
		s.actionsSection(c, cfg)
		s.agentSection(c, cfg)
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
