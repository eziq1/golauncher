package ai

import (
	"encoding/json/jsontext"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// restoreChat requests a chat view again after a hide, as the host does for
// a view kept with KeepOnHide (docs/keep-view.md).
func restoreChat(t *testing.T, p *Plugin, instance string, pl chatPayload) (plugin.View, error) {
	return p.View(t.Context(), plugin.ViewRequest{ID: viewChat, Instance: instance, Payload: plugin.MustPayload(pl), Restore: true})
}

// TestChatKeptAcrossHide: the chat view asks to be kept; restoring it after
// its process was released (R59, and the release after the window hid)
// shows the chat without sending its first message again, and the next
// message continues it.
func TestChatKeptAcrossHide(t *testing.T) {
	fake := &backend.Fake{}
	p := NewWithOptions(Options{Backend: fake, Sync: true, HasProtocol: func(string) bool { return false },
		IdleRelease: 10 * time.Millisecond, ChatIdleRelease: 10 * time.Millisecond})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true")})
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	pl := chatPayload{Text: "first question"}
	if v := openChat(t, p, "v1", pl); !v.KeepOnHide {
		t.Fatal("the chat view does not ask to be kept")
	}
	h.Bus.Publish(plugin.EventWindowHidden)
	deadline := time.Now().Add(5 * time.Second)
	for fake.Released() < 1 { // the chat's idle timer or the hidden window's
		if time.Now().After(deadline) {
			t.Fatal("the conversation was not released")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond) // both timers have fired
	h.Bus.Publish(plugin.EventWindowShown)

	v, err := restoreChat(t, p, "v1", pl)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !v.KeepOnHide || !strings.Contains(v.Detail.Markdown, "You asked: first question") {
		t.Fatalf("restored view = %+v", v)
	}
	if s := fake.Sends(); len(s) != 1 {
		t.Fatalf("sends after the restore = %q", s)
	}
	act(t, p, "send", "v1", "again")
	if s := fake.Sends(); len(s) != 2 || s[1] != "again" {
		t.Fatalf("sends = %q", s)
	}
	if v, _ := restoreChat(t, p, "v1", pl); !strings.Contains(v.Detail.Markdown, "You asked: again") {
		t.Fatalf("markdown:\n%s", v.Detail.Markdown)
	}
}

// TestChatRestoreRefused: a chat deleted meanwhile, an instance the plugin
// never showed, or AI turned off refuse the restore (the host goes back to
// the root search), never opening a new chat from the payload.
func TestChatRestoreRefused(t *testing.T) {
	fake := &backend.Fake{}
	p, h := newTestPlugin(t, fake, true)
	pl := chatPayload{Text: "q"}
	openChat(t, p, "v1", pl)
	openChat(t, p, "v2", chatPayload{Text: "other"})

	if _, err := restoreChat(t, p, "v9", pl); err == nil {
		t.Fatal("restored an instance never shown")
	}
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "delete", Payload: plugin.MustPayload("id-1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreChat(t, p, "v1", pl); err == nil {
		t.Fatal("restored a deleted chat")
	}
	if s := fake.Sends(); len(s) != 2 {
		t.Fatalf("sends = %q (a refused restore must not send again)", s)
	}
	if _, err := restoreChat(t, p, "v2", chatPayload{Text: "other"}); err != nil {
		t.Fatalf("the other chat: %v", err)
	}
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("false")})
	if _, err := restoreChat(t, p, "v2", chatPayload{Text: "other"}); err == nil {
		t.Fatal("restored with AI off")
	}
	// The first-use prompt (AI off) is not kept.
	if v := openChat(t, p, "v3", chatPayload{Text: "x"}); v.KeepOnHide {
		t.Fatal("the prompt to turn AI on asks to be kept")
	}
}
