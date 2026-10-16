package tasks_test

import (
	"encoding/json/v2"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/tasks"
	"github.com/eziq1/golauncher/pkg/plugin"
)

type pluginEnv struct {
	m       *tasks.Manager
	p       *tasks.Plugin
	h       *hostfake.Host
	release chan struct{}
}

// newPluginEnv starts builtin.tasks on a manager whose "hold" commands run
// until released and whose "fail" commands fail.
func newPluginEnv(t *testing.T) *pluginEnv {
	t.Helper()
	e := &pluginEnv{release: make(chan struct{})}
	runner := &tasks.FakeRunner{Hook: func(c plugin.ProcessSpec, out io.Writer, stop <-chan struct{}) int {
		io.WriteString(out, "output of "+c.Argv[0]+"\n")
		switch c.Argv[0] {
		case "hold":
			select {
			case <-e.release:
			case <-stop:
				return 1
			}
		case "fail":
			return 4
		}
		return 0
	}}
	e.m = tasks.New(tasks.Options{Dir: t.TempDir(), Runner: runner, Now: func() time.Time { return hostfake.Fixed }})
	e.p = tasks.NewPlugin(e.m)
	h, err := hostfake.New(e.p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	if err := e.p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.m.Close()
		e.p.Close()
	})
	return e
}

func (e *pluginEnv) view(t *testing.T, req plugin.ViewRequest) plugin.View {
	t.Helper()
	v, err := e.p.View(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (e *pluginEnv) wait(t *testing.T) {
	t.Helper()
	if err := e.m.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func actionIDs(it plugin.Item) []string {
	var ids []string
	for _, a := range it.Actions {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestTaskView(t *testing.T) {
	e := newPluginEnv(t)
	done := e.m.RunProcess("p", plugin.TaskSpec{Title: "Done"}, plugin.ProcessSpec{Argv: []string{"ok"}, Dir: `C:\work`})
	failed := e.m.RunProcess("p", plugin.TaskSpec{Title: "Broken", Icon: plugin.Icon{Name: "package"}}, plugin.ProcessSpec{Argv: []string{"fail"}})
	e.wait(t)
	running := e.m.RunProcess("p", plugin.TaskSpec{Title: "Long", Queue: "q"}, plugin.ProcessSpec{Argv: []string{"hold"}})
	queued := e.m.RunProcess("p", plugin.TaskSpec{Title: "Next", Queue: "q"}, plugin.ProcessSpec{Argv: []string{"hold"}})
	if _, failedCount := e.m.Indicator(); failedCount != 1 {
		t.Fatalf("unseen failures %d", failedCount)
	}

	v := e.view(t, plugin.ViewRequest{ID: tasks.ViewList, Instance: "v1"})
	var titles []string
	var ids []string
	for _, s := range v.Sections {
		titles = append(titles, s.Title)
		for _, it := range s.Items {
			ids = append(ids, it.ID)
		}
	}
	if !slices.Equal(titles, []string{"Running", "Queued", "Finished"}) || !slices.Equal(ids, []string{running, queued, failed, done}) {
		t.Fatalf("sections %v, items %v", titles, ids)
	}
	if !v.ShowDetail || !v.FilterLocally || v.Title != "Background Tasks" {
		t.Errorf("view %+v", v)
	}
	if _, f := e.m.Indicator(); f != 0 {
		t.Error("opening the view did not mark the failure seen")
	}
	run := v.Sections[0].Items[0]
	if run.Accessory != "00:00" || run.Subtitle != "Running…" || run.Icon.Tint != plugin.TintAccent ||
		!slices.Equal(actionIDs(run), []string{"open-log", "copy-output", "stop"}) || run.Actions[2].Style != plugin.StyleDestructive {
		t.Errorf("running item %+v", run)
	}
	fail := v.Sections[2].Items[0]
	if fail.Accessory != "✗ exit 4" || fail.Icon != (plugin.Icon{Name: "package", Tint: plugin.TintDanger}) ||
		!slices.Equal(actionIDs(fail), []string{"open-log", "copy-output", "rerun", "remove", "clear-finished"}) {
		t.Errorf("failed item %+v", fail)
	}
	ok := v.Sections[2].Items[1]
	meta := map[string]string{}
	for _, m := range ok.Detail.Metadata {
		meta[m.Label] = m.Value
	}
	if ok.Accessory != "✓ 0s" || !strings.Contains(ok.Detail.Markdown, "output of ok") || meta["Command"] != "ok" ||
		meta["Folder"] != `C:\work` || meta["Exit code"] != "0" || meta["Started"] != "Oct 14 10:00:00" || !strings.HasSuffix(meta["Log file"], ".log") {
		t.Errorf("done item %+v %v", ok, meta)
	}
	if ok.Actions[0].Do.Kind != plugin.ActionOpen || ok.Actions[0].Do.Target != meta["Log file"] {
		t.Errorf("primary action %+v", ok.Actions[0])
	}

	// One task, as notifications and Result.Do open it.
	tv := e.view(t, plugin.ViewRequest{ID: tasks.ViewTask, Payload: plugin.MustPayload(map[string]string{"id": failed})})
	if tv.Title != "Broken" || len(tv.Sections) != 1 || tv.Sections[0].Items[0].ID != failed ||
		tv.Sections[0].Items[0].Actions[5].Do.Kind != plugin.ActionShowTask {
		t.Errorf("task view %+v", tv)
	}
	if gone := e.view(t, plugin.ViewRequest{ID: tasks.ViewTask, Payload: plugin.MustPayload(map[string]string{"id": "t0"})}); len(gone.Sections) != 0 || gone.EmptyText == "" {
		t.Errorf("gone view %+v", gone)
	}

	// The open view follows the tasks through Render.
	close(e.release)
	e.wait(t)
	deadline := time.Now().Add(5 * time.Second)
	for {
		rs := e.h.Renders()
		if len(rs) > 0 && rs[len(rs)-1].Instance == "v1" && len(rs[len(rs)-1].View.Sections) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no render with all tasks finished: %d renders", len(rs))
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.p.ViewClosed("v1")
	n := len(e.h.Renders())
	e.m.RunProcess("p", plugin.TaskSpec{Title: "After"}, plugin.ProcessSpec{Argv: []string{"ok"}})
	e.wait(t)
	time.Sleep(600 * time.Millisecond)
	if len(e.h.Renders()) != n {
		t.Error("a closed view was rendered")
	}
}

func TestTaskActions(t *testing.T) {
	e := newPluginEnv(t)
	failed := e.m.RunProcess("p", plugin.TaskSpec{Title: "Broken"}, plugin.ProcessSpec{Argv: []string{"fail"}})
	e.wait(t)
	do := func(target, id string) plugin.Result {
		t.Helper()
		r, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: target, Payload: plugin.MustPayload(map[string]string{"id": id})})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := do("copy", failed); r.Do == nil || r.Do.Kind != plugin.ActionCopyText || r.Do.Target != "output of fail" {
		t.Errorf("copy %+v", r)
	}
	r := do("rerun", failed)
	if r.Do == nil || r.Do.Kind != plugin.ActionShowTask || r.Do.Target == failed || r.Toast == nil {
		t.Fatalf("rerun %+v", r)
	}
	e.wait(t)
	if i, _ := e.m.Get(r.Do.Target); i.Title != "Broken" || i.State != plugin.TaskFailed {
		t.Errorf("rerun task %+v", i)
	}
	held := e.m.RunProcess("p", plugin.TaskSpec{}, plugin.ProcessSpec{Argv: []string{"hold"}})
	if r := do("stop", held); !r.Refresh {
		t.Errorf("stop %+v", r)
	}
	e.wait(t)
	if i, _ := e.m.Get(held); i.State != plugin.TaskCancelled {
		t.Errorf("stopped %+v", i)
	}
	do("remove", failed)
	if _, ok := e.m.Get(failed); ok {
		t.Error("remove")
	}
	if r := do("clear", ""); r.Toast == nil || r.Toast.Title != "Removed 2 finished tasks" || len(e.m.List()) != 0 {
		t.Errorf("clear %+v %v", r.Toast, e.m.List())
	}
	if _, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "bogus", Payload: plugin.MustPayload(map[string]string{})}); err == nil {
		t.Error("unknown action accepted")
	}
}

// TestRetryAsAdministrator (R68): a task that failed for lack of
// administrator rights has Retry as Administrator first (Enter), a plain
// callback (the AI treats it as a change), which reruns it with Elevate.
func TestRetryAsAdministrator(t *testing.T) {
	runner := &tasks.FakeRunner{Results: []tasks.FakeResult{
		{Match: "ipconfig", Output: "The requested operation requires elevation.", ExitCode: 1},
	}}
	m := tasks.New(tasks.Options{Dir: t.TempDir(), Runner: runner, Now: func() time.Time { return hostfake.Fixed }})
	p := tasks.NewPlugin(m)
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(); p.Close() })
	id := m.RunProcess("p", plugin.TaskSpec{Title: "Flush DNS Cache"}, plugin.ProcessSpec{Argv: []string{"ipconfig", "/flushdns"}})
	if err := m.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: tasks.ViewTask, Payload: plugin.MustPayload(map[string]string{"id": id})})
	if err != nil {
		t.Fatal(err)
	}
	it := v.Sections[0].Items[0]
	if !slices.Equal(actionIDs(it), []string{"retry-admin", "open-log", "copy-output", "rerun", "remove", "clear-finished", "show-all"}) ||
		it.Subtitle != "Failed: requires administrator rights" {
		t.Fatalf("item %v %q", actionIDs(it), it.Subtitle)
	}
	a := it.Actions[0]
	if a.Title != "Retry as Administrator" || a.Do.Kind != plugin.ActionCallback || a.ReadOnly || a.Style == plugin.StyleDestructive {
		t.Errorf("retry action %+v", a)
	}
	r, err := p.HandleAction(t.Context(), a.Do)
	if err != nil {
		t.Fatal(err)
	}
	if r.Do == nil || r.Do.Kind != plugin.ActionShowTask || r.Do.Target == id {
		t.Fatalf("result %+v", r)
	}
	if err := m.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	i, _ := m.Get(r.Do.Target)
	if !i.Cmd.Elevate || i.State != plugin.TaskFailed || i.RetryableElevated() {
		t.Errorf("retried task %+v", i)
	}
	// The elevated task shows how it runs and no second retry.
	v, _ = p.View(t.Context(), plugin.ViewRequest{ID: tasks.ViewTask, Payload: plugin.MustPayload(map[string]string{"id": i.ID})})
	it = v.Sections[0].Items[0]
	meta := map[string]string{}
	for _, md := range it.Detail.Metadata {
		meta[md.Label] = md.Value
	}
	if slices.Contains(actionIDs(it), "retry-admin") || meta["Runs as"] != "Administrator" ||
		!strings.Contains(it.Detail.Markdown, "tasks.elevate.noOutput") { // the manager has no catalog here
		t.Errorf("elevated item %v %v %q", actionIDs(it), meta, it.Detail.Markdown)
	}
}

func TestManifestAndViewOf(t *testing.T) {
	m := tasks.New(tasks.Options{})
	defer m.Close()
	p := tasks.NewPlugin(m)
	man := p.Manifest()
	if man.ID != tasks.PluginID || len(man.Commands) != 1 || man.Commands[0].Name != tasks.ViewList || !slices.Contains(man.Commands[0].Keywords, "tasks") {
		t.Errorf("manifest %+v", man)
	}
	id, req := m.View("")
	if id != tasks.PluginID || req.ID != tasks.ViewList {
		t.Errorf("View(\"\") = %s %+v", id, req)
	}
	_, req = m.View("t1")
	var pl struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Payload, &pl); err != nil || req.ID != tasks.ViewTask || pl.ID != "t1" {
		t.Errorf("View(t1) = %+v", req)
	}
}
