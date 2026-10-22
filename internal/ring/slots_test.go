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

// lkeys returns the keys of l by position, "" where empty.
func lkeys(l Layout) []string {
	out := make([]string, Slots)
	for i, s := range l {
		if s != nil {
			out[i] = s.Key
		}
	}
	return out
}

// TestAssemble: the fixed slots take the main directions, the context
// entries by Score the diagonals, without duplicates of a fixed action
// ("Ask AI…" is the ring's own Ask AI, an action in a folder counts too),
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
	l := Assemble(defaultFixed, candidates, []core.Entry{entry("recent", "x", 0)})
	want := []string{config.RingLauncher, "builtin.quicklinks/open-url", config.RingAskAI, "builtin.ai/translate",
		config.RingScreenshot, "builtin.ai/explain", config.RingLock, "builtin.files/terminal"}
	if !slices.Equal(lkeys(l), want) {
		t.Fatalf("slots %q, want %q", lkeys(l), want)
	}
	if l[1].Fixed || l[1].Entry == nil || l[1].Entry.Key != "builtin.quicklinks/open-url" || !l[0].Fixed {
		t.Errorf("slots %+v", l)
	}

	// Room for everything: the optional argument is fine; the context
	// takes the diagonals first, then the main directions left empty.
	l = Assemble(defaultFixed[:1], candidates, nil)
	if !slices.Contains(lkeys(l), "p/optional") || l.Count() != 8 || l[1].Key != "builtin.quicklinks/open-url" || l[2].Key != "builtin.files/terminal" {
		t.Errorf("slots %q", lkeys(l))
	}

	// No fixed actions: the context fills the ring.
	if l := Assemble(nil, candidates, nil); l.Count() != 8 || l[1].Key != "builtin.quicklinks/open-url" {
		t.Errorf("slots %q", lkeys(l))
	}
	// A folder: its actions are not offered again; an empty fixed slot
	// stays a place for the context.
	folder := Slot{Key: FolderKey + "1", Fixed: true, Folder: []Slot{ownSlot("x"), {Key: "builtin.ai/translate", Fixed: true}}}
	l = Assemble([]Slot{ownSlot(config.RingLauncher), folder, {}, ownSlot(config.RingLock)}, candidates, nil)
	if got := lkeys(l); slices.Contains(got, "builtin.ai/translate") || got[2] != FolderKey+"1" || got[4] == "" || l.Count() != 8 {
		t.Errorf("with a folder %q", got)
	}
}

// TestAssembleFallback: with nothing for the context, the recently used
// entries, by the same rules.
func TestAssembleFallback(t *testing.T) {
	recent := []core.Entry{entry("builtin.apps", "notepad", 0), entry("builtin.system", "system:lock", 0), entry("builtin.apps", "paint", 0)}
	l := Assemble(defaultFixed, nil, recent)
	if want := []string{config.RingLauncher, "builtin.apps/notepad", config.RingAskAI, "builtin.apps/paint", config.RingScreenshot, "", config.RingLock, ""}; !slices.Equal(lkeys(l), want) {
		t.Errorf("slots %q, want %q", lkeys(l), want)
	}
	// Only unfit candidates: the fallback too.
	preview := entry("builtin.selection", "preview", 1)
	if l := Assemble(defaultFixed, []core.Entry{preview}, recent); l.Count() != 6 {
		t.Errorf("slots %q", lkeys(l))
	}
	// Nothing at all: the fixed ones.
	if l := Assemble(defaultFixed, nil, nil); l.Count() != 4 {
		t.Errorf("slots %q", lkeys(l))
	}
}

// TestArrange: the fixed slots take up, right, down and left by their
// index, an empty one leaving its place; the context ones the diagonals,
// then the free main directions.
func TestArrange(t *testing.T) {
	l := Arrange([]Slot{ownSlot("f1"), ownSlot("f2"), ownSlot("f3"), ownSlot("f4")}, []Slot{{Key: "c1"}, {Key: "c2"}})
	if want := []string{"f1", "c1", "f2", "c2", "f3", "", "f4", ""}; !slices.Equal(lkeys(l), want) {
		t.Errorf("layout %q, want %q", lkeys(l), want)
	}
	if l.Count() != 6 || l.HasFolder() {
		t.Errorf("count %d", l.Count())
	}
	ctx := []Slot{{Key: "c1"}, {Key: "c2"}, {Key: "c3"}, {Key: "c4"}, {Key: "c5"}, {Key: "c6"}}
	l = Arrange([]Slot{{}, {Key: "folder:1", Folder: []Slot{{Key: "a"}, {Key: "b"}}}}, ctx)
	if want := []string{"c5", "c1", "folder:1", "c2", "c6", "c3", "", "c4"}; !slices.Equal(lkeys(l), want) || !l.HasFolder() || !l[2].IsFolder() {
		t.Errorf("layout %q, want %q", lkeys(l), want)
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

// contextKeys returns the keys of the context slots of l, in the order
// they are filled.
func contextKeys(l Layout) []string {
	var out []string
	for _, p := range contextPositions {
		if s := l[p]; s != nil && !s.Fixed {
			out = append(out, s.Key)
		}
	}
	return out
}

// TestFixedSlots: the ring's own actions by name, root entries found
// again, one not found keeping its stored title; an unknown ring action is
// left out, its slot empty; a folder of the actions found, one left with a
// single action is that action; without a source, nothing is looked up.
func TestFixedSlots(t *testing.T) {
	lock := entry("builtin.system", "system:lock", 0)
	lock.Title = "Lock Screen"
	src := &fakeSource{entries: map[string]core.Entry{config.RingLock: lock}}
	slots := []config.RingSlot{{Key: config.RingLauncher}, {Key: "ring:future"},
		config.FolderSlot("Tools", config.RingAction{Key: config.RingLock}, config.RingAction{Key: "gone/x", Title: "Gone", Icon: "trash"}, config.RingAction{Key: "ring:future"}),
		config.FolderSlot("", config.RingAction{Key: config.RingScreenshot}, config.RingAction{Key: "ring:future2"})}
	got := FixedSlots(t.Context(), src, slots, own)
	if want := []string{config.RingLauncher, "", FolderKey + "2", config.RingScreenshot}; !slices.Equal(keys(got), want) {
		t.Fatalf("slots %q", keys(got))
	}
	f := got[2]
	if got[0].Title != "Open golauncher" || got[0].Icon.Name != "command" || f.Title != "Tools" || len(f.Folder) != 2 ||
		f.Folder[0].Title != "Lock Screen" || f.Folder[0].Entry == nil || f.Folder[1].Title != "Gone" || f.Folder[1].Icon.Name != "trash" ||
		f.Folder[1].Entry != nil || got[3].IsFolder() || !f.IsFolder() {
		t.Errorf("slots %+v", got)
	}
	if !slices.Equal(src.searched, []string{config.RingLock + "|", "gone/x|Gone"}) {
		t.Errorf("searched %q", src.searched)
	}
	quick := FixedSlots(t.Context(), nil, slots, own)
	if quick[2].Folder[0].Title != config.RingLock || quick[2].Folder[0].Entry != nil {
		t.Errorf("quick %+v", quick[2])
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
	if got := contextKeys(Content(t.Context(), src, cfg, fixed, nil, sel)); !slices.Equal(got, []string{"builtin.ai/translate"}) || src.sel != sel {
		t.Errorf("selection: %q", got)
	}
	src = &fakeSource{recent: recent}
	if got := contextKeys(Content(t.Context(), src, cfg, fixed, explorer, nil)); !slices.Equal(got, []string{"builtin.files/terminal", "builtin.files/vscode", "builtin.files/copy-path"}) {
		t.Errorf("explorer: %q", got)
	}
	src = &fakeSource{recent: recent}
	if got := contextKeys(Content(t.Context(), src, cfg, fixed, explorer, sel)); len(got) != 4 || got[0] != "builtin.ai/translate" {
		t.Errorf("both: %q", got)
	}
	src = &fakeSource{recent: recent}
	if got := contextKeys(Content(t.Context(), src, cfg, fixed, nil, nil)); !slices.Equal(got, []string{"builtin.apps/calculator"}) || src.calls != 0 {
		t.Errorf("no context: %q (%d calls)", got, src.calls)
	}
	// A context nothing answers for (a plain app): the fallback.
	src = &fakeSource{recent: recent}
	if got := contextKeys(Content(t.Context(), src, cfg, fixed, &plugin.Context{App: plugin.AppInfo{Name: "game.exe"}}, nil)); !slices.Equal(got, []string{"builtin.apps/calculator"}) {
		t.Errorf("plain app: %q", got)
	}
	cfg.Context = false
	src = &fakeSource{recent: recent}
	if got := Content(t.Context(), src, cfg, fixed, explorer, sel); got.Count() != 3 || src.calls != 0 {
		t.Errorf("context off: %q", lkeys(got))
	}
}
