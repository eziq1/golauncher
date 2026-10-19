package core

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"reflect"
	"slices"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// PluginInfo describes a plugin with its strings localized, for the
// settings window and glctl.
type PluginInfo struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Version     string      `json:"version"`
	Icon        plugin.Icon `json:"icon,omitzero"`
	Enabled     bool        `json:"enabled"`
	// Unavailable is set when the plugin could not start on this system;
	// it then has no commands and cannot be enabled.
	Unavailable string              `json:"unavailable,omitempty"`
	Permissions []plugin.Permission `json:"permissions,omitempty"`
	Commands    []CommandInfo       `json:"commands,omitempty"`
	Preferences []plugin.Preference `json:"preferences,omitempty"`
	// Origin is OriginBuiltin or OriginExternal (a plugin in its own process).
	Origin string `json:"origin"`
	// State is an external plugin's process state: running, stopped,
	// crashed, disabled (by the crash breaker) or unavailable; StateReason
	// explains the last three. Empty for built-in plugins.
	State       string `json:"state,omitempty"`
	StateReason string `json:"stateReason,omitempty"`
}

// Plugin origins.
const (
	OriginBuiltin  = "builtin"
	OriginExternal = "external"
)

// ProcessPlugin is implemented by plugins that run in their own process
// (internal/extproc.Proxy).
type ProcessPlugin interface {
	// ProcessState returns the state and a localized reason.
	ProcessState() (state, reason string)
	// Reset turns a plugin disabled by the crash breaker on again.
	Reset()
}

// origin fills in the origin and process state of info.
func origin(info *PluginInfo, p plugin.Plugin) {
	info.Origin = OriginBuiltin
	if pp, ok := p.(ProcessPlugin); ok {
		info.Origin = OriginExternal
		info.State, info.StateReason = pp.ProcessState()
	}
}

// ResetPlugin turns an external plugin that crashed too often on again
// (the settings page's "Turn on again").
func (e *Engine) ResetPlugin(id string) error {
	l, ok := e.byID[id]
	if !ok {
		return fmt.Errorf("core: unknown plugin %q", id)
	}
	pp, ok := l.p.(ProcessPlugin)
	if !ok {
		return fmt.Errorf("core: plugin %q does not run in its own process", id)
	}
	pp.Reset()
	return nil
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

// Plugins describes the registered plugins, those that are on or off first
// and the unavailable ones after them, each in their order.
func (e *Engine) Plugins() []PluginInfo {
	cfg := e.cfg.Get()
	out := make([]PluginInfo, 0, len(e.plugins))
	var unavailable []PluginInfo
	for _, l := range e.plugins {
		if state, reason := e.stateOf(l); state == stateFailed {
			info := PluginInfo{
				ID:          l.m.ID,
				Name:        l.loc.Resolve(l.m.Name),
				Description: l.loc.Resolve(l.m.Description),
				Version:     l.m.Version,
				Icon:        l.m.Icon,
				Unavailable: reason,
				Permissions: l.m.Permissions,
			}
			origin(&info, l.p)
			unavailable = append(unavailable, info)
			continue
		}
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
			p.Group = l.loc.Resolve(p.Group)
			p.Options = resolveOptions(l, p.Options)
			info.Preferences = append(info.Preferences, p)
		}
		origin(&info, l.p)
		out = append(out, info)
	}
	return append(out, unavailable...)
}

// resolveOptions returns a copy of options with localized titles.
func resolveOptions(l *loaded, options []plugin.Option) []plugin.Option {
	out := slices.Clone(options)
	for i := range out {
		out[i].Title = l.loc.Resolve(out[i].Title)
	}
	return out
}

// preference returns the declared preference name of plugin id.
func (e *Engine) preference(id, name string) (*loaded, plugin.Preference, error) {
	l, ok := e.byID[id]
	if !ok {
		return nil, plugin.Preference{}, fmt.Errorf("core: unknown plugin %q", id)
	}
	i := slices.IndexFunc(l.m.Preferences, func(p plugin.Preference) bool { return p.Name == name })
	if i < 0 {
		return nil, plugin.Preference{}, fmt.Errorf("core: plugin %q has no preference %q", id, name)
	}
	return l, l.m.Preferences[i], nil
}

// PreferenceOptions asks plugin id for the options of its preference name,
// declared with DynamicOptions (design m5 §4). The call is guarded like
// every plugin call and gives up after the view time limit.
func (e *Engine) PreferenceOptions(ctx context.Context, id, name string) ([]plugin.Option, error) {
	l, pref, err := e.preference(id, name)
	if err != nil {
		return nil, err
	}
	if _, err := e.running(id); err != nil {
		return nil, err
	}
	pp, ok := l.p.(plugin.PreferenceOptionsProvider)
	if !pref.DynamicOptions || !ok {
		return nil, fmt.Errorf("core: preference %s/%s has no dynamic options", id, name)
	}
	opts, err := callPlugin(ctx, e, id, viewTimeout, func(ctx context.Context) ([]plugin.Option, error) {
		return pp.PreferenceOptions(ctx, name)
	})
	// With an error the plugin may still give the options that remain (e.g.
	// "Default" when a model list could not be had): the error is shown
	// under them.
	return resolveOptions(l, opts), err
}

// RunPreferenceAction runs the Action of the button preference name of
// plugin id, as clicking it in the settings does, and returns its outcome
// (usually a toast to show next to the button).
func (e *Engine) RunPreferenceAction(ctx context.Context, id, name string) (Outcome, error) {
	_, pref, err := e.preference(id, name)
	if err != nil {
		return Outcome{}, err
	}
	if pref.Type != plugin.PrefButton || pref.Action == nil {
		return Outcome{}, fmt.Errorf("core: preference %s/%s is not a button", id, name)
	}
	return e.Run(ctx, Entry{Plugin: id}, plugin.Action{ID: name, Do: *pref.Action}, false)
}

// PrefVisible reports whether preference p shows, given the configured
// values: a VisibleWhen condition compares the other preference's value
// (or its default) with Equals as JSON values.
func PrefVisible(prefs []plugin.Preference, values map[string]jsontext.Value, p plugin.Preference) bool {
	cond := p.VisibleWhen
	if cond == nil {
		return true
	}
	v, ok := values[cond.Pref]
	if !ok {
		i := slices.IndexFunc(prefs, func(o plugin.Preference) bool { return o.Name == cond.Pref })
		if i < 0 {
			return false
		}
		v = prefs[i].Default
	}
	var a, b any
	if json.Unmarshal(v, &a) != nil || json.Unmarshal(cond.Equals, &b) != nil {
		return false
	}
	return reflect.DeepEqual(a, b)
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
