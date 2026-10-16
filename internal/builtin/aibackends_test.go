package builtin

import (
	"testing"

	"github.com/eziq1/golauncher/plugins/ai"
)

// TestAIBackends: the registry's first backend is the AI plugin's default,
// and the IDs are unique.
func TestAIBackends(t *testing.T) {
	bs := aiBackends()
	if len(bs) == 0 || bs[0].ID != ai.DefaultBackendID || bs[0].Backend.Name() != "Claude Code" {
		t.Fatalf("registry %+v", bs)
	}
	seen := map[string]bool{}
	for _, b := range bs {
		if b.ID == "" || seen[b.ID] || b.Backend == nil {
			t.Errorf("bad entry %+v", b)
		}
		seen[b.ID] = true
		if c, ok := b.Backend.(interface{ Close() error }); ok {
			c.Close()
		}
	}
}
