package extproc

import (
	"encoding/json/v2"
	"errors"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/jsonrpc"
	"github.com/eziq1/golauncher/pkg/plugin/protocol"
)

// TestHostTasksAndToast: host/task.runProcess needs the exec permission and
// queues a task of the host; host/toast reaches Host.Toast.
func TestHostTasksAndToast(t *testing.T) {
	handler := func(perms ...plugin.Permission) (*hostfake.Host, func(method string, params any) (any, error)) {
		p := New(t.TempDir(), Manifest{Manifest: plugin.Manifest{ID: "x", Permissions: perms}}, Options{}, quiet)
		h, err := hostfake.New(p, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		p.h = h
		t.Cleanup(func() { h.Tasks.Close() })
		handle := p.hostHandler(&process{subs: map[string]func(){}})
		return h, func(method string, params any) (any, error) {
			b, _ := json.Marshal(params)
			return handle(t.Context(), method, b, false)
		}
	}
	run := protocol.RunProcessParams{Spec: plugin.TaskSpec{Title: "Build", Queue: "q"}, Cmd: plugin.ProcessSpec{Argv: []string{"make", "all"}}}

	h, call := handler()
	if _, err := call(protocol.HostTaskRunProcess, run); !isPermissionDenied(err) {
		t.Errorf("without exec: %v", err)
	}
	if len(h.Tasks.List()) != 0 {
		t.Error("a task was queued without the exec permission")
	}
	if _, err := call(protocol.HostToast, protocol.ToastParams{Toast: plugin.Toast{Style: plugin.ToastInfo, Title: "hello"}}); err != nil {
		t.Fatal(err)
	}
	if ts := h.Toasts(); len(ts) != 1 || ts[0].Title != "hello" {
		t.Errorf("toasts %+v", ts)
	}

	h, call = handler(plugin.PermExec)
	res, err := call(protocol.HostTaskRunProcess, run)
	if err != nil {
		t.Fatal(err)
	}
	id := res.(protocol.RunProcessResult).ID
	h.WaitTasks()
	i, ok := h.Tasks.Get(id)
	if !ok || i.Plugin != "x" || i.Title != "Build" || i.Queue != "q" || i.State != plugin.TaskSucceeded {
		t.Errorf("task %+v", i)
	}
	if c := h.Runner.Commands(); len(c) != 1 || c[0].Argv[1] != "all" {
		t.Errorf("commands %+v", c)
	}
}

func isPermissionDenied(err error) bool {
	e, ok := errors.AsType[*jsonrpc.Error](err)
	return ok && e.Code == jsonrpc.CodePermissionDenied
}
