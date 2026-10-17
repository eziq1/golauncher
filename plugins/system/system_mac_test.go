package system

import (
	"regexp"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// The macOS flavor of the plugin runs on a Fake Mac on every platform.

var macURIRe = regexp.MustCompile(`^x-apple\.systempreferences:[A-Za-z0-9.-]+(\?[A-Za-z_]+)?$`)

// TestMacCatalog checks every System Settings pane: a well-formed, unique
// URI, titles, a known category and keywords.
func TestMacCatalog(t *testing.T) {
	pages, err := loadCatalog(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 40 {
		t.Errorf("only %d settings panes", len(pages))
	}
	h, err := hostfake.New(New(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cats := categories(h.I18n(), true)
	seen := map[string]bool{}
	for _, pg := range pages {
		if !macURIRe.MatchString(pg.URI) {
			t.Errorf("bad URI %q", pg.URI)
		}
		if seen[pg.URI] {
			t.Errorf("duplicate URI %q", pg.URI)
		}
		seen[pg.URI] = true
		if pg.En == "" || pg.Zh == "" || len(pg.Keywords) == 0 || pg.Win != "" {
			t.Errorf("%s: incomplete entry %+v", pg.URI, pg)
		}
		if _, ok := cats[pg.Category]; !ok || pg.Category == "tools" {
			t.Errorf("%s: unknown category %q", pg.URI, pg.Category)
		}
	}
	for _, id := range []string{"com.apple.wifi-settings-extension", "com.apple.BluetoothSettings", "com.apple.Displays-Settings.extension",
		"com.apple.Sound-Settings.extension", "com.apple.preference.security?Privacy_Accessibility"} {
		if !seen[macSettingsPrefix+id] {
			t.Errorf("%s is missing", id)
		}
	}
}

// TestMacItems offers the macOS commands, panes and utilities: no
// hibernation, Show Desktop, Windows Settings or elevated actions.
func TestMacItems(t *testing.T) {
	fake := NewFake()
	fake.Mac, fake.Hibernation, fake.Build, fake.Edition = true, false, 0, ""
	p, h, _ := newPlugin(t, fake)
	if d := h.I18n().T(strings.Trim(p.Manifest().Description, "%")); !strings.Contains(d, "Trash") || strings.Contains(d, "Windows") {
		t.Errorf("description %q", d)
	}
	items := rootItems(t, p)
	for _, id := range []string{"system:hibernate", "system:showDesktop", "settings:display", "tool:regedit"} {
		if hasID(items, id) {
			t.Errorf("%s offered on a Mac", id)
		}
	}
	for id, title := range map[string]string{"system:lock": "Lock Screen", "system:signout": "Log Out", "system:emptyBin": "Empty Trash",
		"system:sleep": "Sleep", "settings:com.apple.wifi-settings-extension": "Wi-Fi", "tool:activityMonitor": "Activity Monitor"} {
		if it := itemByID(t, items, id); it.Title != title {
			t.Errorf("%s title %q, want %q", id, it.Title, title)
		}
	}
	if it := itemByID(t, items, "system:emptyBin"); it.Subtitle != "3 items · 1.5 MB" {
		t.Errorf("Trash subtitle %q", it.Subtitle)
	}
	for _, it := range items {
		for _, a := range it.Actions {
			if a.ID == "runas" || strings.Contains(a.Do.Target, "ms-settings:") || strings.HasSuffix(a.Do.Target, ".exe") {
				t.Errorf("%s: Windows action %+v", it.ID, a)
			}
		}
	}
	tool := itemByID(t, items, "tool:terminal")
	if tool.Accessory != "Utilities" || len(tool.Actions) != 1 || tool.Actions[0].Do.Target != "/System/Applications/Utilities/Terminal.app" {
		t.Errorf("Terminal %+v", tool)
	}
	pane := itemByID(t, items, "settings:com.apple.preference.security?Privacy_Accessibility")
	if pane.Actions[0].Do.Kind != plugin.ActionOpen || pane.Actions[0].Do.Target != "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility" {
		t.Errorf("Accessibility pane %+v", pane.Actions)
	}

	if res := run(t, p, cmdEmptyBin); res.Toast == nil || res.Toast.Title != "Trash emptied" {
		t.Errorf("empty Trash: %+v", res.Toast)
	}
	if res := run(t, p, cmdEmptyBin); res.Toast == nil || res.Toast.Title != "The Trash is already empty" {
		t.Errorf("empty Trash again: %+v", res.Toast)
	}
	if it := itemByID(t, rootItems(t, p), "system:emptyBin"); it.Subtitle != "The Trash is empty" {
		t.Errorf("Trash subtitle %q", it.Subtitle)
	}
}

// TestMacChinese uses the macOS Chinese terms.
func TestMacChinese(t *testing.T) {
	fake := NewFake()
	fake.Mac = true
	p, h, _ := newPlugin(t, fake)
	h.I18N.SetLocale("zh-CN")
	items := rootItems(t, p)
	for id, title := range map[string]string{"system:emptyBin": "清空废纸篓", "system:signout": "退出登录", "tool:keychain": "钥匙串访问"} {
		if it := itemByID(t, items, id); it.Title != title {
			t.Errorf("%s title %q, want %q", id, it.Title, title)
		}
	}
}
