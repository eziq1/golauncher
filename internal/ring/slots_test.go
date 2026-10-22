package ring

import (
	"context"
	"slices"
	"testing"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

func entry(pluginID, id string, score float64) core.Entry {
	return core.Entry{Item: plugin.Item{ID: id, Title: id, Score: score,
		Actions: []plugin.Action{{ID: "run", Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: id}}}},
		Plugin: pluginID, Key: pluginID + "/" + id}
}

func keys(list []Slot) []string {
	var out []string
	for _, s := range list {
		out = append(out, s.Key)
	}
	return out
}

func ownSlot(key string) Slot { return Slot{Key: key, Title: key, Fixed: true} }

var defaultFixed = []Slot{ownSlot(config.RingLauncher), ownSlot(config.RingAskAI), ownSlot(config.RingScreenshot), ownSlot(config.RingLock)}

// TestAssemble: the fixed slots first, then the context entries by Score,
// without duplicates of a fixed action ("Ask AI…" is the ring's own Ask AI),
// the selected text's preview, destructive entries or entries that need an
// argument; at most 8 slots.
func TestAssemble(t *testing.T) {
	destructive := entry("builtin.files", "terminal-admin", 0)
	destructive.Actions[0].Style = plugin.StyleDestructive
	needsArg := entry("p", "arg", 0.9)
	needsArg.Arguments = []plugin.Argument{{Name: "x", Required: true}}
	optionalArg := entry("p", "optional", 0)
	optionalArg.Arguments = []plugin.Argument{{Name: "x"}}
	noAction := entry("p", "none", 0)
	noAction.Actions = nil
	candidates := []core.Entry{
		entry("builtin.selection", "preview", 1),
		entry("builtin.ai", "translate", 0),
		entry("builtin.ai", "ask", 0),
		entry("builtin.ai", "explain", 0),
		entry("builtin.quicklinks", "open-url", 1), // a clear hit: first
		destructive, needsArg, noAction,
		entry("builtin.files", "terminal", 0),
		entry("builtin.files", "terminal", 0), // twice
		entry("builtin.files", "copy-path", 0),
		optionalArg,
		entry("builtin.system", "system:lock", 0), // the fixed Lock Screen
	}
	got := Assemble(defaultFixed, candidates, []core.Entry{entry("recent", "x", 0)})
	wantKeys := []string{config.RingLauncher, config.RingAskAI, config.RingScreenshot, config.RingLock,
		"builtin.quicklinks/open-url", "builtin.ai/translate", "builtin.ai/explain", "builtin.files/terminal"}
	if !slices.Equal(keys(got), wantKeys) {
		t.Fatalf("slots %q, want %q", keys(got), wantKeys)
	}
	if got[4].Fixed || got[4].Entry == nil || got[4].Entry.Key != "builtin.quicklinks/open-url" || !got[0].Fixed {
		t.Errorf("slots %+v", got)
	}

	// Room for everything: the optional argument is fine.
	got = Assemble(defaultFixed[:1], candidates, nil)
	if !slices.Contains(keys(got), "p/optional") || len(got) != 8 {
		t.Errorf("slots %q", keys(got))
	}

	// No fixed actions: the context fills the ring.
	if got := Assemble(nil, candidates, nil); len(got) != 8 || got[0].Key != "builtin.quicklinks/open-url" {
		t.Errorf("slots %q", keys(got))
	}
	// Eight fixed ones: no room.
	var eight []Slot
	for i := range 9 {
		eight = append(eight, ownSlot(string(rune('a'+i))))
	}
	if got := Assemble(eight, candidates, nil); len(got) != 8 || got[7].Key != "h" {
		t.Errorf("slots %q", keys(got))
	}
}

// TestAssembleFallback: with nothing for the context, the recently used
// entries, by the same rules.
func TestAssembleFallback(t *testing.T) {
	recent := []core.Entry{entry("builtin.apps", "notepad", 0), entry("builtin.system", "system:lock", 0), entry("builtin.apps", "paint", 0)}
	got := Assemble(defaultFixed, nil, recent)
	if want := []string{config.RingLauncher, config.RingAskAI, config.RingScreenshot, config.RingLock, "builtin.apps/notepad", "builtin.apps/paint"}; !slices.Equal(keys(got), want) {
		t.Errorf("slots %q, want %q", keys(got), want)
	}
	// Only unfit candidates: the fallback too.
	preview := entry("builtin.selection", "preview", 1)
	if got := Assemble(defaultFixed, []core.Entry{preview}, recent); len(got) != 6 {
		t.Errorf("slots %q", keys(got))
	}
	// Nothing at all: the fixed ones.
	if got := Assemble(defaultFixed, nil, nil); len(got) != 4 {
		t.Errorf("slots %q", keys(got))
	}
}

// TestArrange: the fixed slots take up, right, down and left; the context
// ones the diagonals.
func TestArrange(t *testing.T) {
	list := []Slot{ownSlot("f1"), ownSlot("f2"), ownSlot("f3"), ownSlot("f4"), {Key: "c1"}, {Key: "c2"}}
	l := Arrange(list)
	got := make([]string, Slots)
	for i, s := range l {
		if s != nil {
			got[i] = s.Key
		}
	}
	if want := []string{"f1", "c1", "f2", "c2", "f3", "", "f4", ""}; !slices.Equal(got, want) {
		t.Errorf("layout %q, want %q", got, want)
	}
	if l.Count() != 6 {
		t.Errorf("count %d", l.Count())
	}
}

// TestExcluded: exceptions match executable names with or without .exe,
// paths, bundle identifiers and app names, in any case.
func TestExcluded(t *testing.T) {
	list := config.DefaultRingExceptions
	for _, tt := range []struct {
		names []string
		want  bool
	}{
		{[]string{"mstsc.exe"}, true},
		{[]string{"MSTSC.EXE"}, true},
		{[]string{`C:\Windows\System32\mstsc.exe`}, true},
		{[]string{"blender"}, true},
		{[]string{"", "com.vmware.fusion"}, true},
		{[]string{"notepad.exe", "", ""}, false},
		{[]string{"", "", ""}, false},
	} {
		if got := Excluded(list, tt.names...); got != tt.want {
			t.Errorf("Excluded(%q) = %v", tt.names, got)
		}
	}
	if !Excluded([]string{" Blender "}, "Blender") || Excluded(nil, "x") {
		t.Error("a list of names")
	}
}

// fakeSource stands in for the engine: entries for a context and a
// selection, the recently used ones, and the entries fixed actions find.
type fakeSource struct {
	ctx      *plugin.Context
	sel      *plugin.Selection
	calls    int
	recent   []core.Entry
	entries  map[string]core.Entry
	searched []string
}

func (f *fakeSource) RingEntries(_ context.Context, c *plugin.Context, sel *plugin.Selection) []core.Entry {
	f.calls++
	f.ctx, f.sel = c, sel
	var out []core.Entry
	if sel != nil { // as builtin.selection and builtin.ai answer
		out = append(out, entry("builtin.selection", "preview", 1), entry("builtin.ai", "translate", 0), entry("builtin.ai", "ask", 0))
	}
	if c != nil && c.Kind == plugin.ContextExplorer { // as builtin.files answers
		out = append(out, entry("builtin.files", "terminal", 0), entry("builtin.files", "vscode", 0), entry("builtin.files", "copy-path", 0))
	}
	return out
}

func (f *fakeSource) RecentEntries(n int) []core.Entry { return f.recent[:min(n, len(f.recent))] }

func (f *fakeSource) RingEntry(_ context.Context, key, text string) (core.Entry, bool) {
	f.searched = append(f.searched, key+"|"+text)
	e, ok := f.entries[key]
	return e, ok
}

func own(key string) (string, string, bool) {
	switch key {
	case config.RingLauncher:
		return "Open golauncher", "command", true
	case config.RingAskAI:
		return "Ask AI", "sparkles", true
	case config.RingScreenshot:
		return "Screenshot", "scissors", true
	}
	return "", "", false
}

// TestFixedSlots: the ring's own actions by name, root entries found
// again, one not found keeping its stored title; an unknown ring action is
// left out.
func TestFixedSlots(t *testing.T) {
	lock := entry("builtin.system", "system:lock", 0)
	lock.Title = "Lock Screen"
	src := &fakeSource{entries: map[string]core.Entry{config.RingLock: lock}}
	actions := append(config.DefaultRingFixed(), config.RingAction{Key: "ring:future"},
		config.RingAction{Key: "gone/x", Title: "Gone", Icon: "trash"})
	got := FixedSlots(t.Context(), src, actions, own)
	if want := []string{config.RingLauncher, config.RingAskAI, config.RingScreenshot, config.RingLock, "gone/x"}; !slices.Equal(keys(got), want) {
		t.Fatalf("slots %q", keys(got))
	}
	if got[0].Title != "Open golauncher" || got[0].Icon.Name != "command" || got[3].Title != "Lock Screen" || got[3].Entry == nil ||
		got[4].Title != "Gone" || got[4].Icon.Name != "trash" || got[4].Entry != nil {
		t.Errorf("slots %+v", got)
	}
	if !slices.Equal(src.searched, []string{config.RingLock + "|", "gone/x|Gone"}) {
		t.Errorf("searched %q", src.searched)
	}
}

// TestContent: the selected text's actions, the folder's, both, the
// fallback without a context, and the context turned off.
func TestContent(t *testing.T) {
	fixed := FixedSlots(t.Context(), &fakeSource{}, config.DefaultRingFixed()[:3], own)
	cfg := config.DefaultRing()
	sel := &plugin.Selection{Text: "hello", App: plugin.AppInfo{Name: "notepad.exe"}}
	explorer := &plugin.Context{Kind: plugin.ContextExplorer, Folder: `C:\Users\someone\Documents`}
	recent := []core.Entry{entry("builtin.apps", "calculator", 0)}

	src := &fakeSource{recent: recent}
	if got := keys(Content(t.Context(), src, cfg, fixed, nil, sel)); !slices.Equal(got[3:], []string{"builtin.ai/translate"}) || src.sel != sel {
		t.Errorf("selection: %q", got)
	}
	src = &fakeSource{recent: recent}
	if got := keys(Content(t.Context(), src, cfg, fixed, explorer, nil)); !slices.Equal(got[3:], []string{"builtin.files/terminal", "builtin.files/vscode", "builtin.files/copy-path"}) {
		t.Errorf("explorer: %q", got)
	}
	src = &fakeSource{recent: recent}
	if got := keys(Content(t.Context(), src, cfg, fixed, explorer, sel)); len(got) != 7 || got[3] != "builtin.ai/translate" {
		t.Errorf("both: %q", got)
	}
	src = &fakeSource{recent: recent}
	if got := keys(Content(t.Context(), src, cfg, fixed, nil, nil)); !slices.Equal(got[3:], []string{"builtin.apps/calculator"}) || src.calls != 0 {
		t.Errorf("no context: %q (%d calls)", got, src.calls)
	}
	// A context nothing answers for (a plain app): the fallback.
	src = &fakeSource{recent: recent}
	if got := keys(Content(t.Context(), src, cfg, fixed, &plugin.Context{App: plugin.AppInfo{Name: "game.exe"}}, nil)); !slices.Equal(got[3:], []string{"builtin.apps/calculator"}) {
		t.Errorf("plain app: %q", got)
	}
	cfg.Context = false
	src = &fakeSource{recent: recent}
	if got := Content(t.Context(), src, cfg, fixed, explorer, sel); len(got) != 3 || src.calls != 0 {
		t.Errorf("context off: %q", keys(got))
	}
}
