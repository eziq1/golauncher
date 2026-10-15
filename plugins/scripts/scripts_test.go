package scripts

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

type env struct {
	t   *testing.T
	p   *Plugin
	h   *hostfake.Host
	dir string // the scripts directory

	mu     sync.Mutex
	opened []string
}

// newEnv starts the plugin with a temporary scripts directory holding files.
func newEnv(t *testing.T, sys System, files map[string]string) *env {
	t.Helper()
	e := &env{t: t, dir: filepath.Join(t.TempDir(), "scripts")}
	if err := os.MkdirAll(e.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range files {
		e.write(name, src)
	}
	e.p = NewWithOptions(Options{
		Directories: []string{e.dir},
		System:      sys,
		Open: func(target string, _ plugin.OpenOptions) error {
			e.mu.Lock()
			e.opened = append(e.opened, target)
			e.mu.Unlock()
			return nil
		},
	})
	h, err := hostfake.New(e.p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	if err := e.p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.p.Close() })
	return e
}

func (e *env) write(name, src string) string {
	e.t.Helper()
	p := filepath.Join(e.dir, name)
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// item returns the root item titled title.
func (e *env) item(title string) plugin.Item {
	e.t.Helper()
	items, err := e.p.RootItems(e.t.Context(), plugin.Query{Text: "x"})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, it := range items {
		if it.Title == title {
			return it
		}
	}
	e.t.Fatalf("no item %q in %+v", title, items)
	return plugin.Item{}
}

// run runs the primary action of the item titled title with argument values.
func (e *env) run(title string, values map[string]string) plugin.Result {
	e.t.Helper()
	do := e.item(title).Actions[0].Do
	if len(values) > 0 {
		do.Values = map[string]jsontext.Value{}
		for k, v := range values {
			do.Values[k] = jsontext.Value(plugin.MustPayload(v))
		}
	}
	ctx, cancel := context.WithTimeout(e.t.Context(), 30*time.Second)
	defer cancel()
	res, err := e.p.HandleAction(ctx, do)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

// view renders the output view of a fullOutput result.
func (e *env) view(res plugin.Result) plugin.Item {
	e.t.Helper()
	if res.Push == nil || res.Push.ID != viewOutput {
		e.t.Fatalf("result = %+v, want a pushed output view", res)
	}
	v, err := e.p.View(e.t.Context(), *res.Push)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(v.Sections) != 1 || len(v.Sections[0].Items) != 1 {
		e.t.Fatalf("view = %+v", v)
	}
	return v.Sections[0].Items[0]
}

// eventually polls cond for up to 15 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func needPowerShell(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("powershell"); err != nil {
		if _, err := exec.LookPath("pwsh"); err != nil {
			t.Skip("PowerShell is not available")
		}
	}
}

func needCmd(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("cmd"); err != nil {
		t.Skip("cmd.exe is not available")
	}
}

func TestRootItems(t *testing.T) {
	e := newEnv(t, RealSystem(), map[string]string{
		"flush-dns.ps1": "# @raycast.schemaVersion 1\n# @raycast.title Flush DNS\n# @raycast.mode compact\n" +
			"# @raycast.packageName Network\n# @raycast.icon icon.png\n# @raycast.needsConfirmation true\n" +
			`# @raycast.argument1 { "type": "text", "placeholder": "host", "optional": true }` + "\n",
		"hello.js":          "// @raycast.title Hello\n// @raycast.mode silent\n",
		"plain.ps1":         "Write-Output 1\n",
		"todo.template.ps1": "# @raycast.title Template\n",
		"notes.txt":         "# @raycast.title Notes\n",
	})
	if items, _ := e.p.RootItems(t.Context(), plugin.Query{Text: " "}); items != nil {
		t.Errorf("empty query: %+v", items)
	}
	items, _ := e.p.RootItems(t.Context(), plugin.Query{Text: "f"})
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	it := e.item("Flush DNS")
	path := filepath.Join(e.dir, "flush-dns.ps1")
	if it.ID != path || it.Subtitle != "Network" || it.Group != "Script Commands" ||
		!slices.Equal(it.Keywords, []string{"flush-dns"}) || it.Icon.Path != filepath.Join(e.dir, "icon.png") {
		t.Errorf("item = %+v", it)
	}
	if len(it.Arguments) != 1 || it.Arguments[0].Name != "argument1" || it.Arguments[0].Required {
		t.Errorf("arguments = %+v", it.Arguments)
	}
	if a := it.Actions[0]; a.Style != plugin.StyleDestructive || a.Do.Kind != plugin.ActionCallback || a.Do.Target != "run" {
		t.Errorf("primary action = %+v", a)
	}
	var rp runPayload
	if err := json.Unmarshal(it.Actions[0].Do.Payload, &rp); err != nil || rp.Path != path {
		t.Errorf("payload = %s", it.Actions[0].Do.Payload)
	}
	hello := e.item("Hello")
	if hello.Actions[0].Style != "" || hello.Icon.Name != "command" || hello.Subtitle != "scripts" {
		t.Errorf("hello = %+v", hello)
	}
}

func TestRunCompact(t *testing.T) {
	needCmd(t)
	e := newEnv(t, RealSystem(), map[string]string{
		"ok.bat":   "@echo off\r\n:: @raycast.title OK\r\n:: @raycast.mode compact\r\necho first\r\necho Done: %GOLAUNCHER%\r\necho.\r\n",
		"fail.cmd": "@echo off\r\nREM @raycast.title Fail\r\nREM @raycast.mode inline\r\necho something went wrong\r\nexit /b 3\r\n",
		"cwd.cmd":  "@echo off\r\nREM @raycast.title Cwd\r\nREM @raycast.currentDirectoryPath sub\r\ncd\r\n",
	})
	res := e.run("OK", nil)
	if res.Toast == nil || res.Toast.Style != plugin.ToastSuccess || res.Toast.Title != "Done: 1" || res.Hide {
		t.Errorf("ok: %+v %+v", res, res.Toast)
	}
	res = e.run("Fail", nil)
	if res.Toast == nil || res.Toast.Style != plugin.ToastFailure || res.Toast.Title != "something went wrong" ||
		res.Toast.Message != "Exited with code 3" {
		t.Errorf("fail: %+v", res.Toast)
	}
	sub := filepath.Join(e.dir, "sub")
	os.Mkdir(sub, 0o755)
	if res := e.run("Cwd", nil); res.Toast == nil || !strings.EqualFold(res.Toast.Title, sub) {
		t.Errorf("cwd: %+v, want %s", res.Toast, sub)
	}
}

func TestRunArguments(t *testing.T) {
	needCmd(t)
	e := newEnv(t, RealSystem(), map[string]string{
		"args.cmd": "@echo off\r\n:: @raycast.title Args\r\n:: @raycast.mode compact\r\n" +
			`:: @raycast.argument1 { "type": "text", "placeholder": "a" }` + "\r\n" +
			`:: @raycast.argument2 { "type": "text", "placeholder": "b", "optional": true }` + "\r\n" +
			`:: @raycast.argument3 { "type": "dropdown", "placeholder": "c", "data": [{"title": "X", "value": "x"}] }` + "\r\n" +
			"echo [%1] [%2] [%3]\r\n",
	})
	res := e.run("Args", map[string]string{"argument1": "hello world & more", "argument3": "x"})
	if res.Toast == nil || res.Toast.Title != `["hello world & more"] [""] ["x"]` {
		t.Errorf("toast = %+v", res.Toast)
	}
}

func TestRunPowerShell(t *testing.T) {
	needPowerShell(t)
	e := newEnv(t, RealSystem(), map[string]string{
		"enc.ps1": "# @raycast.title Enc\n# @raycast.mode compact\n" +
			`# @raycast.argument1 { "type": "text", "placeholder": "q", "percentEncoded": true }` + "\n" +
			`# @raycast.argument2 { "type": "text", "placeholder": "r", "optional": true }` + "\n" +
			"param([string]$Q, [string]$R)\nWrite-Output \"q=$Q r=[$R] env=$env:GOLAUNCHER\"\n",
	})
	res := e.run("Enc", map[string]string{"argument1": "a b&c/d", "argument2": "two words"})
	if res.Toast == nil || res.Toast.Title != "q=a%20b%26c%2Fd r=[two words] env=1" {
		t.Errorf("toast = %+v", res.Toast)
	}
}

func TestMissingInterpreter(t *testing.T) {
	e := newEnv(t, fakeSystem(nil), map[string]string{
		"a.py": "# @raycast.title Py\n# @raycast.mode compact\nprint(1)\n",
	})
	res := e.run("Py", nil)
	if res.Toast == nil || res.Toast.Style != plugin.ToastFailure || res.Toast.Title != "Could not run Py" ||
		!strings.Contains(res.Toast.Message, "python was not found") {
		t.Errorf("toast = %+v", res.Toast)
	}
}

func TestRunSilent(t *testing.T) {
	needCmd(t)
	e := newEnv(t, RealSystem(), map[string]string{
		"touch.cmd": "@echo off\r\n:: @raycast.title Touch\r\n:: @raycast.mode silent\r\necho done> marker.txt\r\n",
	})
	res := e.run("Touch", nil)
	if !res.Hide || res.Toast != nil || res.Push != nil {
		t.Errorf("result = %+v", res)
	}
	eventually(t, "the marker file", func() bool {
		b, _ := os.ReadFile(filepath.Join(e.dir, "marker.txt"))
		return strings.TrimSpace(string(b)) == "done"
	})
}

func TestFullOutputStreams(t *testing.T) {
	needCmd(t)
	e := newEnv(t, RealSystem(), map[string]string{
		"stream.cmd": "@echo off\r\n:: @raycast.title Stream\r\n:: @raycast.mode fullOutput\r\n" +
			"echo first\r\nping -n 3 127.0.0.1 >nul\r\necho second\r\n>&2 echo oops\r\nexit /b 2\r\n",
	})
	res := e.run("Stream", nil)
	it := e.view(res)
	if it.Title != "Running…" || it.Actions[0].ID != "stop" {
		t.Errorf("while running: %+v", it)
	}
	eventually(t, "first line", func() bool { return strings.Contains(e.view(res).Detail.Markdown, "first") })
	if md := e.view(res).Detail.Markdown; strings.Contains(md, "second") {
		t.Errorf("second line too early: %s", md)
	}
	eventually(t, "an output refresh", func() bool { return slices.Contains(e.h.Invalidated(), viewOutput) })
	eventually(t, "the end", func() bool { return e.view(res).Title != "Running…" })
	it = e.view(res)
	if it.Title != "Exited with code 2" || it.Actions[0].ID != "copy-output" {
		t.Errorf("finished: %+v", it)
	}
	if want := "```text\nfirst\nsecond\noops\n```"; it.Detail.Markdown != want {
		t.Errorf("markdown = %q, want %q", it.Detail.Markdown, want)
	}
	if it.Actions[0].Do.Target != "first\nsecond\noops" {
		t.Errorf("copy = %q", it.Actions[0].Do.Target)
	}
	if n := len(e.h.Invalidated()); n > 40 {
		t.Errorf("%d invalidations; not throttled", n)
	}
}

func TestStopKillsProcessTree(t *testing.T) {
	needCmd(t)
	old := waitDelay
	waitDelay = time.Minute // a surviving child would hold the pipe that long
	t.Cleanup(func() { waitDelay = old })
	e := newEnv(t, RealSystem(), map[string]string{
		"long.cmd": "@echo off\r\n:: @raycast.title Long\r\n:: @raycast.mode fullOutput\r\n" +
			"echo started\r\nping -n 60 127.0.0.1 >nul\r\necho end\r\n",
	})
	res := e.run("Long", nil)
	eventually(t, "start", func() bool { return strings.Contains(e.view(res).Detail.Markdown, "started") })
	stop := e.view(res).Actions[0]
	if stop.ID != "stop" {
		t.Fatalf("actions = %+v", e.view(res).Actions)
	}
	start := time.Now()
	r, err := e.p.HandleAction(t.Context(), stop.Do)
	if err != nil || !r.Refresh {
		t.Fatalf("stop: %+v %v", r, err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("stop took %v; the child process was not killed", d)
	}
	it := e.view(res)
	if it.Title != "Stopped" || strings.Contains(it.Detail.Markdown, "end") {
		t.Errorf("after stop: %+v", it)
	}
}

func TestCompactTimeout(t *testing.T) {
	needCmd(t)
	old := compactTimeout
	compactTimeout = 500 * time.Millisecond
	t.Cleanup(func() { compactTimeout = old })
	e := newEnv(t, RealSystem(), map[string]string{
		"slow.cmd": "@echo off\r\n:: @raycast.title Slow\r\necho working\r\nping -n 30 127.0.0.1 >nul\r\n",
	})
	res := e.run("Slow", nil)
	if res.Toast == nil || res.Toast.Style != plugin.ToastFailure || !strings.Contains(res.Toast.Title, "took too long") ||
		res.Toast.Message != "working" {
		t.Errorf("toast = %+v", res.Toast)
	}
}

func TestCloseStopsRuns(t *testing.T) {
	needCmd(t)
	e := newEnv(t, RealSystem(), map[string]string{
		"long.cmd": "@echo off\r\n:: @raycast.title Long\r\n:: @raycast.mode silent\r\nping -n 60 127.0.0.1 >nul\r\n",
	})
	e.run("Long", nil)
	start := time.Now()
	e.p.Close()
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("Close took %v", d)
	}
}

func TestRescan(t *testing.T) {
	e := newEnv(t, fakeSystem(nil), nil)
	if n := len(e.p.Scripts()); n != 0 {
		t.Fatalf("%d scripts", n)
	}
	// Nothing changed: no refresh.
	e.h.Bus.PublishSync(plugin.EventWindowShown)
	time.Sleep(100 * time.Millisecond)
	if inv := e.h.Invalidated(); len(inv) != 0 {
		t.Errorf("invalidated without a change: %q", inv)
	}
	path := e.write("a.ps1", "# @raycast.title First\n")
	e.h.Bus.PublishSync(plugin.EventWindowShown)
	eventually(t, "the new script", func() bool { return len(e.p.Scripts()) == 1 })
	eventually(t, "Invalidate", func() bool { return slices.Equal(e.h.Invalidated(), []string{""}) })

	// Editing a script changes its size or time, which is noticed too.
	os.WriteFile(path, []byte("# @raycast.title Renamed script\n"), 0o644)
	os.Chtimes(path, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	e.h.Bus.PublishSync(plugin.EventWindowShown)
	eventually(t, "the edit", func() bool { s := e.p.Scripts(); return len(s) == 1 && s[0].Title == "Renamed script" })

	// A new directory in the preferences is scanned.
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "b.js"), []byte("// @raycast.title Second\n"), 0o644)
	e.h.Prefs.Set(prefDirectories, jsontext.Value(plugin.MustPayload([]string{e.dir, other})))
	e.h.Bus.PublishSync(plugin.EventConfigChanged)
	eventually(t, "the second directory", func() bool { return len(e.p.Scripts()) == 2 })
}

func TestCommands(t *testing.T) {
	e := newEnv(t, fakeSystem(nil), nil)
	res, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdNew})
	if err != nil || !res.Hide || res.Toast == nil || res.Toast.Style != plugin.ToastSuccess {
		t.Fatalf("new: %+v %v", res, err)
	}
	path := filepath.Join(e.dir, "script-command.ps1")
	s := e.p.Scripts()
	if len(s) != 1 || s[0].Path != path || s[0].Title != "My Script Command 1" || s[0].Mode != modeFullOutput || len(s[0].Arguments) != 1 {
		t.Fatalf("scripts after new = %+v", s)
	}
	e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdNew})
	if _, err := os.Stat(filepath.Join(e.dir, "script-command-2.ps1")); err != nil {
		t.Error(err)
	}
	res, err = e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdOpenFolder})
	if err != nil || !res.Hide {
		t.Fatalf("open-folder: %+v %v", res, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !slices.Equal(e.opened, []string{path, filepath.Join(e.dir, "script-command-2.ps1"), e.dir}) {
		t.Errorf("opened = %q", e.opened)
	}
}

func TestNewScriptRuns(t *testing.T) {
	needPowerShell(t)
	e := newEnv(t, RealSystem(), nil)
	e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdNew})
	res := e.run("My Script Command 1", map[string]string{"argument1": "Bob"})
	eventually(t, "the greeting", func() bool { return strings.Contains(e.view(res).Detail.Markdown, "Hello, Bob!") })
}

func TestNoDirectory(t *testing.T) {
	e := newEnv(t, fakeSystem(nil), nil)
	e.h.Prefs.Set(prefDirectories, jsontext.Value("[]"))
	res, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionRunCommand, Target: cmdOpenFolder})
	if err != nil || res.Toast == nil || res.Toast.Style != plugin.ToastFailure {
		t.Errorf("open-folder without a directory: %+v %v", res, err)
	}
}

func TestExpandAndDefaultDirectory(t *testing.T) {
	appdata := t.TempDir()
	sys := fakeSystem(nil)
	sys.Getenv = func(name string) string {
		if name == "APPDATA" {
			return appdata
		}
		return ""
	}
	p := NewWithOptions(Options{Directories: []string{DefaultDirectory}, System: sys})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	want := filepath.Join(appdata, "golauncher", "scripts")
	if st, err := os.Stat(want); err != nil || !st.IsDir() {
		t.Errorf("default directory not created: %v", err)
	}
	if got := p.expand(`%APPDATA%\x\%NOPE%\%%`); got != appdata+`\x\%NOPE%\%%` {
		t.Errorf("expand = %q", got)
	}
}

func TestViewGone(t *testing.T) {
	e := newEnv(t, fakeSystem(nil), nil)
	v, err := e.p.View(t.Context(), plugin.ViewRequest{ID: viewOutput, Payload: plugin.MustPayload(stopPayload{Run: "r99"})})
	if err != nil || v.EmptyText == "" {
		t.Errorf("view = %+v %v", v, err)
	}
	if _, err := e.p.View(t.Context(), plugin.ViewRequest{ID: "bogus"}); err == nil {
		t.Error("unknown view accepted")
	}
}
