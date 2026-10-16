package ui

import (
	"errors"
	"os"
	"strings"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/agent"
	"github.com/eziq1/golauncher/internal/agent/desktop"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
)

// aiPluginID is the AI chat plugin, whose settings page also holds the AI
// tools' settings (docs/ai.md §4).
const aiPluginID = "builtin.ai"

// CommandOpener is implemented by a SettingsHost that can open a launcher
// command (the "AI Activity" view).
type CommandOpener interface {
	OpenCommand(pluginID, name string)
}

// agentState is what the AI tools section shows besides the configuration.
type agentState struct {
	desktop    string // the result of the last "Connect to Claude Desktop"
	desktopErr bool
	codeCopied bool
}

// Where Claude Desktop's configuration is and which program it should run
// (tests replace them).
var (
	desktopConfigPath = desktop.ConfigPath
	executable        = os.Executable
)

// agentSection is the "AI Tools" part of the AI plugin's page: external
// access, the policy for "open" actions, the plugins AI may use, connecting
// Claude Desktop and Claude Code, and the activity.
func (s *Settings) agentSection(c *mui.Context, cfg config.Config) {
	t := c.Theme()
	st := &s.agent
	s.subheading(c, s.loc.T("settings.agent.heading"))
	mui.Text(c, s.loc.T("settings.agent.intro")).FontSize(12).TextColor(t.TextMuted)
	s.toggle(c, s.loc.T("settings.agent.external"), s.loc.T("settings.agent.external.desc"), cfg.Agent.External, func(v bool) {
		s.update(func(c *config.Config) { c.Agent.External = v })
	})
	s.setting(c, s.loc.T("settings.agent.open"), s.loc.T("settings.agent.open.desc"), func() {
		i := 0
		if cfg.Agent.AskOpen {
			i = 1
		}
		if mui.Segmented(c, &i, s.loc.T("settings.agent.open.run"), s.loc.T("settings.agent.open.ask")).
			Label(s.loc.T("settings.agent.open")).Changed() {
			s.update(func(c *config.Config) { c.Agent.AskOpen = i == 1 })
		}
	})

	mui.Text(c, s.loc.T("settings.agent.plugins")).FontSize(13).Bold().TextColor(t.TextMuted).MarginY(6).Level(3)
	mui.Text(c, s.loc.T("settings.agent.plugins.desc")).FontSize(12).TextColor(t.TextMuted)
	for _, p := range s.env.Engine.Plugins() {
		if agent.Hidden(p.ID) || p.Unavailable != "" {
			continue
		}
		on := agent.Exposed(core.PluginInfo{ID: p.ID, Origin: p.Origin, Enabled: true}, cfg.Agent)
		desc := ""
		if !p.Enabled {
			desc = s.loc.T("settings.agent.plugins.off")
		}
		mui.Box(c.Key("agent-plugin:" + p.ID)).Children(func() {
			s.toggle(c, p.Name, desc, on, func(v bool) {
				s.update(func(c *config.Config) {
					if c.Agent.Plugins == nil {
						c.Agent.Plugins = map[string]bool{}
					}
					if v == agent.DefaultExposed(p) {
						delete(c.Agent.Plugins, p.ID)
					} else {
						c.Agent.Plugins[p.ID] = v
					}
				})
			})
		})
	}

	exe, _ := executable()
	s.desktopSetting(c, cfg, exe)
	s.setting(c, s.loc.T("settings.agent.code"), s.loc.T("settings.agent.code.desc"), func() {
		label := s.loc.T("settings.agent.code.copy")
		if st.codeCopied {
			label = s.loc.T("settings.agent.code.copied")
		}
		if button(c, label) {
			st.codeCopied = s.env.Services.Clipboard.WriteText(ClaudeCodeCommand(exe)) == nil
		}
	})
	mui.Text(c, ClaudeCodeCommand(exe)).FontSize(12).Selectable().Padding(6, 8).Radius(6).Background(t.Text.Alpha(0.06))

	n := 0
	if log := s.env.Agent.Activity(); log != nil {
		n = len(log.Entries())
	}
	s.setting(c, s.loc.T("settings.agent.activity"), s.loc.N("settings.agent.activity.desc", n), func() {
		if oc, ok := s.host.(CommandOpener); ok && button(c, s.loc.T("settings.agent.activity.show")) {
			oc.OpenCommand(agent.PluginID, agent.ViewActivity)
		}
		if n > 0 && button(c, s.loc.T("settings.agent.activity.clear")) {
			if _, err := s.env.Agent.Activity().Clear(); err != nil {
				s.setNotice(err.Error(), true)
			}
		}
	})
}

// desktopSetting shows whether Claude Desktop is connected, the button that
// connects it, and what the last connection changed.
func (s *Settings) desktopSetting(c *mui.Context, cfg config.Config, exe string) {
	t := c.Theme()
	st := &s.agent
	path, pathErr := desktopConfigPath()
	status := s.loc.T("settings.agent.desktop.notConnected")
	if pathErr == nil {
		if connected, same, err := desktop.Status(path, exe); err == nil && connected {
			status = s.loc.T("settings.agent.desktop.other")
			if same {
				status = s.loc.T("settings.agent.desktop.connected")
			}
		}
	}
	s.setting(c, s.loc.T("settings.agent.desktop"), s.loc.T("settings.agent.desktop.desc")+" "+status, func() {
		if button(c, s.loc.T("settings.agent.desktop.connect")) {
			st.desktop, st.desktopErr = s.connectDesktop(path, pathErr, exe, cfg)
		}
	})
	if st.desktop != "" {
		color := t.TextMuted
		if st.desktopErr {
			color = t.Danger
		}
		mui.Text(c, st.desktop).FontSize(12).TextColor(color).Selectable()
	}
}

// connectDesktop runs desktop.Connect and describes what it changed.
func (s *Settings) connectDesktop(path string, pathErr error, exe string, cfg config.Config) (string, bool) {
	if pathErr != nil {
		return s.loc.T("settings.agent.desktop.failed", "err", pathErr.Error()), true
	}
	ch, err := desktop.Connect(path, exe, time.Now())
	switch {
	case errors.Is(err, desktop.ErrNotInstalled):
		return s.loc.T("settings.agent.desktop.notInstalled"), true
	case err != nil:
		return s.loc.T("settings.agent.desktop.failed", "err", err.Error()), true
	}
	var lines []string
	if ch.Unchanged {
		lines = append(lines, s.loc.T("settings.agent.desktop.unchanged"))
	} else {
		lines = append(lines, s.loc.T("settings.agent.desktop.done", "path", ch.Path))
		if ch.Backup != "" {
			lines = append(lines, s.loc.T("settings.agent.desktop.backup", "path", ch.Backup))
		}
		if ch.Previous != "" {
			lines = append(lines, s.loc.T("settings.agent.desktop.before"), ch.Previous)
		}
		lines = append(lines, s.loc.T("settings.agent.desktop.after"), ch.New)
	}
	if !cfg.Agent.External {
		lines = append(lines, s.loc.T("settings.agent.desktop.offHint"))
	}
	return strings.Join(lines, "\n"), false
}

// ClaudeCodeCommand is the command that adds golauncher to Claude Code
// (shown for copying, never run).
func ClaudeCodeCommand(exe string) string {
	return `claude mcp add ` + agent.ServerName + ` -- "` + exe + `" mcp`
}
