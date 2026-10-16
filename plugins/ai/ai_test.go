package ai

import (
	"encoding/json/jsontext"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

func newTestPlugin(t *testing.T, fake *backend.Fake, enabled bool) (*Plugin, *hostfake.Host) {
	t.Helper()
	n := 0
	p := NewWithOptions(Options{Backend: fake, Sync: true, HasProtocol: func(string) bool { return false },
		NewID: func() string { n++; return "id-" + string(rune('0'+n)) }})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true")})
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p, h
}

// openChat opens a chat view instance, as the host does.
func openChat(t *testing.T, p *Plugin, instance string, pl chatPayload) plugin.View {
	t.Helper()
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: viewChat, Instance: instance, Payload: plugin.MustPayload(pl)})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func act(t *testing.T, p *Plugin, target, instance, query string) plugin.Result {
	t.Helper()
	r, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: target,
		Payload: plugin.MustPayload(instancePayload{Instance: instance}), Query: query})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestChatSessionIsStoredAndResumed(t *testing.T) {
	fake := &backend.Fake{}
	p, h := newTestPlugin(t, fake, true)
	v := openChat(t, p, "v1", chatPayload{Text: "first question"})
	if !strings.Contains(v.Detail.Markdown, "You asked: first question") {
		t.Fatalf("markdown:\n%s", v.Detail.Markdown)
	}
	if r := act(t, p, "send", "v1", "  second  "); !r.ClearText {
		t.Fatalf("send = %+v", r)
	}
	opens := fake.Opens()
	if len(opens) != 1 || opens[0].SessionID != "id-1" || opens[0].Resume || !strings.Contains(opens[0].SystemPrompt, "Answer in English") {
		t.Fatalf("opens = %+v", opens)
	}
	list, _ := p.st.Sessions()
	if len(list) != 1 || list[0].Title != "first question" || list[0].Messages != 4 || !list[0].Started {
		t.Fatalf("sessions = %+v", list)
	}
	// Reopened from the history (a new plugin, as after a restart), the
	// chat resumes the session.
	p2 := NewWithOptions(Options{Backend: fake, Sync: true, HasProtocol: func(string) bool { return false }})
	if err := p2.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	v = openChat(t, p2, "v9", chatPayload{Session: "id-1"})
	if !strings.Contains(v.Detail.Markdown, "You asked: second") {
		t.Fatalf("reopened markdown:\n%s", v.Detail.Markdown)
	}
	act(t, p2, "send", "v9", "third")
	if opens := fake.Opens(); !opens[len(opens)-1].Resume || opens[len(opens)-1].SessionID != "id-1" {
		t.Fatalf("not resumed: %+v", opens[len(opens)-1])
	}
}

func TestPruneOldChats(t *testing.T) {
	p, h := newTestPlugin(t, &backend.Fake{}, true)
	now := h.Now()
	for i, age := range []time.Duration{time.Hour, 29 * 24 * time.Hour, 31 * 24 * time.Hour, 400 * 24 * time.Hour} {
		s := Session{ID: string(rune('a' + i)), Title: "t", Created: now.Add(-age), Updated: now.Add(-age)}
		if err := p.st.Save(s, []Message{{Role: RoleUser, Text: "q"}}); err != nil {
			t.Fatal(err)
		}
	}
	p.historyView() // prunes
	list, _ := p.st.Sessions()
	if len(list) != 2 || list[0].ID != "a" || list[1].ID != "b" {
		t.Fatalf("after pruning (30 days): %+v", list)
	}
	if _, ok, _ := h.KV.Get(sessionPrefix + "c"); ok {
		t.Fatal("the messages of a pruned chat are kept")
	}
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true"), prefRetention: jsontext.Value(`"7"`)})
	p.historyView()
	if list, _ := p.st.Sessions(); len(list) != 1 {
		t.Fatalf("after pruning (7 days): %+v", list)
	}
}

func TestHistoryDeleteAndClear(t *testing.T) {
	p, _ := newTestPlugin(t, &backend.Fake{}, true)
	openChat(t, p, "v1", chatPayload{Text: "one"})
	openChat(t, p, "v2", chatPayload{Text: "two"})
	v := p.historyView()
	if len(v.Sections) != 1 || len(v.Sections[0].Items) != 2 {
		t.Fatalf("history = %+v", v)
	}
	it := v.Sections[0].Items[0]
	if it.Accessory != "2 messages" || it.Actions[0].Do.Kind != plugin.ActionPush {
		t.Fatalf("item = %+v", it)
	}
	if _, err := p.HandleAction(t.Context(), it.Actions[1].Do); err != nil {
		t.Fatal(err)
	}
	if list, _ := p.st.Sessions(); len(list) != 1 {
		t.Fatalf("after delete: %+v", list)
	}
	r, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "clear"})
	if err != nil || r.Toast == nil || r.Toast.Title != "Deleted 1 chat" {
		t.Fatalf("clear = %+v, %v", r, err)
	}
	if v := p.historyView(); len(v.Sections) != 0 {
		t.Fatalf("history after clear = %+v", v)
	}
}

func TestOffPromptAndEnable(t *testing.T) {
	fake := &backend.Fake{}
	p, h := newTestPlugin(t, fake, false)
	if items, _ := p.FallbackItems(t.Context(), plugin.Query{Text: "x"}); len(items) != 0 {
		t.Fatal("Ask AI offered while off")
	}
	a, ok := p.TabAction(plugin.Query{Text: "hi"})
	if !ok || a.Do.Kind != plugin.ActionPush {
		t.Fatalf("TabAction = %+v %v", a, ok)
	}
	v := openChat(t, p, "v1", chatPayload{Text: "hi"})
	if !strings.Contains(v.Detail.Markdown, "> hi") || v.Sections[0].Items[0].Actions[0].ID != "enable" {
		t.Fatalf("off view = %+v", v)
	}
	r, _ := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "enable"})
	if string(r.Preferences[prefEnabled]) != "true" {
		t.Fatalf("enable = %+v", r)
	}
	if len(fake.Sends()) != 0 {
		t.Fatal("sent while off")
	}
	h.Prefs.SetValues(r.Preferences) // what the host does
	v = openChat(t, p, "v1", chatPayload{})
	if !strings.Contains(v.Detail.Markdown, "You asked: hi") {
		t.Fatalf("pending question not sent:\n%s", v.Detail.Markdown)
	}
	// The fallback follows its preference.
	if items, _ := p.FallbackItems(t.Context(), plugin.Query{Text: "x"}); len(items) != 1 || items[0].Title != "Ask AI: x" {
		t.Fatalf("fallback = %+v", items)
	}
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true"), prefFallback: jsontext.Value("false")})
	if items, _ := p.FallbackItems(t.Context(), plugin.Query{Text: "x"}); len(items) != 0 {
		t.Fatal("fallback shown although turned off")
	}
}

func TestBackendErrorsAreExplained(t *testing.T) {
	for kind, want := range map[backend.ErrorKind]string{
		backend.ErrNotInstalled: "Claude Code is not installed",
		backend.ErrNotLoggedIn:  "run `claude` in a terminal",
		backend.ErrQuota:        "usage limit was reached",
	} {
		fake := &backend.Fake{CheckErr: &backend.Error{Kind: kind, Message: "CLI says so"}}
		p, _ := newTestPlugin(t, fake, true)
		v := openChat(t, p, "v1", chatPayload{Text: "q"})
		md := v.Detail.Markdown
		if !strings.Contains(strings.ToLower(md), strings.ToLower(want)) || !strings.Contains(md, "CLI says so") {
			t.Errorf("%s:\n%s", kind, md)
		}
	}
	fake := &backend.Fake{SendErr: errors.New("boom")}
	p, _ := newTestPlugin(t, fake, true)
	if v := openChat(t, p, "v1", chatPayload{Text: "q"}); !strings.Contains(v.Detail.Markdown, "boom") {
		t.Errorf("send error:\n%s", v.Detail.Markdown)
	}
}

func TestIdleReleaseAfterHide(t *testing.T) {
	fake := &backend.Fake{}
	p := NewWithOptions(Options{Backend: fake, Sync: true, IdleRelease: 10 * time.Millisecond})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true")})
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	openChat(t, p, "v1", chatPayload{Text: "q"})
	h.Bus.Publish(plugin.EventWindowHidden)
	deadline := time.Now().Add(5 * time.Second)
	for fake.Released() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the conversation was not released after the window hid")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHandoffLinks(t *testing.T) {
	q := "what is 2+2?"
	enc := url.QueryEscape(q)
	cases := []struct {
		target   string
		app      bool
		question string
		want     string
		clip     bool
	}{
		{"claude", false, q, "https://claude.ai/new?q=" + enc, false},
		{"claude", true, q, "claude://claude.ai/new?q=" + enc, false},
		{"claude", true, "/etc/hosts?", "https://claude.ai/new?q=" + url.QueryEscape("/etc/hosts?"), false},
		{"chatgpt", true, q, "https://chatgpt.com/?q=" + enc, false},
		{"chatgpt", false, "", "https://chatgpt.com/", false},
		{"chatgpt", false, strings.Repeat("长", 3000), "https://chatgpt.com/", true},
		{"claude", true, strings.Repeat("a", maxClaudeAppRunes+1), "claude://claude.ai/new", true},
	}
	for _, c := range cases {
		got, clip := handoffLink(c.target, c.question, c.app)
		if got != c.want || clip != c.clip {
			t.Errorf("handoffLink(%s, %.20q, app=%v) = %.60q, %v; want %.60q, %v", c.target, c.question, c.app, got, clip, c.want, c.clip)
		}
	}
}

func TestContinueInUsesLastQuestionOrClipboard(t *testing.T) {
	p, h := newTestPlugin(t, &backend.Fake{}, true)
	openChat(t, p, "v1", chatPayload{Text: "tell me a joke"})
	r := act(t, p, "chatgpt", "v1", "")
	if r.Do == nil || r.Do.Kind != plugin.ActionOpen || r.Do.Target != "https://chatgpt.com/?q=tell+me+a+joke" {
		t.Fatalf("continue = %+v", r.Do)
	}
	long := strings.Repeat("x", maxLinkLen)
	r = act(t, p, "claude", "v1", long)
	if r.Do.Target != claudeWeb {
		t.Fatalf("long question link = %q", r.Do.Target)
	}
	if c, _ := h.Clip.Read(); c.Text != long {
		t.Fatal("the long question was not copied")
	}
}

func TestNewSessionIDIsUUID(t *testing.T) {
	id := newSessionID()
	if len(id) != 36 || id[14] != '4' || strings.Count(id, "-") != 4 || id == newSessionID() {
		t.Fatalf("id = %q", id)
	}
}
