package core_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"sync"
	"testing"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// askPlugin exercises the host mechanisms the AI chat is built on: fallback
// items, the Tab action, a full-window detail view whose search field is an
// input (ActionDo.Query, Result.ClearText), View.Escape and
// Result.Preferences.
type askPlugin struct {
	mu      sync.Mutex
	busy    bool // an "answer" is being written: Escape stops it
	sent    []string
	stopped int
	prefErr bool // "enable" sets an undeclared preference
}

func (p *askPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "ask", Name: "Ask", Version: "1",
		Commands: []plugin.Command{{Name: "chat", Title: "Chat Window", Mode: plugin.ModeView}},
		Preferences: []plugin.Preference{
			{Name: "on", Type: plugin.PrefBool, Title: "On"},
			{Name: "secret", Type: plugin.PrefPassword, Title: "Secret"},
		},
	}
}
func (p *askPlugin) Init(context.Context, plugin.Host) error { return nil }
func (p *askPlugin) Close() error                            { return nil }

func (p *askPlugin) RootItems(context.Context, plugin.Query) ([]plugin.Item, error) {
	copyIt := func(s string) []plugin.Action {
		return []plugin.Action{{ID: "copy", Title: "Copy", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: s}}}
	}
	return []plugin.Item{
		{ID: "weather", Title: "Weather Forecast", Actions: copyIt("w")},
		{ID: "argitem", Title: "Argument Item", Actions: copyIt("a"),
			Arguments: []plugin.Argument{{Name: "x", Type: plugin.ArgText, Placeholder: "X"}}},
	}, nil
}

func chatAction(text string) plugin.Action {
	return plugin.Action{ID: "ask", Title: "Ask", Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: "chat", Payload: plugin.MustPayload(text)}}
}

func (p *askPlugin) FallbackItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	return []plugin.Item{{ID: "ask", Title: "Ask: " + q.Text, Actions: []plugin.Action{chatAction(q.Text)}}}, nil
}

func (p *askPlugin) TabAction(q plugin.Query) (plugin.Action, bool) { return chatAction(q.Text), true }

func (p *askPlugin) View(_ context.Context, req plugin.ViewRequest) (plugin.View, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var first string
	_ = json.Unmarshal(req.Payload, &first)
	v := plugin.View{Kind: plugin.ViewDetail, Title: "Chat", Placeholder: "Message",
		Detail: &plugin.Detail{Markdown: "first: " + first + "\n\nsent: " + strings.Join(p.sent, ",")},
		Sections: []plugin.Section{{Items: []plugin.Item{{ID: "chat", Title: "Chat", Actions: []plugin.Action{
			{ID: "send", Title: "Send", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "send"}},
			{ID: "enable", Title: "Enable", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "enable"}},
		}}}}}}
	if p.busy {
		v.Escape = &plugin.Action{ID: "stop", Title: "Stop", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "stop"}}
	}
	return v, nil
}

func (p *askPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch a.Target {
	case "send":
		p.sent = append(p.sent, a.Query)
		p.busy = true
		return plugin.Result{ClearText: true}, nil
	case "stop":
		p.stopped++
		p.busy = false
		return plugin.Result{Refresh: true}, nil
	case "enable":
		name := "on"
		if p.prefErr {
			name = "secret"
		}
		return plugin.Result{Preferences: map[string]jsontext.Value{name: jsontext.Value("true")}, Refresh: true}, nil
	}
	return plugin.Result{}, nil
}

func typeText(t *testing.T, c *core.Controller, text string) core.Snapshot {
	t.Helper()
	c.SetText(t.Context(), text)
	c.Wait()
	return c.Snapshot()
}

func pressKey(t *testing.T, c *core.Controller, key string) core.Outcome {
	t.Helper()
	out, err := c.Key(t.Context(), key)
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	c.Wait()
	return out
}

func TestFallbackItems(t *testing.T) {
	env := buildWith(t, &askPlugin{})
	c := env.Controller
	// A good match: no fallback.
	s := typeText(t, c, "weather")
	for _, sec := range s.Sections {
		if sec.Title == "Fallback Commands" {
			t.Fatalf("fallback shown for a good match: %+v", s.Sections)
		}
	}
	// Nothing matches: the fallback is listed last under its heading.
	s = typeText(t, c, "why is the sky blue")
	last := s.Sections[len(s.Sections)-1]
	if last.Title != "Fallback Commands" || last.Items[0].Title != "Ask: why is the sky blue" || !last.Items[0].Fallback {
		t.Fatalf("sections = %+v", s.Sections)
	}
	// Running it opens the chat and is not recorded as recently used.
	if out := pressKey(t, c, "Enter"); out.Push == nil {
		t.Fatalf("Enter = %+v", out)
	}
	c.Reset(t.Context())
	c.Wait()
	for _, sec := range c.Snapshot().Sections {
		if sec.Title == "Recent" {
			t.Fatalf("fallback recorded as recent: %+v", sec)
		}
	}
}

func TestTabOpensTabAction(t *testing.T) {
	env := buildWith(t, &askPlugin{})
	c := env.Controller
	// Without text Tab does nothing.
	pressKey(t, c, "Tab")
	if s := c.Snapshot(); s.Depth != 0 {
		t.Fatalf("Tab without text pushed %+v", s)
	}
	// An entry with arguments keeps Tab for them.
	typeText(t, c, "argument item")
	pressKey(t, c, "Tab")
	if s := c.Snapshot(); s.Depth != 0 || s.Arguments == nil || s.Arguments.Focused != "x" {
		t.Fatalf("Tab on an entry with arguments: depth %d args %+v", s.Depth, s.Arguments)
	}
	pressKey(t, c, "Escape") // back to the search field
	// Otherwise Tab runs the plugin's Tab action with the text.
	typeText(t, c, "weather")
	pressKey(t, c, "Tab")
	s := c.Snapshot()
	if s.Depth != 1 || s.Layout != "detail" || !strings.Contains(s.Detail.Markdown, "first: weather") {
		t.Fatalf("after Tab: %+v", s)
	}
}

func TestDetailViewInputAndEscape(t *testing.T) {
	p := &askPlugin{}
	env := buildWith(t, p)
	c := env.Controller
	if _, err := c.OpenCommand(t.Context(), "ask", "chat"); err != nil {
		t.Fatal(err)
	}
	c.Wait()
	s := typeText(t, c, "hello there")
	if s.Layout != "detail" || s.ShowDetail || s.Placeholder != "Message" {
		t.Fatalf("snapshot %+v", s)
	}
	// Enter sends the typed text, which is then cleared.
	pressKey(t, c, "Enter")
	s = c.Snapshot()
	if s.Text != "" || !strings.Contains(s.Detail.Markdown, "sent: hello there") {
		t.Fatalf("after Enter: text %q, %q", s.Text, s.Detail.Markdown)
	}
	// While "answering", Escape stops; then it goes back.
	pressKey(t, c, "Escape")
	if p.stopped != 1 || c.Snapshot().Depth != 1 {
		t.Fatalf("first Escape: stopped %d, depth %d", p.stopped, c.Snapshot().Depth)
	}
	pressKey(t, c, "Escape")
	if c.Snapshot().Depth != 0 {
		t.Fatal("second Escape did not go back")
	}
}

func TestResultPreferences(t *testing.T) {
	p := &askPlugin{}
	env := buildWith(t, p)
	c := env.Controller
	if _, err := c.OpenCommand(t.Context(), "ask", "chat"); err != nil {
		t.Fatal(err)
	}
	c.Wait()
	if _, err := c.RunAction(t.Context(), "ask/chat", "enable"); err != nil {
		t.Fatal(err)
	}
	if v := env.Config.Get().Plugin("ask").Preferences["on"]; string(v) != "true" {
		t.Fatalf("preference = %s", v)
	}
	p.prefErr = true
	if _, err := c.RunAction(t.Context(), "ask/chat", "enable"); err == nil {
		t.Fatal("a password preference was set")
	}
}
