package system

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	winsys "github.com/eziq1/golauncher/internal/winapi/system"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// All tests use a Fake system: nothing here locks, sleeps or shuts down the
// computer, empties the Recycle Bin or changes the theme or the volume.

// newPlugin returns an initialized plugin on fake (a new Fake when nil).
func newPlugin(t *testing.T, fake *Fake) (*Plugin, *hostfake.Host, *Fake) {
	t.Helper()
	if fake == nil {
		fake = NewFake()
	}
	p := NewWithOptions(Options{System: fake, Sync: true})
	p.delay = 0
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p, h, fake
}

func rootItems(t *testing.T, p *Plugin) []plugin.Item {
	t.Helper()
	items, err := p.RootItems(t.Context(), plugin.Query{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func itemByID(t *testing.T, items []plugin.Item, id string) plugin.Item {
	t.Helper()
	i := slices.IndexFunc(items, func(it plugin.Item) bool { return it.ID == id })
	if i < 0 {
		t.Fatalf("no item %s", id)
	}
	return items[i]
}

func hasID(items []plugin.Item, id string) bool {
	return slices.ContainsFunc(items, func(it plugin.Item) bool { return it.ID == id })
}

var uriRe = regexp.MustCompile(`^ms-settings:[a-z0-9]+(-[a-z0-9]+)*$`)

// TestCatalog checks every Settings page: a well-formed, unique URI, English
// and Chinese titles, a known category and a valid Windows version.
func TestCatalog(t *testing.T) {
	pages, err := loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 140 {
		t.Errorf("only %d settings pages", len(pages))
	}
	h, err := hostfake.New(New(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cats := categories(h.I18n())
	seen := map[string]bool{}
	for _, pg := range pages {
		if !uriRe.MatchString(pg.URI) {
			t.Errorf("bad URI %q", pg.URI)
		}
		if seen[pg.URI] {
			t.Errorf("duplicate URI %q", pg.URI)
		}
		seen[pg.URI] = true
		if pg.En == "" || strings.ContainsFunc(pg.En, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			t.Errorf("%s: English title %q", pg.URI, pg.En)
		}
		if !strings.ContainsFunc(pg.Zh, func(r rune) bool { return unicode.Is(unicode.Han, r) }) && (pg.Zh == "" || strings.ContainsFunc(pg.Zh, unicode.IsLower)) {
			t.Errorf("%s: Chinese title %q", pg.URI, pg.Zh)
		}
		if _, ok := cats[pg.Category]; !ok || pg.Category == "tools" {
			t.Errorf("%s: unknown category %q", pg.URI, pg.Category)
		}
		if pg.Win != "" && pg.Win != "10" && pg.Win != "11" {
			t.Errorf("%s: win = %q", pg.URI, pg.Win)
		}
		if len(pg.Keywords) == 0 {
			t.Errorf("%s: no keywords", pg.URI)
		}
	}
	for _, uri := range []string{"bluetooth", "display", "sound", "network-wifi", "defaultapps", "windowsupdate",
		"privacy", "powersleep", "appsfeatures", "dateandtime"} {
		if !seen[settingsPrefix+uri] {
			t.Errorf("ms-settings:%s is missing", uri)
		}
	}
}

// TestOSVersionAndEdition shows only the pages of the running Windows
// version, gpedit.msc only on Pro and above, Hibernate only when enabled.
func TestOSVersionAndEdition(t *testing.T) {
	win11 := rootItems(t, func() *Plugin { p, _, _ := newPlugin(t, nil); return p }())
	win10Home := rootItems(t, func() *Plugin {
		f := NewFake()
		f.Build, f.Edition, f.Hibernation = 19045, "Core", false
		p, _, _ := newPlugin(t, f)
		return p
	}())
	for _, c := range []struct {
		id           string
		on11, on10Hm bool
	}{
		{"settings:tabletmode", false, true},
		{"settings:family-group", true, false},
		{"settings:bluetooth", true, true},
		{"tool:gpedit", true, false},
		{"tool:lusrmgr", true, false},
		{"tool:devmgmt", true, true},
		{"system:hibernate", true, false},
	} {
		if hasID(win11, c.id) != c.on11 || hasID(win10Home, c.id) != c.on10Hm {
			t.Errorf("%s: on Windows 11 Pro %v, on Windows 10 Home %v", c.id, hasID(win11, c.id), hasID(win10Home, c.id))
		}
	}
}

// TestCommandItems checks the system commands: icons, groups, destructive
// style and EndsSession on the right ones, callbacks, and the subtitles.
func TestCommandItems(t *testing.T) {
	p, _, _ := newPlugin(t, nil)
	items := rootItems(t, p)
	destructive := map[string]bool{"restart": true, "shutdown": true, "signout": true, "emptyBin": true}
	ends := map[string]bool{"restart": true, "shutdown": true, "signout": true}
	n := 0
	for _, it := range items {
		name, ok := strings.CutPrefix(it.ID, "system:")
		if !ok {
			continue
		}
		n++
		a := it.Actions[0]
		if it.Group != "System" || it.Icon.Name == "" || len(it.Keywords) == 0 {
			t.Errorf("%s: %+v", it.ID, it)
		}
		if (a.Style == plugin.StyleDestructive) != destructive[name] || a.EndsSession != ends[name] {
			t.Errorf("%s: style %q, endsSession %v", it.ID, a.Style, a.EndsSession)
		}
		if name != cmdShowDesktop && (a.Do.Kind != plugin.ActionCallback || a.Do.Target != name) {
			t.Errorf("%s: action %+v", it.ID, a.Do)
		}
		if a.Title != it.Title {
			t.Errorf("%s: action title %q", it.ID, a.Title)
		}
	}
	if n != 13 {
		t.Errorf("%d commands", n)
	}
	if got := itemByID(t, items, "system:emptyBin").Subtitle; got != "3 items · 1.5 MB" {
		t.Errorf("recycle bin subtitle = %q", got)
	}
	if got := itemByID(t, items, "system:darkMode").Subtitle; got != "Currently light" {
		t.Errorf("dark mode subtitle = %q", got)
	}
	desk := itemByID(t, items, "system:showDesktop").Actions[0].Do
	if desk.Kind != plugin.ActionOpen || desk.Target != "explorer.exe" || !strings.Contains(string(desk.Payload), showDesktopCLSID) {
		t.Errorf("show desktop = %+v", desk)
	}
	if it := itemByID(t, items, "settings:bluetooth"); it.Title != "Bluetooth & Devices" || it.Group != "Settings" ||
		it.Accessory != "Bluetooth & devices" || it.Actions[0].Do.Kind != plugin.ActionOpen || it.Actions[0].Do.Target != "ms-settings:bluetooth" ||
		!slices.Contains(it.Keywords, "蓝牙和其他设备") || !slices.Contains(it.Keywords, "lan ya he qi ta she bei") {
		t.Errorf("bluetooth = %+v", it)
	}
	cmd := itemByID(t, items, "tool:cmdAdmin")
	if len(cmd.Actions) != 1 || !strings.Contains(string(cmd.Actions[0].Do.Payload), `"runas"`) {
		t.Errorf("cmd (admin) = %+v", cmd.Actions)
	}
}

func run(t *testing.T, p *Plugin, name string) plugin.Result {
	t.Helper()
	res, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: name})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestHandleAction runs every command against the fake and checks the
// recorded calls and the results.
func TestHandleAction(t *testing.T) {
	p, h, fake := newPlugin(t, nil)
	for _, c := range []struct {
		name string
		call string
		hide bool
	}{
		{cmdLock, "Lock", true},
		{cmdSleep, "Sleep", true},
		{cmdHibernate, "Hibernate", true},
		{cmdDisplayOff, "DisplayOff", true},
		{cmdRestart, "Restart", true},
		{cmdShutDown, "ShutDown", true},
		{cmdSignOut, "SignOut", true},
		{cmdMute, "PressMediaKey(Mute)", false},
		{cmdVolumeUp, "PressMediaKey(VolumeUp)", false},
		{cmdVolumeDown, "PressMediaKey(VolumeDown)", false},
	} {
		res := run(t, p, c.name)
		p.wg.Wait() // sleep, hibernate and display off run in the background
		if res.Hide != c.hide || res.Toast != nil {
			t.Errorf("%s: result %+v", c.name, res)
		}
		if calls := fake.Calls(); len(calls) == 0 || calls[len(calls)-1] != c.call {
			t.Errorf("%s: calls %v", c.name, calls)
		}
	}

	// Dark mode toggles and the subtitle follows.
	if res := run(t, p, cmdDarkMode); res.Toast == nil || res.Toast.Title != "Dark mode on" || !fake.Dark {
		t.Errorf("dark mode: %+v", res)
	}
	if got := itemByID(t, rootItems(t, p), "system:darkMode").Subtitle; got != "Currently dark" {
		t.Errorf("subtitle after toggling = %q", got)
	}
	if res := run(t, p, cmdDarkMode); res.Toast == nil || res.Toast.Title != "Light mode on" || fake.Dark {
		t.Errorf("light mode: %+v", res)
	}

	// Empty Recycle Bin, then again when it is empty.
	if res := run(t, p, cmdEmptyBin); res.Toast == nil || res.Toast.Title != "Recycle Bin emptied" {
		t.Errorf("empty: %+v", res)
	}
	if got := itemByID(t, rootItems(t, p), "system:emptyBin").Subtitle; got != "The Recycle Bin is empty" {
		t.Errorf("subtitle after emptying = %q", got)
	}
	n := len(fake.Calls())
	if res := run(t, p, cmdEmptyBin); res.Toast == nil || res.Toast.Title != "The Recycle Bin is already empty" || len(fake.Calls()) != n {
		t.Errorf("empty again: %+v, calls %v", res, fake.Calls())
	}
	if len(h.Toasts()) != 0 {
		t.Errorf("toasts: %+v", h.Toasts())
	}
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "nope"}); err == nil {
		t.Error("an unknown command ran")
	}
}

// TestFailures reports failed commands as toasts, also for the ones that
// run in the background.
func TestFailures(t *testing.T) {
	fake := NewFake()
	fake.Err = errors.New("access denied")
	p, h, _ := newPlugin(t, fake)
	res := run(t, p, cmdShutDown)
	if res.Hide || res.Toast == nil || res.Toast.Style != plugin.ToastFailure || res.Toast.Title != "Shut Down failed" || res.Toast.Message != "access denied" {
		t.Errorf("shut down: %+v", res)
	}
	run(t, p, cmdSleep)
	p.wg.Wait()
	if ts := h.Toasts(); len(ts) != 1 || ts[0].Title != "Sleep failed" {
		t.Errorf("toasts: %+v", ts)
	}
}

// TestCloseDropsPending: closing the plugin before the delay ends cancels a
// pending sleep.
func TestCloseDropsPending(t *testing.T) {
	fake := NewFake()
	p := NewWithOptions(Options{System: fake, Sync: true})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	run(t, p, cmdSleep)
	p.Close()
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("calls after close: %v", calls)
	}
}

// TestRefreshOnShow reads the Recycle Bin again when the launcher is shown.
func TestRefreshOnShow(t *testing.T) {
	fake := NewFake()
	p, h, _ := newPlugin(t, fake)
	fake.mu.Lock()
	fake.Bin = winsys.RecycleBin{Items: 1, Size: 10}
	fake.mu.Unlock()
	h.Bus.PublishSync(plugin.EventWindowShown)
	p.wg.Wait()
	if got := itemByID(t, rootItems(t, p), "system:emptyBin").Subtitle; got != "1 item · 10 bytes" {
		t.Errorf("subtitle = %q", got)
	}
	if !slices.Contains(h.Invalidated(), "") {
		t.Errorf("invalidated: %v", h.Invalidated())
	}
}

func TestFormatSize(t *testing.T) {
	h, err := hostfake.New(New(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l := h.I18n()
	for n, want := range map[int64]string{1: "1 byte", 1023: "1023 bytes", 1536: "1.5 KB", 1_572_864: "1.5 MB", 3 << 30: "3 GB"} {
		if got := formatSize(l, n); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestPinyin(t *testing.T) {
	for s, want := range map[string]string{"锁屏": "suo ping", "Windows 更新": "windows geng xin", "wifi": "", "人脸识别（Windows Hello）": "ren lian shi bie windows hello"} {
		if got := pinyinOf(s); got != want {
			t.Errorf("pinyinOf(%q) = %q, want %q", s, got, want)
		}
	}
	if got := withPinyin([]string{"关机", "gj", "关机"}); !slices.Equal(got, []string{"关机", "gj", "guan ji"}) {
		t.Errorf("withPinyin = %q", got)
	}
}

// TestSearch runs queries through the real engine (the host's matcher and
// ranking) with only this plugin, as the user types them.
func TestSearch(t *testing.T) {
	p := NewWithOptions(Options{System: NewFake(), Sync: true})
	env, err := app.Build(t.Context(), app.Options{Plugins: []plugin.Plugin{p}, Fake: true, Locale: "en",
		System: &app.Recorder{}, Clipboard: &hostfake.Clipboard{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	ctrl := env.Controller
	titles := func(q string) []string {
		ctrl.SetText(t.Context(), q)
		ctrl.Wait()
		var out []string
		for _, s := range ctrl.Snapshot().Sections {
			for _, it := range s.Items {
				out = append(out, it.Title)
			}
		}
		return out
	}
	for _, c := range []struct {
		query, want string
		within      int // the result is among the first within results
	}{
		{"lock", "Lock", 1},
		{"锁屏", "Lock", 1},
		{"sp", "Lock", 1},
		{"shutdown", "Shut Down", 1},
		{"关机", "Shut Down", 1},
		{"gj", "Shut Down", 1},
		{"guanji", "Shut Down", 1},
		{"重启", "Restart", 1},
		{"cq", "Restart", 1},
		{"注销", "Sign Out", 1},
		{"回收站", "Empty Recycle Bin", 1},
		{"深色模式", "Toggle Dark Mode", 1},
		{"静音", "Toggle Mute", 1},
		{"蓝牙", "Bluetooth & Devices", 1},
		{"bt", "Bluetooth & Devices", 1},
		{"lanya", "Bluetooth & Devices", 1},
		{"wifi", "Wi-Fi", 1},
		{"wlan", "Wi-Fi", 1},
		{"gx", "Windows Update", 1},
		{"更新", "Windows Update", 3},
		{"windows update", "Windows Update", 1},
		{"默认应用", "Default Apps", 1},
		{"display", "Display", 1},
		{"代理", "Proxy", 1},
		{"devmgmt", "Device Manager", 1},
		{"设备管理器", "Device Manager", 1},
		{"环境变量", "Environment Variables", 1},
		{"组策略", "Group Policy Editor", 1},
		{"regedit", "Registry Editor", 1},
		{"任务管理器", "Task Manager", 1},
	} {
		got := titles(c.query)
		i := slices.Index(got, c.want)
		if i < 0 || i >= c.within {
			t.Errorf("%q: %q at %d, want < %d: %q", c.query, c.want, i, c.within, got[:min(len(got), 5)])
		}
	}
}
