package ui_test

import (
	"slices"
	"sync"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/darwin/tcc"
	"github.com/eziq1/golauncher/internal/ui"
)

// permHost is a settings host on macOS with fake permission statuses; it
// never opens System Settings nor shows a prompt.
type permHost struct {
	fakeHost
	holder string

	mu        sync.Mutex
	status    map[tcc.ID]tcc.Status
	checks    int
	opened    []tcc.ID
	requested []tcc.ID
}

func (h *permHost) PermissionStatus(id tcc.ID) tcc.Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks++
	return h.status[id]
}

func (h *permHost) RequestPermission(id tcc.ID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requested = append(h.requested, id)
	if id == tcc.AutomationFinder {
		h.status[id] = tcc.Granted // the user said OK
	}
}

func (h *permHost) OpenPermissionSettings(id tcc.ID) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.opened = append(h.opened, id)
	return nil
}

func (h *permHost) PermissionHolder() string { return h.holder }

func (h *permHost) set(id tcc.ID, st tcc.Status) {
	h.mu.Lock()
	h.status[id] = st
	h.mu.Unlock()
}

func newPermSettings(t *testing.T, locale string, host *permHost) (*ui.Settings, *mui.Tester) {
	t.Helper()
	env, _ := newEnv(t, locale)
	s := ui.NewSettings(env, host, "1.0.0-test")
	tt := mui.NewTester(s.View, ui.SettingsWidth, 1400)
	tt.SetLocale(locale)
	tt.Frame()
	return s, tt
}

// TestSettingsPermissions: the Permissions page shows only with a
// PermissionsHost (macOS), lists the three permissions with their status,
// opens their System Settings pages, asks for them, and reads the statuses
// again when asked to (the settings window got the focus back).
func TestSettingsPermissions(t *testing.T) {
	_, _, plain := newSettings(t, "en")
	plain.Frame()
	if plain.HasText("Permissions") {
		t.Fatal("a Permissions page without a PermissionsHost")
	}

	host := &permHost{status: map[tcc.ID]tcc.Status{
		tcc.Accessibility:    tcc.Denied,
		tcc.AutomationFinder: tcc.NotDetermined,
		tcc.FullDiskAccess:   tcc.Granted,
	}}
	s, tt := newPermSettings(t, "en", host)
	if err := tt.Click("Permissions"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	s.Wait()
	tt.Frame()
	for _, want := range []string{
		"Accessibility", "Automation › Finder", "Full Disk Access (optional)",
		"Not allowed", "Not asked yet — macOS will ask on first use", "Allowed",
		"Open System Settings", "Request Access", "Check Again",
	} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if !tt.HasText("When golauncher is started from a terminal (go run, or a binary that is not in a .app), macOS asks for and records these permissions for that terminal app (Terminal, iTerm2…), not for golauncher: grant them to the terminal, then restart golauncher.") {
		t.Errorf("no terminal note in %q", tt.Texts())
	}

	// The first button opens Accessibility's page.
	if err := tt.Click("Open System Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	host.mu.Lock()
	opened := slices.Clone(host.opened)
	host.mu.Unlock()
	if !slices.Equal(opened, []tcc.ID{tcc.Accessibility}) {
		t.Errorf("opened %v", opened)
	}

	// Request Access: Accessibility (denied) and Automation (not asked)
	// have one; Full Disk Access (granted) has none.
	if n := len(slices.DeleteFunc(slices.Clone(tt.Texts()), func(s string) bool { return s != "Request Access" })); n != 2 {
		t.Errorf("%d Request Access buttons", n)
	}

	// Granted in System Settings, back in the settings window.
	host.set(tcc.Accessibility, tcc.Granted)
	host.set(tcc.AutomationFinder, tcc.Denied)
	s.RefreshPermissions()
	s.Wait()
	tt.Frame()
	if tt.HasText("Not asked yet — macOS will ask on first use") || !tt.HasText("Not allowed") || tt.HasText("Request Access") {
		t.Errorf("after the refresh: %q", tt.Texts())
	}

	// Another page: a refresh does nothing.
	host.mu.Lock()
	checks := host.checks
	host.mu.Unlock()
	s.ShowPage(ui.PageGeneral)
	s.RefreshPermissions()
	s.Wait()
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.checks != checks {
		t.Error("the statuses were read with the page hidden")
	}
}

// TestSettingsPermissionsRequest: Request Access asks for the permission,
// then the statuses are read again.
func TestSettingsPermissionsRequest(t *testing.T) {
	host := &permHost{holder: "iTerm2", status: map[tcc.ID]tcc.Status{
		tcc.Accessibility:    tcc.Granted,
		tcc.AutomationFinder: tcc.NotDetermined,
		tcc.FullDiskAccess:   tcc.Unknown,
	}}
	s, tt := newPermSettings(t, "en", host)
	s.ShowPage(ui.PagePermissions)
	s.Wait()
	tt.Frame()
	if !tt.HasText("golauncher was started from iTerm2, so macOS asks for and records these permissions for iTerm2, not for golauncher. Grant them to iTerm2, then restart golauncher.") ||
		!tt.HasText("Unknown — macOS will ask on first use") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("Request Access"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	s.Wait()
	tt.Frame()
	host.mu.Lock()
	requested := slices.Clone(host.requested)
	host.mu.Unlock()
	if !slices.Equal(requested, []tcc.ID{tcc.AutomationFinder}) {
		t.Errorf("requested %v", requested)
	}
	if tt.HasText("Not asked yet — macOS will ask on first use") || tt.HasText("Request Access") {
		t.Errorf("after the request: %q", tt.Texts())
	}
}

// TestSettingsPermissionsChinese: Apple's own Chinese names.
func TestSettingsPermissionsChinese(t *testing.T) {
	host := &permHost{status: map[tcc.ID]tcc.Status{}}
	s, tt := newPermSettings(t, "zh-CN", host)
	s.ShowPage(ui.PagePermissions)
	s.Wait()
	tt.Frame()
	for _, want := range []string{"权限", "辅助功能", "自动化 › 访达", "完全磁盘访问权限（可选）", "打开系统设置", "未知——首次使用时 macOS 会询问"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	checkLayout(t, tt, ui.SettingsWidth, 1400, "辅助功能", "自动化 › 访达", "完全磁盘访问权限（可选）")
}
