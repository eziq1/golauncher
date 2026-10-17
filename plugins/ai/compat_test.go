package ai

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// TestOlderBackend: what an older CLI runs without is left out of the
// conversation's options (and its system prompt) and named in the footer.
func TestOlderBackend(t *testing.T) {
	fake := &backend.Fake{Status: &backend.Status{Version: "2.1.100",
		Reduced: []string{backend.FeatureWebSearch, backend.FeatureLauncherTools, backend.FeatureStreaming, backend.FeatureModel}}}
	p, _ := newTestPlugin(t, fake, true)
	v := openChat(t, p, "v1", chatPayload{Text: "q"})
	opens := fake.Opens()
	if len(opens) != 1 || opens[0].WebSearch || opens[0].LauncherTools || strings.Contains(opens[0].SystemPrompt, "search the web") ||
		strings.Contains(opens[0].SystemPrompt, "run_action") {
		t.Fatalf("opens = %+v", opens)
	}
	v = openChat(t, p, "v1", chatPayload{})
	// The model preference is the default: its absence is not news.
	if got, want := v.Detail.Metadata[0].Value, "older version 2.1.100, without web search, launcher plugins, streamed answers"; !strings.HasSuffix(got, want) {
		t.Errorf("footer %q, want … %q", got, want)
	}

	// Features the preferences turned off are not named.
	fake = &backend.Fake{Status: &backend.Status{Version: "2.1.100", Reduced: []string{backend.FeatureWebSearch}}}
	p, h := newTestPlugin(t, fake, true)
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true"), prefWebSearch: jsontext.Value("false")})
	v = openChat(t, p, "v1", chatPayload{Text: "q"})
	if got := v.Detail.Metadata[0].Value; strings.Contains(got, "older") {
		t.Errorf("footer %q", got)
	}

	// A current CLI: nothing changes.
	fake = &backend.Fake{}
	p, _ = newTestPlugin(t, fake, true)
	v = openChat(t, p, "v1", chatPayload{Text: "q"})
	if opens := fake.Opens(); len(opens) != 1 || !opens[0].WebSearch || !opens[0].LauncherTools || strings.Contains(v.Detail.Metadata[0].Value, "older") {
		t.Errorf("opens %+v, footer %q", opens, v.Detail.Metadata[0].Value)
	}
}

func TestCheckButton(t *testing.T) {
	m := (&Plugin{be: &chooser{opts: []BackendOption{{ID: DefaultBackendID, Backend: &backend.Fake{}}}}}).Manifest()
	found := false
	for _, pref := range m.Preferences {
		if pref.Name == prefCheck {
			found = pref.Type == plugin.PrefButton && pref.Action != nil && pref.Action.Target == prefCheck
		}
	}
	if !found {
		t.Fatal("no check button")
	}
	for _, tt := range []struct {
		name  string
		fake  *backend.Fake
		style plugin.ToastStyle
		title string
		msg   string
	}{
		{"current", &backend.Fake{Status: &backend.Status{Version: "2.1.296"}}, plugin.ToastSuccess, "Claude Code 2.1.296 found", ""},
		{"older", &backend.Fake{Status: &backend.Status{Version: "2.1.100", Reduced: []string{backend.FeatureWebSearch}}}, plugin.ToastInfo,
			"Claude Code 2.1.100 found", "Older Claude Code: web search unavailable. Update it for the full feature set."},
		{"untested", &backend.Fake{Status: &backend.Status{Version: "3.0.0", Untested: true}}, plugin.ToastSuccess,
			"Claude Code 3.0.0 found", "golauncher was not tested with this version; it uses it more carefully (one process per answer)."},
		{"too old", &backend.Fake{CheckErr: &backend.Error{Kind: backend.ErrVersion, Version: "2.1.5", Message: "Claude Code 2.1.5 lacks --tools, which golauncher needs."}},
			plugin.ToastFailure, "Claude Code cannot be used", "too old"},
		{"not installed", &backend.Fake{CheckErr: &backend.Error{Kind: backend.ErrNotInstalled}}, plugin.ToastFailure,
			"Claude Code cannot be used", "is not installed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := newTestPlugin(t, tt.fake, true)
			r := act(t, p, prefCheck, "", "")
			if r.Toast == nil || r.Toast.Style != tt.style || r.Toast.Title != tt.title || !strings.Contains(r.Toast.Message, tt.msg) || tt.msg == "" && r.Toast.Message != "" {
				t.Fatalf("toast %+v", r.Toast)
			}
		})
	}
}
