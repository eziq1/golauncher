package ui_test

import (
	"slices"
	"strings"
	"testing"
	"time"

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

// fixedKeys shows the fixed slots: an action's key, a folder as
// name[keys], "-" for an empty slot.
func fixedKeys(env *app.Env) []string {
	var out []string
	for _, s := range env.Config.Get().Ring.Fixed {
		switch {
		case s.IsFolder():
			var ks []string
			for _, a := range s.Actions {
				ks = append(ks, a.Key)
			}
			out = append(out, s.Name+"["+strings.Join(ks, " ")+"]")
		case s.IsEmpty():
			out = append(out, "-")
		default:
			out = append(out, s.Key)
		}
	}
	return out
}

// centerOf returns the centre of the element showing or labeled label.
func centerOf(t *testing.T, tt *mui.Tester, label string) (float32, float32) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("no %q in %q", label, tt.Texts())
	}
	return r.X + r.W/2, r.Y + r.H/2
}

// dragTo drags the element labeled from onto the one labeled to, holds it
// there for hold, and lets go.
func dragTo(t *testing.T, tt *mui.Tester, from, to string, hold time.Duration) {
	t.Helper()
	fx, fy := centerOf(t, tt, from)
	tx, ty := centerOf(t, tt, to)
	tt.Press(fx, fy)
	tt.Frame()
	tt.Move(fx+8, fy+8)
	tt.Frame()
	tt.Move(tx, ty)
	tt.Frame()
	if hold > 0 {
		tt.Advance(hold)
		tt.Frame()
	}
	tt.Release(tx, ty)
	tt.Frame()
}

// setFixed stores the fixed slots, then shows them.
func setFixed(t *testing.T, env *app.Env, tt *mui.Tester, slots ...config.RingSlot) {
	t.Helper()
	if err := env.Config.Update(func(c *config.Config) { c.Ring.Fixed = slots }); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

func acts(keys ...string) []config.RingAction {
	var out []config.RingAction
	for _, k := range keys {
		out = append(out, config.RingAction{Key: k, Title: strings.ToUpper(k)})
	}
	return out
}

// TestSettingsRingEditor: the ring editor's drags: an action onto an empty
// slot moves; held over another for half a second it makes a folder, which
// opens to be named, and let go earlier the two trade places; onto a
// folder it goes in, unless the folder is full; a folder's action dragged
// to an empty slot moves out (refused with no empty slot), and a folder
// left with one action is that action; actions reordered in the folder.
func TestSettingsRingEditor(t *testing.T) {
	env, _, tt := newRingSettings(t, "en", &fakeHost{})
	editor, ok := tt.Find("Fixed slots")
	if !ok || editor.W != 600 {
		t.Fatalf("no editor in %q", tt.Texts())
	}
	setFixed(t, env, tt, config.RingSlot{Key: "a", Title: "Alpha"}, config.RingSlot{Key: "b", Title: "Beta"}, config.RingSlot{}, config.RingSlot{Key: "d", Title: "Delta"})
	for _, want := range []string{"Alpha", "Beta", "Empty slot, Bottom", "Delta", "Context"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	// Onto an empty slot: moved.
	dragTo(t, tt, "Alpha", "Empty slot, Bottom", 0)
	if got := fixedKeys(env); !slices.Equal(got, []string{"-", "b", "a", "d"}) {
		t.Fatalf("moved: %q", got)
	}
	// Onto another action, let go before half a second: they trade places.
	dragTo(t, tt, "Beta", "Delta", 300*time.Millisecond)
	if got := fixedKeys(env); !slices.Equal(got, []string{"-", "d", "a", "b"}) {
		t.Fatalf("let go early: %q", got)
	}
	// Held there half a second: a folder, opened, its name to edit.
	dragTo(t, tt, "Delta", "Alpha", 600*time.Millisecond)
	if got := fixedKeys(env); !slices.Equal(got, []string{"-", "-", "Folder[a d]", "b"}) {
		t.Fatalf("merged: %q", got)
	}
	if !tt.HasText("Folder ▸") || !tt.HasText("1 Alpha") || !tt.HasText("2 Delta") {
		t.Fatalf("the folder is not open: %q", tt.Texts())
	}
	typeIntoField(t, tt, "Folder name", "Tools")
	if got := fixedKeys(env); !slices.Equal(got, []string{"-", "-", "Tools[a d]", "b"}) {
		t.Fatalf("renamed: %q", got)
	}
	// Onto the folder: in it.
	dragTo(t, tt, "Beta", "Tools", 0)
	if got := fixedKeys(env); !slices.Equal(got, []string{"-", "-", "Tools[a d b]"}) {
		t.Fatalf("into the folder: %q", got)
	}
	// Reordered in the folder (open below the ring).
	dragTo(t, tt, "Beta", "Alpha", 0)
	if got := fixedKeys(env); !slices.Equal(got, []string{"-", "-", "Tools[b a d]"}) {
		t.Fatalf("reordered: %q", got)
	}
	// Dragged out to an empty slot.
	dragTo(t, tt, "Delta", "Empty slot, Top", 0)
	if got := fixedKeys(env); !slices.Equal(got, []string{"d", "-", "Tools[b a]"}) {
		t.Fatalf("out: %q", got)
	}
	// Out again: the folder of one left is that action.
	dragTo(t, tt, "Beta", "Empty slot, Right", 0)
	if got := fixedKeys(env); !slices.Equal(got, []string{"d", "b", "a"}) {
		t.Fatalf("dissolved: %q", got)
	}
	if tt.HasText("Folder name") {
		t.Errorf("a folder still open: %q", tt.Texts())
	}

	// A full folder refuses, with a hint.
	setFixed(t, env, tt, config.RingSlot{Key: "x", Title: "Extra"}, config.FolderSlot("Six", acts("1", "2", "3", "4", "5", "6")...))
	dragTo(t, tt, "Extra", "Six", 0)
	if got := fixedKeys(env); !slices.Equal(got, []string{"x", "Six[1 2 3 4 5 6]"}) || !tt.HasText("This folder is full: a folder holds at most 6 actions.") {
		t.Fatalf("full: %q %q", got, tt.Texts())
	}
	// Out of a folder with no empty slot: refused, with a hint.
	setFixed(t, env, tt, config.RingSlot{Key: "x", Title: "Extra"}, config.FolderSlot("Two", acts("p", "q")...), config.RingSlot{Key: "y", Title: "Why"}, config.RingSlot{Key: "z", Title: "Zed"})
	clickText(t, tt, "Two")
	dragTo(t, tt, "P", "Extra", 0)
	if got := fixedKeys(env); !slices.Equal(got, []string{"x", "Two[p q]", "y", "z"}) || !tt.HasText("There is no empty slot. Remove or move an action first.") {
		t.Fatalf("no room: %q %q", got, tt.Texts())
	}
}

// TestSettingsRingEditorButtons: without dragging: the selected action
// moved to a position, put in a new folder and in a folder; a folder's
// action moved out and removed; a folder deleted, its actions to the empty
// slots, asking first when they do not all fit.
func TestSettingsRingEditorButtons(t *testing.T) {
	env, _, tt := newRingSettings(t, "en", &fakeHost{})
	setFixed(t, env, tt, config.RingSlot{Key: "a", Title: "Alpha"}, config.RingSlot{Key: "b", Title: "Beta"}, config.RingSlot{}, config.RingSlot{Key: "d", Title: "Delta"})
	clickText(t, tt, "Alpha")
	clickText(t, tt, "Move Alpha…")
	if !tt.HasText("Move Alpha to") {
		t.Fatalf("no dialog: %q", tt.Texts())
	}
	clickText(t, tt, "Bottom")
	if got := fixedKeys(env); !slices.Equal(got, []string{"-", "b", "a", "d"}) {
		t.Fatalf("moved: %q", got)
	}
	clickText(t, tt, "Beta")
	clickText(t, tt, "Move Beta into a folder…")
	clickText(t, tt, "A new folder with Delta")
	if got := fixedKeys(env); !slices.Equal(got, []string{"-", "-", "a", "Folder[d b]"}) {
		t.Fatalf("new folder: %q", got)
	}
	clickText(t, tt, "Alpha")
	clickText(t, tt, "Move Alpha into a folder…")
	clickText(t, tt, "Into Folder")
	if got := fixedKeys(env); !slices.Equal(got, []string{"-", "-", "-", "Folder[d b a]"}) {
		t.Fatalf("into the folder: %q", got)
	}
	// The folder's actions (open below the ring).
	clickText(t, tt, "Delta")
	clickText(t, tt, "Move Delta out of the folder")
	if got := fixedKeys(env); !slices.Equal(got, []string{"d", "-", "-", "Folder[b a]"}) {
		t.Fatalf("out: %q", got)
	}
	clickText(t, tt, "Beta")
	clickText(t, tt, "Move Beta…")
	clickText(t, tt, "Position 2")
	if got := fixedKeys(env); !slices.Equal(got, []string{"d", "-", "-", "Folder[a b]"}) {
		t.Fatalf("reordered: %q", got)
	}
	clickText(t, tt, "Remove Beta")
	if got := fixedKeys(env); !slices.Equal(got, []string{"d", "-", "-", "a"}) {
		t.Fatalf("removed, dissolved: %q", got)
	}
	// Deleted: its actions to the empty slots.
	setFixed(t, env, tt, config.FolderSlot("F", acts("p", "q")...), config.RingSlot{Key: "x", Title: "Extra"})
	clickText(t, tt, "F")
	clickText(t, tt, "Delete the folder F")
	if got := fixedKeys(env); !slices.Equal(got, []string{"p", "x", "q"}) {
		t.Fatalf("deleted: %q", got)
	}
	// Not all fit: asked first.
	setFixed(t, env, tt, config.FolderSlot("G", acts("p", "q", "r")...), config.RingSlot{Key: "x", Title: "Extra"}, config.RingSlot{Key: "y", Title: "Why"})
	clickText(t, tt, "G")
	clickText(t, tt, "Delete the folder G")
	if !tt.HasText("Only 2 of its 3 actions fit in the empty slots; the others are removed.") {
		t.Fatalf("not asked: %q", tt.Texts())
	}
	clickText(t, tt, "Delete")
	if got := fixedKeys(env); !slices.Equal(got, []string{"p", "x", "y", "q"}) {
		t.Fatalf("deleted: %q", got)
	}
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

	// Removed, with the selected slot's button.
	clickText(t, tt, "Screenshot")
	clickText(t, tt, "Remove Screenshot")
	if got := fixedKeys(env); !slices.Equal(got, []string{config.RingLauncher, config.RingAskAI, "-", config.RingLock}) {
		t.Fatalf("fixed %q", got)
	}
	// Added: one of the ring's own (to the empty slot selected), then a
	// launcher item found by a search, to the first empty slot.
	clickText(t, tt, "Empty slot, Bottom")
	clickText(t, tt, "Add Action")
	settle(s, tt)
	if !tt.HasText("Add an Action") || !tt.HasText("Added") {
		t.Fatalf("dialog %q", tt.Texts())
	}
	clickText(t, tt, "Screenshot")
	settle(s, tt)
	clickText(t, tt, "Open golauncher")
	clickText(t, tt, "Remove Open golauncher")
	clickText(t, tt, "Add Action")
	settle(s, tt)
	tt.Type("terminal")
	settle(s, tt)
	clickText(t, tt, "Windows Terminal")
	settle(s, tt)
	ring := env.Config.Get().Ring
	if got := fixedKeys(env); !slices.Equal(got, []string{"test/term", config.RingAskAI, config.RingScreenshot, config.RingLock}) ||
		ring.Fixed[2].Title != "" || ring.Fixed[0].Title != "Windows Terminal" || ring.Fixed[0].Icon != "app" {
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
	for _, want := range []string{"按住鼠标中键打开操作环", "按住时长（毫秒）", "固定操作", "打开 golauncher", "问 AI", "截图", "情境", "固定格子", "情境操作", "例外名单", "隐私", "添加操作", "恢复默认"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
}
