package ui_test

import (
	"strings"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// hideShow hides the launcher window and shows it again, as the shell does.
func (lt *launcherTest) hideShow(o core.ShowOptions) {
	lt.l.Hidden()
	lt.l.ShownWith(o)
	lt.step()
}

// TestAIChatKeptAcrossHide copies an answer, "leaves" the launcher and comes
// back to the chat with its draft and the footer's hint (R75).
func TestAIChatKeptAcrossHide(t *testing.T) {
	gate := make(chan struct{})
	fake := &backend.Fake{Gate: gate, Reply: func(q string) []string { return []string{"You ", "asked: " + q} }}
	lt := newAILauncher(t, fake, true)
	lt.typeText("ai chat")
	lt.key(0, mui.KeyEnter)
	lt.typeText("hello")
	lt.key(0, mui.KeyEnter)
	gate <- struct{}{}
	gate <- struct{}{}
	lt.waitFor("the answer", func(md string) bool { return strings.Contains(md, "You asked: hello") && !strings.Contains(md, "▍") })
	lt.key(mui.Ctrl|mui.Shift, mui.KeyC) // copy the answer
	lt.typeText("draft")

	lt.hideShow(core.ShowOptions{})
	s := lt.Controller.Snapshot()
	if s.Depth != 1 || s.Layout != "detail" || s.Text != "draft" {
		t.Fatalf("restored: depth %d layout %q text %q", s.Depth, s.Layout, s.Text)
	}
	lt.wantText("You asked: hello")
	// The field shows the draft: typing goes on after it.
	lt.typeText("!")
	if got := lt.Controller.Snapshot().Text; got != "draft!" {
		t.Fatalf("text after typing = %q", got)
	}
	// No hint while text is typed (Escape would clear it first).
	if lt.tt.HasText("Back to main screen") {
		t.Fatal("hint shown over a draft")
	}
	lt.key(0, mui.KeyEscape) // clears the draft
	lt.wantText("Back to main screen")
	lt.wantText("Esc")

	// An answer written while hidden keeps streaming; the reopened view
	// shows where it is.
	lt.typeText("more")
	lt.key(0, mui.KeyEnter)
	lt.hideShow(core.ShowOptions{})
	gate <- struct{}{}
	lt.waitFor("the first piece", func(md string) bool { return strings.Contains(md, "You ▍") })
	if s := lt.Controller.Snapshot(); s.Depth != 1 || s.Hint != "" {
		// While the answer is written Escape stops it: no hint.
		t.Fatalf("while answering: depth %d hint %q", s.Depth, s.Hint)
	}
	lt.hideShow(core.ShowOptions{})
	gate <- struct{}{}
	lt.waitFor("the answer", func(md string) bool { return strings.Contains(md, "You asked: more") && !strings.Contains(md, "▍") })

	// The selected-text hotkey goes to the root search.
	lt.hideShow(core.ShowOptions{Root: true})
	if s := lt.Controller.Snapshot(); s.Depth != 0 {
		t.Fatalf("selected-text hotkey: depth %d", s.Depth)
	}
}

// TestSettingsReturnToRoot chooses when kept views give way to the root.
func TestSettingsReturnToRoot(t *testing.T) {
	env, _, tt := newSettings(t, "en")
	if got := env.Config.Get().EffectiveReturnToRoot(); got != config.ReturnToRootAfter {
		t.Fatalf("default = %q", got)
	}
	for _, want := range []string{"Return to the main screen", "After 10 minutes"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Never"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := env.Config.Get().ReturnToRoot; got != config.ReturnToRootNever {
		t.Fatalf("returnToRoot = %q", got)
	}
	if err := tt.Click("Immediately"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := env.Config.Get().ReturnToRoot; got != config.ReturnToRootImmediately {
		t.Fatalf("returnToRoot = %q", got)
	}
}

// TestKeepHintClick goes back to the root search from the footer's hint.
func TestKeepHintClick(t *testing.T) {
	lt := newAILauncher(t, &backend.Fake{}, true)
	lt.typeText("ai chat")
	lt.key(0, mui.KeyEnter)
	lt.hideShow(core.ShowOptions{})
	lt.wantText("Back to main screen")
	if err := lt.tt.Click("Back to main screen"); err != nil {
		t.Fatal(err)
	}
	lt.step()
	if s := lt.Controller.Snapshot(); s.Depth != 0 || s.Text != "" {
		t.Fatalf("after the click: depth %d text %q", s.Depth, s.Text)
	}
}
