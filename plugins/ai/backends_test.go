package ai

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// named is a backend with another display name.
type named struct {
	*backend.Fake
	name string
}

func (n named) Name() string { return n.name }

// TestPermissionRequestRejected: the chat cannot ask yet, so a permission
// request (reserved for ACP agents) is rejected and shows nothing.
func TestPermissionRequestRejected(t *testing.T) {
	answers := []string{}
	fake := &backend.Fake{Steps: func(string) []backend.Event {
		return []backend.Event{{Kind: backend.EventPermission, Permission: &backend.PermissionRequest{ToolID: "t1", Tool: "write_file",
			Options: []backend.PermissionOption{{ID: "allow", Kind: "allow_once"}}, Respond: func(id string) { answers = append(answers, id) }}}}
	}}
	p, _ := newTestPlugin(t, fake, true)
	v := openChat(t, p, "v1", chatPayload{Text: "q"})
	if len(answers) != 1 || answers[0] != "" || !strings.Contains(v.Detail.Markdown, "You asked: q") || strings.Contains(v.Detail.Markdown, "write_file") {
		t.Fatalf("answers %q\n%s", answers, v.Detail.Markdown)
	}
}

// TestBackendPreference: the "Backend" preference picks the backend by ID
// (default: the first); a chat moves to the chosen one; an unknown ID is
// explained in the chat.
func TestBackendPreference(t *testing.T) {
	claude, other := &backend.Fake{}, &backend.Fake{Reply: func(string) []string { return []string{"from other"} }}
	p := NewWithOptions(Options{Sync: true, HasProtocol: func(string) bool { return false }, Backends: []BackendOption{
		{ID: DefaultBackendID, Backend: claude}, {ID: "other", Backend: named{other, "Other CLI"}},
	}})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true")})
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	var pref *plugin.Preference
	for _, pr := range p.Manifest().Preferences {
		if pr.Name == prefBackend {
			pref = &pr
		}
	}
	if pref == nil || pref.Type != plugin.PrefSelect || string(pref.Default) != `"claude-code"` || len(pref.Options) != 2 ||
		pref.Options[0] != (plugin.Option{Value: "claude-code", Title: "Claude Code"}) || pref.Options[1] != (plugin.Option{Value: "other", Title: "Other CLI"}) {
		t.Fatalf("preference %+v", pref)
	}

	// The default.
	v := openChat(t, p, "v1", chatPayload{Text: "one"})
	if !strings.Contains(v.Detail.Markdown, "You asked: one") || len(claude.Sends()) != 1 || len(other.Sends()) != 0 {
		t.Fatalf("default backend:\n%s", v.Detail.Markdown)
	}
	// Switched: the chat's conversation moves to the other backend.
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true"), prefBackend: jsontext.Value(`"other"`)})
	act(t, p, "send", "v1", "two")
	if len(other.Sends()) != 1 || len(claude.Sends()) != 1 || claude.Released() != 1 {
		t.Fatalf("sends %q / %q, released %d", claude.Sends(), other.Sends(), claude.Released())
	}
	v = openChat(t, p, "v1", chatPayload{})
	if !strings.Contains(v.Detail.Markdown, "from other") || v.Detail.Metadata[0].Label != "Other CLI" {
		t.Fatalf("other backend: %+v\n%s", v.Detail.Metadata, v.Detail.Markdown)
	}
	// Unknown (a configuration of another version).
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true"), prefBackend: jsontext.Value(`"gone"`)})
	v = openChat(t, p, "v2", chatPayload{Text: "three"})
	if md := v.Detail.Markdown; !strings.Contains(md, "not available in this version") || !strings.Contains(md, "> gone") {
		t.Fatalf("unknown backend:\n%s", md)
	}
}
