package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// testPlugin offers a few root items, a view command and a no-view command.
type testPlugin struct {
	handled []plugin.ActionDo
}

func (p *testPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: "test", Name: "Test", Version: "1",
		Commands: []plugin.Command{
			{Name: "list", Title: "Browse Fruits", Mode: plugin.ModeView, Keywords: []string{"fruit"}, DefaultHotkey: "Ctrl+Shift+F"},
			{Name: "ping", Title: "Ping Server", Mode: plugin.ModeNoView},
			{Name: "nuke", Title: "Ping Server Reset", Mode: plugin.ModeNoView, Destructive: true},
		},
	}
}

func (p *testPlugin) Init(context.Context, plugin.Host) error { return nil }
func (p *testPlugin) Close() error                            { return nil }

func (p *testPlugin) RootItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	items := []plugin.Item{
		item("vscode", "Visual Studio Code"),
		item("term", "Windows Terminal"),
		item("wechat", "微信"),
	}
	if q.Text == "1+1" {
		items = append(items, plugin.Item{ID: "calc", Title: "2", Score: 1, Group: "Calculator",
			Actions: []plugin.Action{{ID: "copy", Title: "Copy", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: "2"}}}})
	}
	return items, nil
}

func item(id, title string) plugin.Item {
	return plugin.Item{ID: id, Title: title, Group: "Applications", Actions: []plugin.Action{
		{ID: "open", Title: "Open", Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: `C:\` + id + ".exe"}},
		{ID: "copy-path", Title: "Copy Path", Shortcut: "Ctrl+Shift+C", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: `C:\` + id + ".exe"}},
	}}
}

func (p *testPlugin) View(_ context.Context, req plugin.ViewRequest) (plugin.View, error) {
	var items []plugin.Item
	for _, f := range []string{"Apple", "Banana", "Cherry"} {
		if req.Query.Text == "" || strings.Contains(strings.ToLower(f), strings.ToLower(req.Query.Text)) {
			items = append(items, plugin.Item{ID: f, Title: f, Actions: []plugin.Action{
				{ID: "paste", Title: "Paste", Do: plugin.ActionDo{Kind: plugin.ActionPaste, Target: f}},
				{ID: "eat", Title: "Eat", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "eat"}},
			}})
		}
	}
	return plugin.View{Kind: plugin.ViewList, Title: "Fruits", Sections: []plugin.Section{{Items: items}}}, nil
}

func (p *testPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	p.handled = append(p.handled, a)
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: "done"}, PopToRoot: a.Target == "eat"}, nil
}

type env struct {
	*app.Env
	rec *app.Recorder
	tp  *testPlugin
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	clip := &hostfake.Clipboard{}
	e := &env{rec: &app.Recorder{Clipboard: clip}, tp: &testPlugin{}, now: hostfake.Fixed}
	var err error
	e.Env, err = app.Build(t.Context(), app.Options{
		Plugins:   []plugin.Plugin{e.tp},
		Fake:      true,
		Now:       func() time.Time { return e.now },
		Locale:    "en",
		System:    e.rec,
		Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func (e *env) typeText(t *testing.T, s string) core.Snapshot {
	t.Helper()
	e.Controller.SetText(t.Context(), s)
	e.Controller.Wait()
	return e.Controller.Snapshot()
}

func (e *env) key(t *testing.T, k string) core.Outcome {
	t.Helper()
	out, err := e.Controller.Key(t.Context(), k)
	if err != nil {
		t.Fatalf("Key(%s): %v", k, err)
	}
	e.Controller.Wait()
	return out
}

func titles(s core.Snapshot) []string {
	var out []string
	for _, sec := range s.Sections {
		for _, it := range sec.Items {
			out = append(out, it.Title)
		}
	}
	return out
}

func TestRootEmptyShowsCommands(t *testing.T) {
	e := newEnv(t)
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	s := e.Controller.Snapshot()
	if len(s.Sections) != 1 || s.Sections[0].Title != "Commands" {
		t.Fatalf("sections = %+v", s.Sections)
	}
	if got := titles(s); strings.Join(got, ",") != "Browse Fruits,Ping Server,Ping Server Reset" {
		t.Fatalf("titles = %v", got)
	}
}

func TestRootSearchRanking(t *testing.T) {
	e := newEnv(t)
	s := e.typeText(t, "vsc")
	if got := titles(s); len(got) == 0 || got[0] != "Visual Studio Code" {
		t.Fatalf("vsc -> %v", got)
	}
	s = e.typeText(t, "wx")
	if got := titles(s); len(got) == 0 || got[0] != "微信" {
		t.Fatalf("wx -> %v", got)
	}
	s = e.typeText(t, "fruit")
	if got := titles(s); len(got) == 0 || got[0] != "Browse Fruits" {
		t.Fatalf("fruit keyword -> %v", got)
	}
	s = e.typeText(t, "1+1")
	if s.Sections[0].Title != "Calculator" {
		t.Fatalf("pre-scored item not first: %+v", s.Sections)
	}
}

func TestFrecencyPromotes(t *testing.T) {
	e := newEnv(t)
	// "w" is a prefix of both "Windows Terminal" and 微信 ("wei xin"); the
	// shorter title wins until Terminal has been used.
	s := e.typeText(t, "w")
	if got := titles(s); len(got) < 2 || got[0] != "微信" {
		t.Fatalf("initial order = %v", got)
	}
	for range 5 {
		e.Controller.Reset(t.Context())
		e.typeText(t, "w")
		if _, err := e.Controller.RunAction(t.Context(), "test/term", ""); err != nil {
			t.Fatal(err)
		}
	}
	e.Controller.Reset(t.Context())
	s = e.typeText(t, "w")
	if got := titles(s); got[0] != "Windows Terminal" {
		t.Fatalf("after using Windows Terminal 5 times: %v", got)
	}
	// It also shows up as recent on the empty root.
	s = e.typeText(t, "")
	if s.Sections[0].Title != "Recent" || s.Sections[0].Items[0].Title != "Windows Terminal" {
		t.Fatalf("recent = %+v", s.Sections[0])
	}
	// Frecency decays: a month later the match quality wins again.
	e.now = e.now.Add(60 * 24 * time.Hour)
	e.typeText(t, "")
	s = e.typeText(t, "w")
	if got := titles(s); got[0] != "微信" {
		t.Fatalf("after a month: %v", got)
	}
}

func TestEnterOpensAndHides(t *testing.T) {
	e := newEnv(t)
	e.typeText(t, "terminal")
	out := e.key(t, "Enter")
	if !out.Hide {
		t.Fatal("open should hide the window")
	}
	effects := e.rec.Effects()
	if len(effects) != 1 || effects[0].Kind != plugin.ActionOpen || effects[0].Target != `C:\term.exe` {
		t.Fatalf("effects = %+v", effects)
	}
}

func TestShortcutAndActionPanel(t *testing.T) {
	e := newEnv(t)
	e.typeText(t, "terminal")
	e.key(t, "ctrl+shift+c")
	if got, _ := e.rec.Effects()[0], 0; got.Kind != plugin.ActionCopyText {
		t.Fatalf("shortcut ran %+v", got)
	}
	e.rec.Reset()

	e.key(t, "Ctrl+K")
	s := e.Controller.Snapshot()
	if s.Panel == nil || len(s.Panel.Actions) != 2 {
		t.Fatalf("panel = %+v", s.Panel)
	}
	e.Controller.SetText(t.Context(), "copy")
	s = e.Controller.Snapshot()
	if len(s.Panel.Actions) != 1 || s.Panel.Actions[0].ID != "copy-path" {
		t.Fatalf("filtered panel = %+v", s.Panel.Actions)
	}
	e.key(t, "Enter")
	if s := e.Controller.Snapshot(); s.Panel != nil {
		t.Fatal("panel should close after running an action")
	}
	if eff := e.rec.Effects(); len(eff) != 1 || eff[0].Kind != plugin.ActionCopyText {
		t.Fatalf("effects = %+v", eff)
	}
	if s := e.Controller.Snapshot(); s.Toast == nil || s.Toast.Title != "Copied to clipboard" {
		t.Fatalf("toast = %+v", s.Toast)
	}
}

func TestPushViewSearchAndBack(t *testing.T) {
	e := newEnv(t)
	e.typeText(t, "browse fruits")
	e.key(t, "Enter")
	s := e.Controller.Snapshot()
	if s.Depth != 1 || s.Title != "Fruits" || len(titles(s)) != 3 {
		t.Fatalf("pushed view = %+v", s)
	}
	s = e.typeText(t, "an")
	if got := titles(s); len(got) != 1 || got[0] != "Banana" {
		t.Fatalf("view search = %v", got)
	}
	e.key(t, "Enter")
	if eff := e.rec.Effects(); len(eff) != 1 || eff[0].Kind != plugin.ActionPaste || eff[0].Target != "Banana" {
		t.Fatalf("effects = %+v", eff)
	}
	// Escape clears the text, then goes back, then hides.
	e.key(t, "Escape")
	if s := e.Controller.Snapshot(); s.Depth != 1 || s.Text != "" {
		t.Fatalf("after first Escape: depth %d text %q", s.Depth, s.Text)
	}
	e.key(t, "Escape")
	if s := e.Controller.Snapshot(); s.Depth != 0 {
		t.Fatalf("after second Escape: depth %d", s.Depth)
	}
	e.typeText(t, "")
	if out := e.key(t, "Escape"); !out.Hide {
		t.Fatal("Escape on empty root should hide")
	}
}

func TestCallbackPopToRoot(t *testing.T) {
	e := newEnv(t)
	if _, err := e.Controller.OpenCommand(t.Context(), "test", "list"); err != nil {
		t.Fatal(err)
	}
	e.Controller.Wait()
	e.key(t, "Ctrl+Enter") // "Eat" -> callback -> PopToRoot
	if s := e.Controller.Snapshot(); s.Depth != 0 {
		t.Fatalf("depth = %d", s.Depth)
	}
	if len(e.tp.handled) != 1 || e.tp.handled[0].Target != "eat" {
		t.Fatalf("handled = %+v", e.tp.handled)
	}
}

func TestNoViewCommandAndAliasAndHotkeys(t *testing.T) {
	e := newEnv(t)
	e.Config.Update(func(c *config.Config) {
		c.Plugins = map[string]config.PluginConfig{"test": {Commands: map[string]config.CommandConfig{
			"ping": {Alias: "pp", Hotkey: "Alt+P"},
			"list": {Hotkey: config.HotkeyNone},
		}}}
	})
	s := e.typeText(t, "pp")
	if got := titles(s); len(got) == 0 || got[0] != "Ping Server" {
		t.Fatalf("alias -> %v", got)
	}
	e.key(t, "Enter")
	if len(e.tp.handled) != 1 || e.tp.handled[0].Kind != plugin.ActionRunCommand {
		t.Fatalf("handled = %+v", e.tp.handled)
	}
	hk := e.Engine.CommandHotkeys()
	if len(hk) != 1 || hk[0].Command != "ping" || hk[0].Hotkey != "Alt+P" {
		t.Fatalf("hotkeys = %+v", hk)
	}
}

func TestDestructiveNeedsConfirmation(t *testing.T) {
	e := newEnv(t)
	s := e.typeText(t, "ping server")
	if got := titles(s); len(got) != 2 || got[0] != "Ping Server" {
		t.Fatalf("destructive command should rank last: %v", got)
	}
	e.Controller.Select(1)
	e.key(t, "Enter")
	s = e.Controller.Snapshot()
	if s.Confirm == nil || s.Confirm.Title != "Ping Server Reset?" || len(e.tp.handled) != 0 {
		t.Fatalf("confirm = %+v, handled = %v", s.Confirm, e.tp.handled)
	}
	e.key(t, "Down") // ignored while confirming
	e.key(t, "Escape")
	if s := e.Controller.Snapshot(); s.Confirm != nil || len(e.tp.handled) != 0 {
		t.Fatal("Escape should cancel without running")
	}
	e.key(t, "Enter")
	e.key(t, "Enter")
	if len(e.tp.handled) != 1 || e.tp.handled[0].Target != "nuke" {
		t.Fatalf("handled = %+v", e.tp.handled)
	}
}

func TestDisabledPluginHidden(t *testing.T) {
	e := newEnv(t)
	e.Config.Update(func(c *config.Config) {
		c.Plugins = map[string]config.PluginConfig{"test": {Disabled: true}}
	})
	if s := e.typeText(t, "terminal"); len(s.Sections) != 0 {
		t.Fatalf("disabled plugin still returns %v", titles(s))
	}
}
