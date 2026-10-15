package terminal

import (
	"context"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// fakeRunner stands in for wsl.exe: it records the command lines and
// answers --list --verbose with list. Nothing is ever started.
type fakeRunner struct {
	mu    sync.Mutex
	calls [][]string
	list  []byte
	fail  map[string][]byte // output of a failing command, by argv[1]
}

func (f *fakeRunner) run(_ context.Context, argv ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, slices.Clone(argv))
	if len(argv) > 1 {
		if out, ok := f.fail[argv[1]]; ok {
			return out, errors.New("exit status 1")
		}
	}
	if slices.Equal(argv, []string{wslExe, "--list", "--verbose"}) {
		return f.list, nil
	}
	return nil, nil
}

func (f *fakeRunner) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

type env struct {
	p   *Plugin
	h   *hostfake.Host
	run *fakeRunner
	la  string
	now atomic.Pointer[time.Time]
	reg atomic.Pointer[Registry]
}

// newEnv starts the plugin with Windows Terminal settings copied from the
// glctl fixture (when wt) and the WSL distributions of the fixture (when
// wsl).
func newEnv(t *testing.T, wt, wsl bool, o Options) *env {
	t.Helper()
	e := &env{run: &fakeRunner{}, la: t.TempDir()}
	if wt {
		src := filepath.Join(fixtureRoot, "LocalAppData", "Packages", "Microsoft.WindowsTerminal_8wekyb3d8bbwe", "LocalState", "settings.json")
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, settingsLocations(e.la)[0], string(data))
	}
	reg := Registry{}
	if wsl {
		data, err := os.ReadFile(filepath.Join(fixtureRoot, "wsl.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &reg); err != nil {
			t.Fatal(err)
		}
		e.run.list = readFixture(t, "wsl-list-zh.txt")
	}
	e.reg.Store(&reg)
	now := hostfake.Fixed
	e.now.Store(&now)
	o.Getenv = func(string) string { return "" }
	o.LocalAppData = e.la
	if o.Which == nil {
		o.Which = func(string) string { return "" }
	}
	o.Registry = func() (Registry, error) { return *e.reg.Load(), nil }
	o.Run = e.run.run
	e.p = NewWithOptions(o)
	h, err := hostfake.New(e.p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Clock = func() time.Time { return *e.now.Load() }
	e.h = h
	if err := e.p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.p.Close() })
	return e
}

func (e *env) items(t *testing.T, folder string) []plugin.Item {
	t.Helper()
	q := plugin.Query{}
	if folder != "" {
		q.Context = &plugin.Context{Kind: plugin.ContextExplorer, Folder: folder}
	}
	items, err := e.p.RootItems(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func item(t *testing.T, items []plugin.Item, title string) plugin.Item {
	t.Helper()
	for _, it := range items {
		if it.Title == title {
			return it
		}
	}
	var titles []string
	for _, it := range items {
		titles = append(titles, it.Title)
	}
	t.Fatalf("no item %q in %q", title, titles)
	return plugin.Item{}
}

func action(t *testing.T, it plugin.Item, id string) plugin.Action {
	t.Helper()
	for _, a := range it.Actions {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("%s: no action %q", it.Title, id)
	return plugin.Action{}
}

func hasAction(it plugin.Item, id string) bool {
	return slices.ContainsFunc(it.Actions, func(a plugin.Action) bool { return a.ID == id })
}

func openOpts(t *testing.T, a plugin.Action) plugin.OpenOptions {
	t.Helper()
	if a.Do.Kind != plugin.ActionOpen {
		t.Fatalf("%s: kind %s", a.ID, a.Do.Kind)
	}
	var o plugin.OpenOptions
	if len(a.Do.Payload) > 0 {
		if err := json.Unmarshal(a.Do.Payload, &o); err != nil {
			t.Fatal(err)
		}
	}
	return o
}

func TestUnavailable(t *testing.T) {
	p := NewWithOptions(Options{
		Getenv:   func(string) string { return "" },
		Which:    func(string) string { return "" },
		Registry: func() (Registry, error) { return Registry{}, nil },
		Run:      func(context.Context, ...string) ([]byte, error) { t.Fatal("ran a program"); return nil, nil },
	})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = p.Init(t.Context(), h)
	if !errors.Is(err, plugin.ErrUnavailable) || !strings.Contains(err.Error(), "Neither Windows Terminal nor a WSL distribution") {
		t.Fatalf("Init = %v", err)
	}
	if err := p.Close(); err != nil {
		t.Error(err)
	}
	// FixtureOptions without fixtures: unavailable too.
	p = NewWithOptions(FixtureOptions(t.TempDir()))
	h, _ = hostfake.New(p, t.TempDir())
	if err := p.Init(t.Context(), h); !errors.Is(err, plugin.ErrUnavailable) {
		t.Errorf("FixtureOptions(empty): Init = %v", err)
	}
	// wt.exe alone (never started, no settings yet) is enough.
	p = NewWithOptions(Options{
		Getenv:   func(string) string { return "" },
		Which:    func(string) string { return `C:\wt\wt.exe` },
		Registry: func() (Registry, error) { return Registry{}, nil },
	})
	h, _ = hostfake.New(p, t.TempDir())
	if err := p.Init(t.Context(), h); err != nil {
		t.Errorf("with wt.exe: %v", err)
	}
	p.Close()
}

// TestOnlyOneHalf shows only Windows Terminal or only WSL.
func TestOnlyOneHalf(t *testing.T) {
	e := newEnv(t, true, false, Options{Sync: true})
	for _, it := range e.items(t, "") {
		if strings.HasPrefix(it.ID, "distro:") || strings.HasPrefix(it.ID, "wsl-") {
			t.Errorf("WSL item %s without WSL", it.ID)
		}
	}
	if len(e.run.commands()) != 0 {
		t.Errorf("ran %q without distributions", e.run.commands())
	}
	e = newEnv(t, false, true, Options{Sync: true})
	for _, it := range e.items(t, "") {
		if strings.HasPrefix(it.ID, "profile:") {
			t.Errorf("profile %s without Windows Terminal", it.ID)
		}
	}
	// Without Windows Terminal, a distribution opens with wsl.exe in ~.
	deb := item(t, e.items(t, ""), "Ubuntu-22.04")
	open := action(t, deb, "open")
	if o := openOpts(t, open); open.Do.Target != "wsl.exe" || o.Args != "-d Ubuntu-22.04 --cd ~" {
		t.Errorf("open = %s %+v", open.Do.Target, o)
	}
}

// TestProfileActions checks the command lines built for profiles: quoting,
// semicolons, the context folder, administrator, duplicate names.
func TestProfileActions(t *testing.T) {
	e := newEnv(t, true, true, Options{Sync: true, Which: func(string) string { return `C:\Apps\wt.exe` }})
	items := e.items(t, "")
	ps := item(t, items, "Windows PowerShell")
	if ps.Group != "Terminal" || ps.Icon.Name != "terminal" || !strings.HasPrefix(ps.Subtitle, "Windows Terminal profile · ") {
		t.Errorf("item = %+v", ps)
	}
	cases := map[string]string{"open": `-w new -p "Windows PowerShell"`, "new-tab": `-w 0 nt -p "Windows PowerShell"`, "admin": `-w new -p "Windows PowerShell"`}
	for id, args := range cases {
		a := action(t, ps, id)
		if o := openOpts(t, a); a.Do.Target != `C:\Apps\wt.exe` || o.Args != args {
			t.Errorf("%s: %s %+v, want args %s", id, a.Do.Target, o, args)
		}
	}
	if a := action(t, ps, "admin"); openOpts(t, a).Verb != "runas" || a.Style != plugin.StyleDestructive || a.Shortcut != "Ctrl+Shift+Enter" {
		t.Errorf("admin = %+v", a)
	}
	if a := action(t, ps, "new-tab"); ps.Actions[1].ID != "new-tab" || a.Shortcut != "Ctrl+Enter" {
		t.Errorf("new tab is not the secondary action: %+v", ps.Actions)
	}
	if hasAction(ps, "open-here") {
		t.Error("open-here without a context folder")
	}
	if a := action(t, ps, "copy"); a.Do.Kind != plugin.ActionCopyText || !strings.HasSuffix(a.Do.Target, `powershell.exe`) {
		t.Errorf("copy = %+v", a.Do)
	}
	// Dynamic profiles have no command line to copy.
	pwsh := item(t, items, "PowerShell")
	if hasAction(pwsh, "copy") || pwsh.Accessory != "Default" {
		t.Errorf("PowerShell = %+v", pwsh)
	}
	// A name with quotes and a semicolon (wt's subcommand separator).
	dev := item(t, items, `Dev; Tools "Nightly"`)
	if got := openOpts(t, action(t, dev, "open")).Args; got != `-w new -p "Dev\; Tools \"Nightly\""` {
		t.Errorf("quoting: %s", got)
	}
	// The hidden profile is not listed; the WSL profile of a distribution
	// is opened by the distribution's item instead.
	for _, it := range items {
		if it.Title == "Azure Cloud Shell" || it.ID == "profile:{2c4de342-38b7-51cf-b940-2309a097f518}" {
			t.Errorf("listed %s (%s)", it.Title, it.ID)
		}
	}

	// In a folder with spaces (and a trailing backslash).
	dir := `C:\My Projects\a;b\`
	ps = item(t, e.items(t, dir), "Windows PowerShell")
	here := action(t, ps, "open-here")
	if o := openOpts(t, here); o.Args != `-w new -p "Windows PowerShell" -d "C:\My Projects\a\;b\\"` || o.Dir != dir || here.Title != `Open in `+dir {
		t.Errorf("open-here = %q %+v", here.Title, o)
	}
}

func TestDuplicateNames(t *testing.T) {
	s := snapshot{hasWT: true, wt: "wt.exe", term: Terminal{Profiles: []Profile{
		{GUID: "{a}", Name: "Dev Prompt"}, {GUID: "{b}", Name: "dev prompt"}, {GUID: "{c}", Name: "Other"},
	}}}
	e := newEnv(t, true, false, Options{Sync: true})
	items := buildItems(e.h.I18n(), s, "")
	if len(items) != 3 {
		t.Fatal(len(items))
	}
	for i, want := range []string{"-w new -p {a}", "-w new -p {b}", "-w new -p Other"} {
		if got := openOpts(t, items[i].Actions[0]).Args; got != want {
			t.Errorf("%s: %s, want %s", items[i].Title, got, want)
		}
	}
}

// TestDistroActions checks the distribution items and their actions.
func TestDistroActions(t *testing.T) {
	e := newEnv(t, true, true, Options{Sync: true})
	items := e.items(t, `D:\src dir`)
	ubuntu := item(t, items, "Ubuntu-22.04")
	if ubuntu.Subtitle != "WSL 2 · Running" || ubuntu.Accessory != "Default" || ubuntu.Icon.Tint != plugin.TintSuccess {
		t.Errorf("Ubuntu = %q %q %+v", ubuntu.Subtitle, ubuntu.Accessory, ubuntu.Icon)
	}
	// The distribution has a Windows Terminal profile: open it.
	if o := openOpts(t, action(t, ubuntu, "open")); o.Args != "-w new -p Ubuntu-22.04" {
		t.Errorf("open = %+v", o)
	}
	if a := ubuntu.Actions[1]; a.ID != "new-tab" || openOpts(t, a).Args != "-w 0 nt -p Ubuntu-22.04" {
		t.Errorf("secondary = %+v", a)
	}
	here := action(t, ubuntu, "open-here")
	if o := openOpts(t, here); here.Do.Target != "wsl.exe" || o.Args != `-d Ubuntu-22.04 --cd "D:\src dir"` || o.Dir != `D:\src dir` {
		t.Errorf("open-here = %s %+v", here.Do.Target, o)
	}
	if a := action(t, ubuntu, "explorer"); a.Do.Target != `\\wsl.localhost\Ubuntu-22.04` {
		t.Errorf("explorer = %+v", a.Do)
	}
	if hasAction(ubuntu, "set-default") {
		t.Error("set-default on the default distribution")
	}
	if a := action(t, ubuntu, "terminate"); a.Style != plugin.StyleDestructive || a.Do.Kind != plugin.ActionCallback {
		t.Errorf("terminate = %+v", a)
	}

	debian := item(t, items, "Debian")
	if debian.Subtitle != "WSL 2 · Stopped" || debian.Accessory != "" || hasAction(debian, "terminate") {
		t.Errorf("Debian = %+v", debian)
	}
	if !hasAction(debian, "set-default") {
		t.Error("Debian cannot be made the default")
	}
	// Legacy is not in the (Chinese) listing: no state, version from the registry.
	legacy := item(t, items, "Legacy")
	if legacy.Subtitle != "WSL 1" || !hasAction(legacy, "terminate") {
		t.Errorf("Legacy = %+v", legacy)
	}
	if legacy.Detail == nil || legacy.Detail.Metadata[0].Value != `D:\WSL\Legacy` {
		t.Errorf("detail = %+v", legacy.Detail)
	}

	// The WSL commands.
	shutdown := item(t, items, "Shut Down WSL")
	if a := shutdown.Actions[0]; a.Style != plugin.StyleDestructive || a.Do.Target != cbShutdown {
		t.Errorf("shutdown = %+v", a)
	}
	update := item(t, items, "Update WSL").Actions[0]
	var rp plugin.RunProcessPayload
	if err := json.Unmarshal(update.Do.Payload, &rp); err != nil || update.Do.Kind != plugin.ActionRunProcess {
		t.Fatal(err, update.Do.Kind)
	}
	if !slices.Equal(rp.Cmd.Argv, []string{"wsl.exe", "--update"}) || rp.Spec.Title != "Update WSL" || rp.Spec.Queue != "wsl" {
		t.Errorf("update = %+v", rp)
	}
	// Only the read-only listing ran.
	if got := e.run.commands(); !slices.Equal(got, []string{"wsl.exe --list --verbose"}) {
		t.Errorf("commands = %q", got)
	}
}

func callbackOf(t *testing.T, items []plugin.Item, title, id string) plugin.ActionDo {
	t.Helper()
	return action(t, item(t, items, title), id).Do
}

// TestCallbacks runs the WSL commands with the fake runner.
func TestCallbacks(t *testing.T) {
	e := newEnv(t, false, true, Options{Sync: true})
	items := e.items(t, "")
	for _, tt := range []struct {
		do    plugin.ActionDo
		cmd   string
		toast string
	}{
		{callbackOf(t, items, "Debian", "set-default"), "wsl.exe -s Debian", "Debian is now the default distribution"},
		{callbackOf(t, items, "Ubuntu-22.04", "terminate"), "wsl.exe -t Ubuntu-22.04", "Ubuntu-22.04 was stopped"},
		{callbackOf(t, items, "Shut Down WSL", "shutdown"), "wsl.exe --shutdown", "WSL was shut down"},
	} {
		before := len(e.run.commands())
		res, err := e.p.HandleAction(t.Context(), tt.do)
		if err != nil {
			t.Fatal(err)
		}
		cmds := e.run.commands()[before:]
		// The command, then a fresh listing.
		if len(cmds) != 2 || cmds[0] != tt.cmd || cmds[1] != "wsl.exe --list --verbose" {
			t.Errorf("commands = %q, want %s", cmds, tt.cmd)
		}
		if res.Toast == nil || res.Toast.Style != plugin.ToastSuccess || res.Toast.Title != tt.toast {
			t.Errorf("toast = %+v", res.Toast)
		}
	}

	// A failure shows wsl.exe's (UTF-16LE) message.
	msg := "There is no distribution with the supplied name."
	e.run.fail = map[string][]byte{"-t": utf16le(msg + "\r\nError code: Wsl/Service/WSL_E_DISTRO_NOT_FOUND\r\n")}
	res, err := e.p.HandleAction(t.Context(), callbackOf(t, items, "Ubuntu-22.04", "terminate"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Toast == nil || res.Toast.Style != plugin.ToastFailure || res.Toast.Message != msg || res.Toast.Title != "wsl.exe -t Ubuntu-22.04 failed" {
		t.Errorf("failure toast = %+v", res.Toast)
	}
	if _, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbTerminate}); err == nil {
		t.Error("terminate without a name")
	}
	if _, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "nope"}); err == nil {
		t.Error("unknown callback")
	}
}

func utf16le(s string) []byte {
	var b []byte
	for _, r := range s {
		b = append(b, byte(r), byte(r>>8))
	}
	return b
}

// TestRefreshOnShown re-reads settings.json when its time changed, and
// lists the running states at most every 5 seconds.
func TestRefreshOnShown(t *testing.T) {
	e := newEnv(t, true, true, Options{Sync: true})
	shown := func() { e.h.Bus.PublishSync(plugin.EventWindowShown) }
	listings := func() int {
		n := 0
		for _, c := range e.run.commands() {
			if c == "wsl.exe --list --verbose" {
				n++
			}
		}
		return n
	}
	if listings() != 1 {
		t.Fatalf("listings after Init = %d", listings())
	}
	shown()
	if n := len(e.h.Invalidated()); n != 0 || listings() != 1 {
		t.Errorf("nothing changed: %d invalidations, %d listings", n, listings())
	}

	// After 5 seconds the states are listed again; the same states do not
	// refresh.
	later := hostfake.Fixed.Add(6 * time.Second)
	e.now.Store(&later)
	shown()
	if listings() != 2 || len(e.h.Invalidated()) != 0 {
		t.Errorf("%d listings, %d invalidations", listings(), len(e.h.Invalidated()))
	}
	// Debian started: refresh.
	later = later.Add(6 * time.Second)
	e.now.Store(&later)
	e.run.mu.Lock()
	e.run.list = utf16le("  NAME STATE VERSION\r\n* Ubuntu-22.04 Running 2\r\n  Debian Running 2\r\n")
	e.run.mu.Unlock()
	shown()
	if listings() != 3 || len(e.h.Invalidated()) != 1 {
		t.Errorf("%d listings, %d invalidations", listings(), len(e.h.Invalidated()))
	}
	if got := item(t, e.items(t, ""), "Debian").Subtitle; got != "WSL 2 · Running" {
		t.Errorf("Debian = %q", got)
	}

	// Editing settings.json: a new profile appears after the next show.
	settings := settingsLocations(e.la)[0]
	writeFile(t, settings, `{"profiles": {"list": [{"name": "Fresh Profile", "commandline": "fresh.exe"}]}}`)
	future := time.Now().Add(time.Minute)
	os.Chtimes(settings, future, future)
	shown()
	if inv := e.h.Invalidated(); !slices.Equal(inv, []string{"", ""}) {
		t.Errorf("invalidated = %q", inv)
	}
	item(t, e.items(t, ""), "Fresh Profile")

	// A broken save keeps the profiles.
	writeFile(t, settings, `{"profiles": {"list": [{"name": `)
	os.Chtimes(settings, future.Add(time.Minute), future.Add(time.Minute))
	shown()
	item(t, e.items(t, ""), "Fresh Profile")

	// A new distribution in the registry.
	reg := *e.reg.Load()
	reg.Distros = append(slices.Clone(reg.Distros), Distro{GUID: "{n}", Name: "Arch", Version: 2})
	e.reg.Store(&reg)
	shown()
	item(t, e.items(t, ""), "Arch")
	if n := len(e.h.Invalidated()); n != 3 {
		t.Errorf("invalidations = %d", n)
	}
}

// TestBackgroundRefresh refreshes on a Host.Go goroutine (not Sync).
func TestBackgroundRefresh(t *testing.T) {
	e := newEnv(t, false, true, Options{})
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(e.h.Invalidated(), "") {
		if time.Now().After(deadline) {
			t.Fatal("the first listing did not refresh")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := item(t, e.items(t, ""), "Ubuntu-22.04").Subtitle; got != "WSL 2 · Running" {
		t.Errorf("subtitle = %q", got)
	}
	e.p.Close()
	e.h.Bus.PublishSync(plugin.EventWindowShown) // after Close: no work
}
