package core_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// selPlugin is a selection provider: a preview row (Score 1 while nothing
// is typed), a "clear hit" when the text is a number, eight actions, and
// an instruction row (Score 0.6) when text is typed. It also has a root
// command, so the root search has other results.
type selPlugin struct {
	id    string
	perms []plugin.Permission
	mu    sync.Mutex
	calls []plugin.SelectionQuery
}

func (p *selPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: p.id, Name: p.id, Version: "1", Permissions: p.perms,
		Commands: []plugin.Command{{Name: "translator", Title: "Translator Settings", Mode: plugin.ModeNoView}}}
}
func (p *selPlugin) Init(context.Context, plugin.Host) error { return nil }
func (p *selPlugin) Close() error                            { return nil }

func (p *selPlugin) SelectionItems(_ context.Context, q plugin.SelectionQuery) ([]plugin.Item, error) {
	p.mu.Lock()
	p.calls = append(p.calls, q)
	p.mu.Unlock()
	act := func(id string) []plugin.Action {
		return []plugin.Action{{ID: "run", Title: "Run", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: id + ":" + q.Selection.Text}}}
	}
	var out []plugin.Item
	score := func(s float64) float64 {
		if q.Text != "" {
			return 0
		}
		return s
	}
	out = append(out, plugin.Item{ID: "preview", Title: "Preview " + q.Selection.Text, Score: score(1), Actions: act("preview")})
	for i := range 8 {
		out = append(out, plugin.Item{ID: fmt.Sprintf("a%d", i), Title: fmt.Sprintf("Action %d", i), Actions: act(fmt.Sprintf("a%d", i))})
	}
	if q.Selection.Text == "42" {
		out = append(out, plugin.Item{ID: "hit", Title: "Clear Hit", Score: score(1), Actions: act("hit")})
	}
	if q.Text != "" {
		out = append(out, plugin.Item{ID: "instruction", Title: "Handle: " + q.Text, Score: 0.6, Actions: act("instruction")})
	}
	return out, nil
}

func (p *selPlugin) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// lastPlugin is a selection provider ordered last (a catch-all web search).
type lastPlugin struct{ selPlugin }

func (*lastPlugin) FallbackOrder() int { return plugin.FallbackOrderLast }
func (p *lastPlugin) SelectionItems(context.Context, plugin.SelectionQuery) ([]plugin.Item, error) {
	return []plugin.Item{{ID: "search", Title: "Search the Web", Actions: []plugin.Action{{ID: "open", Title: "Open",
		Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: "https://example.com"}}}}}, nil
}

// extPlugin pretends to run in its own process: it never gets the selection.
type extPlugin struct{ selPlugin }

func (*extPlugin) ProcessState() (string, string) { return "running", "" }
func (*extPlugin) Reset()                         {}

type selEnv struct {
	*app.Env
	rec       *app.Recorder
	main      *selPlugin
	noPerm    *selPlugin
	ext       *extPlugin
	ctxPlugin *ctxPlugin
}

func newSelEnv(t *testing.T, withLast bool) *selEnv {
	t.Helper()
	clip := &hostfake.Clipboard{}
	e := &selEnv{rec: &app.Recorder{Clipboard: clip},
		main:      &selPlugin{id: "sel", perms: []plugin.Permission{plugin.PermSelection}},
		noPerm:    &selPlugin{id: "noperm"},
		ext:       &extPlugin{selPlugin{id: "ext", perms: []plugin.Permission{plugin.PermSelection}}},
		ctxPlugin: &ctxPlugin{},
	}
	plugins := []plugin.Plugin{e.ctxPlugin}
	if withLast {
		plugins = append(plugins, &lastPlugin{selPlugin{id: "last", perms: []plugin.Permission{plugin.PermSelection}}})
	}
	plugins = append(plugins, e.main, e.noPerm, e.ext)
	var err error
	e.Env, err = app.Build(t.Context(), app.Options{
		Plugins: plugins, Fake: true, Locale: "en", System: e.rec, Clipboard: clip,
		Now: func() time.Time { return hostfake.Fixed },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

var notes = plugin.Selection{Text: "Let's move the meeting to Thursday", App: plugin.AppInfo{Name: "notepad.exe"}, Source: plugin.SelectionUIA}

func hasSelectionSection(s core.Snapshot) bool {
	return len(s.Sections) > 0 && s.Sections[0].Title == "Selected Text"
}

func waitSel(t *testing.T, e *selEnv, ok func(core.Snapshot) bool) core.Snapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		e.Controller.Wait()
		s := e.Controller.Snapshot()
		if ok(s) || time.Now().After(deadline) {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestSelectionArrivesAfterRoot: the root shows at once; the selected-text
// section appears on top when the text arrives later, with its first row
// (the preview) selected unless the user moved the selection (R26/R27).
func TestSelectionArrivesAfterRoot(t *testing.T) {
	for _, moved := range []bool{false, true} {
		e := newSelEnv(t, false)
		release := make(chan struct{})
		e.Engine.DetectContext(nil)
		e.Engine.DetectSelection(func(ctx context.Context) (plugin.Selection, bool) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return notes, true
		})
		e.Controller.Reset(t.Context())
		e.Controller.Wait()
		s := e.Controller.Snapshot()
		if hasSelectionSection(s) || s.Selection != nil {
			t.Fatalf("selection shown before it was read: %v", sectionTitles(s))
		}
		before, _ := s.SelectedEntry()
		if moved {
			e.Controller.Key(t.Context(), "Down")
			e.Controller.Key(t.Context(), "Up")
		}
		close(release)
		s = waitSel(t, e, hasSelectionSection)
		if !hasSelectionSection(s) {
			t.Fatalf("sections %v: the selection section did not appear", sectionTitles(s))
		}
		got, _ := s.SelectedEntry()
		switch {
		case !moved && (s.Selected != 0 || got.Key != "sel/preview"):
			t.Errorf("untouched: selected %d %q, want the preview row", s.Selected, got.Key)
		case moved && got.Key != before.Key:
			t.Errorf("moved: selected %q, want %q kept", got.Key, before.Key)
		}
		if s.Selection == nil || s.Selection.Chars != len([]rune(notes.Text)) || !strings.Contains(s.Selection.Label, "characters selected") {
			t.Errorf("selection chip %+v", s.Selection)
		}
	}
}

// TestSelectionBeforeRoot: a selection read before the root search needs no
// second search.
func TestSelectionBeforeRoot(t *testing.T) {
	e := newSelEnv(t, false)
	e.Engine.DetectContext(nil)
	e.Engine.DetectSelection(func(context.Context) (plugin.Selection, bool) { return notes, true })
	waitSel(t, e, func(core.Snapshot) bool { return e.Engine.Selection() != nil })
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); !hasSelectionSection(s) {
		t.Fatalf("sections %v", sectionTitles(s))
	}
}

// TestSelectionContextAwareOff: with context awareness off the reader
// (which reads another window) is never called; turning it off drops the
// selection.
func TestSelectionContextAwareOff(t *testing.T) {
	e := newSelEnv(t, false)
	e.Engine.SetSelection(&notes)
	if err := e.Config.Update(func(c *config.Config) { c.ContextAware = false }); err != nil {
		t.Fatal(err)
	}
	if e.Engine.Selection() != nil {
		t.Error("turning context awareness off keeps the selection")
	}
	var called atomic.Bool
	e.Engine.DetectContext(nil)
	e.Engine.DetectSelection(func(context.Context) (plugin.Selection, bool) {
		called.Store(true)
		return notes, true
	})
	time.Sleep(50 * time.Millisecond)
	if called.Load() {
		t.Error("the reader ran although context awareness is off")
	}
}

// TestSelectionTimeout: a reader slower than the budget is abandoned.
func TestSelectionTimeout(t *testing.T) {
	e := newSelEnv(t, false)
	release := make(chan struct{})
	defer close(release)
	e.Engine.DetectContext(nil)
	e.Engine.DetectSelection(func(context.Context) (plugin.Selection, bool) {
		<-release // ignores ctx, as a hung UIA call would
		return notes, true
	})
	time.Sleep(350 * time.Millisecond)
	if e.Engine.Selection() != nil {
		t.Error("a selection after the budget")
	}
}

// TestSelectionNewShowAndHide: a new show starts without the previous
// selection, a late result of an old read is ignored, and hiding the
// launcher clears the selection and drops a read in flight.
func TestSelectionNewShowAndHide(t *testing.T) {
	e := newSelEnv(t, false)
	e.Engine.SetSelection(&notes)
	release := make(chan struct{})
	e.Engine.DetectContext(nil)
	if e.Engine.Selection() != nil {
		t.Fatal("the previous show's selection is kept")
	}
	e.Engine.DetectSelection(func(context.Context) (plugin.Selection, bool) {
		<-release
		return notes, true
	})
	e.Engine.DetectContext(nil) // the next show
	close(release)
	time.Sleep(50 * time.Millisecond)
	if e.Engine.Selection() != nil {
		t.Error("an old read's result replaced the current show's selection")
	}

	release = make(chan struct{})
	e.Engine.DetectContext(nil)
	e.Engine.DetectSelection(func(context.Context) (plugin.Selection, bool) {
		<-release
		return notes, true
	})
	e.Engine.ClearSelection() // the launcher hid
	close(release)
	time.Sleep(50 * time.Millisecond)
	if e.Engine.Selection() != nil {
		t.Error("a read finishing after the launcher hid set the selection")
	}
	e.Engine.SetSelection(&notes)
	e.Engine.ClearSelection()
	if e.Engine.Selection() != nil {
		t.Error("hiding keeps the selection")
	}
}

// TestSelectionProviders: only plugins declaring PermSelection that run in
// this process are asked; their items are sensitive, transient and never
// recorded as recently used.
func TestSelectionProviders(t *testing.T) {
	e := newSelEnv(t, false)
	e.Engine.SetSelection(&notes)
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	if e.noPerm.callCount() != 0 || e.ext.callCount() != 0 {
		t.Errorf("asked without permission %d, external %d", e.noPerm.callCount(), e.ext.callCount())
	}
	if e.main.callCount() == 0 {
		t.Fatal("the provider was not asked")
	}
	s := e.Controller.Snapshot()
	for _, it := range s.Sections[0].Items {
		if !it.Sensitive || !it.Transient || !it.Context || !it.Selection || it.Group != "Selected Text" {
			t.Errorf("entry %q: %+v", it.Key, it)
		}
	}
	if _, err := e.Controller.Key(t.Context(), "Enter"); err != nil {
		t.Fatal(err)
	}
	if eff := e.rec.Effects(); len(eff) != 1 || eff[0].Target != "preview:"+notes.Text {
		t.Fatalf("effects %+v", eff)
	}
	e.Engine.ClearSelection()
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); slices.Contains(sectionTitles(s), "Recent") {
		t.Errorf("a selection item was recorded: %v", sectionTitles(s))
	}
}

// TestSelectionHiddenFromAI: the root search as AI sees it (Engine.Search)
// never has the selection's items.
func TestSelectionHiddenFromAI(t *testing.T) {
	e := newSelEnv(t, false)
	e.Engine.SetSelection(&notes)
	for _, q := range []string{"preview", "action", "thursday", "handle"} {
		entries, err := e.Engine.Search(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		for _, ent := range entries {
			if ent.Selection || strings.Contains(ent.Title, "Thursday") {
				t.Errorf("Search(%q) returned %q", q, ent.Key)
			}
		}
	}
	if e.main.callCount() != 0 {
		t.Errorf("Search asked the selection provider %d times", e.main.callCount())
	}
}

// TestSelectionEmptyRoot: while nothing is typed the section is on top of
// the context's, shows the preview and at most 6 actions, the clear hits
// (Score) first and a catch-all provider last.
func TestSelectionEmptyRoot(t *testing.T) {
	e := newSelEnv(t, true)
	e.Engine.SetContext(downloads)
	sel := notes
	sel.Text = "42"
	e.Engine.SetSelection(&sel)
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	s := e.Controller.Snapshot()
	if titles := sectionTitles(s); len(titles) < 2 || titles[0] != "Selected Text" || titles[1] != "In Downloads" {
		t.Fatalf("sections %v", titles)
	}
	var keys []string
	for _, it := range s.Sections[0].Items {
		keys = append(keys, it.Key)
	}
	want := []string{"sel/preview", "sel/hit", "sel/a0", "sel/a1", "sel/a2", "sel/a3", "sel/a4"}
	if !slices.Equal(keys, want) {
		t.Errorf("rows %v, want %v", keys, want)
	}
	// Typing finds the rest, the catch-all included.
	e.Controller.SetText(t.Context(), "search the web")
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); len(s.Sections) == 0 || s.Sections[0].Items[0].Key != "last/search" {
		t.Errorf("typed: %+v", s.Sections)
	}
}

// TestSelectionTyped: with text typed the selection's items are matched like
// context items; an item with a Score (the instruction row) matches at that
// score without pushing out a clear match, and does not hide the fallback
// items of a search that found nothing good.
func TestSelectionTyped(t *testing.T) {
	e := newSelEnv(t, false)
	e.Engine.SetSelection(&notes)
	e.Controller.Reset(t.Context())
	e.Controller.SetText(t.Context(), "translator settings")
	e.Controller.Wait()
	s := e.Controller.Snapshot()
	first, _ := s.SelectedEntry()
	if first.Title != "Translator Settings" {
		t.Errorf("first %q, want the clear match", first.Title)
	}
	var inst *core.Entry
	for _, sec := range s.Sections {
		for _, it := range sec.Items {
			if it.Key == "sel/instruction" {
				inst = &it
			}
		}
	}
	if inst == nil || inst.Rank < 0.51 || inst.Rank > 0.53 {
		t.Errorf("instruction row %+v, want rank 0.52", inst)
	}
	e.main.mu.Lock()
	calls := slices.Clone(e.main.calls)
	e.main.mu.Unlock()
	if calls[len(calls)-1].Text != "translator settings" {
		t.Errorf("query text %q", calls[len(calls)-1].Text)
	}
}

// TestSelectionLimits: the host truncates text over the maximum length,
// ignores blank text and the excluded apps.
func TestSelectionLimits(t *testing.T) {
	e := newSelEnv(t, false)
	if err := e.Config.Update(func(c *config.Config) { c.Selection.MaxChars = config.MinSelectionMaxChars }); err != nil {
		t.Fatal(err)
	}
	long := notes
	long.Text = strings.Repeat("字", 1500)
	e.Engine.SetSelection(&long)
	if got := e.Engine.Selection(); got == nil || len([]rune(got.Text)) != 1000 || !got.Truncated {
		t.Errorf("long selection: %v", got != nil && got.Truncated)
	}
	for _, s := range []plugin.Selection{
		{Text: " \n\t", App: plugin.AppInfo{Name: "notepad.exe"}},
		{Text: "hunter2", App: plugin.AppInfo{Name: "keepassxc.EXE"}},
	} {
		e.Engine.ClearSelection()
		e.Engine.SetSelection(&s)
		if e.Engine.Selection() != nil {
			t.Errorf("selection %q from %s kept", s.Text, s.App.Name)
		}
	}
}

// TestOpenSelection: the footer chip runs the preview row's action.
func TestOpenSelection(t *testing.T) {
	e := newSelEnv(t, false)
	e.Engine.SetSelection(&notes)
	e.Controller.Reset(t.Context())
	e.Controller.SetText(t.Context(), "something")
	e.Controller.Wait()
	if _, err := e.Controller.OpenSelection(t.Context()); err != nil {
		t.Fatal(err)
	}
	if eff := e.rec.Effects(); len(eff) != 1 || eff[0].Target != "preview:"+notes.Text {
		t.Fatalf("effects %+v", eff)
	}
}
