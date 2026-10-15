package core

import (
	"slices"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// PluginInfo describes a plugin with its strings localized, for the
// settings window and glctl.
type PluginInfo struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Version     string              `json:"version"`
	Icon        plugin.Icon         `json:"icon,omitzero"`
	Enabled     bool                `json:"enabled"`
	Permissions []plugin.Permission `json:"permissions,omitempty"`
	Commands    []CommandInfo       `json:"commands,omitempty"`
	Preferences []plugin.Preference `json:"preferences,omitempty"`
}

// CommandInfo describes a command and its effective settings.
type CommandInfo struct {
	Name          string             `json:"name"`
	Title         string             `json:"title"`
	Mode          plugin.CommandMode `json:"mode"`
	Alias         string             `json:"alias,omitempty"`
	DefaultHotkey string             `json:"defaultHotkey,omitempty"`
	// Hotkey is the effective hotkey ("" when none).
	Hotkey       string `json:"hotkey,omitempty"`
	HideFromRoot bool   `json:"hideFromRoot,omitzero"`
}

// Plugins describes all loaded plugins.
func (e *Engine) Plugins() []PluginInfo {
	cfg := e.cfg.Get()
	out := make([]PluginInfo, 0, len(e.plugins))
	for _, l := range e.plugins {
		pc := cfg.Plugin(l.m.ID)
		info := PluginInfo{
			ID:          l.m.ID,
			Name:        l.loc.Resolve(l.m.Name),
			Description: l.loc.Resolve(l.m.Description),
			Version:     l.m.Version,
			Icon:        l.m.Icon,
			Enabled:     !pc.Disabled,
			Permissions: l.m.Permissions,
		}
		for _, c := range l.m.Commands {
			cc := pc.Commands[c.Name]
			info.Commands = append(info.Commands, CommandInfo{
				Name:          c.Name,
				Title:         l.loc.Resolve(c.Title),
				Mode:          c.Mode,
				Alias:         cc.Alias,
				DefaultHotkey: c.DefaultHotkey,
				Hotkey:        effectiveHotkey(c.DefaultHotkey, cc.Hotkey),
				HideFromRoot:  cc.HideFromRoot,
			})
		}
		for _, p := range l.m.Preferences {
			p.Title = l.loc.Resolve(p.Title)
			p.Description = l.loc.Resolve(p.Description)
			p.Options = slices.Clone(p.Options)
			for i := range p.Options {
				p.Options[i].Title = l.loc.Resolve(p.Options[i].Title)
			}
			info.Preferences = append(info.Preferences, p)
		}
		out = append(out, info)
	}
	return out
}

func effectiveHotkey(def, override string) string {
	switch override {
	case "":
		return def
	case config.HotkeyNone:
		return ""
	default:
		return override
	}
}

// CommandHotkey is a global hotkey bound to a command.
type CommandHotkey struct {
	Plugin  string `json:"plugin"`
	Command string `json:"command"`
	Hotkey  string `json:"hotkey"`
}

// CommandHotkeys lists the effective hotkeys of all enabled plugins' commands.
func (e *Engine) CommandHotkeys() []CommandHotkey {
	var out []CommandHotkey
	for _, info := range e.Plugins() {
		if !info.Enabled {
			continue
		}
		for _, c := range info.Commands {
			if c.Hotkey != "" {
				out = append(out, CommandHotkey{Plugin: info.ID, Command: c.Name, Hotkey: c.Hotkey})
			}
		}
	}
	return out
}
