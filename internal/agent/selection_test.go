package agent_test

import (
	"testing"

	"github.com/eziq1/golauncher/internal/agent"
)

// TestSelectionPluginHidden: the selected text's plugin is never offered to
// AI, whatever the settings say (docs/selected-text.md §4.1); its items in
// the root are Sensitive too, and Engine.Search never has them (core).
func TestSelectionPluginHidden(t *testing.T) {
	if !agent.Hidden("builtin.selection") {
		t.Error("builtin.selection is offered to AI")
	}
}
