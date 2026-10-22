package ring_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/files"
	"github.com/eziq1/golauncher/plugins/selection"
	"github.com/eziq1/golauncher/plugins/system"
)

// ringEnv is a fake environment with the built-in providers of the ring's
// context: the selected text's preview (builtin.selection), the folder's
// actions (builtin.files) and the system commands with a fake system
// (nothing is locked).
type ringEnv struct {
	*app.Env
	rec *app.Recorder
	sys *system.Fake
}

func newRingEnv(t *testing.T) *ringEnv {
	t.Helper()
	clip := &hostfake.Clipboard{}
	e := &ringEnv{rec: &app.Recorder{Clipboard: clip}, sys: system.NewFake()}
	which := func(name string) string {
		if name == "wt" || name == "code" {
			return name + ".exe"
		}
		return ""
	}
	var err error
	e.Env, err = app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{selection.New(), files.NewWithOptions(files.Options{Which: which}),
			system.NewWithOptions(system.Options{System: e.sys, Sync: true})},
		Fake: true, Locale: "en", System: e.rec, Clipboard: clip, Now: func() time.Time { return hostfake.Fixed },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func own(key string) (string, string, bool) {
	switch key {
	case config.RingLauncher, config.RingAskAI, config.RingScreenshot:
		return strings.TrimPrefix(key, "ring:"), "app", true
	}
	return "", "", false
}

func slotKeys(list []ring.Slot) []string {
	var out []string
	for _, s := range list {
		out = append(out, s.Key)
	}
	return out
}

// TestContentFromProviders: the ring's slots from the real providers: the
// default fixed actions (Lock found in the system plugin), then the
// folder's actions under the pointer; the selected text's preview is left
// out; a run of a slot goes through the launcher's controller.
func TestContentFromProviders(t *testing.T) {
	e := newRingEnv(t)
	cfg := e.Config.Get().Ring
	fixed := ring.FixedSlots(t.Context(), e.Engine, cfg.Fixed, own)
	if got := slotKeys(fixed); !slices.Equal(got, []string{config.RingLauncher, config.RingAskAI, config.RingScreenshot, config.RingLock}) ||
		fixed[3].Entry == nil || fixed[3].Title != "Lock" || fixed[3].Icon.Name != "lock" {
		t.Fatalf("fixed %q %+v", got, fixed[3])
	}

	for _, key := range []string{"builtin.system/system:nothing", "nothing", "gone/x", "builtin.files/cmd:nothing"} {
		if _, ok := e.Engine.RingEntry(t.Context(), key, ""); ok {
			t.Errorf("found %q", key)
		}
	}

	folder := &plugin.Context{App: plugin.AppInfo{Name: "explorer.exe"}, Kind: plugin.ContextExplorer, Folder: t.TempDir()}
	sel := &plugin.Selection{Text: "some text", App: plugin.AppInfo{Name: "explorer.exe"}, Source: plugin.SelectionUIA}
	slots := ring.Content(t.Context(), e.Engine, cfg, fixed, folder, sel)
	got := slotKeys(slots)
	if len(got) != 8 || slices.Contains(got, "builtin.selection/preview") {
		t.Fatalf("slots %q", got)
	}
	for _, k := range got[4:] {
		if !strings.HasPrefix(k, "builtin.files/") {
			t.Errorf("a context slot %q", k)
		}
	}
	if !slices.Equal(got[4:], []string{"builtin.files/terminal", "builtin.files/cmd", "builtin.files/vscode", "builtin.files/new-folder"}) {
		t.Errorf("folder actions in the providers' order: %q", got)
	}
	// With one fixed action, more of them; never a destructive one.
	slots = ring.Content(t.Context(), e.Engine, cfg, fixed[3:], folder, sel)
	got = slotKeys(slots)
	if len(got) != 7 || !slices.Contains(got, "builtin.files/copy-path") || slices.Contains(got, "builtin.files/terminal-admin") {
		t.Errorf("slots %q", got)
	}

	// Copy Path runs hidden: copied, nothing to show.
	i := slices.Index(got, "builtin.files/copy-path")
	out, err := e.Controller.RunEntry(t.Context(), *slots[i].Entry)
	if err != nil || !out.Hide || e.Controller.NeedsWindow() {
		t.Fatalf("copy path: %+v %v, window %v", out, err, e.Controller.NeedsWindow())
	}
	if eff := e.rec.Effects(); len(eff) != 1 || eff[0].Kind != plugin.ActionCopyText || eff[0].Target != folder.Folder {
		t.Errorf("effects %+v", eff)
	}

	// Lock Screen runs the system plugin's command (a fake system here).
	if _, err := e.Controller.RunEntry(t.Context(), *fixed[3].Entry); err != nil {
		t.Fatal(err)
	}
	if calls := e.sys.Calls(); !slices.Equal(calls, []string{"Lock"}) {
		t.Errorf("system calls %q", calls)
	}
}

// TestContentFallback: no context (or one nothing answers for): the
// recently used entries fill the ring; a used context item is not one.
func TestContentFallback(t *testing.T) {
	e := newRingEnv(t)
	cfg := e.Config.Get().Ring
	fixed := ring.FixedSlots(t.Context(), e.Engine, cfg.Fixed, own)
	if got := ring.Content(t.Context(), e.Engine, cfg, fixed, nil, nil); len(got) != 4 {
		t.Fatalf("nothing used yet: %q", slotKeys(got))
	}
	// Used from the root search: Empty Recycle Bin is destructive (left out),
	// Toggle Dark Mode is not.
	for _, text := range []string{"dark mode", "lock"} {
		secs, err := e.Engine.RootSearch(t.Context(), text)
		if err != nil || len(secs) == 0 {
			t.Fatal(text, err)
		}
		ent := secs[0].Items[0]
		if _, err := e.Engine.Run(t.Context(), ent, ent.Actions[0], true); err != nil {
			t.Fatal(err)
		}
	}
	got := slotKeys(ring.Content(t.Context(), e.Engine, cfg, fixed, &plugin.Context{App: plugin.AppInfo{Name: "game.exe"}}, nil))
	if !slices.Equal(got[4:], []string{"builtin.system/system:darkMode"}) {
		t.Errorf("fallback %q (Lock Screen is a fixed one already)", got)
	}
}

// TestRunEntryNeedsWindow: an action that opens a view or asks for a
// confirmation leaves the launcher something to show; Show with Current
// keeps it.
func TestRunEntryNeedsWindow(t *testing.T) {
	e := newRingEnv(t)
	secs, err := e.Engine.RootSearch(t.Context(), "empty recycle bin")
	if err != nil {
		t.Fatal(err)
	}
	bin := secs[0].Items[0]
	if _, err := e.Controller.RunEntry(t.Context(), bin); err != nil {
		t.Fatal(err)
	}
	if !e.Controller.NeedsWindow() || e.Controller.Snapshot().Confirm == nil {
		t.Fatal("a destructive action runs without its confirmation")
	}
	e.Controller.Show(t.Context(), core.ShowOptions{Current: true})
	if e.Controller.Snapshot().Confirm == nil {
		t.Error("Show with Current dropped the confirmation")
	}
	if len(e.sys.Calls()) != 0 {
		t.Errorf("ran before the confirmation: %q", e.sys.Calls())
	}
}
