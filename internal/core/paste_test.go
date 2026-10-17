package core_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestPasteNotAllowed: when the content was copied but pasting is not
// allowed (macOS without Accessibility), the paste action still succeeds
// and hides the launcher, the content stays on the clipboard, and a notice
// says to press ⌘V and links to the Permissions settings.
func TestPasteNotAllowed(t *testing.T) {
	e := newEnv(t)
	cause := errors.New("clip: golauncher needs Accessibility permission")
	e.rec.PasteErr = fmt.Errorf("%w: %w", core.ErrNotPasted, cause)
	var (
		mu      sync.Mutex
		notices []core.Notice
	)
	e.Engine.SetNotifier(func(n core.Notice) {
		mu.Lock()
		notices = append(notices, n)
		mu.Unlock()
	})

	e.typeText(t, "browse fruits")
	e.key(t, "Enter")
	e.typeText(t, "an")
	out := e.key(t, "Enter") // pastes Banana
	if !out.Hide {
		t.Error("the launcher does not hide")
	}
	if eff := e.rec.Effects(); len(eff) != 1 || eff[0].Kind != plugin.ActionPaste || eff[0].Target != "Banana" {
		t.Fatalf("effects = %+v", eff)
	}
	if c, err := e.Services.Clipboard.Read(); err != nil || c.Text != "Banana" {
		t.Fatalf("clipboard = %+v, %v", c, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 1 {
		t.Fatalf("notices = %+v", notices)
	}
	n := notices[0]
	if n.Toast.Title != "Copied — press ⌘V to paste" || n.Toast.Message == "" || n.Settings != core.SettingsPermissions {
		t.Errorf("notice = %+v", n)
	}
	if s := e.Controller.Snapshot(); s.Toast != nil && s.Toast.Style == plugin.ToastFailure {
		t.Errorf("failure toast %+v", s.Toast)
	}
}

// TestPasteFails: any other paste error is still the action's failure.
func TestPasteFails(t *testing.T) {
	e := newEnv(t)
	e.rec.PasteErr = errors.New("clip: the target application is no longer running")
	e.typeText(t, "browse fruits")
	e.key(t, "Enter")
	e.typeText(t, "an")
	if _, err := e.Controller.Key(t.Context(), "Enter"); err == nil || errors.Is(err, core.ErrNotPasted) {
		t.Fatalf("err = %v", err)
	}
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); s.Toast == nil || s.Toast.Style != plugin.ToastFailure {
		t.Errorf("toast = %+v", s.Toast)
	}
}
