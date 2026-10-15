package core_test

import (
	"context"
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

// ctxPlugin is a context provider offering one item per folder, and a root
// provider remembering the contexts its queries carried.
type ctxPlugin struct {
	mu   sync.Mutex
	seen []*plugin.Context
}

func (p *ctxPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "ctx", Name: "Ctx", Version: "1",
		Commands: []plugin.Command{{Name: "hello", Title: "Hello World", Mode: plugin.ModeNoView}}}
}
func (p *ctxPlugin) Init(context.Context, plugin.Host) error { return nil }
func (p *ctxPlugin) Close() error                            { return nil }

func (p *ctxPlugin) RootItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	p.mu.Lock()
	p.seen = append(p.seen, q.Context)
	p.mu.Unlock()
	return []plugin.Item{{ID: "term-app", Title: "Terminal Emulator", Actions: []plugin.Action{{ID: "open", Title: "Open",
		Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: "emu.exe"}}}}}, nil
}

func (p *ctxPlugin) ContextItems(_ context.Context, c plugin.Context) ([]plugin.Item, error) {
	if c.Folder == "" {
		return nil, nil
	}
	return []plugin.Item{
		{ID: "terminal", Title: "Open Terminal Here", Subtitle: c.Folder, Actions: []plugin.Action{{ID: "open", Title: "Open",
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: "wt.exe", Payload: plugin.MustPayload(plugin.OpenOptions{Dir: c.Folder})}}}},
		{ID: "copy", Title: "Copy Folder Path", Actions: []plugin.Action{{ID: "copy", Title: "Copy",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: c.Folder}}}},
	}, nil
}

func (p *ctxPlugin) contexts() []*plugin.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.seen)
}

type ctxEnv struct {
	*app.Env
	rec *app.Recorder
	p   *ctxPlugin
}

func newCtxEnv(t *testing.T) *ctxEnv {
	t.Helper()
	clip := &hostfake.Clipboard{}
	e := &ctxEnv{rec: &app.Recorder{Clipboard: clip}, p: &ctxPlugin{}}
	var err error
	e.Env, err = app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{e.p}, Fake: true, Locale: "en", System: e.rec, Clipboard: clip,
		Now: func() time.Time { return hostfake.Fixed },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

var downloads = &plugin.Context{App: plugin.AppInfo{Name: "explorer.exe"}, Kind: plugin.ContextExplorer, Folder: `C:\Users\ann\Downloads`}

func sectionTitles(s core.Snapshot) []string {
	var out []string
	for _, sec := range s.Sections {
		out = append(out, sec.Title)
	}
	return out
}

// waitFor polls the snapshot until ok holds.
func waitFor(t *testing.T, e *ctxEnv, ok func(core.Snapshot) bool) core.Snapshot {
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

func hasContextSection(s core.Snapshot) bool {
	return len(s.Sections) > 0 && s.Sections[0].Title == "In Downloads"
}

// TestContextArrivesAfterRoot: the root shows at once; the context section
// appears when detection finishes later, through an invalidation.
func TestContextArrivesAfterRoot(t *testing.T) {
	e := newCtxEnv(t)
	release := make(chan struct{})
	e.Engine.DetectContext(func(ctx context.Context) (plugin.Context, bool) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return *downloads, true
	})
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); hasContextSection(s) || s.Context != nil {
		t.Fatalf("context shown before it was detected: %v", sectionTitles(s))
	}
	close(release)
	s := waitFor(t, e, hasContextSection)
	if !hasContextSection(s) {
		t.Fatalf("sections %v: the context section did not appear", sectionTitles(s))
	}
	if got := s.Sections[0].Items; len(got) != 2 || got[0].Title != "Open Terminal Here" || !got[0].Context {
		t.Errorf("context items %+v", got)
	}
	if want := (core.ContextSnapshot{Kind: plugin.ContextExplorer, Name: "Downloads", Folder: downloads.Folder, App: "explorer.exe"}); s.Context == nil || *s.Context != want {
		t.Errorf("snapshot context %+v, want %+v", s.Context, want)
	}
	if !slices.Equal(sectionTitles(s)[1:], []string{"Commands"}) {
		t.Errorf("sections %v, want the context first", sectionTitles(s))
	}
}

// TestContextBeforeRoot: a context detected before the root search needs no
// second search.
func TestContextBeforeRoot(t *testing.T) {
	e := newCtxEnv(t)
	done := make(chan struct{})
	e.Engine.DetectContext(func(context.Context) (plugin.Context, bool) {
		defer close(done)
		return *downloads, true
	})
	<-done
	waitFor(t, e, func(core.Snapshot) bool { return e.Engine.Context() != nil })
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); !hasContextSection(s) {
		t.Fatalf("sections %v", sectionTitles(s))
	}
}

// TestContextAwareOff: with the preference off the detector (which reads
// other windows) is never called and no context is kept.
func TestContextAwareOff(t *testing.T) {
	e := newCtxEnv(t)
	e.Engine.SetContext(downloads)
	if err := e.Config.Update(func(c *config.Config) { c.ContextAware = false }); err != nil {
		t.Fatal(err)
	}
	if e.Engine.Context() != nil {
		t.Error("turning context awareness off keeps the context")
	}
	var called atomic.Bool
	e.Engine.DetectContext(func(context.Context) (plugin.Context, bool) {
		called.Store(true)
		return *downloads, true
	})
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	time.Sleep(50 * time.Millisecond)
	if called.Load() {
		t.Error("the detector ran although context awareness is off")
	}
	if s := e.Controller.Snapshot(); hasContextSection(s) || s.Context != nil {
		t.Errorf("context shown although off: %v", sectionTitles(s))
	}
}

// TestContextDetectionTimeout: a detector slower than the budget is
// abandoned.
func TestContextDetectionTimeout(t *testing.T) {
	e := newCtxEnv(t)
	release := make(chan struct{})
	defer close(release)
	e.Engine.DetectContext(func(context.Context) (plugin.Context, bool) {
		<-release // ignores ctx, as a hung COM call would
		return *downloads, true
	})
	time.Sleep(400 * time.Millisecond)
	if e.Engine.Context() != nil {
		t.Error("a context after the detection budget")
	}
}

// TestContextNewShowDropsOld: a show starts without the previous show's
// context, and a late result of an old detection is ignored.
func TestContextNewShowDropsOld(t *testing.T) {
	e := newCtxEnv(t)
	e.Engine.SetContext(downloads)
	release := make(chan struct{})
	e.Engine.DetectContext(func(ctx context.Context) (plugin.Context, bool) {
		<-release
		return *downloads, true
	})
	if e.Engine.Context() != nil {
		t.Fatal("the previous show's context is kept")
	}
	e.Engine.DetectContext(nil) // the next show, without detection
	close(release)
	time.Sleep(50 * time.Millisecond)
	if e.Engine.Context() != nil {
		t.Error("an old detection's result replaced the current show's context")
	}
}

// TestContextNotRecorded: running a context item does not put it into
// Recent, which would store the folder.
func TestContextNotRecorded(t *testing.T) {
	e := newCtxEnv(t)
	e.Engine.SetContext(downloads)
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	if _, err := e.Controller.Key(t.Context(), "Enter"); err != nil {
		t.Fatal(err)
	}
	eff := e.rec.Effects()
	if len(eff) != 1 || eff[0].Target != "wt.exe" || eff[0].Open == nil || eff[0].Open.Dir != downloads.Folder {
		t.Fatalf("effects %+v", eff)
	}
	e.Engine.SetContext(nil)
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	s := e.Controller.Snapshot()
	if slices.Contains(sectionTitles(s), "Recent") {
		t.Errorf("a context item was recorded: %v", sectionTitles(s))
	}
	for _, sec := range s.Sections {
		for _, it := range sec.Items {
			if strings.Contains(it.Subtitle, "Downloads") {
				t.Errorf("the context's folder shows in %q", sec.Title)
			}
		}
	}
}

// TestContextSearch: typed text matches context items, which rank first on
// a tie and carry the context section's title; RootItems gets the context.
func TestContextSearch(t *testing.T) {
	e := newCtxEnv(t)
	e.Engine.SetContext(downloads)
	e.Controller.Reset(t.Context())
	e.Controller.SetText(t.Context(), "terminal")
	e.Controller.Wait()
	s := e.Controller.Snapshot()
	if len(s.Sections) < 2 || s.Sections[0].Title != "In Downloads" || s.Sections[0].Items[0].Key != "ctx/terminal" {
		t.Fatalf("sections %+v", s.Sections)
	}
	seen := e.p.contexts()
	if len(seen) == 0 || seen[len(seen)-1] == nil || seen[len(seen)-1].Folder != downloads.Folder {
		t.Errorf("RootItems got contexts %v", seen)
	}
}

func TestCopyContextPath(t *testing.T) {
	e := newCtxEnv(t)
	e.Engine.SetContext(downloads)
	if err := e.Controller.CopyContextPath(); err != nil {
		t.Fatal(err)
	}
	if eff := e.rec.Effects(); len(eff) != 1 || eff[0].Kind != plugin.ActionCopyText || eff[0].Target != downloads.Folder {
		t.Errorf("effects %+v", eff)
	}
	if s := e.Controller.Snapshot(); s.Toast == nil || s.Toast.Style != plugin.ToastSuccess {
		t.Errorf("toast %+v", s.Toast)
	}
}

func TestContextName(t *testing.T) {
	for _, tt := range []struct {
		c    plugin.Context
		want string
	}{
		{plugin.Context{Folder: `C:\Users\ann\Downloads`}, "Downloads"},
		{plugin.Context{Folder: `C:\`}, `C:\`},
		{plugin.Context{Folder: `D:\项目\`}, "项目"},
		{plugin.Context{Title: "Inbox - Outlook", App: plugin.AppInfo{Name: "outlook.exe"}}, "Inbox - Outlook"},
		{plugin.Context{App: plugin.AppInfo{Name: "outlook.exe"}}, "outlook"},
	} {
		if got := core.ContextName(&tt.c); got != tt.want {
			t.Errorf("ContextName(%+v) = %q, want %q", tt.c, got, tt.want)
		}
	}
}

// TestContextArrivalSelectsFirstRow is the bug where selecting a file in
// Explorer and opening the launcher left the old first row ("Hello World")
// selected below the late context section, scrolling the list: until the
// user moves the selection, the context's first row is selected.
func TestContextArrivalSelectsFirstRow(t *testing.T) {
	for _, moved := range []bool{false, true} {
		e := newCtxEnv(t)
		release := make(chan struct{})
		e.Engine.DetectContext(func(ctx context.Context) (plugin.Context, bool) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return *downloads, true
		})
		e.Controller.Reset(t.Context())
		e.Controller.Wait()
		before, _ := e.Controller.Snapshot().SelectedEntry()
		if moved {
			e.Controller.Key(t.Context(), "Down")
			e.Controller.Key(t.Context(), "Up")
		}
		close(release)
		s := waitFor(t, e, hasContextSection)
		got, ok := s.SelectedEntry()
		switch {
		case !ok:
			t.Fatalf("moved=%v: nothing selected", moved)
		case !moved && (s.Selected != 0 || got.Title != "Open Terminal Here"):
			t.Errorf("untouched: selected %d %q, want the context's first row", s.Selected, got.Title)
		case moved && got.Key != before.Key:
			t.Errorf("after the user moved: selected %q, want %q kept", got.Title, before.Title)
		}
	}
}
