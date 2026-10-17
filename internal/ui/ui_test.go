package ui_test

import (
	"cmp"
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
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
			{Name: "token", Type: plugin.PrefPassword, Title: "API token"},
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

	// The file dialogs answer with these paths ("" cancels).
	exportPath, importPath string
	exportName             string // the name suggested for the export
	exports, imports       int    // the dialogs shown

	// The theme file dialogs answer with these paths ("" cancels).
	themeSavePath, themeOpenPath string
	themeSaveName                string
	previews                     int // PreviewLauncher calls

	// The launcher's open dialog (ChooseFiles) answers with chosen.
	mu         sync.Mutex
	chosen     []string
	chooseOpts []plugin.ChooseFilesOptions
}

func (h *fakeHost) ChooseThemeFile(save bool, name string, done func(string, error)) {
	if save {
		h.themeSaveName = name
		done(h.themeSavePath, nil)
		return
	}
	done(h.themeOpenPath, nil)
}

func (h *fakeHost) PreviewLauncher() { h.previews++ }

func (h *fakeHost) ChooseExportFile(name string, done func(string, error)) {
	h.exports++
	h.exportName = name
	done(h.exportPath, nil)
}

func (h *fakeHost) ChooseImportFile(done func(string, error)) {
	h.imports++
	done(h.importPath, nil)
}

func (h *fakeHost) Hide()         { h.hidden.Add(1) }
func (h *fakeHost) OpenSettings() { h.settings.Add(1) }
func (h *fakeHost) Invalidate()   {}

// ChooseFiles answers the launcher's open dialog with chosen (nil cancels)
// and records the options.
func (h *fakeHost) ChooseFiles(o plugin.ChooseFilesOptions) ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.chooseOpts = append(h.chooseOpts, o)
	return h.chosen, nil
}

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
	return newEnvThemes(t, locale, "")
}

// newEnvThemes is newEnv with user themes in themesDir.
func newEnvThemes(t *testing.T, locale, themesDir string) (*app.Env, *app.Recorder) {
	t.Helper()
	clip := &hostfake.Clipboard{}
	rec := &app.Recorder{Clipboard: clip}
	env, err := app.Build(t.Context(), app.Options{
		Plugins:   []plugin.Plugin{testPlugin{}},
		Fake:      true,
		Locale:    locale,
		System:    rec,
		Clipboard: clip,
		ThemesDir: themesDir,
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
	return newSettingsWith(t, locale, &fakeHost{})
}

func newSettingsWith(t *testing.T, locale string, host *fakeHost) (*app.Env, *ui.Settings, *mui.Tester) {
	env, _ := newEnv(t, locale)
	s := ui.NewSettings(env, host, "1.0.0-test")
	return env, s, mui.NewTester(s.View, ui.SettingsWidth, ui.SettingsHeight)
}

// missingPlugin cannot start on this system.
type missingPlugin struct{}

func (missingPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "missing", Name: "Missing", Version: "0.1.0"}
}

func (missingPlugin) Init(context.Context, plugin.Host) error {
	return fmt.Errorf("thing is not installed: %w", plugin.ErrUnavailable)
}
func (missingPlugin) Close() error { return nil }

func TestSettingsUnavailablePlugin(t *testing.T) {
	clip := &hostfake.Clipboard{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{missingPlugin{}}, Fake: true, Locale: "en",
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	s := ui.NewSettings(env, &fakeHost{}, "1.0.0-test")
	tt := mui.NewTester(s.View, ui.SettingsWidth, ui.SettingsHeight)
	s.ShowPage(ui.PagePlugins)
	tt.Frame()
	if !tt.HasText("Not available on this system: thing is not installed") {
		t.Fatalf("texts = %q", tt.Texts())
	}
	// Only Background Tasks and AI Activity, which the host always provides,
	// have a Configure button.
	if n := len(slices.DeleteFunc(slices.Clone(tt.Texts()), func(s string) bool { return s != "Configure" })); n > 2 {
		t.Fatal("an unavailable plugin cannot be configured")
	}
	if err := tt.Click("Enable Missing"); err == nil {
		tt.Frame()
	}
	if env.Config.Get().Plugin("missing").Disabled {
		t.Fatal("the disabled switch changed the configuration")
	}
}

func TestSettingsPassword(t *testing.T) {
	env, s, tt := newSettings(t, "en")
	tt.SetSize(ui.SettingsWidth, 1400) // the whole plugin page, without scrolling
	s.ShowPage(ui.PagePlugins)
	tt.Frame()
	if err := tt.Click("Configure"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"API token", "Not set", "Stored in Windows Credential Manager, not in the configuration file."} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("API token"); err != nil { // the label focuses the input
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("s3cret")
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	secrets := env.Services.Secrets
	if got, ok, _ := secrets.Get("test", "token"); !ok || got != "s3cret" {
		t.Fatalf("secret = %q, %v", got, ok)
	}
	if !tt.HasText("Saved") {
		t.Fatalf("no Saved status in %q", tt.Texts())
	}
	data, err := config.Export(env.Config.Get())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cret") || strings.Contains(string(data), "token") {
		t.Fatalf("the configuration has the password:\n%s", data)
	}

	// The input starts empty again; Enter saves too.
	if err := tt.Click("API token"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("other")
	tt.Key(0, mui.KeyEnter)
	tt.Frame()
	if got, _, _ := secrets.Get("test", "token"); got != "other" {
		t.Fatalf("secret after Enter = %q", got)
	}

	if err := tt.Click("Clear"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok, _ := secrets.Get("test", "token"); ok {
		t.Fatal("Clear should delete the secret")
	}
	if !tt.HasText("Not set") {
		t.Fatalf("no Not set status in %q", tt.Texts())
	}
}

func TestSettingsExportImport(t *testing.T) {
	dir := t.TempDir()
	host := &fakeHost{exportPath: filepath.Join(dir, "out.json")}
	env, s, tt := newSettingsWith(t, "en", host)
	env.Config.Update(func(c *config.Config) { c.Theme = "dark"; c.Hotkey = "Ctrl+Space" })
	s.ShowPage(ui.PageAdvanced)
	tt.Frame()

	if err := tt.Click("Export settings…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if host.exports != 1 || host.exportName != ui.ExportFileName {
		t.Fatalf("export dialog: %d, %q", host.exports, host.exportName)
	}
	if !tt.HasText("Settings exported to " + host.exportPath + ".") {
		t.Fatalf("texts = %q", tt.Texts())
	}
	data, err := os.ReadFile(host.exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := config.Import(data); err != nil || got.Theme != "dark" || got.Hotkey != "Ctrl+Space" {
		t.Fatalf("exported %+v, %v", got, err)
	}

	// Import replaces everything: what the file leaves out is reset.
	host.importPath = filepath.Join(dir, "in.json")
	os.WriteFile(host.importPath, []byte(`{"schemaVersion": 1, "theme": "light",
		"plugins": {"test": {"preferences": {"max": 5, "token": "old-secret"}}}}`), 0o644)
	if err := tt.Click("Import settings…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Settings imported from " + host.importPath + ".") {
		t.Fatalf("texts = %q", tt.Texts())
	}
	cfg := env.Config.Get()
	if cfg.Theme != "light" || cfg.Hotkey != config.DefaultHotkey || string(cfg.Plugin("test").Preferences["max"]) != "5" {
		t.Fatalf("imported config = %+v", cfg)
	}
	if _, ok := cfg.Plugin("test").Preferences["token"]; ok {
		t.Fatal("an imported password stayed in the configuration")
	}
	if got, _, _ := env.Services.Secrets.Get("test", "token"); got != "old-secret" {
		t.Fatalf("imported secret = %q", got)
	}

	// A bad file changes nothing and says why.
	os.WriteFile(host.importPath, []byte(`{"schemaVersion": 99}`), 0o644)
	if err := tt.Click("Import settings…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.ContainsFunc(tt.Texts(), func(s string) bool { return strings.HasPrefix(s, "Could not import the settings:") }) {
		t.Fatalf("texts = %q", tt.Texts())
	}
	if env.Config.Get().Theme != "light" {
		t.Fatal("a failed import changed the configuration")
	}

	// Canceling does nothing.
	host.importPath = ""
	if err := tt.Click("Import settings…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if host.imports != 3 || env.Config.Get().Theme != "light" {
		t.Fatalf("imports = %d", host.imports)
	}
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
	if tt.HasText("Theme") {
		t.Fatal("the theme moved to the Appearance page")
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
	// The engine closes it (R88): its views are gone, while its settings
	// still show so it can be configured and turned on again.
	env.Controller.Wait()
	nav := core.Nav{Plugin: "test", Request: plugin.ViewRequest{ID: "list"}}
	if _, err := env.Engine.View(t.Context(), nav); err == nil {
		t.Fatal("a disabled plugin still shows its views")
	}
	tt.Frame()
	if !tt.HasText("Maximum items") {
		t.Fatalf("the disabled plugin's settings are gone: %q", tt.Texts())
	}
	if err := tt.Click("Enable Test"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	env.Controller.Wait()
	if env.Config.Get().Plugin("test").Disabled {
		t.Fatal("the plugin should be enabled")
	}
	if _, err := env.Engine.View(t.Context(), nav); err != nil {
		t.Fatalf("turned on again: %v", err)
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
// themes, the pseudo-locale and Simplified Chinese, which must not break the
// layout code. They
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
	for _, locale := range []string{"en", "en-XA", "zh-CN"} {
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
			for _, page := range []string{ui.PageGeneral, ui.PageAppearance, ui.PagePlugins, ui.PageAdvanced, ui.PageAbout} {
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

// emptyView is a plugin whose view has a section without items, which once
// crashed the footer (Selected == -1).
type emptyView struct{}

func (emptyView) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "empty", Name: "Empty", Version: "1",
		Commands: []plugin.Command{{Name: "find", Title: "Find Nothing", Mode: plugin.ModeView}}}
}
func (emptyView) Init(context.Context, plugin.Host) error { return nil }
func (emptyView) Close() error                            { return nil }
func (emptyView) View(context.Context, plugin.ViewRequest) (plugin.View, error) {
	return plugin.View{Kind: plugin.ViewList, ShowDetail: true, Sections: []plugin.Section{{Title: "Results"}}}, nil
}

func TestEmptySectionDoesNotCrash(t *testing.T) {
	clip := &hostfake.Clipboard{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{emptyView{}}, Fake: true, Locale: "en",
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	lt := &launcherTest{Env: env, t: t, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	if _, err := env.Controller.OpenCommand(t.Context(), "empty", "find"); err != nil {
		t.Fatal(err)
	}
	lt.step()
	lt.typeText("x")
	lt.key(0, mui.KeyDown)
	lt.key(0, mui.KeyEnter)
	lt.key(mui.Ctrl, mui.KeyK)
}

// meters is a root provider whose items have Accessories, as the process
// manager's rows have their CPU and memory use.
type meters struct{}

func (meters) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "meters", Name: "Meters", Version: "1"}
}
func (meters) Init(context.Context, plugin.Host) error { return nil }
func (meters) Close() error                            { return nil }
func (meters) RootItems(context.Context, plugin.Query) ([]plugin.Item, error) {
	f := func(v float64) *float64 { return &v }
	return []plugin.Item{
		{ID: "a", Title: "Worker Alpha", Accessory: "Process", Accessories: []plugin.Accessory{
			{Text: "1%", Meter: f(0.01), Load: true}, {Text: "12 MB"}}},
		{ID: "b", Title: "Worker Beta", Accessory: "Process", Accessories: []plugin.Accessory{
			{Text: "100.0%", Meter: f(1), Load: true}, {Text: "3.4 GB"}}},
		{ID: "c", Title: "Worker Gamma", Accessories: []plugin.Accessory{{Meter: f(0.6)}, {Meter: f(2)}, {}}},
	}, nil
}

// TestRowAccessories shows the values and bars of Item.Accessories at the
// right edge, the texts right-aligned in a fixed width: a value changing
// length does not move the row.
func TestRowAccessories(t *testing.T) {
	clip := &hostfake.Clipboard{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{meters{}}, Fake: true, Locale: "en",
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	lt := &launcherTest{Env: env, t: t, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	lt.typeText("worker")
	for _, s := range []string{"Worker Alpha", "Process", "1%", "12 MB", "Worker Beta", "100.0%", "3.4 GB"} {
		lt.wantText(s)
	}
	short, _ := lt.tt.Find("1%")
	long, _ := lt.tt.Find("100.0%")
	if short.W != long.W || short.X+short.W != long.X+long.W {
		t.Errorf("CPU values %+v and %+v: want the same width and right edge", short, long)
	}
	lt.tt.SetDark(true) // the other theme's colors
	lt.key(0, mui.KeyDown)
}

// ctxRows is a root provider with many items and a context provider, for
// the context section arriving above a full list.
type ctxRows struct{}

func (ctxRows) Manifest() plugin.Manifest {
	m := plugin.Manifest{ID: "rows", Name: "Rows", Version: "1"}
	// Enough commands for a root list taller than the window, as on a real
	// machine with recent items and many plugins.
	for i := range 12 {
		m.Commands = append(m.Commands, plugin.Command{Name: fmt.Sprint("c", i), Title: fmt.Sprintf("Command %02d", i), Mode: plugin.ModeNoView})
	}
	return m
}
func (ctxRows) Init(context.Context, plugin.Host) error                        { return nil }
func (ctxRows) Close() error                                                   { return nil }
func (ctxRows) RootItems(context.Context, plugin.Query) ([]plugin.Item, error) { return nil, nil }
func (ctxRows) ContextItems(_ context.Context, c plugin.Context) ([]plugin.Item, error) {
	var items []plugin.Item
	for _, title := range []string{"Open Terminal Here", "Open Command Prompt Here", "Open Folder in VS Code", "New Folder", "New Text File", "Copy Folder Path", "Open Terminal Here as Administrator", "Search This Folder"} {
		items = append(items, plugin.Item{ID: title, Title: title, Subtitle: c.Folder, Icon: plugin.Icon{Name: "folder"},
			Actions: []plugin.Action{{ID: "open", Title: "Open", Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: c.Folder}}}})
	}
	return items, nil
}

// TestLateContextDoesNotOverlap is the bug where the context section
// arriving after the launcher showed left the list scrolled and its header
// drawn over the first row.
func TestLateContextDoesNotOverlap(t *testing.T) {
	clip := &hostfake.Clipboard{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{ctxRows{}, testPlugin{}}, Fake: true, Locale: "en",
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	release := make(chan struct{})
	env.Engine.DetectContext(func(ctx context.Context) (plugin.Context, bool) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return plugin.Context{App: plugin.AppInfo{Name: "explorer.exe"}, Kind: plugin.ContextExplorer, Folder: `C:\Users\ann\Photos`}, true
	})
	lt := &launcherTest{Env: env, t: t, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	close(release)
	for range 50 {
		lt.step()
		if lt.tt.HasText("In Photos") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	header, ok1 := lt.tt.Find("In Photos")
	first, ok2 := lt.tt.Find("Open Terminal Here")
	if !ok1 || !ok2 {
		t.Fatalf("header found %v, first row found %v; texts %q", ok1, ok2, lt.tt.Texts())
	}
	if header.Y+header.H > first.Y {
		t.Errorf("the section header %+v overlaps the first row %+v", header, first)
	}
	if s := env.Controller.Snapshot(); s.Selected != 0 {
		t.Errorf("selected row %d, want the context's first row", s.Selected)
	}
}

// TestLateContextWithPointerOnList is the follow-up: with the mouse resting
// over the list, the rows shifting under it when the context arrived counted
// as hovering, which kept a lower row selected and the list scrolled past the
// context section.
func TestLateContextWithPointerOnList(t *testing.T) {
	clip := &hostfake.Clipboard{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{ctxRows{}, testPlugin{}}, Fake: true, Locale: "en",
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	release := make(chan struct{})
	env.Engine.DetectContext(func(ctx context.Context) (plugin.Context, bool) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return plugin.Context{App: plugin.AppInfo{Name: "explorer.exe"}, Kind: plugin.ContextExplorer, Folder: `C:\Users\ann\Photos`}, true
	})
	lt := &launcherTest{Env: env, t: t, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	// The pointer rests over the list and has moved there once (armed), then
	// moves onto the third row: hovering selects it.
	lt.tt.Move(300, 200)
	lt.step()
	lt.tt.Move(300, 260)
	lt.step()
	if s := env.Controller.Snapshot(); s.Selected == 0 {
		t.Fatalf("hovering did not select a row (texts %q)", lt.tt.Texts())
	}
	close(release)
	for range 50 {
		lt.step()
		if lt.tt.HasText("In Photos") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	lt.step() // a frame with the rows moved under the resting pointer
	s := env.Controller.Snapshot()
	if s.Selected != 0 {
		e, _ := s.SelectedEntry()
		t.Errorf("selected row %d %q, want the context's first row", s.Selected, e.Title)
	}
	header, ok1 := lt.tt.Find("In Photos")
	first, ok2 := lt.tt.Find("Open Terminal Here")
	if !ok1 || !ok2 {
		t.Fatalf("header visible %v, first row visible %v", ok1, ok2)
	}
	if header.Y < 40 || header.Y+header.H > first.Y {
		t.Errorf("header %+v and first row %+v: want the header at the top of the list, above the row", header, first)
	}
}
