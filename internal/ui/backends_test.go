package ui_test

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Made-up installations of Codex (never this computer's).
const (
	appCodex      = `C:\Users\someone\AppData\Local\OpenAI\Codex\bin\codex.exe`
	npmCodex      = `C:\Users\someone\AppData\Roaming\npm\node_modules\@openai\codex\vendor\x86_64-pc-windows-msvc\codex\codex.exe`
	launcherCodex = `C:\Users\someone\AppData\Roaming\npm\codex.cmd`
)

// newBackendsSettings is the settings on the AI plugin's page, with Claude
// Code (which lists no installations) and Codex (which does).
func newBackendsSettings(t *testing.T, locale string) (*ui.Settings, *mui.Tester, *app.Env, *backend.FakeInstalls, *hostfake.Clipboard) {
	t.Helper()
	codex := &backend.FakeInstalls{Fake: &backend.Fake{}, BackendName: "Codex (ChatGPT)", Found: backend.Discovery{
		MinVersion: "0.130.0", Auto: 0, AutoReason: backend.ReasonOnly, Status: backend.StatusAvailable,
		Installs: []backend.Install{
			{Path: appCodex, Source: backend.SourceApp, Version: "0.162.0"},
			{Path: npmCodex, Source: backend.SourceNpmVendor, Version: "0.46.0", TooOld: true},
			{Path: launcherCodex, Source: backend.SourceInstallDir, Launcher: true},
		}}}
	clip := &hostfake.Clipboard{}
	p := ai.NewWithOptions(ai.Options{Sync: true, HasProtocol: func(string) bool { return false }, Backends: []ai.BackendOption{
		{ID: ai.DefaultBackendID, Backend: &backend.Fake{}}, {ID: "codex", Backend: codex},
	}})
	env, err := app.Build(t.Context(), app.Options{Plugins: []plugin.Plugin{p}, Fake: true, Locale: locale,
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	if env.AIBackends == nil {
		t.Fatal("no AI backends in the environment")
	}
	s := ui.NewSettings(env, &fakeHost{}, "1.0.0-test")
	tt := mui.NewTester(s.View, ui.SettingsWidth, 3200)
	s.ShowPage(ui.PagePlugins)
	tt.Frame()
	if err := tt.Click(map[string]string{"en": "Configure", "zh-CN": "配置"}[locale]); err != nil { // the AI plugin is first
		t.Fatal(err)
	}
	settle(s, tt)
	return s, tt, env, codex, clip
}

// settle lets the background calls finish and draws their results.
func settle(s *ui.Settings, tt *mui.Tester) {
	tt.Frame()
	s.Wait()
	tt.Frame()
	s.Wait()
	tt.Frame()
}

func hasTextPart(tt *mui.Tester, want string) bool {
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, want) {
			return true
		}
	}
	return false
}

// TestSettingsBackends: the Backends section shows each backend's status,
// the program in use and why, the other installations and why they are not
// used; the path can be copied; Re-detect looks again.
func TestSettingsBackends(t *testing.T) {
	s, tt, _, codex, clip := newBackendsSettings(t, "en")
	for _, want := range []string{
		"Backends", "Codex (ChatGPT)", "Available", "In use:", appCodex,
		"Version 0.162.0 · found in the desktop app · chosen automatically: the only usable installation",
		"Other installations",
		npmCodex + " — version 0.46.0, too old · found inside an npm installation",
		launcherCodex + " — launcher script, not used · found in a usual install folder",
		"Program", "Which installation of Codex (ChatGPT) golauncher runs.", "Automatic (recommended)", "Re-detect",
	} {
		if !hasTextPart(tt, want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	// Claude Code (a backend without installations to list) has no row.
	if hasTextPart(tt, "Which installation of Claude Code") {
		t.Fatal("a row for a backend that lists no installations")
	}
	if err := tt.Click("Copy Path"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if content, _ := clip.Read(); content.Text != appCodex || !tt.HasText("Copied") {
		t.Fatalf("clipboard %q, texts %q", content.Text, tt.Texts())
	}
	if _, refreshed := codex.Searches(); refreshed != 0 {
		t.Fatal("probed again without Re-detect")
	}
	if err := tt.Click("Re-detect"); err != nil {
		t.Fatal(err)
	}
	settle(s, tt)
	if _, refreshed := codex.Searches(); refreshed != 1 {
		t.Fatal("Re-detect did not look again")
	}
}

// TestSettingsBackendProgram: the Program choice: a detected installation,
// a custom path that is checked first (missing, too old, then fine), and a
// chosen program that disappeared.
func TestSettingsBackendProgram(t *testing.T) {
	s, tt, env, codex, _ := newBackendsSettings(t, "en")
	program := func() string {
		var v string
		if raw, ok := env.Config.Get().Plugin(ai.ID).Preferences[backend.ProgramPref("codex")]; ok {
			v = strings.Trim(string(raw), `"`)
		}
		return strings.ReplaceAll(v, `\\`, `\`)
	}
	choose := func(option string) {
		t.Helper()
		if err := tt.Click("Automatic (recommended)"); err != nil { // opens the select
			t.Fatal(err)
		}
		tt.Frame()
		if err := tt.Click(option); err != nil {
			t.Fatalf("%v in %q", err, tt.Texts())
		}
		settle(s, tt)
	}
	// Only usable installations are offered.
	if err := tt.Click("Automatic (recommended)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(appCodex+" (0.162.0)") || !tt.HasText("Custom path…") || tt.HasText(npmCodex+" (0.46.0)") {
		t.Fatalf("options %q", tt.Texts())
	}
	tt.Key(0, mui.KeyEscape)
	tt.Frame()

	// A custom path is checked before it is used.
	choose("Custom path…")
	typePath := func(path string) {
		t.Helper()
		if err := tt.Click("Path of the program"); err != nil {
			t.Fatalf("%v in %q", err, tt.Texts())
		}
		tt.Key(0, mui.KeyEnd)
		for range 200 {
			tt.Key(0, mui.KeyBackspace)
		}
		tt.Type(path)
		tt.Frame()
		if err := tt.Click("Use"); err != nil {
			t.Fatal(err)
		}
		settle(s, tt)
	}
	typePath(`C:\nowhere\codex.exe`)
	if !tt.HasText("There is no file at this path.") || program() != "" {
		t.Fatalf("missing path: %q, program %q", tt.Texts(), program())
	}
	typePath(npmCodex)
	if !tt.HasText("Version 0.46.0 is too old: Codex (ChatGPT) needs 0.130.0 or newer.") || program() != "" {
		t.Fatalf("too old: %q, program %q", tt.Texts(), program())
	}
	typePath(appCodex)
	settle(s, tt)
	if program() != appCodex || codex.Program() != appCodex {
		t.Fatalf("program %q, backend's %q, texts %q", program(), codex.Program(), tt.Texts())
	}
	if !hasTextPart(tt, "chosen by you") || !tt.HasText(appCodex+" (0.162.0)") || tt.HasText("Path of the program") {
		t.Fatalf("after choosing: %q", tt.Texts())
	}

	// Back to automatic.
	if err := tt.Click(appCodex + " (0.162.0)"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Automatic (recommended)"); err != nil {
		t.Fatal(err)
	}
	settle(s, tt)
	if program() != "" {
		t.Fatalf("program %q", program())
	}

	// A chosen program that is gone: the automatic choice, with a warning.
	env.Config.Update(func(c *config.Config) {
		pc := c.Plugins[ai.ID]
		pc.Preferences[backend.ProgramPref("codex")] = jsontext.Value(`"C:\\gone\\codex.exe"`)
		c.Plugins[ai.ID] = pc
	})
	if err := tt.Click("Re-detect"); err != nil {
		t.Fatal(err)
	}
	settle(s, tt)
	if !tt.HasText(`The chosen program C:\gone\codex.exe no longer exists, so golauncher uses the one it finds automatically.`) {
		t.Fatalf("no warning in %q", tt.Texts())
	}
}

// TestSettingsBackendsChinese: the section in Chinese.
func TestSettingsBackendsChinese(t *testing.T) {
	_, tt, _, _, _ := newBackendsSettings(t, "zh-CN")
	for _, want := range []string{"后端", "可用", "重新检测", "其他安装", "启动脚本，不使用", "自动（推荐）", "版本 0.162.0 · 在桌面应用里找到 · 自动选择：唯一能用的一份"} {
		if !hasTextPart(tt, want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
}
