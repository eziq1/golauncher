package ui_test

import (
	"slices"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/darwin/tcc"
	"github.com/eziq1/golauncher/internal/ui"
)

// newRingSettings is the settings on the Action Ring page, tall enough for
// all of it.
func newRingSettings(t *testing.T, locale string, host ui.SettingsHost) (*app.Env, *ui.Settings, *mui.Tester) {
	t.Helper()
	env, _ := newEnv(t, locale)
	s := ui.NewSettings(env, host, "1.0.0-test")
	tt := mui.NewTester(s.View, ui.SettingsWidth, 3000)
	tt.SetLocale(locale)
	tt.Frame()
	if err := tt.Click(map[string]string{"en": "Action Ring", "zh-CN": "操作环"}[locale]); err != nil {
		t.Fatal(err)
	}
	settle(s, tt)
	return env, s, tt
}

// typeIntoField replaces the text of the input of the field labeled label,
// clicking the label (at the field's top left), which focuses it.
func typeIntoField(t *testing.T, tt *mui.Tester, label, text string) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("no %q in %q", label, tt.Texts())
	}
	tt.ClickAt(r.X+20, r.Y+8)
	tt.Frame()
	tt.Key(0, mui.KeyEnd)
	for range 20 {
		tt.Key(0, mui.KeyBackspace)
	}
	tt.Type(text)
	tt.Frame()
}

func fixedKeys(env *app.Env) []string {
	var out []string
	for _, a := range env.Config.Get().Ring.Fixed {
		out = append(out, a.Key)
	}
	return out
}

// TestSettingsRing: the Action Ring page: on and off, the hold duration
// (checked), the fixed actions moved, removed, added from the ring's own
// and from a search, and restored; the context actions; the exceptions
// added and removed; the privacy note. No permission notice off macOS.
func TestSettingsRing(t *testing.T) {
	env, s, tt := newRingSettings(t, "en", &fakeHost{})
	for _, want := range []string{"Open the Action Ring with the middle mouse button", "Hold for (milliseconds)", "Fixed actions",
		"Open golauncher", "Ask AI", "Screenshot", config.RingLock, "Context actions", "Exceptions", "mstsc.exe", "Privacy"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if !hasTextPart(tt, "Only the middle mouse button is handled") || hasTextPart(tt, "Accessibility") {
		t.Errorf("texts %q", tt.Texts())
	}

	// Off.
	clickText(t, tt, "Open the Action Ring with the middle mouse button")
	if env.Config.Get().Ring.Enabled {
		t.Fatal("still on")
	}

	// The hold duration, within 150–1000ms.
	typeIntoField(t, tt, "Hold for (milliseconds)", "100")
	if !tt.HasText("Enter a number from 150 to 1000.") || env.Config.Get().Ring.HoldMS != 0 {
		t.Fatalf("hold 100: %q", tt.Texts())
	}
	typeIntoField(t, tt, "Hold for (milliseconds)", "450")
	if tt.HasText("Enter a number from 150 to 1000.") || env.Config.Get().Ring.HoldMS != 450 {
		t.Fatalf("hold 450: %d", env.Config.Get().Ring.HoldMS)
	}

	// Moved and removed.
	clickText(t, tt, "Move Ask AI up")
	clickText(t, tt, "Remove Screenshot")
	if got := fixedKeys(env); !slices.Equal(got, []string{config.RingAskAI, config.RingLauncher, config.RingLock}) {
		t.Fatalf("fixed %q", got)
	}
	// Added: one of the ring's own, then a launcher item found by a search.
	clickText(t, tt, "Add Action")
	settle(s, tt)
	if !tt.HasText("Add an Action") || !tt.HasText("Added") {
		t.Fatalf("dialog %q", tt.Texts())
	}
	clickText(t, tt, "Screenshot")
	settle(s, tt)
	clickText(t, tt, "Add Action")
	settle(s, tt)
	tt.Type("terminal")
	settle(s, tt)
	clickText(t, tt, "Windows Terminal")
	settle(s, tt)
	ring := env.Config.Get().Ring
	if got := fixedKeys(env); !slices.Equal(got, []string{config.RingAskAI, config.RingLauncher, config.RingLock, config.RingScreenshot, "test/term"}) ||
		ring.Fixed[3].Title != "" || ring.Fixed[4].Title != "Windows Terminal" || ring.Fixed[4].Icon != "app" {
		t.Fatalf("fixed %+v", ring.Fixed)
	}
	if !tt.HasText("Windows Terminal") || tt.HasText("Add an Action") {
		t.Fatalf("after adding: %q", tt.Texts())
	}
	// Restored, after a confirmation.
	clickText(t, tt, "Restore Defaults")
	if !tt.HasText("Restore the default actions?") {
		t.Fatalf("no confirmation in %q", tt.Texts())
	}
	clickText(t, tt, "Restore")
	if got := fixedKeys(env); !slices.Equal(got, []string{config.RingLauncher, config.RingAskAI, config.RingScreenshot, config.RingLock}) {
		t.Fatalf("restored %q", got)
	}

	// Context actions off.
	clickText(t, tt, "Fill the other slots with actions for what you are doing")
	if env.Config.Get().Ring.Context {
		t.Fatal("context still on")
	}

	// An exception added (twice: once), one removed.
	for range 2 {
		typeInto(t, tt, "Program name, such as mstsc.exe", "Game.exe")
		clickText(t, tt, "Add")
	}
	clickText(t, tt, "Remove mstsc.exe")
	ex := env.Config.Get().Ring.Exceptions
	if slices.Contains(ex, "mstsc.exe") || slices.Index(ex, "Game.exe") != len(ex)-1 || len(ex) != len(config.DefaultRingExceptions) {
		t.Fatalf("exceptions %q", ex)
	}
	if _, gone := tt.Find("mstsc.exe"); !tt.HasText("Game.exe") || gone {
		t.Errorf("texts %q", tt.Texts())
	}
}

// TestSettingsRingPermission: on macOS without Accessibility the page says
// that the ring needs it, and its button opens the System Settings page;
// with it, nothing is said.
func TestSettingsRingPermission(t *testing.T) {
	host := &permHost{status: map[tcc.ID]tcc.Status{tcc.Accessibility: tcc.Denied}}
	_, s, tt := newRingSettings(t, "en", host)
	if !hasTextPart(tt, "The Action Ring needs the Accessibility permission") {
		t.Fatalf("no notice in %q", tt.Texts())
	}
	clickText(t, tt, "Open Accessibility Settings")
	host.mu.Lock()
	opened, requested := slices.Clone(host.opened), slices.Clone(host.requested)
	host.mu.Unlock()
	if !slices.Equal(opened, []tcc.ID{tcc.Accessibility}) || len(requested) != 0 {
		t.Errorf("opened %v, requested %v", opened, requested)
	}
	host.set(tcc.Accessibility, tcc.Granted)
	s.ShowPage(ui.PageRing)
	settle(s, tt)
	if hasTextPart(tt, "Accessibility permission") {
		t.Errorf("a notice with the permission: %q", tt.Texts())
	}
}

// TestSettingsRingChinese: the page in Chinese.
func TestSettingsRingChinese(t *testing.T) {
	_, _, tt := newRingSettings(t, "zh-CN", &fakeHost{})
	for _, want := range []string{"按住鼠标中键打开操作环", "按住时长（毫秒）", "固定操作", "打开 golauncher", "问 AI", "截图", "情境操作", "例外名单", "隐私", "添加操作", "恢复默认"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
}
