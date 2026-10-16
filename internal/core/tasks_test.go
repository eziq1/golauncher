package core_test

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/agent"
	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/tasks"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// withoutTasks drops the host-provided Background Tasks plugin.
func withoutTasks(infos []core.PluginInfo) []core.PluginInfo {
	return slices.DeleteFunc(infos, func(p core.PluginInfo) bool { return p.ID == tasks.PluginID || p.ID == agent.PluginID })
}

// taskPlugin starts background tasks from its actions.
type taskPlugin struct {
	h    plugin.Host
	exec bool
	done chan plugin.TaskResult
}

func (p *taskPlugin) Manifest() plugin.Manifest {
	m := plugin.Manifest{ID: "tp", Name: "Task Plugin", Version: "1",
		Commands: []plugin.Command{{Name: "build", Title: "Build Project", Mode: plugin.ModeNoView}}}
	if p.exec {
		m.Permissions = []plugin.Permission{plugin.PermExec}
	}
	return m
}

func (p *taskPlugin) Init(_ context.Context, h plugin.Host) error { p.h = h; return nil }
func (p *taskPlugin) Close() error                                { return nil }

func (p *taskPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	switch a.Target {
	case "build":
		id := p.h.RunProcess(plugin.TaskSpec{Title: "Build", Notify: plugin.NotifyAlways,
			OnDone: func(r plugin.TaskResult) { p.done <- r }},
			plugin.ProcessSpec{Argv: []string{"make", "all"}})
		return plugin.Result{Do: &plugin.ActionDo{Kind: plugin.ActionShowTask, Target: id}}, nil
	case "toast":
		p.h.Toast(plugin.Toast{Style: plugin.ToastInfo, Title: "Hello from the background"}) // lint:allow-literal
		return plugin.Result{}, nil
	case "bad-do":
		return plugin.Result{Do: &plugin.ActionDo{Kind: plugin.ActionCallback, Target: "build"}}, nil
	}
	return plugin.Result{}, nil
}

func buildTasks(t *testing.T, p *taskPlugin, runner tasks.Runner) *app.Env {
	t.Helper()
	clip := &hostfake.Clipboard{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{p}, Fake: true, Locale: "en", Now: func() time.Time { return hostfake.Fixed },
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip, TaskRunner: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	return env
}

func TestRunProcessFromAnAction(t *testing.T) {
	p := &taskPlugin{exec: true, done: make(chan plugin.TaskResult, 1)}
	runner := &tasks.FakeRunner{Results: []tasks.FakeResult{{Match: "make", Output: "compiling\nall done", ExitCode: 0}}}
	env := buildTasks(t, p, runner)
	finished := make(chan struct{}, 1)
	env.Services.Bus.Subscribe(plugin.EventTaskFinished, func() { finished <- struct{}{} })

	out, err := env.Controller.OpenCommand(t.Context(), "tp", "build")
	if err != nil {
		t.Fatal(err)
	}
	if out.Push == nil || out.Push.Plugin != tasks.PluginID || out.Push.Request.ID != tasks.ViewTask {
		t.Fatalf("Result.Do ShowTask should open the task: %+v", out)
	}
	r := <-p.done
	if r.State != plugin.TaskSucceeded || r.LastLine != "all done" {
		t.Errorf("OnDone got %+v", r)
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("no task-finished event")
	}
	if err := env.Tasks.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	env.Controller.Refresh(t.Context())
	env.Controller.Wait()
	s := env.Controller.Snapshot()
	e, ok := s.SelectedEntry()
	if !ok || e.Title != "Build" || e.Accessory != "✓ 0s" || !strings.Contains(e.Detail.Markdown, "compiling\nall done") {
		t.Fatalf("task view: %+v", s)
	}
	// NotifyAlways: the launcher shows the notification as a toast.
	if s.Toast == nil || s.Toast.Title != "Build finished" || s.Toast.Style != plugin.ToastSuccess {
		t.Errorf("toast = %+v", s.Toast)
	}
	if cmds := runner.Commands(); len(cmds) != 1 || cmds[0].Argv[1] != "all" {
		t.Errorf("commands = %+v", cmds)
	}
}

func TestRunProcessNeedsExecPermission(t *testing.T) {
	p := &taskPlugin{done: make(chan plugin.TaskResult, 1)}
	runner := &tasks.FakeRunner{}
	env := buildTasks(t, p, runner)
	// Through the engine: the controller would open the task, which marks
	// the failure seen.
	if _, err := env.Engine.RunCommand(t.Context(), "tp", "build"); err != nil {
		t.Fatal(err)
	}
	r := <-p.done
	if r.State != plugin.TaskFailed || !strings.Contains(r.Error, "exec permission") {
		t.Errorf("result = %+v", r)
	}
	if len(runner.Commands()) != 0 {
		t.Error("a plugin without the exec permission ran a program")
	}
	env.Tasks.Wait(t.Context())
	s := env.Controller.Snapshot()
	if s.Tasks == nil || s.Tasks.Failed != 1 || s.Tasks.Active != 0 || s.Tasks.Label != "A background task failed" {
		t.Fatalf("indicator = %+v", s.Tasks)
	}
	// Opening the task view clears the red dot.
	if _, err := env.Controller.Key(t.Context(), "Ctrl+J"); err != nil {
		t.Fatal(err)
	}
	env.Controller.Wait()
	s = env.Controller.Snapshot()
	if s.Title != "Background Tasks" || s.Tasks != nil {
		t.Fatalf("after Ctrl+J: title %q, indicator %+v", s.Title, s.Tasks)
	}
}

func TestIndicatorCountsActiveTasks(t *testing.T) {
	release := make(chan struct{})
	runner := &tasks.FakeRunner{Hook: func(_ plugin.ProcessSpec, out io.Writer, stop <-chan struct{}) int {
		select {
		case <-release:
			return 0
		case <-stop:
			return 1
		}
	}}
	env := buildTasks(t, &taskPlugin{exec: true}, runner)
	for range 2 {
		env.Tasks.RunProcess("tp", plugin.TaskSpec{Title: "Wait"}, plugin.ProcessSpec{Argv: []string{"x"}})
	}
	s := env.Controller.Snapshot()
	if s.Tasks == nil || s.Tasks.Active != 2 || s.Tasks.Label != "2 background tasks running" {
		t.Fatalf("indicator = %+v", s.Tasks)
	}
	close(release)
	env.Tasks.Wait(t.Context())
	if s := env.Controller.Snapshot(); s.Tasks != nil {
		t.Fatalf("indicator after the tasks = %+v", s.Tasks)
	}
}

func TestHostToastAndResultDo(t *testing.T) {
	p := &taskPlugin{exec: true}
	env := buildTasks(t, p, nil)
	var notices []core.Notice
	env.Engine.SetNotifier(func(n core.Notice) { notices = append(notices, n) })
	if _, err := env.Engine.Run(t.Context(), core.Entry{Plugin: "tp"}, plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "toast"}}, false); err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].Plugin != "tp" || notices[0].Toast.Title != "Hello from the background" {
		t.Errorf("notices = %+v", notices)
	}
	_, err := env.Engine.Run(t.Context(), core.Entry{Plugin: "tp"}, plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "bad-do"}}, false)
	if err == nil || !strings.Contains(err.Error(), "Result.Do") {
		t.Errorf("a Callback in Result.Do was accepted: %v", err)
	}
}

func TestActionRunProcess(t *testing.T) {
	runner := &tasks.FakeRunner{}
	env := buildTasks(t, &taskPlugin{exec: true}, runner)
	payload := plugin.MustPayload(plugin.RunProcessPayload{
		Spec: plugin.TaskSpec{Title: "Echo"}, Cmd: plugin.ProcessSpec{Argv: []string{"echo", "hi there"}}})
	out, err := env.Engine.Run(t.Context(), core.Entry{Plugin: "tp"}, plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionRunProcess, Payload: payload}}, false)
	if err != nil || out.Toast == nil || out.Toast.Title != "Running in the background: Echo" {
		t.Fatalf("outcome %+v, %v", out, err)
	}
	env.Tasks.Wait(t.Context())
	list := env.Tasks.List()
	if len(list) != 1 || list[0].Plugin != "tp" || list[0].State != plugin.TaskSucceeded || tasks.CommandLine(*list[0].Cmd) != `echo "hi there"` {
		t.Fatalf("tasks = %+v", list)
	}
}
