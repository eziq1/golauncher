package builtin

import (
	"strings"
	"testing"

	"github.com/eziq1/golauncher/plugins/ai"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// TestAIBackends: the registry's first backend is the AI plugin's default,
// and the IDs are unique.
func TestAIBackends(t *testing.T) {
	bs := aiBackends()
	if len(bs) == 0 || bs[0].ID != ai.DefaultBackendID || bs[0].Backend.Name() != "Claude Code" {
		t.Fatalf("registry %+v", bs)
	}
	var names []string
	for _, b := range bs {
		names = append(names, b.ID+"="+b.Backend.Name())
		if _, ok := b.Backend.(backend.Forgetter); !ok {
			t.Errorf("%s cannot forget a deleted chat", b.ID)
		}
	}
	if strings.Join(names, ",") != "claude-code=Claude Code,codex=Codex (ChatGPT),gemini=Gemini CLI" || bs[0].Models != nil || bs[1].Models == nil {
		t.Errorf("registry %q", names)
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
