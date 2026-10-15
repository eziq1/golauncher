package ui_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// newTasksLauncher is a launcher whose Go tasks are controlled by the test.
func newTasksLauncher(t *testing.T) *launcherTest {
	t.Helper()
	clip := &hostfake.Clipboard{}
	rec := &app.Recorder{Clipboard: clip}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{testPlugin{}}, Fake: true, Locale: "en",
		System: rec, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	lt := &launcherTest{Env: env, t: t, rec: rec, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.l.Shown()
	lt.step()
	return lt
}

// until steps the launcher until cond holds (views follow tasks through
// pushes that arrive a little later).
func (lt *launcherTest) until(what string, cond func() bool) {
	lt.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		lt.step()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			lt.t.Fatalf("timed out waiting for %s; texts %q", what, lt.tt.Texts())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTasksIndicatorAndView(t *testing.T) {
	lt := newTasksLauncher(t)
	release := make(chan struct{})
	write := make(chan string)
	lt.Tasks.RunTask("test", plugin.TaskSpec{Title: "Long Job"}, func(ctx context.Context, tk plugin.Task) error {
		for {
			select {
			case s := <-write:
				io.WriteString(tk, s+"\n")
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
	lt.step()
	// The footer shows a spinner with the number of running tasks.
	if s := lt.Controller.Snapshot(); s.Tasks == nil || s.Tasks.Active != 1 {
		t.Fatalf("indicator %+v", s.Tasks)
	}
	lt.wantText("1")
	// Clicking it opens Background Tasks.
	if err := lt.tt.Click("1 background task running"); err != nil {
		t.Fatalf("%v; texts %q", err, lt.tt.Texts())
	}
	lt.step()
	lt.wantText("Background Tasks")
	lt.wantText("Long Job")

	// The open view follows the task's output.
	write <- "first line of output"
	lt.until("the output", func() bool { return lt.tt.HasText("first line of output") })
	close(release)
	lt.until("the end", func() bool { return lt.tt.HasText("✓ 0s") })
	if s := lt.Controller.Snapshot(); s.Tasks != nil {
		t.Errorf("indicator after the task: %+v", s.Tasks)
	}
}

func TestTasksRedDotAndCtrlJ(t *testing.T) {
	lt := newTasksLauncher(t)
	lt.Tasks.RunTask("test", plugin.TaskSpec{Title: "Broken Job", Notify: plugin.NotifyNever}, func(context.Context, plugin.Task) error {
		return errors.New("no network")
	})
	lt.Tasks.Wait(t.Context())
	lt.step()
	s := lt.Controller.Snapshot()
	if s.Tasks == nil || s.Tasks.Failed != 1 || s.Tasks.Active != 0 {
		t.Fatalf("indicator %+v", s.Tasks)
	}
	if lt.tt.HasText("0") {
		t.Error("a count shows without running tasks")
	}
	lt.key(mui.Ctrl, mui.KeyJ)
	s = lt.Controller.Snapshot()
	if s.Title != "Background Tasks" || s.Tasks != nil {
		t.Fatalf("after Ctrl+J: %q %+v", s.Title, s.Tasks)
	}
	lt.wantText("Broken Job")
	lt.wantText("✗ failed")
	// Escape goes back to the root search.
	lt.key(0, mui.KeyEscape)
	if s := lt.Controller.Snapshot(); s.Depth != 0 {
		t.Errorf("depth %d after Escape", s.Depth)
	}
}

func TestTaskNotificationToast(t *testing.T) {
	lt := newTasksLauncher(t)
	lt.Tasks.RunTask("test", plugin.TaskSpec{Title: "Export", Notify: plugin.NotifyAlways}, func(_ context.Context, tk plugin.Task) error {
		io.WriteString(tk, "42 files\n")
		return nil
	})
	lt.Tasks.Wait(t.Context())
	lt.step()
	lt.wantText("Export finished")
	lt.wantText("42 files")
}
