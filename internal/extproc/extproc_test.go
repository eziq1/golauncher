package extproc

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/extproc/extproctest"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/jsonrpc"
)

// Built example plugins, set up by TestMain.
var (
	goHelloDir string // a copy of examples/plugins/go-hello with go-hello.exe
	crashyExe  string
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	tmp, err := os.MkdirTemp("", "extproc-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)
	goHelloDir, err = extproctest.BuildExample(tmp, "go-hello")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	crashyDir, err := extproctest.BuildExample(tmp, "crashy")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	crashyExe = filepath.Join(crashyDir, "crashy.exe")
	return m.Run()
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// start creates and initializes a proxy for the plugin in dir.
func start(t *testing.T, dir string, o Options) (*Proxy, *hostfake.Host) {
	t.Helper()
	m, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := New(dir, m, o, quiet)
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p, h
}

// crashyDir writes a manifest for crashy with args and permissions.
func crashyDir(t *testing.T, args []string, perms ...plugin.Permission) string {
	t.Helper()
	dir := t.TempDir()
	m := Manifest{
		Manifest: plugin.Manifest{ID: "test.crashy", Name: "Crashy", Version: "1", Permissions: perms},
		Protocol: 1,
		Run:      Run{Command: crashyExe, Args: args},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func callback(p *Proxy, ctx context.Context, name string) (plugin.Result, error) {
	return p.HandleAction(ctx, plugin.ActionDo{Kind: plugin.ActionCallback, Target: name})
}

func withTimeout(t *testing.T, d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), d)
	t.Cleanup(cancel)
	return ctx
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func state(p *Proxy) string {
	s, _ := p.ProcessState()
	return s
}

func TestGoHelloRoundTrip(t *testing.T) {
	p, h := start(t, goHelloDir, Options{})
	if got := p.Manifest().ID; got != "example.go-hello" {
		t.Fatalf("id = %q", got)
	}
	if state(p) != StateStopped {
		t.Fatalf("state before first call = %q, want stopped (lazy start)", state(p))
	}
	ctx := withTimeout(t, 10*time.Second)
	items, err := p.RootItems(ctx, plugin.Query{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Say Hello from Go" || items[0].Subtitle != "Said hello 0 times" {
		t.Fatalf("root items = %+v", items)
	}
	if state(p) != StateRunning {
		t.Fatalf("state = %q", state(p))
	}
	// go-hello has no context items: once initialize said so, contextItems
	// is not sent (it would fail with "method not found").
	if items, err := p.ContextItems(ctx, plugin.Context{Kind: plugin.ContextExplorer, Folder: `C:\tmp`}); err != nil || items != nil {
		t.Fatalf("context items = %+v, %v", items, err)
	}
	v, err := p.View(ctx, plugin.ViewRequest{ID: "greetings"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Title != "Greetings" || len(v.Sections) != 1 || len(v.Sections[0].Items) != 6 {
		t.Fatalf("view = %+v", v)
	}
	if do := v.Sections[0].Items[1].Actions[0].Do; do.Kind != plugin.ActionCopyText || do.Target != "Bonjour" {
		t.Fatalf("copy action = %+v", do)
	}
	r, err := callback(p, ctx, "count")
	if err != nil {
		t.Fatal(err)
	}
	if r.Toast == nil || r.Toast.Title != "Said hello 1 time" {
		t.Fatalf("result = %+v", r)
	}
	if v, ok, _ := h.KV.Get("count"); !ok || string(v) != "1" {
		t.Fatalf("stored count = %q %v", v, ok)
	}
	eventually(t, "invalidate", func() bool { return slices.Contains(h.Invalidated(), "") })
	items, _ = p.RootItems(ctx, plugin.Query{Text: "hello"})
	if items[0].Subtitle != "Said hello 1 time" {
		t.Fatalf("subtitle after count = %q", items[0].Subtitle)
	}
	// The plugin's catalogs are served to the host for manifest strings.
	if _, err := p.Locales().Open("zh-CN.json"); err != nil {
		t.Fatal(err)
	}
}

func TestCancellationReachesPlugin(t *testing.T) {
	p, h := start(t, crashyDir(t, nil), Options{})
	if _, err := callback(p, withTimeout(t, 10*time.Second), "ok"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	if _, err := callback(p, ctx, "slow"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	eventually(t, "the plugin to see the cancellation", func() bool { return slices.Contains(h.Invalidated(), "cancelled") })
	if state(p) != StateRunning {
		t.Fatalf("state = %q; a cancelled call must not count against the plugin", state(p))
	}
}

func TestPanicInHandlerIsAnError(t *testing.T) {
	p, _ := start(t, crashyDir(t, nil), Options{})
	ctx := withTimeout(t, 10*time.Second)
	_, err := callback(p, ctx, "panic")
	if err == nil || !strings.Contains(err.Error(), "crashy panics") {
		t.Fatalf("err = %v", err)
	}
	if _, err := callback(p, ctx, "go-panic"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := callback(p, ctx, "ok"); err != nil || state(p) != StateRunning {
		t.Fatalf("after panics: err %v, state %q", err, state(p))
	}
	if _, err := callback(p, ctx, "error"); err == nil || err.Error() != "crashy failed" {
		t.Fatalf("plugin error = %v", err)
	}
}

func TestCrashFailsCallAndRestartsAfterBackoff(t *testing.T) {
	p, _ := start(t, crashyDir(t, nil), Options{BackoffMin: 300 * time.Millisecond})
	ctx := withTimeout(t, 10*time.Second)
	_, err := callback(p, ctx, "exit")
	if !errors.Is(err, ErrCrashed) {
		t.Fatalf("err = %v, want ErrCrashed", err)
	}
	eventually(t, "crashed state", func() bool { return state(p) == StateCrashed })
	if _, err := callback(p, ctx, "ok"); !errors.Is(err, ErrCrashed) {
		t.Fatalf("call during backoff: %v", err)
	}
	time.Sleep(350 * time.Millisecond)
	if _, err := callback(p, ctx, "ok"); err != nil {
		t.Fatalf("after backoff: %v", err)
	}
	if state(p) != StateRunning {
		t.Fatalf("state = %q", state(p))
	}
}

func TestBreakerTurnsPluginOffUntilReset(t *testing.T) {
	p, _ := start(t, crashyDir(t, nil), Options{BackoffMin: time.Millisecond, BackoffMax: time.Millisecond, BreakerCrashes: 3})
	ctx := withTimeout(t, 20*time.Second)
	eventually(t, "the breaker to trip", func() bool {
		if _, err := callback(p, ctx, "exit"); !errors.Is(err, ErrCrashed) && !errors.Is(err, ErrDisabled) {
			t.Fatalf("err = %v", err)
		}
		time.Sleep(5 * time.Millisecond)
		return state(p) == StateDisabled
	})
	st, reason := p.ProcessState()
	if st != StateDisabled || reason != "The plugin crashed too often and was turned off. Turn it on again in Settings." {
		t.Fatalf("state %q, reason %q", st, reason)
	}
	_, err := callback(p, ctx, "ok")
	if !errors.Is(err, ErrDisabled) || err.Error() != reason {
		t.Fatalf("call while disabled: %v", err)
	}
	p.Reset()
	if _, err := callback(p, ctx, "ok"); err != nil {
		t.Fatalf("after Reset: %v", err)
	}
}

func TestHungPluginIsKilled(t *testing.T) {
	p, _ := start(t, crashyDir(t, nil), Options{BackoffMin: time.Millisecond})
	if _, err := callback(p, withTimeout(t, 10*time.Second), "ok"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := callback(p, withTimeout(t, 100*time.Millisecond), "hang"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v", err)
		}
	}
	eventually(t, "the hung process to be killed", func() bool { return state(p) == StateCrashed })
	time.Sleep(10 * time.Millisecond)
	if _, err := callback(p, withTimeout(t, 10*time.Second), "ok"); err != nil {
		t.Fatalf("after restart: %v", err)
	}
}

func TestPermissionDenied(t *testing.T) {
	p, h := start(t, crashyDir(t, nil), Options{})
	ctx := withTimeout(t, 10*time.Second)
	for _, cb := range []string{"storage", "clipboard"} {
		_, err := callback(p, ctx, cb)
		e, ok := errors.AsType[*jsonrpc.Error](err)
		if !ok || e.Code != jsonrpc.CodePermissionDenied {
			t.Fatalf("%s: err = %#v, want code %d", cb, err, jsonrpc.CodePermissionDenied)
		}
	}
	if _, ok, _ := h.KV.Get("k"); ok {
		t.Fatal("storage was written without permission")
	}

	// With the permissions declared the same calls work.
	p2, h2 := start(t, crashyDir(t, nil, plugin.PermStorage, plugin.PermClipboard), Options{})
	for _, cb := range []string{"storage", "clipboard"} {
		if _, err := callback(p2, ctx, cb); err != nil {
			t.Fatalf("%s: %v", cb, err)
		}
	}
	if v, _, _ := h2.KV.Get("k"); string(v) != "v" {
		t.Fatalf("stored %q", v)
	}
	if c, _ := h2.Clip.Read(); c.Text != "x" {
		t.Fatalf("clipboard %q", c.Text)
	}
}

func TestHostHandlerChecksPermissions(t *testing.T) {
	p := New(t.TempDir(), Manifest{Manifest: plugin.Manifest{ID: "x"}}, Options{}, quiet)
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.h = h
	handle := p.hostHandler(&process{subs: map[string]func(){}})
	for _, m := range []string{"host/storage.get", "host/storage.set", "host/storage.delete", "host/clipboard.read", "host/clipboard.writeText", "host/clipboard.writeImage", "host/clipboard.writeFiles"} {
		_, err := handle(t.Context(), m, []byte(`{}`), false)
		if e, ok := errors.AsType[*jsonrpc.Error](err); !ok || e.Code != jsonrpc.CodePermissionDenied {
			t.Errorf("%s: %v", m, err)
		}
	}
	if _, err := handle(t.Context(), "host/log", []byte(`{"level":"info","message":"hi"}`), false); err != nil {
		t.Errorf("host/log: %v", err)
	}
	if _, err := handle(t.Context(), "host/bogus", nil, false); err == nil {
		t.Error("unknown method accepted")
	}
	// Render versions must increase per instance.
	for _, v := range []string{`{"instance":"v1","version":2,"view":{"kind":"list","title":"a"}}`, `{"instance":"v1","version":1,"view":{"kind":"list","title":"old"}}`, `{"instance":"v1","version":3,"view":{"kind":"list","title":"b"}}`} {
		if _, err := handle(t.Context(), "ui/render", []byte(v), true); err != nil {
			t.Fatal(err)
		}
	}
	var titles []string
	for _, r := range h.Renders() {
		titles = append(titles, r.View.Title)
	}
	if !slices.Equal(titles, []string{"a", "b"}) {
		t.Fatalf("rendered %q, want [a b]", titles)
	}
}

func TestRenderAndInvalidateReachHost(t *testing.T) {
	p, h := start(t, crashyDir(t, nil), Options{})
	ctx := withTimeout(t, 10*time.Second)
	if _, err := callback(p, ctx, "render"); err != nil {
		t.Fatal(err)
	}
	if _, err := callback(p, ctx, "invalidate"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "render", func() bool {
		r := h.Renders()
		return len(r) == 1 && r[0].Instance == "v1" && r[0].View.Title == "rendered"
	})
	eventually(t, "invalidate", func() bool { return slices.Contains(h.Invalidated(), "list") })
}

func TestIdleShutdownAndOpenViews(t *testing.T) {
	p, _ := start(t, crashyDir(t, nil), Options{IdleTimeout: 150 * time.Millisecond})
	ctx := withTimeout(t, 10*time.Second)
	if _, err := callback(p, ctx, "ok"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "idle stop", func() bool { return state(p) == StateStopped })
	if _, err := p.View(ctx, plugin.ViewRequest{ID: "x", Instance: "v7"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if state(p) != StateRunning {
		t.Fatalf("stopped with an open view: %q", state(p))
	}
	p.ViewClosed("v7")
	eventually(t, "idle stop after the view closed", func() bool { return state(p) == StateStopped })
	if _, err := callback(p, ctx, "ok"); err != nil {
		t.Fatalf("restart after idle: %v", err)
	}
}

func TestStartFailures(t *testing.T) {
	ctx := withTimeout(t, 20*time.Second)
	t.Run("exit", func(t *testing.T) {
		p, _ := start(t, crashyDir(t, []string{"-mode", "exit"}), Options{})
		if _, err := callback(p, ctx, "ok"); !errors.Is(err, ErrCrashed) {
			t.Fatalf("err = %v", err)
		}
		if state(p) != StateCrashed {
			t.Fatalf("state = %q", state(p))
		}
	})
	t.Run("badproto", func(t *testing.T) {
		p, _ := start(t, crashyDir(t, []string{"-mode", "badproto"}), Options{})
		_, err := callback(p, ctx, "ok")
		if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "protocol version 99") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		p, _ := start(t, crashyDir(t, []string{"-mode", "unavailable"}), Options{})
		if _, err := callback(p, ctx, "ok"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("err = %v", err)
		}
		if st, reason := p.ProcessState(); st != StateUnavailable || reason != "needs something that is missing" {
			t.Fatalf("state %q reason %q", st, reason)
		}
	})
	t.Run("noise", func(t *testing.T) {
		p, _ := start(t, crashyDir(t, []string{"-mode", "noise"}), Options{})
		if _, err := callback(p, ctx, "ok"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing command", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ManifestFile), []byte(`{"id":"x.missing","name":"M","version":"1","protocol":1,"run":{"command":"no-such-program-here"}}`), 0o644)
		m, _ := ReadManifest(dir)
		p := New(dir, m, Options{}, quiet)
		h, _ := hostfake.New(p, t.TempDir())
		err := p.Init(t.Context(), h)
		if !errors.Is(err, plugin.ErrUnavailable) || !strings.Contains(err.Error(), "no-such-program-here") {
			t.Fatalf("Init err = %v", err)
		}
	})
	t.Run("protocol in manifest", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, ManifestFile), []byte(`{"id":"x.proto","name":"M","version":"1","protocol":2,"run":{"command":"`+filepath.ToSlash(crashyExe)+`"}}`), 0o644)
		m, _ := ReadManifest(dir)
		p := New(dir, m, Options{}, quiet)
		h, _ := hostfake.New(p, t.TempDir())
		if err := p.Init(t.Context(), h); !errors.Is(err, plugin.ErrUnavailable) {
			t.Fatalf("Init err = %v", err)
		}
	})
}

func TestEagerStart(t *testing.T) {
	p, _ := start(t, goHelloDir, Options{Eager: true})
	if state(p) != StateRunning {
		t.Fatalf("state = %q", state(p))
	}
}

func TestCloseStopsProcess(t *testing.T) {
	p, _ := start(t, crashyDir(t, nil), Options{})
	if _, err := callback(p, withTimeout(t, 10*time.Second), "ok"); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	proc := p.proc
	p.mu.Unlock()
	p.Close()
	select {
	case <-proc.done:
	default:
		t.Fatal("process still running after Close")
	}
	if _, err := callback(p, t.Context(), "ok"); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after Close: %v", err)
	}
}

func TestPythonHello(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python is not on PATH")
	}
	if err := exec.Command(py, "-c", "import sys; sys.exit(0 if sys.version_info >= (3, 7) else 1)").Run(); err != nil {
		t.Skip("python 3.7+ is not available")
	}
	p, h := start(t, filepath.Join("..", "..", "examples", "plugins", "python-hello"), Options{})
	ctx := withTimeout(t, 20*time.Second)
	items, err := p.RootItems(ctx, plugin.Query{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Title != "Say Hello from Python" || items[0].Subtitle != "" {
		t.Fatalf("items = %+v", items)
	}
	v, err := p.View(ctx, plugin.ViewRequest{ID: "greet"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != plugin.ViewForm || v.Form == nil || len(v.Form.Fields) != 2 || v.Form.Submit.Do.Target != "submit" {
		t.Fatalf("view = %+v", v)
	}
	r, err := p.HandleAction(ctx, plugin.ActionDo{Kind: plugin.ActionCallback, Target: "submit",
		Values: map[string]jsontext.Value{"name": jsontext.Value(`""`)}})
	if err != nil || r.FieldErrors["name"] != "Enter a name." {
		t.Fatalf("empty name: %+v %v", r, err)
	}
	r, err = p.HandleAction(ctx, plugin.ActionDo{Kind: plugin.ActionCallback, Target: "submit",
		Values: map[string]jsontext.Value{"name": jsontext.Value(`"Ann"`), "excited": jsontext.Value(`true`)}})
	if err != nil || r.Toast == nil || !r.PopToRoot {
		t.Fatalf("submit: %+v %v", r, err)
	}
	if c, _ := h.Clip.Read(); c.Text != "Hello, Ann!!!" {
		t.Fatalf("clipboard = %q", c.Text)
	}
	if v, _, _ := h.KV.Get("lastName"); string(v) != "Ann" {
		t.Fatalf("lastName = %q", v)
	}
	items, _ = p.RootItems(ctx, plugin.Query{Text: "hello"})
	if items[0].Subtitle != "Last greeted: Ann" {
		t.Fatalf("subtitle = %q", items[0].Subtitle)
	}
}
