package shell

import (
	"testing"

	"github.com/egoist/mygo"

	"github.com/eziq1/golauncher/internal/i18n"
)

func coreLocalizer(t *testing.T) *i18n.Localizer {
	t.Helper()
	m := i18n.NewManager(i18n.Source, nil)
	if err := m.Register(i18n.CoreNamespace, i18n.CoreLocales()); err != nil {
		t.Fatal(err)
	}
	m.SetLocale(i18n.Source)
	return m.Localizer(i18n.CoreNamespace)
}

func TestTrayToolTip(t *testing.T) {
	l := coreLocalizer(t)
	for n, want := range map[int]string{0: "golauncher", 1: "golauncher — 1 background task running", 3: "golauncher — 3 background tasks running"} {
		if got := trayToolTip(l, n); got != want {
			t.Errorf("trayToolTip(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestQuitPrompt: quitting with running tasks asks, and Escape or Enter
// cancel (the default button is Cancel).
func TestQuitPrompt(t *testing.T) {
	o := quitPrompt(coreLocalizer(t), 2)
	if o.Title != "Quit golauncher?" || o.Message != "2 background tasks are running. Quitting stops them." ||
		len(o.Buttons) != 2 || o.Buttons[0] != "Quit" || o.DefaultButton != 1 || o.CancelButton != 1 || o.Type != mygo.MessageWarning {
		t.Errorf("prompt %+v", o)
	}
}
