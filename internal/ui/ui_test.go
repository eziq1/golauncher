package ui_test

import (
	"cmp"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// testPlugin offers a few root items, a list view with details and a
// destructive action.
type testPlugin struct{}

func (testPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: "test", Name: "Test", Version: "1.2.3", Permissions: []plugin.Permission{plugin.PermClipboard},
		Commands: []plugin.Command{
			{Name: "list", Title: "Browse Fruits", Mode: plugin.ModeView, DefaultHotkey: "Ctrl+Shift+F"},
		},
		Preferences: []plugin.Preference{
			{Name: "max", Type: plugin.PrefNumber, Title: "Maximum items", Default: []byte("10")},
			{Name: "images", Type: plugin.PrefBool, Title: "Keep images", Default: []byte("true")},
		},
	}
}

func (testPlugin) Init(context.Context, plugin.Host) error { return nil }
func (testPlugin) Close() error                            { return nil }

func (testPlugin) RootItems(context.Context, plugin.Query) ([]plugin.Item, error) {
	return []plugin.Item{item("vscode", "Visual Studio Code"), item("term", "Windows Terminal")}, nil
}

func item(id, title string) plugin.Item {
	return plugin.Item{ID: id, Title: title, Group: "Applications", Accessory: "Application", Icon: plugin.Icon{Name: "app"},
		Actions: []plugin.Action{
			{ID: "open", Title: "Open", Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: `C:\` + id + ".exe"}},
			{ID: "copy-path", Title: "Copy Path", Shortcut: "Ctrl+Shift+C", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: `C:\` + id + ".exe"}},
		}}
}

func (testPlugin) View(_ context.Context, req plugin.ViewRequest) (plugin.View, error) {
	var items []plugin.Item
	for _, f := range []string{"Apple", "Banana"} {
		items = append(items, plugin.Item{ID: f, Title: f, Icon: plugin.Icon{Name: "text"},
			Detail: &plugin.Detail{Markdown: "# " + f, Metadata: []plugin.Metadata{{Label: "Weight", Value: f + " grams"}}},
			Actions: []plugin.Action{
				{ID: "paste", Title: "Paste", Do: plugin.ActionDo{Kind: plugin.ActionPaste, Target: f}},
				{ID: "delete", Title: "Delete", Shortcut: "Ctrl+X", Style: plugin.StyleDestructive, Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "delete"}},
			}})
	}
	return plugin.View{Kind: plugin.ViewList, Title: "Fruits", ShowDetail: true, FilterLocally: true, Sections: []plugin.Section{{Items: items}}}, nil
}

func (testPlugin) HandleAction(context.Context, plugin.ActionDo) (plugin.Result, error) {
	return plugin.Result{Refresh: true}, nil
}

type fakeHost struct {
	hidden, settings atomic.Int32
}

func (h *fakeHost) Hide()         { h.hidden.Add(1) }
func (h *fakeHost) OpenSettings() { h.settings.Add(1) }
func (h *fakeHost) Invalidate()   {}

func (h *fakeHost) OpenPath(string) error { return nil }
func (h *fakeHost) SuspendHotkeys(bool)   {}

type launcherTest struct {
	*app.Env
	t    *testing.T
	rec  *app.Recorder
	host *fakeHost
	l    *ui.Launcher
	tt   *mui.Tester
}

func newEnv(t *testing.T, locale string) (*app.Env, *app.Recorder) {
	t.Helper()
	clip := &hostfake.Clipboard{}
	rec := &app.Recorder{Clipboard: clip}
	env, err := app.Build(t.Context(), app.Options{
		Plugins:   []plugin.Plugin{testPlugin{}},
		Fake:      true,
		Locale:    locale,
		System:    rec,
		Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	return env, rec
}

func newLauncher(t *testing.T, locale string) *launcherTest {
	t.Helper()
	env, rec := newEnv(t, locale)
	lt := &launcherTest{Env: env, t: t, rec: rec, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	return lt
}

// step lets queued input and searches finish and draws the result; a frame
// may queue more (the text typed), hence twice.
func (lt *launcherTest) step() {
	for range 2 {
		lt.l.Wait()
		lt.tt.Frame()
	}
}

func (lt *launcherTest) typeText(s string) {
	lt.tt.Type(s)
	lt.step()
}

func (lt *launcherTest) key(mods mui.Modifiers, k mui.Key) {
	lt.tt.Key(mods, k)
	lt.step()
}

func (lt *launcherTest) wantText(s string) {
	lt.t.Helper()
	if !lt.tt.HasText(s) {
		lt.t.Fatalf("no %q in %q", s, lt.tt.Texts())
	}
}

func TestTypingFilters(t *testing.T) {
	lt := newLauncher(t, "en")
	lt.wantText("Browse Fruits") // commands on the empty root
	lt.typeText("vsc")
	if got := lt.Controller.Snapshot().Text; got != "vsc" {
		t.Fatalf("controller text = %q", got)
	}
	lt.wantText("Visual Studio Code")
	if lt.tt.HasText("Windows Terminal") {
		t.Fatalf("Windows Terminal not filtered out: %q", lt.tt.Texts())
	}
	lt.wantText("Applications") // section header
	lt.wantText("Open")         // primary action in the footer
}

func TestDownEnterRunsAction(t *testing.T) {
	lt := newLauncher(t, "en")
	lt.typeText("windows terminal")
	lt.key(0, mui.KeyEnter)
	eff := lt.rec.Effects()
	if len(eff) != 1 || eff[0].Kind != plugin.ActionOpen || eff[0].Target != `C:\term.exe` {
		t.Fatalf("effects = %+v", eff)
	}
	if lt.host.hidden.Load() != 1 {
		t.Fatal("opening should hide the launcher")
	}

	lt.l.Shown()
	lt.step()
	lt.rec.Reset()
	lt.typeText("Code") // both apps? only VS Code matches well; move down and back up
	lt.key(0, mui.KeyDown)
	lt.key(0, mui.KeyUp)
	lt.key(0, mui.KeyEnter)
	if eff := lt.rec.Effects(); len(eff) != 1 || eff[0].Target != `C:\vscode.exe` {
		t.Fatalf("effects = %+v", eff)
	}
}

func TestShortcutForwarded(t *testing.T) {
	lt := newLauncher(t, "en")
	lt.typeText("terminal")
	lt.key(mui.Ctrl|mui.Shift, mui.KeyC)
	if eff := lt.rec.Effects(); len(eff) != 1 || eff[0].Kind != plugin.ActionCopyText {
		t.Fatalf("effects = %+v", eff)
	}
	// Ctrl+A is no action's shortcut: it stays with the text field.
	lt.rec.Reset()
	lt.key(mui.Ctrl, mui.KeyA)
	if eff := lt.rec.Effects(); len(eff) != 0 {
		t.Fatalf("Ctrl+A ran %+v", eff)
	}
	lt.key(mui.Ctrl, mui.KeyComma)
	if lt.host.settings.Load() != 1 {
		t.Fatal("Ctrl+, should open the settings")
	}
}

func TestActionPanel(t *testing.T) {
	lt := newLauncher(t, "en")
	lt.typeText("terminal")
	lt.key(mui.Ctrl, mui.KeyK)
	s := lt.Controller.Snapshot()
	if s.Panel == nil {
		t.Fatal("Ctrl+K did not open the action panel")
	}
	lt.wantText("Copy Path")
	lt.wantText("Search for actions…")
	lt.typeText("copy")
	if s := lt.Controller.Snapshot(); s.Panel == nil || len(s.Panel.Actions) != 1 || s.Panel.Text != "copy" {
		t.Fatalf("filtered panel = %+v", s.Panel)
	}
	if got := lt.Controller.Snapshot().Text; got != "terminal" {
		t.Fatalf("the search text changed to %q", got)
	}
	lt.key(0, mui.KeyEnter)
	if eff := lt.rec.Effects(); len(eff) != 1 || eff[0].Kind != plugin.ActionCopyText || eff[0].Target != `C:\term.exe` {
		t.Fatalf("effects = %+v", eff)
	}
	if lt.Controller.Snapshot().Panel != nil {
		t.Fatal("the panel should close")
	}
	lt.wantText("Copied to clipboard") // the toast

	// Escape closes the panel without running anything.
	lt.rec.Reset()
	lt.l.Shown()
	lt.step()
	hidden := lt.host.hidden.Load()
	lt.typeText("terminal")
	lt.key(mui.Ctrl, mui.KeyK)
	lt.key(0, mui.KeyEscape)
	if lt.Controller.Snapshot().Panel != nil || len(lt.rec.Effects()) != 0 || lt.host.hidden.Load() != hidden {
		t.Fatal("Escape should only close the panel")
	}
}

func TestEscape(t *testing.T) {
	lt := newLauncher(t, "en")
	lt.typeText("abc")
	lt.key(0, mui.KeyEscape)
	if s := lt.Controller.Snapshot(); s.Text != "" {
		t.Fatalf("Escape should clear the text, got %q", s.Text)
	}
	lt.wantText("Browse Fruits") // the empty root is back
	if lt.host.hidden.Load() != 0 {
		t.Fatal("hidden too early")
	}
	lt.key(0, mui.KeyEscape)
	if lt.host.hidden.Load() != 1 {
		t.Fatal("Escape on the empty root should hide")
	}
}

func TestViewDetailAndBack(t *testing.T) {
	lt := newLauncher(t, "en")
	lt.typeText("browse fruits")
	lt.key(0, mui.KeyEnter)
	s := lt.Controller.Snapshot()
	if s.Depth != 1 || !s.ShowDetail {
		t.Fatalf("snapshot = %+v", s)
	}
	lt.wantText("Fruits") // the view title
	lt.wantText("Weight")
	lt.wantText("Apple grams")
	lt.key(0, mui.KeyDown)
	lt.wantText("Banana grams")

	// A destructive action asks first.
	lt.key(mui.Ctrl, mui.KeyX)
	if lt.Controller.Snapshot().Confirm == nil {
		t.Fatal("no confirmation for a destructive action")
	}
	lt.wantText("Cancel")
	lt.key(0, mui.KeyEscape)
	if lt.Controller.Snapshot().Confirm != nil {
		t.Fatal("Escape should cancel the confirmation")
	}

	// Backspace in the empty field goes back to the root.
	lt.key(0, mui.KeyBackspace)
	if s := lt.Controller.Snapshot(); s.Depth != 0 {
		t.Fatalf("depth after Backspace = %d", s.Depth)
	}
}

func TestClickActivates(t *testing.T) {
	lt := newLauncher(t, "en")
	lt.typeText("terminal")
	if err := lt.tt.Click("Windows Terminal"); err != nil {
		t.Fatal(err)
	}
	lt.step()
	if eff := lt.rec.Effects(); len(eff) != 1 || eff[0].Target != `C:\term.exe` {
		t.Fatalf("effects = %+v", eff)
	}
}

func newSettings(t *testing.T, locale string) (*app.Env, *ui.Settings, *mui.Tester) {
	env, _ := newEnv(t, locale)
	s := ui.NewSettings(env, &fakeHost{}, "1.0.0-test")
	return env, s, mui.NewTester(s.View, ui.SettingsWidth, ui.SettingsHeight)
}

func TestSettingsGeneral(t *testing.T) {
	env, _, tt := newSettings(t, "en")
	if !env.Config.Get().HideOnBlur {
		t.Fatal("HideOnBlur should default to true")
	}
	if err := tt.Click("Hide when focus is lost"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if env.Config.Get().HideOnBlur {
		t.Fatal("clicking the setting should turn HideOnBlur off")
	}
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := env.Config.Get().Theme; got != "dark" {
		t.Fatalf("theme = %q", got)
	}

	// Record a new launcher hotkey.
	if err := tt.Click("Space"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Press keys…") {
		t.Fatalf("not recording: %q", tt.Texts())
	}
	tt.Key(mui.Ctrl|mui.Shift, mui.KeyK)
	tt.Frame()
	if got := env.Config.Get().Hotkey; got != "Ctrl+Shift+K" {
		t.Fatalf("hotkey = %q", got)
	}
}

func TestSettingsPlugins(t *testing.T) {
	env, s, tt := newSettings(t, "en")
	s.ShowPage(ui.PagePlugins)
	tt.Frame()
	if !tt.HasText("Test") || !tt.HasText("1.2.3") {
		t.Fatalf("plugins page = %q", tt.Texts())
	}
	if err := tt.Click("Configure"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"Browse Fruits", "clipboard", "Maximum items", "Keep images", "Ctrl"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Keep images"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := string(env.Config.Get().Plugin("test").Preferences["images"]); got != "false" {
		t.Fatalf("images preference = %q", got)
	}
	if err := tt.Click("Show Browse Fruits in root search"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !env.Config.Get().Plugin("test").Commands["list"].HideFromRoot {
		t.Fatal("the command should be hidden from the root search")
	}
	if err := tt.Click("Enable Test"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !env.Config.Get().Plugin("test").Disabled {
		t.Fatal("the plugin should be disabled")
	}
}

func TestSettingsAdvanced(t *testing.T) {
	env, s, tt := newSettings(t, "en")
	env.Config.Update(func(c *config.Config) { c.LogLevel = "info" })
	s.ShowPage(ui.PageAdvanced)
	tt.Frame()
	if err := tt.Click("Debug"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := env.Config.Get().LogLevel; got != "debug" {
		t.Fatalf("log level = %q", got)
	}
	if err := tt.Click("Clear"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Ranking history cleared.") {
		t.Fatalf("texts = %q", tt.Texts())
	}
}

// TestSnapshots renders the windows to PNG files, in the light and dark
// themes and the pseudo-locale, which must not break the layout code. They
// go to a temporary directory, or to $GOLAUNCHER_SNAPSHOTS to look at them.
func TestSnapshots(t *testing.T) {
	dir := cmp.Or(os.Getenv("GOLAUNCHER_SNAPSHOTS"), t.TempDir())
	save := func(name string, tt *mui.Tester) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	for _, locale := range []string{"en", "en-XA"} {
		for _, dark := range []bool{false, true} {
			name := locale
			if dark {
				name += "-dark"
			}
			lt := newLauncher(t, locale)
			lt.tt.SetDark(dark)
			lt.typeText("t")
			lt.key(mui.Ctrl, mui.KeyK)
			save("launcher-"+name, lt.tt)
			if locale == "en-XA" && !slices.ContainsFunc(lt.tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "[") }) {
				t.Fatalf("no pseudo-localized text in %q", lt.tt.Texts())
			}
			_, s, tt := newSettings(t, locale)
			tt.SetDark(dark)
			for _, page := range []string{ui.PageGeneral, ui.PagePlugins, ui.PageAdvanced, ui.PageAbout} {
				s.ShowPage(page)
				tt.Frame()
				save("settings-"+page+"-"+name, tt)
			}
		}
	}
	img := mui.Render(func(c *mui.Context) { mui.Text(c, "golauncher") }, 200, 60, 2)
	if img.Bounds().Dx() != 400 {
		t.Fatalf("Render size = %v", img.Bounds())
	}
	t.Logf("snapshots in %s", dir)
}
