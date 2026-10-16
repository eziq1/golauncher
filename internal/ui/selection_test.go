package ui_test

import (
	"context"
	"slices"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// selRows is a selection provider with a preview row and several actions.
type selRows struct{}

func (selRows) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "selrows", Name: "Sel", Version: "1", Permissions: []plugin.Permission{plugin.PermSelection}}
}
func (selRows) Init(context.Context, plugin.Host) error { return nil }
func (selRows) Close() error                            { return nil }
func (selRows) SelectionItems(_ context.Context, q plugin.SelectionQuery) ([]plugin.Item, error) {
	open := []plugin.Action{{ID: "open", Title: "Open", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: q.Selection.Text}}}
	items := []plugin.Item{{ID: "preview", Title: "“" + q.Selection.Text + "”", Score: 1, Actions: open}}
	for _, title := range []string{"Translate to English", "Ask AI", "Explain", "Rewrite", "Suggest Replies"} {
		items = append(items, plugin.Item{ID: title, Title: title, Actions: open})
	}
	return items, nil
}

// TestLateSelectionWithPointerOnList: the selected text arrives 100ms after
// the root list (taller than the window, with a context section) showed,
// while the pointer rests over the list and hovered a row: the selection's
// section is at the top, its header above its first row, and the preview
// row is selected (R26/R27, docs/selected-text.md §3.3).
func TestLateSelectionWithPointerOnList(t *testing.T) {
	clip := &hostfake.Clipboard{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{ctxRows{}, selRows{}, testPlugin{}}, Fake: true, Locale: "en",
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	env.Engine.DetectContext(func(context.Context) (plugin.Context, bool) {
		return plugin.Context{App: plugin.AppInfo{Name: "explorer.exe"}, Kind: plugin.ContextExplorer, Folder: `C:\Users\ann\Photos`}, true
	})
	release := make(chan struct{})
	env.Engine.DetectSelection(func(ctx context.Context) (plugin.Selection, bool) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return plugin.Selection{Text: "Let's meet on Thursday", App: plugin.AppInfo{Name: "notepad.exe"}, Source: plugin.SelectionUIA}, true
	})
	lt := &launcherTest{Env: env, t: t, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	for range 50 { // the context comes first
		lt.step()
		if lt.tt.HasText("In Photos") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	lt.tt.Move(300, 200)
	lt.step()
	lt.tt.Move(300, 260)
	lt.step()
	if s := env.Controller.Snapshot(); s.Selected == 0 {
		t.Fatalf("hovering did not select a row (texts %q)", lt.tt.Texts())
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	for range 50 {
		lt.step()
		if lt.tt.HasText("Selected Text") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	lt.step() // a frame with the rows moved under the resting pointer
	s := env.Controller.Snapshot()
	if e, _ := s.SelectedEntry(); s.Selected != 0 || e.Key != "selrows/preview" {
		t.Errorf("selected row %d %q, want the preview row", s.Selected, e.Key)
	}
	header, ok1 := lt.tt.Find("Selected Text")
	first, ok2 := lt.tt.Find("“Let's meet on Thursday”")
	if !ok1 || !ok2 {
		t.Fatalf("header visible %v, first row visible %v; texts %q", ok1, ok2, lt.tt.Texts())
	}
	if header.Y < 40 || header.Y+header.H > first.Y {
		t.Errorf("header %+v and first row %+v: want the header at the top of the list, above the row", header, first)
	}
	if ctxHeader, ok := lt.tt.Find("In Photos"); ok && ctxHeader.Y < first.Y {
		t.Errorf("the context section %+v is above the selection %+v", ctxHeader, first)
	}
	// The footer chip tells how much was selected.
	if !lt.tt.HasText("22 characters selected") {
		t.Errorf("no selection chip in %q", lt.tt.Texts())
	}
}

// historyHost reports Windows clipboard history as on.
type historyHost struct{ fakeHost }

func (*historyHost) ClipboardHistory() (bool, bool) { return true, false }

// TestSettingsSelection: the settings of the selected text show under
// context awareness and only while it is on; the copying fallback (off by
// default) shows its app list and the clipboard history warning; the lists
// and the maximum length are saved.
func TestSettingsSelection(t *testing.T) {
	env, _ := newEnv(t, "en")
	host := &historyHost{}
	s := ui.NewSettings(env, host, "1.0.0-test")
	tt := mui.NewTester(s.View, ui.SettingsWidth, 1600)
	tt.Frame()
	cfg := env.Config.Get()
	if cfg.Selection.Clipboard || cfg.Selection.EffectiveMaxChars() != config.DefaultSelectionMaxChars ||
		!slices.Equal(cfg.Selection.ExcludeApps, config.DefaultSelectionExcludeApps) {
		t.Fatalf("defaults %+v", cfg.Selection)
	}
	fallback := "Fall back to copying when the text can't be read"
	for _, want := range []string{fallback, "Open for the selected text", "Never read the selected text in", "Read at most (characters)"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("Always copy in these apps") || tt.HasText("Windows clipboard history or the cloud clipboard is on: it may keep the text copied this way.") {
		t.Error("the copying settings show while copying is off")
	}
	if err := tt.Click(fallback); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !env.Config.Get().Selection.Clipboard {
		t.Fatal("the fallback did not turn on")
	}
	for _, want := range []string{"Always copy in these apps", "Windows clipboard history or the cloud clipboard is on: it may keep the text copied this way."} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}

	// Context awareness off hides them all.
	if err := tt.Click("Context awareness"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText(fallback) {
		t.Error("the selection settings show with context awareness off")
	}
}
