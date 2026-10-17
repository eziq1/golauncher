package apps

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
)

const fixture = "testdata/apps.json"

func fixtureApps(t *testing.T) []shell.App {
	t.Helper()
	apps, err := FixtureSource(fixture)(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return apps
}

// newHost returns a fake host for p with a settable clock.
func newHost(t *testing.T, p *Plugin, dir string) (*hostfake.Host, *atomic.Pointer[time.Time]) {
	t.Helper()
	h, err := hostfake.New(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	var now atomic.Pointer[time.Time]
	now.Store(&hostfake.Fixed)
	h.Clock = func() time.Time { return *now.Load() }
	return h, &now
}

// start creates and initializes a plugin on src, waiting for its first refresh.
func start(t *testing.T, src Source, icons IconFunc) (*Plugin, *hostfake.Host) {
	t.Helper()
	p := NewWithSource(src, icons)
	h, _ := newHost(t, p, t.TempDir())
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	p.waitIdle()
	return p, h
}

func rootItems(t *testing.T, p *Plugin, q string) []plugin.Item {
	t.Helper()
	items, err := p.RootItems(t.Context(), plugin.Query{Text: q})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func find(t *testing.T, items []plugin.Item, title string) plugin.Item {
	t.Helper()
	i := slices.IndexFunc(items, func(it plugin.Item) bool { return it.Title == title })
	if i < 0 {
		t.Fatalf("no item %q", title)
	}
	return items[i]
}

func actionIDs(it plugin.Item) []string {
	var ids []string
	for _, a := range it.Actions {
		ids = append(ids, a.ID)
	}
	return ids
}

func openOptions(t *testing.T, a plugin.Action) plugin.OpenOptions {
	t.Helper()
	var o plugin.OpenOptions
	if len(a.Do.Payload) > 0 {
		if err := json.Unmarshal(a.Do.Payload, &o); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func TestManifest(t *testing.T) {
	p := NewWithSource(FixtureSource(fixture), nil)
	m := p.Manifest()
	if m.ID != "builtin.apps" || m.Icon.Name != "app" {
		t.Errorf("manifest = %+v", m)
	}
	if !slices.Equal(m.Permissions, []plugin.Permission{plugin.PermExec, plugin.PermStorage}) {
		t.Errorf("permissions = %v", m.Permissions)
	}
	if len(m.Commands) != 1 || m.Commands[0].Name != "reload" || m.Commands[0].Mode != plugin.ModeNoView {
		t.Errorf("commands = %+v", m.Commands)
	}
	if len(m.Preferences) != 1 || m.Preferences[0].Name != "hiddenApps" || m.Preferences[0].Type != plugin.PrefStringList {
		t.Errorf("preferences = %+v", m.Preferences)
	}
	h, _ := newHost(t, p, t.TempDir())
	l := h.I18N.Localizer(ID)
	want := map[string]string{
		m.Name:                       "Applications",
		m.Commands[0].Title:          "Reload Applications",
		m.Preferences[0].Title:       "Hidden Applications",
		m.Description:                "Search and launch installed applications",
		m.Preferences[0].Description: "IDs of applications that are not shown in search",
	}
	for s, w := range want {
		if _, ok := i18n.ManifestKey(s); !ok {
			t.Errorf("%q is not a %%key%% reference", s)
		}
		if got := l.Resolve(s); got != w {
			t.Errorf("Resolve(%q) = %q, want %q", s, got, w)
		}
	}
}

func TestRootItems(t *testing.T) {
	p, _ := start(t, FixtureSource(fixture), nil)
	if items := rootItems(t, p, "  "); items != nil {
		t.Errorf("empty query returned %d items", len(items))
	}
	items := rootItems(t, p, "w")
	if len(items) != len(fixtureApps(t)) {
		t.Fatalf("got %d items, want all %d apps", len(items), len(fixtureApps(t)))
	}
	for _, it := range items {
		if it.Score != 0 || it.Accessory != "Application" || it.Group != "Applications" || it.Subtitle != "" || it.Icon.Name != "app" {
			t.Errorf("item %+v", it)
		}
	}

	wx := find(t, items, "微信")
	if wx.ID != "Tencent.WeChat" || !slices.Equal(wx.Keywords, []string{"Weixin", "WeChat"}) {
		t.Errorf("微信 item = %+v", wx)
	}
	if got := actionIDs(wx); !slices.Equal(got, []string{"open", "runas", "reveal", "copy-path", "hide"}) {
		t.Errorf("Win32 actions = %v", got)
	}
	exe := `C:\Program Files\Tencent\Weixin\Weixin.exe`
	lnk := `C:\ProgramData\Microsoft\Windows\Start Menu\Programs\微信\微信.lnk`
	byID := map[string]plugin.Action{}
	for _, a := range wx.Actions {
		byID[a.ID] = a
	}
	if a := byID["open"]; a.Title != "Open" || a.Do.Kind != plugin.ActionOpen || a.Do.Target != lnk || len(a.Do.Payload) != 0 {
		t.Errorf("open = %+v", a)
	}
	if a := byID["runas"]; a.Title != "Run as Administrator" || a.Shortcut != "Ctrl+Shift+Enter" || a.Do.Target != lnk || openOptions(t, a) != (plugin.OpenOptions{Verb: "runas"}) {
		t.Errorf("runas = %+v", a)
	}
	if a := byID["reveal"]; a.Title != "Show in File Explorer" || a.Shortcut != "Ctrl+Shift+F" || a.Do.Target != exe || !openOptions(t, a).Reveal {
		t.Errorf("reveal = %+v", a)
	}
	if a := byID["copy-path"]; a.Title != "Copy Path" || a.Shortcut != "Ctrl+Shift+C" || a.Do.Kind != plugin.ActionCopyText || a.Do.Target != exe {
		t.Errorf("copy-path = %+v", a)
	}
	if a := byID["hide"]; a.Title != "Hide Application" || a.Do.Kind != plugin.ActionCallback || a.Do.Target != "hide" || string(a.Do.Payload) != `{"id":"Tencent.WeChat"}` {
		t.Errorf("hide = %+v", a)
	}

	// The shortcut carries the arguments, so none are passed again.
	vs := find(t, items, "x64 Native Tools Command Prompt for VS 2022")
	if a := vs.Actions[0]; !strings.HasSuffix(a.Do.Target, ".lnk") || len(a.Do.Payload) != 0 {
		t.Errorf("open = %+v", a)
	}
	if a := vs.Actions[1]; !strings.HasSuffix(a.Do.Target, ".lnk") || openOptions(t, a) != (plugin.OpenOptions{Verb: "runas"}) {
		t.Errorf("runas = %+v", a)
	}
	if a := vs.Actions[2]; a.Do.Target != `C:\WINDOWS\system32\cmd.exe` {
		t.Errorf("reveal = %+v", a)
	}

	// Without a shortcut, the app opens through the Applications folder and
	// runs elevated through its executable.
	ch := find(t, items, "Charles")
	if got := actionIDs(ch); !slices.Equal(got, []string{"open", "runas", "reveal", "copy-path", "hide"}) {
		t.Errorf("Charles actions = %v", got)
	}
	if a := ch.Actions[0]; a.Do.Target != `shell:AppsFolder\{6D809377-6AF0-444B-8957-A3773F02200E}\Charles\Charles.exe` {
		t.Errorf("Charles open = %+v", a)
	}
	if a := ch.Actions[1]; a.Do.Target != `C:\Program Files\Charles\Charles.exe` || openOptions(t, a).Verb != "runas" {
		t.Errorf("Charles runas = %+v", a)
	}

	calc := find(t, items, "Calculator")
	if got := actionIDs(calc); !slices.Equal(got, []string{"open", "copy-path", "hide"}) {
		t.Errorf("packaged actions = %v", got)
	}
	aumid := `shell:AppsFolder\Microsoft.WindowsCalculator_8wekyb3d8bbwe!App`
	if calc.Actions[0].Do.Target != aumid || calc.Actions[1].Do.Target != aumid || calc.Keywords != nil {
		t.Errorf("packaged item = %+v", calc)
	}

	cp := find(t, items, "Control Panel")
	if got := actionIDs(cp); !slices.Equal(got, []string{"open", "copy-path", "hide"}) || cp.Actions[0].Do.Target != `shell:AppsFolder\Microsoft.Windows.ControlPanel` {
		t.Errorf("no-path item = %+v", cp)
	}
}

func TestOpenFallsBackToPath(t *testing.T) {
	p, h := start(t, FixtureSource(fixture), nil)
	acts := p.actions(h.I18n(), shell.App{Name: "Tool", Path: `C:\tool.exe`, Args: "-x"})
	if a := acts[0]; a.Do.Target != `C:\tool.exe` || openOptions(t, a) != (plugin.OpenOptions{Args: "-x"}) {
		t.Errorf("open = %+v", a)
	}
	if a := acts[1]; a.Do.Target != `C:\tool.exe` || openOptions(t, a) != (plugin.OpenOptions{Args: "-x", Verb: "runas"}) {
		t.Errorf("runas = %+v", a)
	}
}

func TestHide(t *testing.T) {
	p, h := start(t, FixtureSource(fixture), nil)
	h.Prefs.Set("hiddenApps", jsontext.Value(`["Chrome"]`))
	wx := find(t, rootItems(t, p, "x"), "微信")
	hide := wx.Actions[len(wx.Actions)-1]
	res, err := p.HandleAction(t.Context(), hide.Do)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refresh || res.Toast == nil || res.Toast.Title != "微信 is now hidden" || res.Toast.Style != plugin.ToastSuccess {
		t.Errorf("result = %+v, toast %+v", res, res.Toast)
	}
	items := rootItems(t, p, "x")
	for _, it := range items {
		if it.ID == "Tencent.WeChat" || it.ID == "Chrome" {
			t.Errorf("hidden app %q listed", it.ID)
		}
	}
	if len(items) != len(fixtureApps(t))-2 {
		t.Errorf("got %d items", len(items))
	}
	if b, _, _ := h.KV.Get("hidden"); string(b) != `["Tencent.WeChat"]` {
		t.Errorf("stored hidden = %s", b)
	}

	// Hiding twice keeps one entry; a new instance on the same storage keeps it hidden.
	if _, err := p.HandleAction(t.Context(), hide.Do); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := h.KV.Get("hidden"); string(b) != `["Tencent.WeChat"]` {
		t.Errorf("stored hidden = %s", b)
	}
	p2 := NewWithSource(FixtureSource(fixture), nil)
	if err := p2.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	p2.waitIdle()
	if slices.ContainsFunc(rootItems(t, p2, "x"), func(it plugin.Item) bool { return it.ID == "Tencent.WeChat" }) {
		t.Error("hidden app listed after restart")
	}

	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "hide", Payload: jsontext.Value(`{}`)}); err == nil {
		t.Error("hide without id succeeded")
	}
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "nope"}); err == nil {
		t.Error("unknown callback succeeded")
	}
}

func TestCacheLoadedOnInit(t *testing.T) {
	cached := fixtureApps(t)[:3]
	b, err := json.Marshal(index{Updated: hostfake.Fixed.Add(-time.Hour), Apps: cached})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	stopped := make(chan error, 1)
	blocking := func(ctx context.Context) ([]shell.App, error) {
		close(started)
		<-ctx.Done()
		stopped <- ctx.Err()
		return nil, ctx.Err()
	}
	p := NewWithSource(blocking, nil)
	h, _ := newHost(t, p, t.TempDir())
	h.KV.Set("index", b)
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	<-started
	// The refresh is still running: results come from the cache without blocking.
	items := rootItems(t, p, "a")
	if len(items) != 3 || items[0].Title != cached[0].Name {
		t.Fatalf("items from cache = %d", len(items))
	}
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the background refresh")
	}
	if err := <-stopped; !errors.Is(err, context.Canceled) {
		t.Errorf("refresh ended with %v", err)
	}
	// After Close, events start no work.
	h.Bus.PublishSync(plugin.EventWindowShown)
	p.waitIdle()
	if err := p.Close(); err != nil {
		t.Error(err)
	}
}

func TestReload(t *testing.T) {
	var n atomic.Int32
	all := fixtureApps(t)
	src := func(context.Context) ([]shell.App, error) {
		if n.Add(1) == 2 {
			return all[:1], nil
		}
		return all, nil
	}
	p, h := start(t, src, nil)
	reload := plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: "reload"}
	res, err := p.HandleAction(t.Context(), reload)
	if err != nil {
		t.Fatal(err)
	}
	if res.Toast == nil || res.Toast.Title != "Found 1 application" || res.Toast.Style != plugin.ToastSuccess {
		t.Errorf("toast = %+v", res.Toast)
	}
	res, _ = p.HandleAction(t.Context(), reload)
	if want := "Found 10 applications"; res.Toast == nil || res.Toast.Title != want {
		t.Errorf("toast = %+v, want %q", res.Toast, want)
	}
	var idx index
	b, _, _ := h.KV.Get("index")
	if err := json.Unmarshal(b, &idx); err != nil || len(idx.Apps) != 10 || !idx.Updated.Equal(hostfake.Fixed) {
		t.Errorf("stored index = %+v, %v", idx, err)
	}

	failing := func(context.Context) ([]shell.App, error) { return nil, errors.New("boom") }
	pf, _ := start(t, failing, nil)
	res, err = pf.HandleAction(t.Context(), reload)
	if err != nil || res.Toast == nil || res.Toast.Style != plugin.ToastFailure || res.Toast.Message != "boom" {
		t.Errorf("failed reload = %+v, %v", res.Toast, err)
	}
}

func TestWindowShownRefresh(t *testing.T) {
	var calls atomic.Int32
	src := func(ctx context.Context) ([]shell.App, error) {
		calls.Add(1)
		return FixtureSource(fixture)(ctx)
	}
	p := NewWithSource(src, nil)
	h, now := newHost(t, p, t.TempDir())
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.waitIdle()
	if c := calls.Load(); c != 1 {
		t.Fatalf("calls after Init = %d", c)
	}
	h.Bus.PublishSync(plugin.EventWindowShown)
	p.waitIdle()
	if c := calls.Load(); c != 1 {
		t.Errorf("fresh index refreshed: calls = %d", c)
	}
	later := hostfake.Fixed.Add(61 * time.Second)
	now.Store(&later)
	h.Bus.PublishSync(plugin.EventWindowShown)
	p.waitIdle()
	if c := calls.Load(); c != 2 {
		t.Errorf("stale index not refreshed: calls = %d", c)
	}
}

func TestIcons(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	icons := func(a shell.App, size int) ([]byte, error) {
		calls.Add(1)
		if size != 64 {
			t.Errorf("icon size %d", size)
		}
		if a.Packaged {
			return nil, errors.New("no icon")
		}
		return buf.Bytes(), nil
	}
	p, h := start(t, FixtureSource(fixture), icons)
	// The refreshed index and the new icons each invalidate the root results.
	if got := h.Invalidated(); !slices.Equal(got, []string{"", ""}) {
		t.Fatalf("invalidated = %q", got)
	}
	items := rootItems(t, p, "a")
	wx := find(t, items, "微信")
	if dir := filepath.Join(h.CacheDir(), "icons"); filepath.Dir(wx.Icon.Path) != dir || wx.Icon.Name != "" {
		t.Fatalf("icon = %+v", wx.Icon)
	}
	if b, err := os.ReadFile(wx.Icon.Path); err != nil || !bytes.Equal(b, buf.Bytes()) {
		t.Errorf("icon file: %v", err)
	}
	if calc := find(t, items, "Calculator"); calc.Icon != (plugin.Icon{Name: "app"}) {
		t.Errorf("failed icon = %+v", calc.Icon)
	}
	// Existing icons are not drawn again; a restarted plugin sees them at once.
	before := calls.Load()
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: "reload"}); err != nil {
		t.Fatal(err)
	}
	p.waitIdle()
	if got := calls.Load() - before; got != 2 { // only the two packaged apps are retried
		t.Errorf("icons drawn again: %d", got)
	}
	if got := h.Invalidated(); len(got) != 3 { // the index only: no new icon
		t.Errorf("invalidated after reload = %q", got)
	}
	p2 := NewWithSource(func(ctx context.Context) ([]shell.App, error) { <-ctx.Done(); return nil, ctx.Err() }, icons)
	if err := p2.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	if it := find(t, rootItems(t, p2, "a"), "微信"); it.Icon.Path == "" {
		t.Errorf("cached icon not used after restart: %+v", it.Icon)
	}
}

func TestFixtureSourceErrors(t *testing.T) {
	if _, err := FixtureSource(filepath.Join(t.TempDir(), "missing.json"))(t.Context()); err == nil {
		t.Error("missing fixture succeeded")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("{"), 0o644)
	if _, err := FixtureSource(bad)(t.Context()); err == nil {
		t.Error("bad fixture succeeded")
	}
}

// TestKeywords covers R40: an app whose shortcut and executable carry only
// its English name (a "WeChat.exe" listed as "WeChat") is found by its
// Chinese name too, and the names the shell found are kept.
func TestKeywords(t *testing.T) {
	tests := []struct {
		app  shell.App
		want []string
	}{
		{shell.App{Name: "WeChat", ID: "w", Path: `C:\Apps\wechat\current\WeChat.exe`}, []string{"微信", "Weixin"}},
		{shell.App{Name: "微信", ID: "w2", Path: `C:\Program Files\Tencent\Weixin\Weixin.exe`}, []string{"Weixin", "WeChat"}},
		{shell.App{Name: "Contoso Chat", ID: "c", Path: `C:\Apps\Contoso\ContosoChat.exe`, Keywords: []string{"康托聊天", "contosochat"}}, []string{"ContosoChat", "康托聊天"}},
		{shell.App{Name: "画板", ID: "s", Path: `C:\Apps\Sketch\sketch.exe`, Keywords: []string{"Sketch Pad"}}, []string{"sketch", "Sketch Pad"}},
		{shell.App{Name: "Store App", ID: "x!App", Packaged: true}, nil},
	}
	for _, tt := range tests {
		if got := keywords(tt.app); !slices.Equal(got, tt.want) {
			t.Errorf("keywords(%s) = %q, want %q", tt.app.Name, got, tt.want)
		}
	}
}

func TestKnownNames(t *testing.T) {
	m := knownNames()
	if len(m) == 0 {
		t.Fatal("no known names")
	}
	for exe, names := range m {
		if exe != strings.ToLower(exe) || !strings.HasSuffix(exe, ".exe") || strings.ContainsAny(exe, `\/`) {
			t.Errorf("bad executable name %q", exe)
		}
		if len(names) < 2 || slices.Contains(names, "") {
			t.Errorf("%s: names %q", exe, names)
		}
	}
}

// TestBundleActions covers the apps of macOS: an .app bundle opens by its
// path, has no "Run as Administrator" and is shown in Finder.
func TestBundleActions(t *testing.T) {
	p, h := start(t, FixtureSource(fixture), nil)
	wx := shell.App{Name: "微信", ID: "com.tencent.xinWeChat", Path: "/Applications/WeChat.app", Keywords: []string{"Weixin"}}
	acts := p.actions(h.I18n(), wx)
	var ids []string
	for _, a := range acts {
		ids = append(ids, a.ID)
	}
	if !slices.Equal(ids, []string{"open", "reveal", "copy-path", "hide"}) {
		t.Fatalf("actions = %v", ids)
	}
	if a := acts[0]; a.Do.Kind != plugin.ActionOpen || a.Do.Target != wx.Path || len(a.Do.Payload) != 0 {
		t.Errorf("open = %+v", a)
	}
	if a := acts[1]; a.Title != "Show in Finder" || a.Do.Target != wx.Path || !openOptions(t, a).Reveal {
		t.Errorf("reveal = %+v", a)
	}
	if a := acts[2]; a.Do.Kind != plugin.ActionCopyText || a.Do.Target != wx.Path {
		t.Errorf("copy-path = %+v", a)
	}
	if got := keywords(wx); !slices.Equal(got, []string{"WeChat", "Weixin"}) {
		t.Errorf("keywords = %q", got)
	}
}
