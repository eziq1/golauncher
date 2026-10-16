package ai

import (
	"encoding/json/jsontext"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// launcherSteps imitates the model using golauncher's tools: a search, a
// view command, a detail and an action, each started without input, then
// given its input, then its result.
func launcherSteps(action string) func(string) []backend.Event {
	call := func(id, tool, input, result string, isErr bool) []backend.Event {
		name := "mcp__golauncher__" + tool
		return []backend.Event{
			{Kind: backend.EventTool, Tool: name, ToolID: id},
			{Kind: backend.EventTool, Tool: name, ToolID: id, Input: input},
			{Kind: backend.EventToolResult, ToolID: id, Result: result, IsError: isErr},
		}
	}
	return func(string) []backend.Event {
		var evs []backend.Event
		evs = append(evs, call("t1", "search", `{"query":"port 8080"}`, `{"query":"port 8080","items":[{"handle":"i1","title":"node.exe","plugin":"builtin.port"}]}`, false)...)
		evs = append(evs, call("t2", "list_plugins", `{}`, `{"plugins":[{"id":"a"},{"id":"b"}],"more":10}`, false)...)
		evs = append(evs, call("t3", "run_command", `{"plugin":"builtin.port","command":"ports"}`, `{"status":"done","title":"Ports","view":{"title":"Ports","items":[{}],"more":0}}`, false)...)
		evs = append(evs, call("t4", "get_detail", `{"handle":"i1"}`, `{"title":"node.exe","markdown":"x"}`, false)...)
		switch action {
		case "done":
			evs = append(evs, call("t5", "run_action", `{"handle":"i9","action":"install"}`, `{"status":"done","action":"Install","item":"7-Zip"}`, false)...)
		case "declined":
			evs = append(evs, call("t5", "run_action", `{"handle":"i9"}`, `{"status":"declined","error":"The user declined.","action":"Install","item":"7-Zip"}`, true)...)
		case "error":
			evs = append(evs, call("t5", "run_action", `{"handle":"i9"}`, `{"status":"error","error":"unknown handle i9"}`, true)...)
		case "refused":
			evs = append(evs, call("t5", "run_action", `{"handle":"i9"}`, `{"status":"refused","error":"AI may not use this plugin"}`, true)...)
		case "garbage":
			evs = append(evs, call("t5", "run_action", `not json`, `not json either`, false)...)
		}
		// A web search without an ID.
		evs = append(evs, backend.Event{Kind: backend.EventTool, Tool: "WebSearch"})
		return evs
	}
}

func TestToolStepsInTranscript(t *testing.T) {
	fake := &backend.Fake{Steps: launcherSteps("done")}
	p, h := newTestPlugin(t, fake, true)
	v := openChat(t, p, "v1", chatPayload{Text: "who uses port 8080?"})
	want := []string{
		"_🔧 Search “port 8080” → 1 result_",
		"_🔧 List plugins → 12 plugins_",
		"_🔧 Open Ports → 1 item_",
		"_🔧 Read “node.exe”_",
		"_✅ Install “7-Zip”_",
		"_Searched the web_",
		"You asked: who uses port 8080?",
	}
	md := v.Detail.Markdown
	last := -1
	for _, w := range want {
		i := strings.Index(md, w)
		if i < 0 || i < last {
			t.Fatalf("%q missing or out of order in:\n%s", w, md)
		}
		last = i
	}
	if opens := fake.Opens(); len(opens) != 1 || !opens[0].LauncherTools || !strings.Contains(opens[0].SystemPrompt, "run_action") ||
		strings.Contains(opens[0].SystemPrompt, "You cannot see or change") {
		t.Fatalf("opens = %+v", opens)
	}

	// The steps are stored and shown again from the store.
	_, msgs, err := p.st.Get("id-1")
	if err != nil || len(msgs) != 2 {
		t.Fatalf("stored %+v, %v", msgs, err)
	}
	steps := msgs[1].Steps
	if len(steps) != 6 || steps[0] != (ToolStep{ID: "t1", Name: "mcp__golauncher__search", Input: `{"query":"port 8080"}`,
		Result: `{"query":"port 8080","items":[{"handle":"i1","title":"node.exe","plugin":"builtin.port"}]}`, Done: true}) ||
		steps[5] != (ToolStep{Name: "WebSearch"}) || len(msgs[1].Tools) != 0 {
		t.Fatalf("steps = %+v", steps)
	}
	p2 := NewWithOptions(Options{Backend: fake, Sync: true, HasProtocol: func(string) bool { return false }})
	if err := p2.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	if v := openChat(t, p2, "v9", chatPayload{Session: "id-1"}); v.Detail.Markdown != md {
		t.Fatalf("reopened:\n%s\nwant:\n%s", v.Detail.Markdown, md)
	}

	// In Chinese.
	h.I18N.SetLocale("zh-CN")
	if v := openChat(t, p2, "v9", chatPayload{}); !strings.Contains(v.Detail.Markdown, "_🔧 搜索“port 8080” → 1 个结果_") ||
		!strings.Contains(v.Detail.Markdown, "_✅ Install“7-Zip”_") {
		t.Fatalf("zh-CN:\n%s", v.Detail.Markdown)
	}
}

func TestToolStepFailures(t *testing.T) {
	for action, want := range map[string]string{
		"declined": "_⛔ Declined: Install “7-Zip”_",
		"error":    "_❌ run\\_action: unknown handle i9_",
		"refused":  "_⛔ AI may not use this plugin_",
		"garbage":  "_✅ Done_",
	} {
		p, _ := newTestPlugin(t, &backend.Fake{Steps: launcherSteps(action)}, true)
		if v := openChat(t, p, "v1", chatPayload{Text: "q"}); !strings.Contains(v.Detail.Markdown, want) {
			t.Errorf("%s: no %q in:\n%s", action, want, v.Detail.Markdown)
		}
	}
}

func TestToolStepsWhileAnswering(t *testing.T) {
	p, _ := newTestPlugin(t, &backend.Fake{}, true)
	ch := &chat{sess: Session{ID: "x"}, msgs: []Message{{Role: RoleUser, Text: "q"}}, busy: true}
	for _, ev := range []backend.Event{
		{Kind: backend.EventTool, Tool: "mcp__golauncher__search", ToolID: "a"},
		{Kind: backend.EventTool, Tool: "mcp__golauncher__run_action", ToolID: "b", Input: `{"handle":"i1"}`},
		{Kind: backend.EventToolResult, ToolID: "unknown", Result: "x"},
		{Kind: backend.EventTool, Tool: "mcp__golauncher__list_plugins"},
	} {
		ch.addStep(ev)
	}
	md := p.transcript(ch)
	for _, w := range []string{"_🔧 Search…_", "_⏳ Waiting for the action…_", "_🔧 List plugins…_"} {
		if !strings.Contains(md, w) {
			t.Errorf("no %q in:\n%s", w, md)
		}
	}
	if len(ch.steps) != 3 {
		t.Errorf("steps %+v", ch.steps)
	}
	ch.addStep(backend.Event{Kind: backend.EventTool, ToolID: "a", Input: `{"query":"a_b"}`})
	if md := p.transcript(ch); !strings.Contains(md, `_🔧 Search “a\_b”…_`) {
		t.Errorf("updated input:\n%s", md)
	}
	// Old records: tool names only.
	if md := p.transcript(&chat{msgs: []Message{{Role: RoleUser, Text: "q"}, {Role: RoleAssistant, Text: "a", Tools: []string{"WebFetch", "Other"}}}}); !strings.Contains(md, "_Read a web page_") ||
		!strings.Contains(md, "_🔧 Other_") {
		t.Errorf("old record:\n%s", md)
	}
}

func TestCapResult(t *testing.T) {
	var items []string
	for i := range 500 {
		items = append(items, `{"handle":"i`+strings.Repeat("x", 20)+`","title":"item `+string(rune('a'+i%26))+`"}`)
	}
	long := `{"query":"q","items":[` + strings.Join(items, ",") + `],"more":5}`
	got := capResult(long)
	if len(got) > maxStepResult || !strings.Contains(got, `"more":505`) || !strings.Contains(got, `"query":"q"`) {
		t.Fatalf("capResult = %.200s", got)
	}
	if got := capResult(strings.Repeat("é", maxStepResult)); len(got) > maxStepResult || !utf8.ValidString(got) {
		t.Fatalf("capResult of text: %d bytes", len(got))
	}
}

func TestLauncherToolsPreference(t *testing.T) {
	fake := &backend.Fake{}
	p, h := newTestPlugin(t, fake, true)
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true"), prefTools: jsontext.Value("false")})
	openChat(t, p, "v1", chatPayload{Text: "q"})
	opens := fake.Opens()
	if len(opens) != 1 || opens[0].LauncherTools || !strings.Contains(opens[0].SystemPrompt, "You cannot see or change the user's computer or files.") ||
		strings.Contains(opens[0].SystemPrompt, "run_action") {
		t.Fatalf("opens = %+v", opens)
	}
	if s := systemPrompt("en", false, true); !strings.Contains(s, "list_plugins, search, run_command, get_detail, run_action") ||
		!strings.Contains(s, "do not retry") || strings.Contains(s, "You cannot see") {
		t.Fatalf("system prompt with tools: %s", s)
	}
}

func TestNewSessionIDIsUUID(t *testing.T) {
	id := newSessionID()
	if len(id) != 36 || id[14] != '4' || strings.Count(id, "-") != 4 || id == newSessionID() {
		t.Fatalf("id = %q", id)
	}
}

func TestBusy(t *testing.T) {
	p, _ := newTestPlugin(t, &backend.Fake{}, true)
	if p.Busy() {
		t.Fatal("busy without chats")
	}
	p.mu.Lock()
	p.chats["x"] = &chat{sess: Session{ID: "x"}, busy: true}
	p.mu.Unlock()
	if !p.Busy() {
		t.Fatal("not busy while an answer is written")
	}
}
