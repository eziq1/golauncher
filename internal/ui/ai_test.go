package ui_test

import (
	"encoding/json/jsontext"
	"strings"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// newAILauncher is a launcher with the AI chat answering from fake, which
// writes its answers piece by piece as the test releases them.
func newAILauncher(t *testing.T, fake *backend.Fake, enabled bool) *launcherTest {
	t.Helper()
	fake.Go = func(fn func()) { go fn() }
	clip := &hostfake.Clipboard{}
	rec := &app.Recorder{Clipboard: clip}
	p := ai.NewWithOptions(ai.Options{Backend: fake, HasProtocol: func(string) bool { return false }})
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{testPlugin{}, p}, Fake: true, Locale: "en", System: rec, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	if enabled {
		if err := env.Config.Update(func(c *config.Config) {
			c.Plugins = map[string]config.PluginConfig{ai.ID: {Preferences: map[string]jsontext.Value{"enabled": jsontext.Value("true")}}}
		}); err != nil {
			t.Fatal(err)
		}
	}
	lt := &launcherTest{Env: env, t: t, rec: rec, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	return lt
}

// waitFor steps the launcher until cond holds for the controller's snapshot.
func (lt *launcherTest) waitFor(what string, cond func(md string) bool) {
	lt.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		lt.step()
		md := ""
		if d := lt.Controller.Snapshot().Detail; d != nil {
			md = d.Markdown
		}
		if cond(md) {
			return
		}
		if time.Now().After(deadline) {
			lt.t.Fatalf("timed out waiting for %s; markdown:\n%s", what, md)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAIChatStreamsAndEscapeStopsThenCloses(t *testing.T) {
	gate := make(chan struct{})
	fake := &backend.Fake{Gate: gate, Reply: func(string) []string { return []string{"Rayleigh ", "scattering ", "of light."} }}
	lt := newAILauncher(t, fake, true)

	// Nothing matches the question: "Ask AI" is offered last.
	lt.typeText("why is the sky blue")
	lt.wantText("Ask AI: why is the sky blue")
	lt.wantText("Fallback Commands")

	// Tab opens a chat that sends the text at once.
	lt.key(0, mui.KeyTab)
	s := lt.Controller.Snapshot()
	if s.Depth != 1 || s.Layout != "detail" {
		t.Fatalf("after Tab: depth %d layout %q", s.Depth, s.Layout)
	}
	lt.waitFor("the question", func(md string) bool {
		return strings.Contains(md, "why is the sky blue") && strings.Contains(md, "Thinking…")
	})
	lt.wantText("why is the sky blue")

	// The answer shows as it arrives.
	gate <- struct{}{}
	lt.waitFor("the first piece", func(md string) bool { return strings.Contains(md, "Rayleigh ▍") })
	gate <- struct{}{}
	lt.waitFor("the second piece", func(md string) bool { return strings.Contains(md, "Rayleigh scattering ▍") })

	// Escape stops the answer and stays in the chat...
	lt.key(0, mui.KeyEscape)
	lt.waitFor("the stop", func(md string) bool { return strings.Contains(md, "Stopped.") && !strings.Contains(md, "▍") })
	if d := lt.Controller.Snapshot().Depth; d != 1 {
		t.Fatalf("the first Escape left the chat (depth %d)", d)
	}
	// ...and the second one closes it.
	lt.key(0, mui.KeyEscape)
	if d := lt.Controller.Snapshot().Depth; d != 0 {
		t.Fatalf("the second Escape did not close the chat (depth %d)", d)
	}
	if got := fake.Sends(); len(got) != 1 || got[0] != "why is the sky blue" {
		t.Fatalf("sent %q", got)
	}
}

func TestAIChatFollowUpAndCopy(t *testing.T) {
	fake := &backend.Fake{}
	lt := newAILauncher(t, fake, true)
	lt.typeText("ai chat")
	lt.key(0, mui.KeyEnter)
	if ph := lt.Controller.Snapshot().Placeholder; ph != "Ask anything…" {
		t.Fatalf("placeholder %q", ph)
	}
	lt.typeText("hello")
	lt.key(0, mui.KeyEnter)
	lt.waitFor("the answer", func(md string) bool { return strings.Contains(md, "You asked: hello") })
	if s := lt.Controller.Snapshot(); s.Text != "" {
		t.Fatalf("the message stayed in the field: %q", s.Text)
	}
	if ph := lt.Controller.Snapshot().Placeholder; ph != "Ask a follow-up…" {
		t.Fatalf("placeholder %q", ph)
	}
	lt.typeText("more")
	lt.key(0, mui.KeyEnter)
	lt.waitFor("the follow-up", func(md string) bool { return strings.Contains(md, "You asked: more") })

	lt.key(mui.Ctrl|mui.Shift, mui.KeyC)
	if eff := lt.rec.Effects(); len(eff) == 0 || eff[len(eff)-1].Kind != plugin.ActionCopyText || eff[len(eff)-1].Target != "You asked: more" {
		t.Fatalf("effects = %+v", eff)
	}
}

func TestAIOffPromptsFirst(t *testing.T) {
	fake := &backend.Fake{}
	lt := newAILauncher(t, fake, false)
	lt.typeText("why is the sky blue")
	if lt.tt.HasText("Ask AI: why is the sky blue") {
		t.Fatal("Ask AI offered while AI is off")
	}
	lt.key(0, mui.KeyTab)
	lt.wantText("Turn On AI")
	if len(fake.Sends()) != 0 {
		t.Fatal("sent while AI is off")
	}
	lt.key(0, mui.KeyEnter)
	lt.waitFor("the answer", func(md string) bool { return strings.Contains(md, "You asked: why is the sky blue") })
}
