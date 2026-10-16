package ui_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai"
	"github.com/eziq1/golauncher/plugins/ai/backend"
	"github.com/eziq1/golauncher/plugins/clipboard"
)

// confirmation runs ConfirmExternal in the background.
type confirmation struct {
	done chan core.Decision
}

func ask(lt *launcherTest, req core.ExternalRequest) *confirmation {
	c := &confirmation{done: make(chan core.Decision, 1)}
	go func() { c.done <- lt.Controller.ConfirmExternal(context.Background(), req) }()
	return c
}

func (c *confirmation) wait(t *testing.T) core.Decision {
	t.Helper()
	select {
	case d := <-c.done:
		return d
	case <-time.After(10 * time.Second):
		t.Fatal("no decision")
		return 0
	}
}

// waitShown waits until n requests have been shown.
func waitShown(t *testing.T, shown func() int, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for shown() < n {
		if time.Now().After(deadline) {
			t.Fatalf("shown %d of %d", shown(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestExternalConfirm shows an AI client's request over the launcher, asks
// the front end to show the window, and answers it with the keys.
func TestExternalConfirm(t *testing.T) {
	lt := newLauncher(t, "en")
	var mu sync.Mutex
	var shown []core.ExternalRequest
	lt.Controller.OnExternalConfirm(func(r core.ExternalRequest) {
		mu.Lock()
		shown = append(shown, r)
		mu.Unlock()
	})
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(shown) }

	first := ask(lt, core.ExternalRequest{Client: "Claude Desktop", Action: "Install", Item: "7-Zip", Plugin: "WinGet", AllowSession: true})
	waitShown(t, count, 1)
	lt.step()
	for _, want := range []string{"Claude Desktop wants to:", "Install “7-Zip” (WinGet)", "Allow", "Allow for this session", "Deny"} {
		lt.wantText(want)
	}
	// A second request waits for the first.
	second := ask(lt, core.ExternalRequest{Client: "Claude (AI Chat)", Action: "Shut Down", Destructive: true, EndsSession: true, AllowSession: true, InPlace: true})
	time.Sleep(50 * time.Millisecond)
	if count() != 1 {
		t.Fatal("the second request showed before the first was answered")
	}
	lt.key(0, mui.KeyEnter)
	if d := first.wait(t); d != core.DecisionAllow {
		t.Fatalf("first = %v", d)
	}
	waitShown(t, count, 2)
	lt.step()
	lt.wantText("Claude (AI Chat) wants to:")
	lt.wantText("Open apps will be closed. Save your work first.")
	if lt.tt.HasText("Allow for this session") {
		t.Fatal("a destructive action offers Allow for this session")
	}
	mu.Lock()
	if !shown[1].InPlace || shown[1].AllowSession {
		t.Errorf("shown %+v", shown[1])
	}
	mu.Unlock()
	lt.key(mui.Ctrl, mui.KeyEnter) // not offered: nothing happens
	lt.key(0, mui.KeyEscape)
	if d := second.wait(t); d != core.DecisionDeny {
		t.Fatalf("second = %v", d)
	}
	lt.step()
	if lt.tt.HasText("Deny") || lt.Controller.Snapshot().Confirm != nil {
		t.Fatal("the dialog is still open")
	}

	// Allow for this session.
	third := ask(lt, core.ExternalRequest{Client: "Claude Code", Action: "Reload Applications", AllowSession: true})
	waitShown(t, count, 3)
	lt.step()
	lt.key(mui.Ctrl, mui.KeyEnter)
	if d := third.wait(t); d != core.DecisionAllowSession {
		t.Fatalf("third = %v", d)
	}
	// The launcher's own state is untouched: the root search still works.
	lt.typeText("vsc")
	lt.wantText("Visual Studio Code")
}

// TestExternalConfirmTimeout denies a request nobody answers.
func TestExternalConfirmTimeout(t *testing.T) {
	old := core.ExternalTimeout
	core.ExternalTimeout = 200 * time.Millisecond
	defer func() { core.ExternalTimeout = old }()
	lt := newLauncher(t, "en")
	c := ask(lt, core.ExternalRequest{Client: "Claude Desktop", Action: "Lock"})
	if d := c.wait(t); d != core.DecisionDeny {
		t.Fatalf("decision %v", d)
	}
	lt.step()
	if lt.Controller.Snapshot().Confirm != nil {
		t.Fatal("the dialog stayed")
	}
	// A cancelled request is withdrawn too.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan core.Decision, 1)
	go func() { done <- lt.Controller.ConfirmExternal(ctx, core.ExternalRequest{Client: "x", Action: "y"}) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if d := <-done; d != core.DecisionDeny || lt.Controller.Snapshot().Confirm != nil {
		t.Fatalf("cancelled: %v %+v", d, lt.Controller.Snapshot().Confirm)
	}
}

// TestExternalConfirmChinese shows the dialog in Chinese.
func TestExternalConfirmChinese(t *testing.T) {
	lt := newLauncher(t, "zh-CN")
	c := ask(lt, core.ExternalRequest{Client: "Claude 桌面版", Action: "安装", Item: "7-Zip", AllowSession: true})
	deadline := time.Now().Add(5 * time.Second)
	for lt.Controller.Snapshot().Confirm == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	lt.step()
	lt.wantText("Claude 桌面版想要：")
	lt.wantText("本次会话内都允许")
	lt.key(0, mui.KeyEscape)
	if d := c.wait(t); d != core.DecisionDeny {
		t.Fatalf("decision %v", d)
	}
}

// TestSettingsAgentSection shows the AI tools on the AI plugin's page: the
// switches change the configuration, Connect to Claude Desktop writes (only)
// the test file, and the Claude Code command is shown.
func TestSettingsAgentSection(t *testing.T) {
	clip := &hostfake.Clipboard{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{ai.NewWithOptions(ai.Options{Backend: &backend.Fake{}, Sync: true, HasProtocol: func(string) bool { return false }}),
			clipboard.New(), testPlugin{}},
		Fake: true, Locale: "en", System: &app.Recorder{Clipboard: clip}, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	dir := filepath.Join(t.TempDir(), "Claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "claude_desktop_config.json")
	os.WriteFile(cfgPath, []byte(`{"theme":"dark","mcpServers":{"other":{"command":"x"}}}`), 0o644)
	exe := `C:\Program Files\golauncher\golauncher.exe`
	defer ui.SetDesktop(cfgPath, exe)()

	s := ui.NewSettings(env, &fakeHost{}, "1.0.0-test")
	tt := mui.NewTester(s.View, ui.SettingsWidth, 3000)
	s.ShowPage(ui.PagePlugins)
	tt.Frame()
	if err := tt.Click("Configure"); err != nil { // the AI plugin is first
		t.Fatal(err)
	}
	tt.Frame()
	s.Wait()
	tt.Frame()
	for _, want := range []string{"AI Tools", "Allow AI apps to use golauncher", "Plugins AI may use", "Clipboard History", "Connect to Claude Desktop",
		`claude mcp add golauncher -- "` + exe + `" mcp`} {
		found := false
		for _, txt := range tt.Texts() {
			if strings.Contains(txt, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Allow AI apps to use golauncher"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !env.Config.Get().Agent.External {
		t.Fatal("external access not turned on")
	}
	if err := tt.Click("Clipboard History"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if v, ok := env.Config.Get().Agent.Plugins["builtin.clipboard"]; !ok || !v {
		t.Fatalf("clipboard exposure: %v", env.Config.Get().Agent)
	}
	if err := tt.Click("Ask every time"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !env.Config.Get().Agent.AskOpen {
		t.Fatal("open policy not changed")
	}
	if err := tt.Click("Connect to Claude Desktop"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	b, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(b), `"golauncher"`) || !strings.Contains(string(b), `"theme":"dark"`) || !strings.Contains(string(b), `"other"`) {
		t.Fatalf("config %s", b)
	}
	if !tt.HasText("Connected.") && !strings.Contains(strings.Join(tt.Texts(), "\n"), "Restart Claude Desktop") {
		t.Fatalf("no result in %q", tt.Texts())
	}
}
