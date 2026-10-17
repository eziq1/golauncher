package ui_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/internal/update"
)

// fakeUpdater is the update service of the About page's tests.
type fakeUpdater struct {
	mu       sync.Mutex
	st       update.Status
	checks   int
	installs int
	skipped  string
}

func (f *fakeUpdater) Status() update.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

func (f *fakeUpdater) Check(context.Context) (update.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	f.st.Latest, f.st.Available, f.st.LastCheck = "0.5.0", true, time.Date(2026, 10, 16, 9, 30, 0, 0, time.Local)
	return f.st, nil
}

func (f *fakeUpdater) InstallNow(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installs++
	return nil
}

func (f *fakeUpdater) Skip(v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skipped = v
	f.st.Skipped = true
}

func (f *fakeUpdater) OnChange(func()) {}

func (f *fakeUpdater) set(fn func(*update.Status)) {
	f.mu.Lock()
	fn(&f.st)
	f.mu.Unlock()
}

func TestSettingsUpdates(t *testing.T) {
	env, s, tt := newSettings(t, "en")
	u := &fakeUpdater{st: update.Status{Phase: update.PhaseIdle, Current: "0.4.0"}}
	s.SetUpdater(u)
	tt.SetSize(ui.SettingsWidth, 1400)
	s.ShowPage(ui.PageAbout)
	tt.Frame()
	for _, want := range []string{"Updates", "golauncher is up to date. Last checked: never", "Check for Updates", "Automatic updates", "Check for updates", "What's new"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Check for Updates"); err != nil {
		t.Fatal(err)
	}
	s.Wait()
	tt.Frame()
	if u.checks != 1 || !tt.HasText("golauncher 0.5.0 is available.") || !tt.HasText("It is being downloaded and installs when golauncher is idle.") {
		t.Fatalf("after checking (%d): %q", u.checks, tt.Texts())
	}

	u.set(func(st *update.Status) { st.Phase, st.Progress = update.PhaseDownloading, 0.42 })
	tt.Frame()
	if !tt.HasText("Downloading golauncher 0.5.0… 42%") {
		t.Fatalf("downloading: %q", tt.Texts())
	}
	u.set(func(st *update.Status) { st.Phase = update.PhaseReady })
	tt.Frame()
	if !tt.HasText("golauncher 0.5.0 is ready. It installs when golauncher is idle.") {
		t.Fatalf("ready: %q", tt.Texts())
	}
	if err := tt.Click("Restart to Update"); err != nil {
		t.Fatal(err)
	}
	s.Wait()
	if u.installs != 1 {
		t.Fatal("Restart to Update did nothing")
	}
	if err := tt.Click("Skip This Version"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if u.skipped != "0.5.0" {
		t.Fatalf("skipped %q", u.skipped)
	}

	// Only notify: the user installs.
	env.Config.Update(func(c *config.Config) { c.Updates.Mode = config.UpdatesNotify })
	u.set(func(st *update.Status) { st.Phase, st.Skipped = update.PhaseIdle, false })
	tt.Frame()
	if !tt.HasText("Download and Install") || !tt.HasText("Only notify me") {
		t.Fatalf("notify mode: %q", tt.Texts())
	}
	// Off: no frequency.
	env.Config.Update(func(c *config.Config) { c.Updates.Mode = config.UpdatesOff })
	tt.Frame()
	if tt.HasText("Every day") {
		t.Fatalf("off: %q", tt.Texts())
	}
}

func TestSettingsUpdatesPortable(t *testing.T) {
	_, s, tt := newSettings(t, "en")
	s.SetUpdater(&fakeUpdater{st: update.Status{Phase: update.PhaseIdle, Current: "0.4.0", Disabled: update.DisabledPortable, Latest: "0.5.0", Available: true}})
	tt.SetSize(ui.SettingsWidth, 1400)
	s.ShowPage(ui.PageAbout)
	tt.Frame()
	if tt.HasText("Automatic updates") || tt.HasText("Download and Install") || !tt.HasText("Check for Updates") {
		t.Fatalf("portable: %q", tt.Texts())
	}
	found := false
	for _, txt := range tt.Texts() {
		if len(txt) > 30 && txt[:30] == "This copy is not the installed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no note about the portable copy: %q", tt.Texts())
	}
}

func TestSettingsUpdatesWinget(t *testing.T) {
	_, s, tt := newSettings(t, "en")
	s.SetUpdater(&fakeUpdater{st: update.Status{Phase: update.PhaseIdle, Current: "0.4.0", Disabled: update.DisabledWinget, Latest: "0.5.0", Available: true}})
	tt.SetSize(ui.SettingsWidth, 1400)
	s.ShowPage(ui.PageAbout)
	tt.Frame()
	if tt.HasText("Automatic updates") || tt.HasText("Download and Install") || tt.HasText("Skip This Version") || !tt.HasText("Check for Updates") {
		t.Fatalf("winget: %q", tt.Texts())
	}
	if !tt.HasText("golauncher 0.5.0 is available.") {
		t.Fatalf("the newer version is not shown: %q", tt.Texts())
	}
	found := false
	for _, txt := range tt.Texts() {
		if strings.HasPrefix(txt, "Managed by winget") && strings.Contains(txt, "winget upgrade eziq1.golauncher") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no note about winget: %q", tt.Texts())
	}
}

func TestSettingsUpdatesChinese(t *testing.T) {
	_, s, tt := newSettings(t, "zh-CN")
	tt.SetLocale("zh-CN")
	s.SetUpdater(&fakeUpdater{st: update.Status{Phase: update.PhaseReady, Current: "0.4.0", Latest: "0.5.0", Available: true}})
	tt.SetSize(ui.SettingsWidth, 1400)
	s.ShowPage(ui.PageAbout)
	tt.Frame()
	checkLayout(t, tt, ui.SettingsWidth, 1400, "更新", "golauncher 0.5.0 已就绪，将在 golauncher 空闲时安装。", "检查更新", "重启以更新", "跳过此版本", "自动更新", "检查频率", "更新内容")
}
