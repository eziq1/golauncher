package agent_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/agent"
	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/builtin"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

const fixtures = "../../testdata/fixtures"

func TestMain(m *testing.M) {
	agent.SetSettle(20 * time.Millisecond)
	os.Exit(m.Run())
}

// world is a fake launcher with an agent whose clock and confirmations the
// test controls.
type world struct {
	t   *testing.T
	env *app.Env
	rec *app.Recorder
	clk *clock
	ag  *agent.Agent

	mu       sync.Mutex
	asked    []core.ExternalRequest
	decision core.Decision
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newWorld(t *testing.T, cfg func(*config.Config)) *world {
	t.Helper()
	clip := &hostfake.Clipboard{}
	rec := &app.Recorder{Clipboard: clip}
	env, err := app.Build(t.Context(), app.Options{Fake: true, Locale: "en", System: rec, Clipboard: clip,
		Plugins: builtin.Fake(fixtures), Now: func() time.Time { return hostfake.Fixed }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	if cfg != nil {
		if err := env.Config.Update(cfg); err != nil {
			t.Fatal(err)
		}
	}
	w := &world{t: t, env: env, rec: rec, clk: &clock{now: hostfake.Fixed}}
	log, err := agent.OpenActivity(filepath.Join(t.TempDir(), "activity.jsonl"), w.clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	w.ag = agent.New(agent.Options{Engine: env.Engine, Config: env.Config, Activity: log, Token: "tok", Now: w.clk.Now,
		Confirm: func(_ context.Context, req core.ExternalRequest) core.Decision {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.asked = append(w.asked, req)
			return w.decision
		}})
	return w
}

func (w *world) decide(d core.Decision) {
	w.mu.Lock()
	w.decision = d
	w.mu.Unlock()
}

// takeAsked returns and forgets the confirmations asked for.
func (w *world) takeAsked() []core.ExternalRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	a := w.asked
	w.asked = nil
	return a
}

// chat is the session of the launcher's own chat (allowed without the
// external switch).
func (w *world) chat() *agent.Session {
	return w.ag.Session(agent.Client{Chat: true, ChatSession: "c1"})
}

// result is a decoded tool result.
type result map[string]any

func (w *world) call(s *agent.Session, tool string, args any) (result, bool) {
	w.t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		w.t.Fatal(err)
	}
	res, err := s.Call(w.t.Context(), tool, jsontext.Value(b))
	if err != nil {
		w.t.Fatalf("%s: %v", tool, err)
	}
	var out result
	if err := json.Unmarshal([]byte(res.Text), &out); err != nil {
		w.t.Fatalf("%s: result is not JSON: %s", tool, res.Text)
	}
	return out, res.IsError
}

func (w *world) mustCall(s *agent.Session, tool string, args any) result {
	w.t.Helper()
	out, isErr := w.call(s, tool, args)
	if isErr {
		w.t.Fatalf("%s(%v) failed: %v", tool, args, out)
	}
	return out
}

func items(r result) []map[string]any {
	var out []map[string]any
	list, _ := r["items"].([]any)
	if v, ok := r["view"].(map[string]any); ok && list == nil {
		list, _ = v["items"].([]any)
	}
	for _, x := range list {
		out = append(out, x.(map[string]any))
	}
	return out
}

// find returns the handle of the item titled title.
func find(t *testing.T, r result, title string) map[string]any {
	t.Helper()
	for _, it := range items(r) {
		if it["title"] == title {
			return it
		}
	}
	t.Fatalf("no item %q in %v", title, r)
	return nil
}

func TestListPluginsExposure(t *testing.T) {
	w := newWorld(t, nil)
	s := w.chat()
	r := w.mustCall(s, agent.ToolListPlugins, map[string]any{})
	var ids []string
	for _, p := range r["plugins"].([]any) {
		ids = append(ids, p.(map[string]any)["id"].(string))
	}
	for _, want := range []string{"builtin.apps", "builtin.winget", "builtin.network", "builtin.tasks"} {
		if !slices.Contains(ids, want) {
			t.Errorf("%s not listed: %q", want, ids)
		}
	}
	for _, hidden := range []string{"builtin.clipboard", "builtin.ai", agent.PluginID} {
		if slices.Contains(ids, hidden) {
			t.Errorf("%s listed: %q", hidden, ids)
		}
	}
	// The clipboard history when the user allows it; a plugin turned off is gone.
	w.env.Config.Update(func(c *config.Config) {
		c.Agent.Plugins = map[string]bool{"builtin.clipboard": true, "builtin.apps": false}
	})
	r = w.mustCall(s, agent.ToolListPlugins, map[string]any{"plugin": "builtin.clipboard"})
	if n := len(r["plugins"].([]any)); n != 1 {
		t.Errorf("clipboard: %v", r)
	}
	if r, isErr := w.call(s, agent.ToolListPlugins, map[string]any{"plugin": "builtin.apps"}); !isErr || r["status"] != "refused" {
		t.Errorf("apps off: %v", r)
	}
	if r, isErr := w.call(s, agent.ToolSearch, map[string]any{"query": "code", "plugin": "builtin.apps"}); !isErr || r["status"] != "refused" {
		t.Errorf("search in apps off: %v", r)
	}
}

func TestExposedDefaults(t *testing.T) {
	cases := []struct {
		p    core.PluginInfo
		cfg  config.AgentConfig
		want bool
	}{
		{core.PluginInfo{ID: "builtin.apps", Origin: core.OriginBuiltin, Enabled: true}, config.AgentConfig{}, true},
		{core.PluginInfo{ID: "builtin.clipboard", Origin: core.OriginBuiltin, Enabled: true}, config.AgentConfig{}, false},
		{core.PluginInfo{ID: "acme.tool", Origin: core.OriginExternal, Enabled: true}, config.AgentConfig{}, false},
		{core.PluginInfo{ID: "acme.tool", Origin: core.OriginExternal, Enabled: true}, config.AgentConfig{Plugins: map[string]bool{"acme.tool": true}}, true},
		{core.PluginInfo{ID: "builtin.apps", Origin: core.OriginBuiltin}, config.AgentConfig{}, false}, // disabled
		{core.PluginInfo{ID: "builtin.ai", Origin: core.OriginBuiltin, Enabled: true}, config.AgentConfig{Plugins: map[string]bool{"builtin.ai": true}}, false},
	}
	for _, c := range cases {
		if got := agent.Exposed(c.p, c.cfg); got != c.want {
			t.Errorf("Exposed(%s, %v) = %v", c.p.ID, c.cfg.Plugins, got)
		}
	}
}

func TestRiskOf(t *testing.T) {
	cases := []struct {
		a    plugin.Action
		want agent.Risk
	}{
		{plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionPush}}, agent.RiskRead},
		{plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: "x"}}, agent.RiskOpen},
		{plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionOpen, Payload: plugin.MustPayload(plugin.OpenOptions{Verb: "runas"})}}, agent.RiskChange},
		{plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionCopyText}}, agent.RiskOpen},
		{plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionPaste}}, agent.RiskChange},
		{plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionCallback}}, agent.RiskChange},
		{plugin.Action{ReadOnly: true, Do: plugin.ActionDo{Kind: plugin.ActionCallback}}, agent.RiskRead},
		{plugin.Action{ReadOnly: true, Style: plugin.StyleDestructive, Do: plugin.ActionDo{Kind: plugin.ActionCallback}}, agent.RiskDestructive},
		{plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionRunCommand}}, agent.RiskChange},
		{plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionRunProcess}}, agent.RiskChange},
	}
	for _, c := range cases {
		if got := agent.RiskOf(c.a); got != c.want {
			t.Errorf("RiskOf(%+v) = %s, want %s", c.a, got, c.want)
		}
	}
}

func TestSearchAndDetail(t *testing.T) {
	w := newWorld(t, nil)
	s := w.chat()
	r := w.mustCall(s, agent.ToolSearch, map[string]any{"query": "code", "limit": 2})
	if n := len(items(r)); n != 2 || r["more"] == nil {
		t.Fatalf("limit: %v", r)
	}
	vsc := find(t, r, "Visual Studio Code")
	if vsc["plugin"] != "builtin.apps" || !strings.HasPrefix(vsc["handle"].(string), "i") {
		t.Errorf("item %v", vsc)
	}
	acts := vsc["actions"].([]any)
	if a := acts[0].(map[string]any); a["id"] != "open" || a["risk"] != "open" {
		t.Errorf("actions %v", acts)
	}
	for _, k := range []string{"icon", "shortcut", "keywords"} {
		if strings.Contains(marshal(t, r), `"`+k+`"`) {
			t.Errorf("%s in the result: %v", k, r)
		}
	}
	// Plugin filter.
	r = w.mustCall(s, agent.ToolSearch, map[string]any{"query": "code", "plugin": "builtin.system"})
	for _, it := range items(r) {
		if it["plugin"] != "builtin.system" {
			t.Errorf("other plugin: %v", it)
		}
	}
	if r, isErr := w.call(s, agent.ToolSearch, map[string]any{}); !isErr || r["status"] != "error" {
		t.Errorf("no query: %v", r)
	}
	// Details of a winget package.
	r = w.mustCall(s, agent.ToolRunCommand, map[string]any{"plugin": "builtin.winget", "command": "search", "query": "textpad"})
	if r["status"] != "done" || r["title"] != "Search WinGet Packages" {
		t.Fatalf("run_command: %v", r)
	}
	it := items(r)
	if len(it) == 0 {
		t.Fatalf("no packages: %v", r)
	}
	d := w.mustCall(s, agent.ToolGetDetail, map[string]any{"handle": it[0]["handle"]})
	if d["title"] != it[0]["title"] || d["text"] == nil && d["metadata"] == nil && d["note"] == nil {
		t.Errorf("detail %v", d)
	}
	// A view handle searches in the view.
	view := r["view"].(map[string]any)["handle"].(string)
	r = w.mustCall(s, agent.ToolSearch, map[string]any{"view": view, "query": "tailspin"})
	find(t, r, "Tailspin Toolbox")
	if r, isErr := w.call(s, agent.ToolGetDetail, map[string]any{"handle": view}); !isErr {
		t.Errorf("detail of a view: %v", r)
	}
	if len(w.takeAsked()) != 0 || len(w.rec.Effects()) != 0 {
		t.Errorf("reading asked or did something: %v", w.rec.Effects())
	}
}

func TestHandles(t *testing.T) {
	w := newWorld(t, nil)
	s := w.chat()
	r := w.mustCall(s, agent.ToolSearch, map[string]any{"query": "Visual Studio Code"})
	h := find(t, r, "Visual Studio Code")["handle"]
	// Made-up and foreign handles do not work.
	if r, isErr := w.call(s, agent.ToolRunAction, map[string]any{"handle": "i999"}); !isErr || !strings.Contains(r["error"].(string), "unknown handle") {
		t.Errorf("made up: %v", r)
	}
	other := w.ag.Session(agent.Client{Chat: true, ChatSession: "c2"})
	if _, isErr := w.call(other, agent.ToolRunAction, map[string]any{"handle": h}); !isErr {
		t.Error("a handle of another session worked")
	}
	// The chat's session survives a reconnection (a new bridge).
	again := w.ag.Session(agent.Client{Chat: true, ChatSession: "c1"})
	if _, isErr := w.call(again, agent.ToolGetDetail, map[string]any{"handle": h}); isErr {
		t.Error("the chat lost its handles")
	}
	w.clk.Add(agent.HandleTTL + time.Second)
	if r, isErr := w.call(s, agent.ToolRunAction, map[string]any{"handle": h}); !isErr || !strings.Contains(r["error"].(string), "expired") {
		t.Errorf("expired: %v", r)
	}
	if len(w.rec.Effects()) != 0 {
		t.Errorf("effects %v", w.rec.Effects())
	}
	if r, isErr := w.call(s, agent.ToolRunAction, map[string]any{"handle": h, "bogus": 1}); !isErr || !strings.Contains(r["error"].(string), "invalid arguments") {
		t.Errorf("unknown argument: %v", r)
	}
}

func TestPolicy(t *testing.T) {
	w := newWorld(t, nil)
	s := w.chat()
	vsc := func() any {
		r := w.mustCall(s, agent.ToolSearch, map[string]any{"query": "Visual Studio Code"})
		return find(t, r, "Visual Studio Code")["handle"]
	}

	// "Open" runs directly by default.
	r := w.mustCall(s, agent.ToolRunAction, map[string]any{"handle": vsc()})
	if r["status"] != "done" || r["action"] != "Open" || r["item"] != "Visual Studio Code" {
		t.Errorf("open: %v", r)
	}
	if e := w.rec.Effects(); len(e) != 1 || e[0].Kind != plugin.ActionOpen || len(w.takeAsked()) != 0 {
		t.Fatalf("open effects %v", e)
	}
	w.rec.Reset()

	// "Change" asks: declined, nothing happens.
	r, isErr := w.call(s, agent.ToolRunAction, map[string]any{"handle": vsc(), "action": "runas"})
	if !isErr || r["status"] != "declined" || !strings.Contains(r["error"].(string), "declined") {
		t.Errorf("declined: %v", r)
	}
	asked := w.takeAsked()
	if len(asked) != 1 || asked[0].Client != "Claude (AI Chat)" || asked[0].Action != "Run as Administrator" || asked[0].Item != "Visual Studio Code" ||
		asked[0].Plugin != "Applications" || !asked[0].AllowSession || asked[0].Destructive || !asked[0].InPlace {
		t.Errorf("asked %+v", asked)
	}
	if len(w.rec.Effects()) != 0 {
		t.Fatalf("declined but did %v", w.rec.Effects())
	}

	// Allowed for the session: asked once, then not again.
	w.decide(core.DecisionAllowSession)
	w.mustCall(s, agent.ToolRunAction, map[string]any{"handle": vsc(), "action": "runas"})
	w.mustCall(s, agent.ToolRunAction, map[string]any{"handle": vsc(), "action": "runas"})
	if n := len(w.takeAsked()); n != 1 {
		t.Errorf("asked %d times", n)
	}
	if n := len(w.rec.Effects()); n != 2 {
		t.Errorf("effects %v", w.rec.Effects())
	}
	// … for this session only.
	w.decide(core.DecisionAllow)
	other := w.ag.Session(agent.Client{Chat: true, ChatSession: "c2"})
	r = w.mustCall(other, agent.ToolSearch, map[string]any{"query": "Visual Studio Code"})
	w.mustCall(other, agent.ToolRunAction, map[string]any{"handle": find(t, r, "Visual Studio Code")["handle"], "action": "runas"})
	if n := len(w.takeAsked()); n != 1 {
		t.Errorf("other session asked %d times", n)
	}

	// Destructive always asks, without "for this session", even when
	// allowed for the session.
	w.decide(core.DecisionAllowSession)
	for range 2 {
		r := w.mustCall(s, agent.ToolSearch, map[string]any{"query": "Developer PowerShell"})
		h := find(t, r, "Developer PowerShell for VS 2022")["handle"]
		w.mustCall(s, agent.ToolRunAction, map[string]any{"handle": h, "action": "admin"})
	}
	asked = w.takeAsked()
	if len(asked) != 2 || !asked[0].Destructive || asked[0].AllowSession {
		t.Errorf("destructive asked %+v", asked)
	}

	// "Open" asks when the user wants it to.
	w.env.Config.Update(func(c *config.Config) { c.Agent.AskOpen = true })
	w.decide(core.DecisionDeny)
	if r, isErr := w.call(s, agent.ToolRunAction, map[string]any{"handle": vsc()}); !isErr || r["status"] != "declined" {
		t.Errorf("open with ask: %v", r)
	}
	if len(w.takeAsked()) != 1 {
		t.Error("open did not ask")
	}
}

func TestRunCommand(t *testing.T) {
	w := newWorld(t, nil)
	s := w.chat()
	// A no-view command is a change: it asks.
	if r, isErr := w.call(s, agent.ToolRunCommand, map[string]any{"plugin": "builtin.apps", "command": "reload"}); !isErr || r["status"] != "declined" {
		t.Errorf("reload: %v", r)
	}
	if a := w.takeAsked(); len(a) != 1 || a[0].Action != "Reload Applications" || a[0].Item != "" {
		t.Errorf("asked %+v", a)
	}
	// A destructive command.
	w.call(s, agent.ToolRunCommand, map[string]any{"plugin": "builtin.winget", "command": "upgrade-all"})
	if a := w.takeAsked(); len(a) != 1 || !a[0].Destructive {
		t.Errorf("asked %+v", a)
	}
	// Arguments.
	if r, isErr := w.call(s, agent.ToolRunCommand, map[string]any{"plugin": "builtin.network", "command": "dns"}); !isErr || !strings.Contains(r["error"].(string), "argument") {
		t.Errorf("missing argument: %v", r)
	}
	if r, isErr := w.call(s, agent.ToolRunCommand, map[string]any{"plugin": "builtin.nope", "command": "x"}); !isErr || r["status"] != "refused" {
		t.Errorf("unknown plugin: %v", r)
	}
	if r, isErr := w.call(s, agent.ToolRunCommand, map[string]any{"plugin": "builtin.apps", "command": "nope"}); !isErr {
		t.Errorf("unknown command: %v", r)
	}
	// Installing from the search list runs a background task once allowed.
	w.decide(core.DecisionAllow)
	r := w.mustCall(s, agent.ToolRunCommand, map[string]any{"plugin": "builtin.winget", "command": "search", "query": "tailspin"})
	h := find(t, r, "Tailspin Toolbox")["handle"]
	r = w.mustCall(s, agent.ToolRunAction, map[string]any{"handle": h})
	if r["status"] != "done" || r["item"] != "Tailspin Toolbox" || !strings.Contains(r["message"].(string), "background") {
		t.Errorf("install: %v", r)
	}
	if a := w.takeAsked(); len(a) != 1 || a[0].Plugin != "WinGet" {
		t.Errorf("asked %+v", a)
	}
}

func TestExternalAccess(t *testing.T) {
	w := newWorld(t, nil)
	ext := w.ag.Session(agent.Client{Name: "claude-ai"})
	if r, isErr := w.call(ext, agent.ToolListPlugins, map[string]any{}); !isErr || r["status"] != "refused" || !strings.Contains(r["error"].(string), "Allow AI apps") {
		t.Errorf("external with access off: %v", r)
	}
	w.env.Config.Update(func(c *config.Config) { c.Agent.External = true })
	w.mustCall(ext, agent.ToolListPlugins, map[string]any{})
	r := w.mustCall(ext, agent.ToolSearch, map[string]any{"query": "Visual Studio Code"})
	w.call(ext, agent.ToolRunAction, map[string]any{"handle": find(t, r, "Visual Studio Code")["handle"], "action": "runas"})
	if a := w.takeAsked(); len(a) != 1 || a[0].Client != "Claude Desktop" || a[0].InPlace {
		t.Errorf("asked %+v", a)
	}
	for name, want := range map[string]string{"claude-code": "Claude Code", "claude-ai": "Claude Desktop", "other-app": "other-app", "": "An AI app"} {
		if got := w.ag.DisplayName(agent.Client{Name: name}); got != want {
			t.Errorf("DisplayName(%q) = %q", name, got)
		}
	}
}

func TestSensitiveClips(t *testing.T) {
	w := newWorld(t, func(c *config.Config) {
		c.Agent.Plugins = map[string]bool{"builtin.clipboard": true}
		// The user took the password managers off the ignore list.
		c.Plugins = map[string]config.PluginConfig{"builtin.clipboard": {Preferences: map[string]jsontext.Value{"ignoreApps": jsontext.Value(`[]`)}}}
	})
	clip := w.env.Services.Clipboard.(*hostfake.Clipboard)
	for _, c := range []plugin.ClipboardContent{
		{Text: "hunter2-secret", SourceApp: "KeePassXC.exe"},
		{Text: "never recorded", Exclude: true},
		{Text: "meeting notes", SourceApp: "notepad.exe"},
	} {
		clip.Set(c)
		w.env.Services.Bus.PublishSync(plugin.EventClipboardChanged)
	}
	s := w.chat()
	r := w.mustCall(s, agent.ToolRunCommand, map[string]any{"plugin": "builtin.clipboard", "command": "history"})
	out := marshal(t, r)
	if !strings.Contains(out, "meeting notes") || strings.Contains(out, "hunter2") || strings.Contains(out, "never recorded") {
		t.Errorf("history: %s", out)
	}
}

func TestActivityLog(t *testing.T) {
	w := newWorld(t, nil)
	s := w.chat()
	w.mustCall(s, agent.ToolSearch, map[string]any{"query": "Visual Studio Code"})
	r := w.mustCall(s, agent.ToolSearch, map[string]any{"query": "Visual Studio Code"})
	w.call(s, agent.ToolRunAction, map[string]any{"handle": find(t, r, "Visual Studio Code")["handle"], "action": "runas"})
	w.call(s, agent.ToolGetDetail, map[string]any{"handle": "i999"})
	log := w.ag.Activity()
	e := log.Entries()
	if len(e) != 4 {
		t.Fatalf("entries %+v", e)
	}
	if e[1].Tool != agent.ToolRunAction || e[1].Outcome != agent.OutcomeDeclined || e[1].Client != "Claude (AI Chat)" ||
		e[1].Summary != "Run as Administrator “Visual Studio Code” (Applications)" {
		t.Errorf("run_action entry %+v", e[1])
	}
	if e[0].Outcome != agent.OutcomeError || e[3].Summary != "“Visual Studio Code”" || !e[3].Time.Equal(hostfake.Fixed) {
		t.Errorf("entries %+v", e)
	}
}

func TestActivityRetention(t *testing.T) {
	clk := &clock{now: hostfake.Fixed}
	path := filepath.Join(t.TempDir(), "a", "activity.jsonl")
	log, err := agent.OpenActivity(path, clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	log.Add(agent.Entry{Time: clk.Now(), Tool: "old", Outcome: agent.OutcomeOK})
	clk.Add(20 * 24 * time.Hour)
	log.Add(agent.Entry{Time: clk.Now(), Tool: "new", Outcome: agent.OutcomeOK})
	clk.Add(11 * 24 * time.Hour) // the first is 31 days old
	if e := log.Entries(); len(e) != 1 || e[0].Tool != "new" {
		t.Errorf("after 31 days: %+v", e)
	}
	// Pruned on disk too.
	again, err := agent.OpenActivity(path, clk.Now)
	if err != nil {
		t.Fatal(err)
	}
	if e := again.Entries(); len(e) != 1 || e[0].Tool != "new" {
		t.Errorf("reopened: %+v", e)
	}
	if n, err := again.Clear(); n != 1 || err != nil {
		t.Errorf("Clear = %d, %v", n, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file after Clear: %v", err)
	}
	if e := again.Entries(); len(e) != 0 {
		t.Errorf("after Clear: %+v", e)
	}
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
