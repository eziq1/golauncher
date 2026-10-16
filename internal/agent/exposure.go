package agent

import (
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
)

// Plugins that are never offered to AI: the AI chat itself, the selected
// text (docs/selected-text.md §4.1) and the activity log of the tools.
const (
	aiPluginID        = "builtin.ai"
	selectionPluginID = "builtin.selection"
	clipboardPluginID = "builtin.clipboard"
)

// Hidden reports whether plugin id is never offered to AI, whatever the
// settings say.
func Hidden(id string) bool { return id == aiPluginID || id == selectionPluginID || id == PluginID }

// DefaultExposed reports whether AI may use plugin p when the user has not
// decided (docs/ai.md §4.2): the built-in plugins except the clipboard
// history; plugins running in their own process (third-party) are off.
func DefaultExposed(p core.PluginInfo) bool {
	return p.Origin != core.OriginExternal && p.ID != clipboardPluginID
}

// Exposed reports whether AI may use plugin p now: it is on and available,
// not hidden, and allowed by the settings or by default.
func Exposed(p core.PluginInfo, cfg config.AgentConfig) bool {
	if Hidden(p.ID) || !p.Enabled || p.Unavailable != "" {
		return false
	}
	if v, ok := cfg.Plugins[p.ID]; ok {
		return v
	}
	return DefaultExposed(p)
}
