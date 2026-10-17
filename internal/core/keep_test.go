package core_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// keepPlugin has a "chat" view that asks to be kept across a hide
// (plugin.View.KeepOnHide), with an action pushing a kept "sub" view, and a
// "plain" view that does not ask.
type keepPlugin struct {
	mu       sync.Mutex
	refuse   bool                 // refuse restores
	requests []plugin.ViewRequest // every View call
	closed   []string             // instances closed
	closes   int                  // Close calls
}

func (p *keepPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "keep", Name: "Keep", Version: "1", Commands: []plugin.Command{
		{Name: "chat", Title: "Keep Chat", Mode: plugin.ModeView},
		{Name: "plain", Title: "Plain List", Mode: plugin.ModeView},
	}}
}

func (p *keepPlugin) Init(context.Context, plugin.Host) error { return nil }
func (p *keepPlugin) Close() error {
	p.mu.Lock()
	p.closes++
	p.mu.Unlock()
	return nil
}

func (p *keepPlugin) View(_ context.Context, req plugin.ViewRequest) (plugin.View, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if req.Restore && p.refuse {
		return plugin.View{}, errors.New("gone")
	}
	var items []plugin.Item
	for _, id := range []string{"one", "two", "three"} {
		items = append(items, plugin.Item{ID: id, Title: id, Actions: []plugin.Action{
			{ID: "sub", Title: "Open", Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: "sub"}},
			{ID: "copy", Title: "Copy", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: id}},
		}})
	}
	return plugin.View{Kind: plugin.ViewList, Title: req.ID, Sections: []plugin.Section{{Items: items}}, KeepOnHide: req.ID != "plain"}, nil
}

func (p *keepPlugin) ViewClosed(instance string) {
	p.mu.Lock()
	p.closed = append(p.closed, instance)
	p.mu.Unlock()
}

func (p *keepPlugin) lastRequest() plugin.ViewRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[len(p.requests)-1]
}

// waitClosed waits until the instances were closed (the hooks run in the
// background).
func (p *keepPlugin) waitClosed(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.mu.Lock()
		got := slices.Clone(p.closed)
		p.mu.Unlock()
		slices.Sort(got)
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("closed = %v, want %v", got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (p *keepPlugin) closedNow() []string {
	time.Sleep(20 * time.Millisecond) // the hooks would have run
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.closed)
}

type keepEnv struct {
	*env
	kp *keepPlugin
}

func newKeepEnv(t *testing.T) *keepEnv {
	t.Helper()
	clip := &hostfake.Clipboard{}
	e := &keepEnv{env: &env{rec: &app.Recorder{Clipboard: clip}, tp: &testPlugin{}, now: hostfake.Fixed}, kp: &keepPlugin{}}
	var err error
	e.Env, err = app.Build(t.Context(), app.Options{
		Plugins:   []plugin.Plugin{e.tp, e.kp},
		Fake:      true,
		Now:       func() time.Time { return e.now },
		Locale:    "en",
		System:    e.rec,
		Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	return e
}

// open opens the command titled title from the root search.
func (e *keepEnv) open(t *testing.T, title string) core.Snapshot {
	t.Helper()
	e.typeText(t, title)
	e.key(t, "Enter")
	s := e.Controller.Snapshot()
	if s.Depth != 1 {
		t.Fatalf("%s did not open: depth %d", title, s.Depth)
	}
	return s
}

// hideShow hides the window, lets after pass and shows it again.
func (e *keepEnv) hideShow(t *testing.T, after time.Duration, o core.ShowOptions) core.Snapshot {
	t.Helper()
	e.Controller.Hidden()
	e.now = e.now.Add(after)
	e.Controller.Show(t.Context(), o)
	e.Controller.Wait()
	return e.Controller.Snapshot()
}

func setReturnToRoot(t *testing.T, e *keepEnv, v string) {
	t.Helper()
	if err := e.Config.Update(func(c *config.Config) { c.ReturnToRoot = v }); err != nil {
		t.Fatal(err)
	}
}

func TestKeepRestoresStack(t *testing.T) {
	e := newKeepEnv(t)
	e.open(t, "Keep Chat")
	e.typeText(t, "t") // matches nothing locally; the plugin ignores it
	e.key(t, "Down")
	inst := e.kp.lastRequest().Instance

	s := e.hideShow(t, 9*time.Minute, core.ShowOptions{})
	if s.Depth != 1 || s.Title != "chat" || s.Text != "t" || s.Selected != 1 {
		t.Fatalf("restored = depth %d title %q text %q selected %d", s.Depth, s.Title, s.Text, s.Selected)
	}
	if req := e.kp.lastRequest(); !req.Restore || req.Instance != inst || req.Query.Text != "t" {
		t.Fatalf("restore request = %+v", req)
	}
	if closed := e.kp.closedNow(); len(closed) != 0 {
		t.Fatalf("kept views were closed: %v", closed)
	}
	// Typing in the restored view is an ordinary request again.
	e.typeText(t, "")
	if e.kp.lastRequest().Restore {
		t.Fatal("a later request still says Restore")
	}
	// Escape from the view goes to the root search, whose text is kept too.
	e.key(t, "Escape")
	if s := e.Controller.Snapshot(); s.Depth != 0 || s.Text != "Keep Chat" {
		t.Fatalf("after Escape: depth %d text %q", s.Depth, s.Text)
	}
	e.kp.waitClosed(t, inst)
}

func TestKeepHint(t *testing.T) {
	e := newKeepEnv(t)
	e.open(t, "Keep Chat")
	if s := e.Controller.Snapshot(); s.Hint != "" {
		t.Fatalf("hint before any hide: %q", s.Hint)
	}
	s := e.hideShow(t, time.Minute, core.ShowOptions{})
	if s.Hint != "Back to main screen" {
		t.Fatalf("hint = %q", s.Hint)
	}
	// Deeper: Escape goes back one level.
	e.key(t, "Enter") // pushes "sub"
	if s := e.Controller.Snapshot(); s.Depth != 2 || s.Hint != "" {
		t.Fatalf("after push: depth %d hint %q", s.Depth, s.Hint)
	}
	s = e.hideShow(t, time.Minute, core.ShowOptions{})
	if s.Depth != 2 || s.Hint != "Back" {
		t.Fatalf("deep restore: depth %d hint %q", s.Depth, s.Hint)
	}
	e.key(t, "Escape")
	if s := e.Controller.Snapshot(); s.Depth != 1 || s.Hint != "" {
		t.Fatalf("after Escape: depth %d hint %q", s.Depth, s.Hint)
	}
	// Kept again at depth 1.
	s = e.hideShow(t, time.Minute, core.ShowOptions{})
	if s.Depth != 1 || s.Hint != "Back to main screen" {
		t.Fatalf("restore again: depth %d hint %q", s.Depth, s.Hint)
	}
	// The hint's click: straight to the root.
	e.Controller.PopToRoot(t.Context())
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); s.Depth != 0 || s.Hint != "" {
		t.Fatalf("after PopToRoot: depth %d hint %q", s.Depth, s.Hint)
	}
}

func TestKeepTimeout(t *testing.T) {
	e := newKeepEnv(t)
	e.open(t, "Keep Chat")
	e.key(t, "Enter") // a second kept level
	first, second := e.kp.requests[0].Instance, e.kp.lastRequest().Instance
	s := e.hideShow(t, 11*time.Minute, core.ShowOptions{})
	if s.Depth != 0 || s.Text != "" || s.Hint != "" {
		t.Fatalf("after 11 minutes: depth %d text %q hint %q", s.Depth, s.Text, s.Hint)
	}
	e.kp.waitClosed(t, first, second)
}

func TestKeepSettings(t *testing.T) {
	e := newKeepEnv(t)
	setReturnToRoot(t, e, config.ReturnToRootImmediately)
	e.open(t, "Keep Chat")
	if s := e.hideShow(t, 0, core.ShowOptions{}); s.Depth != 0 {
		t.Fatalf("immediately: depth %d", s.Depth)
	}
	setReturnToRoot(t, e, config.ReturnToRootNever)
	e.open(t, "Keep Chat")
	if s := e.hideShow(t, 72*time.Hour, core.ShowOptions{}); s.Depth != 1 {
		t.Fatalf("never: depth %d", s.Depth)
	}
}

func TestKeepOnlyKeptViews(t *testing.T) {
	e := newKeepEnv(t)
	e.open(t, "Plain List")
	inst := e.kp.lastRequest().Instance
	if s := e.hideShow(t, 0, core.ShowOptions{}); s.Depth != 0 {
		t.Fatalf("a view without KeepOnHide was restored: depth %d", s.Depth)
	}
	e.kp.waitClosed(t, inst)
	// Hidden at the root: nothing kept.
	if s := e.hideShow(t, 0, core.ShowOptions{}); s.Depth != 0 {
		t.Fatalf("root: depth %d", s.Depth)
	}
	// Reset always goes to the root.
	e.open(t, "Keep Chat")
	e.Controller.Hidden()
	e.Controller.Reset(t.Context())
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); s.Depth != 0 {
		t.Fatalf("Reset restored: depth %d", s.Depth)
	}
}

func TestKeepRootShow(t *testing.T) {
	e := newKeepEnv(t)
	e.open(t, "Keep Chat")
	inst := e.kp.lastRequest().Instance
	if s := e.hideShow(t, 0, core.ShowOptions{Root: true}); s.Depth != 0 {
		t.Fatalf("selected-text hotkey: depth %d", s.Depth)
	}
	e.kp.waitClosed(t, inst)
}

func TestKeepSelectionGoesToRoot(t *testing.T) {
	sel := &plugin.Selection{Text: "some selected words"}

	t.Run("before show", func(t *testing.T) {
		e := newKeepEnv(t)
		e.open(t, "Keep Chat")
		e.Controller.Hidden()
		e.Engine.SetSelection(sel)
		e.Controller.Show(t.Context(), core.ShowOptions{})
		e.Controller.Wait()
		if s := e.Controller.Snapshot(); s.Depth != 0 || s.Selection == nil {
			t.Fatalf("depth %d selection %v", s.Depth, s.Selection)
		}
	})
	t.Run("read after the restore", func(t *testing.T) {
		e := newKeepEnv(t)
		e.open(t, "Keep Chat")
		inst := e.kp.lastRequest().Instance
		e.hideShow(t, 0, core.ShowOptions{})
		e.Engine.SetSelection(sel)
		e.Controller.Wait()
		if s := e.Controller.Snapshot(); s.Depth != 0 {
			t.Fatalf("depth %d", s.Depth)
		}
		e.kp.waitClosed(t, inst)
	})
	t.Run("after the user acted", func(t *testing.T) {
		e := newKeepEnv(t)
		e.open(t, "Keep Chat")
		e.hideShow(t, 0, core.ShowOptions{})
		e.key(t, "Down")
		e.Engine.SetSelection(sel)
		e.Controller.Wait()
		if s := e.Controller.Snapshot(); s.Depth != 1 {
			t.Fatalf("the selection interrupted the user: depth %d", s.Depth)
		}
	})
	// An unsent draft keeps the view, whenever the selection arrives (R76).
	t.Run("draft, selection before show", func(t *testing.T) {
		e := newKeepEnv(t)
		e.open(t, "Keep Chat")
		e.typeText(t, "half a message")
		e.Controller.Hidden()
		e.Engine.SetSelection(sel)
		e.Controller.Show(t.Context(), core.ShowOptions{})
		e.Controller.Wait()
		if s := e.Controller.Snapshot(); s.Depth != 1 || s.Text != "half a message" {
			t.Fatalf("depth %d text %q", s.Depth, s.Text)
		}
	})
	t.Run("draft, selection read after the restore", func(t *testing.T) {
		e := newKeepEnv(t)
		e.open(t, "Keep Chat")
		e.typeText(t, "half a message")
		e.hideShow(t, 0, core.ShowOptions{})
		e.Engine.SetSelection(sel)
		e.Controller.Wait()
		if s := e.Controller.Snapshot(); s.Depth != 1 || s.Text != "half a message" {
			t.Fatalf("depth %d text %q", s.Depth, s.Text)
		}
	})
	// Blank text is no draft.
	t.Run("blank text", func(t *testing.T) {
		e := newKeepEnv(t)
		e.open(t, "Keep Chat")
		e.typeText(t, "   ")
		e.Controller.Hidden()
		e.Engine.SetSelection(sel)
		e.Controller.Show(t.Context(), core.ShowOptions{})
		e.Controller.Wait()
		if s := e.Controller.Snapshot(); s.Depth != 0 {
			t.Fatalf("depth %d", s.Depth)
		}
	})
	// The selected-text hotkey still goes to the root, draft or not.
	t.Run("draft, selected-text hotkey", func(t *testing.T) {
		e := newKeepEnv(t)
		e.open(t, "Keep Chat")
		e.typeText(t, "half a message")
		if s := e.hideShow(t, 0, core.ShowOptions{Root: true}); s.Depth != 0 {
			t.Fatalf("depth %d", s.Depth)
		}
	})
}

// TestKeepDraftView: a view declaring a draft besides its text
// (plugin.View.Draft, a chat's attachments) is kept for newly selected text
// like typed text (R76, R91).
func TestKeepDraftView(t *testing.T) {
	sel := &plugin.Selection{Text: "some selected words"}
	showWithSelection := func(e *filesEnv) core.Snapshot {
		e.Controller.Hidden()
		e.Engine.SetSelection(sel)
		e.Controller.Show(t.Context(), core.ShowOptions{})
		e.Controller.Wait()
		return e.Controller.Snapshot()
	}
	e := newFilesEnv(t)
	e.open(t, "Bravo Chat")
	if s := showWithSelection(e); s.Depth != 0 {
		t.Fatalf("no draft: depth %d", s.Depth)
	}
	e.Engine.ClearSelection()
	e.open(t, "Bravo Chat")
	e.drop(t, "notes.txt")
	s := showWithSelection(e)
	if s.Depth != 1 || len(s.Chips) != 1 {
		t.Fatalf("draft: depth %d chips %v", s.Depth, s.Chips)
	}
	// Also when the selection arrives after the restore.
	e.Engine.ClearSelection()
	e.Controller.Hidden()
	e.Controller.Show(t.Context(), core.ShowOptions{})
	e.Controller.Wait()
	e.Engine.SetSelection(sel)
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); s.Depth != 1 {
		t.Fatalf("draft, selection read after the restore: depth %d", s.Depth)
	}
}

func TestKeepRefused(t *testing.T) {
	e := newKeepEnv(t)
	e.open(t, "Keep Chat")
	inst := e.kp.lastRequest().Instance
	e.kp.mu.Lock()
	e.kp.refuse = true
	e.kp.mu.Unlock()
	s := e.hideShow(t, 0, core.ShowOptions{})
	if s.Depth != 0 || s.Error != "" || s.Toast != nil {
		t.Fatalf("refused restore: depth %d error %q toast %v", s.Depth, s.Error, s.Toast)
	}
	if len(s.Sections) == 0 {
		t.Fatal("the root search did not run")
	}
	e.kp.waitClosed(t, inst)
}

func TestKeepDisabledPlugin(t *testing.T) {
	e := newKeepEnv(t)
	e.open(t, "Keep Chat")
	inst := e.kp.lastRequest().Instance
	e.Controller.Hidden()
	if err := e.Config.Update(func(c *config.Config) {
		if c.Plugins == nil {
			c.Plugins = map[string]config.PluginConfig{}
		}
		c.Plugins["keep"] = config.PluginConfig{Disabled: true}
	}); err != nil {
		t.Fatal(err)
	}
	e.Controller.Show(t.Context(), core.ShowOptions{})
	e.Controller.Wait()
	if s := e.Controller.Snapshot(); s.Depth != 0 {
		t.Fatalf("disabled plugin restored: depth %d", s.Depth)
	}
	// The kept view is released either way: told it closed (the show came
	// first) or, the plugin being turned off in the background (R88), the
	// whole plugin was Closed and nothing more reaches it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.kp.mu.Lock()
		closed, closes := slices.Clone(e.kp.closed), e.kp.closes
		e.kp.mu.Unlock()
		if slices.Equal(closed, []string{inst}) || closes > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("neither ViewClosed(%s) nor Close: closed %v", inst, closed)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestKeepClosesPanelAndConfirm(t *testing.T) {
	e := newKeepEnv(t)
	e.open(t, "Keep Chat")
	e.key(t, "Ctrl+K")
	if e.Controller.Snapshot().Panel == nil {
		t.Fatal("no panel")
	}
	s := e.hideShow(t, 0, core.ShowOptions{})
	if s.Depth != 1 || s.Panel != nil {
		t.Fatalf("depth %d panel %v", s.Depth, s.Panel)
	}
}
