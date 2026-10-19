package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// testCatalog is a small, partly invented `codex debug models` output: the
// order is not the priority order, one model is hidden, one has no
// priority, one no display name, one no slug, one a level that cannot be
// passed, levels as objects and as strings, and fields that are not used.
const testCatalog = `{"models":[
 {"slug":"test-b","display_name":"Test B","visibility":"list","priority":2,"default_reasoning_level":"low",
  "supported_reasoning_levels":[{"effort":"low","description":"Fast"},{"effort":"high","description":"Deep"}],"base_instructions":"…"},
 {"slug":"test-a","display_name":"Test A","visibility":"list","priority":1,
  "supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"xhigh"}]},
 {"slug":"test-reviewer","display_name":"Reviewer","visibility":"hide","priority":0,"supported_reasoning_levels":[{"effort":"low"}]},
 {"slug":"test-c","visibility":"list","supported_reasoning_levels":["low","medium"]},
 {"display_name":"No slug","visibility":"list","priority":0},
 {"slug":"gpt-5.5","display_name":"GPT-5.5","visibility":"list","priority":13,
  "supported_reasoning_levels":[{"effort":"low"},{"effort":"Not A Level"},{"effort":"low"}],"extra":{"x":[1,2]}},
 {"slug":"test-nolevels","display_name":"No Levels","visibility":"list","priority":20}
]}`

func modelIDs(l backend.ModelList) []string {
	var out []string
	for _, m := range l.Models {
		out = append(out, m.ID)
	}
	return out
}

func TestParseCatalog(t *testing.T) {
	l, err := parseCatalog(testCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if got := modelIDs(l); !slices.Equal(got, []string{"test-a", "test-b", "gpt-5.5", "test-nolevels", "test-c"}) {
		t.Fatalf("models %q", got)
	}
	a, _ := l.Find("test-a")
	b, _ := l.Find("test-b")
	c, _ := l.Find("test-c")
	g, _ := l.Find("gpt-5.5")
	n, _ := l.Find("test-nolevels")
	if a.Name != "Test A" || !slices.Equal(a.Efforts, []string{"low", "medium", "high", "xhigh"}) ||
		b.DefaultEffort != "low" || !slices.Equal(b.Efforts, []string{"low", "high"}) ||
		c.Name != "test-c" || !slices.Equal(c.Efforts, []string{"low", "medium"}) ||
		!slices.Equal(g.Efforts, []string{"low"}) || len(n.Efforts) != 0 || l.Bundled {
		t.Fatalf("parsed %+v", l)
	}
	// The default model: the levels every listed model takes (none here, as
	// one takes none).
	if lv, ok := l.EffortsFor(""); !ok || len(lv) != 0 {
		t.Errorf("default model: %q %v", lv, ok)
	}
	if lv, ok := l.EffortsFor("test-b"); !ok || !slices.Equal(lv, []string{"low", "high"}) {
		t.Errorf("test-b: %q %v", lv, ok)
	}
	if _, ok := l.EffortsFor("test-reviewer"); ok {
		t.Error("a hidden model was found")
	}

	// A line before the JSON; an empty list.
	if l, err := parseCatalog("WARN something (fake)\n" + `{"models":[{"slug":"x","visibility":"list"}]}`); err != nil || len(l.Models) != 1 {
		t.Errorf("with a warning line: %+v, %v", l, err)
	}
	if l, err := parseCatalog(`{"models":[]}`); err != nil || len(l.Models) != 0 {
		t.Errorf("empty: %+v, %v", l, err)
	}
	for _, s := range []string{"", "not json", "{}", `{"models":null}`, `{"models":"x"}`, `[1,2]`, `{"models":[{"slug":`} {
		if l, err := parseCatalog(s); err == nil {
			t.Errorf("parseCatalog(%q) = %+v", s, l)
		}
	}
}

// fakeRun is a runner that answers by the arguments.
type fakeRun struct {
	mu      sync.Mutex
	calls   [][]string
	refresh func() (string, string, int, error)
	bundled func() (string, string, int, error)
}

func (f *fakeRun) run(_ context.Context, path string, args []string, _ int) (string, string, int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, args)
	f.mu.Unlock()
	if slices.Contains(args, "--bundled") {
		return f.bundled()
	}
	return f.refresh()
}

func prints(out string) func() (string, string, int, error) {
	return func() (string, string, int, error) { return out, "", 0, nil }
}

func failure(stderr string, code int) func() (string, string, int, error) {
	return func() (string, string, int, error) { return "", stderr, code, nil }
}

// TestFetchModels: the account's list, else the bundled one, else why not.
func TestFetchModels(t *testing.T) {
	const one = `{"models":[{"slug":"m1","visibility":"list"}]}`
	const two = `{"models":[{"slug":"b1","visibility":"list"},{"slug":"b2","visibility":"list"}]}`
	timeout := func() (string, string, int, error) { return "", "", 0, context.DeadlineExceeded }
	for _, tt := range []struct {
		name             string
		refresh, bundled func() (string, string, int, error)
		models           []string
		bundledUsed      bool
		calls            int
		kind             backend.ErrorKind
		msg              string
	}{
		{name: "refresh", refresh: prints(one), bundled: prints(two), models: []string{"m1"}, calls: 1},
		{name: "offline", refresh: failure("Error: failed to refresh available models: offline\n", 1), bundled: prints(two),
			models: []string{"b1", "b2"}, bundledUsed: true, calls: 2},
		{name: "timeout", refresh: timeout, bundled: prints(two), models: []string{"b1", "b2"}, bundledUsed: true, calls: 2},
		{name: "garbage", refresh: prints("Not logged in"), bundled: prints(two), models: []string{"b1", "b2"}, bundledUsed: true, calls: 2},
		{name: "both fail", refresh: failure("first line\nError: no network\n", 1), bundled: failure("", 3), calls: 2,
			kind: backend.ErrFailed, msg: "`codex debug models` failed: Error: no network"},
		{name: "both time out", refresh: timeout, bundled: timeout, calls: 2, kind: backend.ErrFailed, msg: "did not finish within"},
		{name: "too old", refresh: failure("error: unrecognized subcommand 'debug'\n", 2), bundled: prints(two), calls: 1,
			kind: backend.ErrVersion, msg: "Codex 0.130.0 cannot list its models"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeRun{refresh: tt.refresh, bundled: tt.bundled}
			l, err := fetchModels(t.Context(), f.run, `C:\bin\codex.exe`, "0.130.0")
			if len(f.calls) != tt.calls {
				t.Errorf("calls %q", f.calls)
			}
			for _, c := range f.calls {
				if !slices.Equal(c[:6], []string{"debug", "models", "-c", `model_provider="openai"`, "-c", "check_for_update_on_startup=false"}) {
					t.Errorf("arguments %q", c)
				}
			}
			if tt.kind != "" {
				e, ok := errors.AsType[*backend.Error](err)
				if !ok || e.Kind != tt.kind || !strings.Contains(e.Message, tt.msg) {
					t.Fatalf("error %v, want %s with %q", err, tt.kind, tt.msg)
				}
				return
			}
			if err != nil || !slices.Equal(modelIDs(l), tt.models) || l.Bundled != tt.bundledUsed {
				t.Fatalf("list %+v, %v", l, err)
			}
		})
	}
}

// modelsFake is the fake CLI with the catalogs (contents, or "fail",
// "sleep", "old") printed by `debug models` and with --bundled.
func modelsFake(t *testing.T, refresh, bundled string) *fake {
	t.Helper()
	dir := t.TempDir()
	file := func(name, content string) string {
		switch content {
		case "fail", "sleep", "old":
			return content
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	return newFake(t, func(o *Options) {
		o.Env = append(o.Env, "FAKECODEX_MODELS="+file("refresh.json", refresh), "FAKECODEX_MODELS_BUNDLED="+file("bundled.json", bundled))
	})
}

// debugRuns counts the `debug models` runs in the fake's log.
func (f *fake) debugRuns() (refresh, bundled int) {
	b, _ := os.ReadFile(f.log)
	for line := range strings.Lines(string(b)) {
		if !strings.Contains(line, `"debug"`) {
			continue
		}
		if strings.Contains(line, "--bundled") {
			bundled++
		} else {
			refresh++
		}
	}
	return refresh, bundled
}

// TestModelsCache: the list is fetched once per CLI binary, Redetect and
// another binary fetch it again; the fallbacks run the real process.
func TestModelsCache(t *testing.T) {
	const bundled = `{"models":[{"slug":"bundled-1","visibility":"list"}]}`
	f := modelsFake(t, testCatalog, bundled)
	for range 3 {
		l, err := f.b.Models(t.Context())
		if err != nil || len(l.Models) != 5 || l.Bundled {
			t.Fatalf("models %+v, %v", l, err)
		}
	}
	if r, b := f.debugRuns(); r != 1 || b != 0 {
		t.Fatalf("runs %d, %d", r, b)
	}
	f.b.Redetect()
	if _, err := f.b.Models(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r, _ := f.debugRuns(); r != 2 {
		t.Fatalf("after Redetect: %d runs", r)
	}

	// Another binary at the same path (an update in place): fetched again.
	dir := t.TempDir()
	exe := filepath.Join(dir, filepath.Base(fakePath))
	copyFake(t, exe, "codex-cli 0.163.0")
	f.b.SetProgram(exe)
	if _, err := f.b.Models(t.Context()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond) // a different modification time
	copyFake(t, exe, "codex-cli 0.164.0")
	if _, err := f.b.Models(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r, _ := f.debugRuns(); r != 4 {
		t.Fatalf("after the update: %d runs", r)
	}
}

// TestModelsFallback: offline (the refresh fails), the bundled list; both
// failing, the error (kept until Redetect); a version without the command.
func TestModelsFallback(t *testing.T) {
	f := modelsFake(t, "fail", `{"models":[{"slug":"bundled-1","visibility":"list"}]}`)
	l, err := f.b.Models(t.Context())
	if err != nil || !l.Bundled || !slices.Equal(modelIDs(l), []string{"bundled-1"}) {
		t.Fatalf("offline: %+v, %v", l, err)
	}

	f = modelsFake(t, "fail", "fail")
	for range 2 {
		_, err = f.b.Models(t.Context())
		if e, ok := errors.AsType[*backend.Error](err); !ok || e.Kind != backend.ErrFailed || !strings.Contains(e.Message, "failed to refresh available models") {
			t.Fatalf("both failed: %v", err)
		}
	}
	if r, b := f.debugRuns(); r != 1 || b != 1 {
		t.Fatalf("runs %d, %d (the failure is kept)", r, b)
	}

	f = modelsFake(t, "old", "old")
	if _, err = f.b.Models(t.Context()); backend.KindOf(err) != backend.ErrVersion {
		t.Fatalf("old: %v", err)
	}

	// Not installed: the program chosen does not exist and nothing else is
	// found.
	setCandidateEnv(t, findEnv{OS: runtime.GOOS, Path: []string{t.TempDir()}})
	f = newFake(t, func(o *Options) { o.Path = filepath.Join(t.TempDir(), "codex.exe") })
	if _, err = f.b.Models(t.Context()); backend.KindOf(err) != backend.ErrNotInstalled {
		t.Fatalf("not installed: %v", err)
	}
}

// TestModelsTimeout: a refresh that hangs gives way to the bundled list; a
// caller that gives up first leaves the fetch running for the next one.
func TestModelsTimeout(t *testing.T) {
	old := modelsTimeout
	modelsTimeout = 2 * time.Second
	t.Cleanup(func() { modelsTimeout = old })
	f := modelsFake(t, "sleep", `{"models":[{"slug":"bundled-1","visibility":"list"}]}`)
	f.b.Discover(t.Context(), false) // the version check is not what times out
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := f.b.Models(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("gave up: %v", err)
	}
	l, err := f.b.Models(t.Context())
	if err != nil || !l.Bundled || len(l.Models) != 1 {
		t.Fatalf("after the timeout: %+v, %v", l, err)
	}
	if r, b := f.debugRuns(); r != 1 || b != 1 {
		t.Fatalf("runs %d, %d", r, b)
	}
}
