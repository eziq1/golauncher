package control_test

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/control"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/calc"
)

type fakeWindow struct{ visible atomic.Bool }

func (w *fakeWindow) Show()         { w.visible.Store(true) }
func (w *fakeWindow) Hide()         { w.visible.Store(false) }
func (w *fakeWindow) Visible() bool { return w.visible.Load() }

func TestRoundTrip(t *testing.T) {
	clip := &hostfake.Clipboard{}
	rec := &app.Recorder{Clipboard: clip}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{calc.New()}, Fake: true, Locale: "en",
		System: rec, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()

	sock := filepath.Join(t.TempDir(), "control.sock")
	win := &fakeWindow{}
	go control.Serve(t.Context(), sock, env.Controller, win)

	var c *control.Client
	for range 50 {
		if c, err = control.Dial(sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if r, err := c.Do(control.Request{Cmd: "show"}); err != nil || !r.Visible {
		t.Fatalf("show: %+v %v", r, err)
	}
	r, err := c.Do(control.Request{Cmd: "type", Text: "6*7"})
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := r.Snapshot.SelectedEntry(); !ok || e.Title != "42" {
		t.Fatalf("snapshot = %+v", r.Snapshot)
	}
	r, err = c.Do(control.Request{Cmd: "key", Key: "Enter"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Visible || !r.Outcome.Hide {
		t.Fatalf("Enter should copy and hide: %+v", r)
	}
	if got, _ := clip.Read(); got.Text != "42" {
		t.Fatalf("clipboard = %q", got.Text)
	}
	if _, err := c.Do(control.Request{Cmd: "bogus"}); err == nil {
		t.Fatal("want error for unknown command")
	}
}
