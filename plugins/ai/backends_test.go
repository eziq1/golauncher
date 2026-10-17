package ai

import (
	"context"
	"encoding/json/jsontext"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// named is a backend with another display name, which records Forget.
type named struct {
	*backend.Fake
	name string

	mu        sync.Mutex
	forgotten []string
}

func (n *named) Name() string { return n.name }

func (n *named) Forget(id string) {
	n.mu.Lock()
	n.forgotten = append(n.forgotten, id)
	n.mu.Unlock()
}

func (n *named) forgot() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.forgotten)
}

// newMultiPlugin is the plugin with Claude Code (a Fake) and "other", AI on.
func newMultiPlugin(t *testing.T, claude *backend.Fake, other *named, models []plugin.Option) (*Plugin, *hostfake.Host) {
	t.Helper()
	p := NewWithOptions(Options{Sync: true, HasProtocol: func(string) bool { return false }, Backends: []BackendOption{
		{ID: DefaultBackendID, Backend: claude}, {ID: "other", Backend: other, Models: models},
	}})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true")})
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p, h
}

func setPrefs(h *hostfake.Host, kv ...string) {
	m := map[string]jsontext.Value{prefEnabled: jsontext.Value("true")}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = jsontext.Value(kv[i+1])
	}
	h.Prefs.SetValues(m)
}

// TestBackendPreference: the "Backend" preference picks the backend of new
// chats by ID (default: the first); a chat keeps the backend it was started
// with (R72); an unknown ID is explained in the chat.
func TestBackendPreference(t *testing.T) {
	claude, other := &backend.Fake{}, &named{Fake: &backend.Fake{Reply: func(string) []string { return []string{"from other"} }}, name: "Other CLI"}
	p, h := newMultiPlugin(t, claude, other, nil)

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
	// Switched: the existing chat stays on Claude Code, a new one uses the
	// other backend.
	setPrefs(h, prefBackend, `"other"`)
	act(t, p, "send", "v1", "two")
	if len(other.Sends()) != 0 || len(claude.Sends()) != 2 {
		t.Fatalf("sends %q / %q", claude.Sends(), other.Sends())
	}
	v = openChat(t, p, "v2", chatPayload{Text: "three"})
	if !strings.Contains(v.Detail.Markdown, "from other") || v.Detail.Metadata[0].Label != "Other CLI" || len(other.Sends()) != 1 ||
		!strings.Contains(v.Detail.Markdown, "**Other CLI**") {
		t.Fatalf("other backend: %+v\n%s", v.Detail.Metadata, v.Detail.Markdown)
	}
	v = openChat(t, p, "v1", chatPayload{})
	if v.Detail.Metadata[0].Label != "Claude Code" || !strings.Contains(v.Detail.Markdown, "**Claude**") {
		t.Fatalf("first chat: %+v\n%s", v.Detail.Metadata, v.Detail.Markdown)
	}
	// The history shows each chat's backend.
	hv := p.historyView()
	var subs []string
	for _, it := range hv.Sections[0].Items {
		subs = append(subs, it.Subtitle)
	}
	if len(subs) != 2 || !slices.ContainsFunc(subs, func(s string) bool { return strings.HasSuffix(s, " · Other CLI") }) ||
		!slices.ContainsFunc(subs, func(s string) bool { return strings.HasSuffix(s, " · Claude Code") }) {
		t.Fatalf("history subtitles %q", subs)
	}
	// Unknown (a configuration of another version).
	setPrefs(h, prefBackend, `"gone"`)
	v = openChat(t, p, "v3", chatPayload{Text: "four"})
	if md := v.Detail.Markdown; !strings.Contains(md, "not available in this version") || !strings.Contains(md, "> gone") {
		t.Fatalf("unknown backend:\n%s", md)
	}
}

// TestChatOfRemovedBackend: a chat started with a backend this version does
// not have says so when it opens, and when a message is sent.
func TestChatOfRemovedBackend(t *testing.T) {
	claude, other := &backend.Fake{}, &named{Fake: &backend.Fake{}, name: "Other CLI"}
	p, h := newMultiPlugin(t, claude, other, nil)
	now := h.Now()
	s := Session{ID: "old", BackendID: "removed-cli", Backend: "Removed CLI", Title: "t", Created: now, Updated: now, Started: true}
	if err := p.st.Save(s, []Message{{Role: RoleUser, Text: "q"}, {Role: RoleAssistant, Text: "a"}}); err != nil {
		t.Fatal(err)
	}
	v := openChat(t, p, "v1", chatPayload{Session: "old"})
	if !strings.Contains(v.Detail.Markdown, "started with Removed CLI, which this version of golauncher does not have") {
		t.Fatalf("opened:\n%s", v.Detail.Markdown)
	}
	act(t, p, "send", "v1", "more")
	v = openChat(t, p, "v1", chatPayload{})
	if !strings.Contains(v.Detail.Markdown, "This chat was started with Removed CLI, which is not available") ||
		len(claude.Sends())+len(other.Sends()) != 0 {
		t.Fatalf("sent:\n%s", v.Detail.Markdown)
	}
	// An older record without a backend ID is Claude Code's.
	if err := p.st.Save(Session{ID: "legacy", Backend: "Claude Code", Title: "t", Created: now, Updated: now, Started: true},
		[]Message{{Role: RoleUser, Text: "q"}, {Role: RoleAssistant, Text: "a"}}); err != nil {
		t.Fatal(err)
	}
	setPrefs(h, prefBackend, `"other"`)
	openChat(t, p, "v2", chatPayload{Session: "legacy"})
	act(t, p, "send", "v2", "again")
	if len(claude.Sends()) != 1 || len(other.Sends()) != 0 || !claude.Opens()[0].Resume {
		t.Fatalf("legacy chat: %q / %q", claude.Sends(), other.Sends())
	}
}

// TestBackendModels: a backend with its own models gets its own "Model"
// preference, which its chats use; Claude Code keeps the "Model" preference.
func TestBackendModels(t *testing.T) {
	claude, other := &backend.Fake{}, &named{Fake: &backend.Fake{}, name: "Other CLI"}
	p, h := newMultiPlugin(t, claude, other, []plugin.Option{{Value: "m1", Title: "M1"}})
	var names []string
	for _, pr := range p.Manifest().Preferences {
		names = append(names, pr.Name)
		if pr.Name == "model.other" && (len(pr.Options) != 2 || pr.Options[0].Value != "" || pr.Options[1].Value != "m1") {
			t.Errorf("options %+v", pr.Options)
		}
	}
	if !slices.Contains(names, "model.other") || slices.Index(names, "model.other") != slices.Index(names, prefModel)+1 {
		t.Fatalf("preferences %q", names)
	}
	setPrefs(h, prefModel, `"opus"`, "model.other", `"m1"`)
	openChat(t, p, "v1", chatPayload{Text: "one"})
	setPrefs(h, prefModel, `"opus"`, "model.other", `"m1"`, prefBackend, `"other"`)
	v := openChat(t, p, "v2", chatPayload{Text: "two"})
	if claude.Opens()[0].Model != "opus" || other.Opens()[0].Model != "m1" || v.Detail.Metadata[0].Value != "m1" {
		t.Fatalf("models %q %q, metadata %+v", claude.Opens()[0].Model, other.Opens()[0].Model, v.Detail.Metadata)
	}
}

// TestForgetOnDelete: deleting, clearing or pruning chats tells their
// backend to forget the CLI's session.
func TestForgetOnDelete(t *testing.T) {
	claude, other := &backend.Fake{}, &named{Fake: &backend.Fake{}, name: "Other CLI"}
	p, h := newMultiPlugin(t, claude, other, nil)
	setPrefs(h, prefBackend, `"other"`)
	openChat(t, p, "v1", chatPayload{Text: "one"})
	openChat(t, p, "v2", chatPayload{Text: "two"})
	list, _ := p.st.Sessions()
	if len(list) != 2 || list[0].BackendID != "other" {
		t.Fatalf("sessions %+v", list)
	}
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "delete", Payload: plugin.MustPayload(list[0].ID)}); err != nil {
		t.Fatal(err)
	}
	if got := other.forgot(); len(got) != 1 || got[0] != list[0].ID {
		t.Fatalf("forgotten %q", got)
	}
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "clear"}); err != nil {
		t.Fatal(err)
	}
	if got := other.forgot(); len(got) != 2 || got[1] != list[1].ID {
		t.Fatalf("forgotten %q", got)
	}
	old := h.Now().Add(-400 * 24 * time.Hour)
	p.st.Save(Session{ID: "old", BackendID: "other", Title: "t", Created: old, Updated: old}, []Message{{Role: RoleUser, Text: "q"}})
	p.historyView() // prunes
	if got := other.forgot(); len(got) != 3 || got[2] != "old" {
		t.Fatalf("forgotten %q", got)
	}
}

// TestNotice: a backend notice (a new session) is shown before the answer
// and kept with it.
func TestNotice(t *testing.T) {
	fake := &backend.Fake{Steps: func(string) []backend.Event {
		return []backend.Event{{Kind: backend.EventNotice, Text: backend.NoticeNewSession}}
	}}
	p, _ := newTestPlugin(t, fake, true)
	v := openChat(t, p, "v1", chatPayload{Text: "q"})
	if !strings.Contains(v.Detail.Markdown, "_Claude Code could not continue the earlier conversation") {
		t.Fatalf("%s", v.Detail.Markdown)
	}
	_, msgs, _ := p.st.Get(p.views["v1"])
	if len(msgs) != 2 || msgs[1].Notice != backend.NoticeNewSession {
		t.Fatalf("messages %+v", msgs)
	}
}

// permissionEvent is a permission request whose answers are recorded.
func permissionEvent(kind string, options []backend.PermissionOption, answers *[]string, done chan struct{}) backend.Event {
	return backend.Event{Kind: backend.EventPermission, Permission: &backend.PermissionRequest{ToolID: "t1", Tool: "web_fetch",
		Title: "Fetching content from: example.com", Kind: kind, Options: options, Done: done,
		Respond: func(id string) { *answers = append(*answers, id) }}}
}

var acpOptions = []backend.PermissionOption{
	{ID: "always", Title: "Always Allow", Kind: "allow_always"},
	{ID: "once", Title: "Allow", Kind: "allow_once"},
	{ID: "no", Title: "Reject", Kind: "reject_once"},
}

// TestPermissionRequests: an agent's permission request is asked in the
// host's dialog (backend name, action, tool) and answered with the option
// of the decision; without an asker it is rejected.
func TestPermissionRequests(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     string
		decision backend.PermissionDecision
		want     string
		session  bool
	}{
		{"allow", "fetch", backend.PermissionAllow, "once", true},
		{"session", "fetch", backend.PermissionAllowSession, "always", true},
		{"deny", "fetch", backend.PermissionDeny, "", true},
		{"destructive", "delete", backend.PermissionAllow, "once", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var answers []string
			fake := &backend.Fake{Steps: func(string) []backend.Event {
				return []backend.Event{permissionEvent(tc.kind, acpOptions, &answers, nil)}
			}}
			p, _ := newTestPlugin(t, fake, true)
			var asked []backend.PermissionAsk
			p.SetPermissionAsker(func(_ context.Context, a backend.PermissionAsk) backend.PermissionDecision {
				asked = append(asked, a)
				return tc.decision
			})
			openChat(t, p, "v1", chatPayload{Text: "q"})
			if len(answers) != 1 || answers[0] != tc.want || len(asked) != 1 {
				t.Fatalf("answers %q, asked %+v", answers, asked)
			}
			a := asked[0]
			if a.Client != "Claude Code (AI Chat)" || a.Action != "Fetching content from: example.com" || a.Tool != "web_fetch" ||
				a.Message != "Tool: web_fetch" || a.AllowSession != tc.session || a.Destructive != !tc.session {
				t.Fatalf("asked %+v", a)
			}
		})
	}
	t.Run("noAsker", func(t *testing.T) {
		var answers []string
		fake := &backend.Fake{Steps: func(string) []backend.Event {
			return []backend.Event{permissionEvent("fetch", acpOptions, &answers, nil)}
		}}
		p, _ := newTestPlugin(t, fake, true)
		v := openChat(t, p, "v1", chatPayload{Text: "q"})
		if len(answers) != 1 || answers[0] != "" || !strings.Contains(v.Detail.Markdown, "You asked: q") {
			t.Fatalf("answers %q\n%s", answers, v.Detail.Markdown)
		}
	})
	t.Run("answeredElsewhere", func(t *testing.T) {
		// The runtime answered it (timeout, stop): Done closes the dialog.
		var answers []string
		done := make(chan struct{})
		close(done)
		fake := &backend.Fake{Steps: func(string) []backend.Event {
			return []backend.Event{permissionEvent("fetch", []backend.PermissionOption{{ID: "once", Kind: "allow_once"}}, &answers, done)}
		}}
		p, _ := newTestPlugin(t, fake, true)
		p.SetPermissionAsker(func(ctx context.Context, a backend.PermissionAsk) backend.PermissionDecision {
			select {
			case <-ctx.Done():
				return backend.PermissionDeny
			case <-time.After(10 * time.Second):
				return backend.PermissionAllow
			}
		})
		start := time.Now()
		openChat(t, p, "v1", chatPayload{Text: "q"})
		if len(answers) != 1 || answers[0] != "" || time.Since(start) > 5*time.Second {
			t.Fatalf("answers %q after %v", answers, time.Since(start))
		}
	})
}
