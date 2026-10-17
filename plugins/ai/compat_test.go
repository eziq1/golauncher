package ai

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

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
