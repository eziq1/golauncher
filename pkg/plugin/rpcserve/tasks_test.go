package rpcserve

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/jsonrpc"
	"github.com/eziq1/golauncher/pkg/plugin/protocol"
)

// taskPlugin starts tasks and toasts from its actions.
type taskPlugin struct {
	h    plugin.Host
	done chan plugin.TaskResult
	ids  chan string
}

func (p *taskPlugin) Manifest() plugin.Manifest                   { return plugin.Manifest{ID: "tp"} }
func (p *taskPlugin) Init(_ context.Context, h plugin.Host) error { p.h = h; return nil }
func (p *taskPlugin) Close() error                                { return nil }
func (p *taskPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	switch a.Target {
	case "task":
		p.ids <- p.h.RunProcess(plugin.TaskSpec{Title: "Build", Queue: "q", OnDone: func(r plugin.TaskResult) { p.done <- r }},
			plugin.ProcessSpec{Argv: []string{"make"}})
	case "toast":
		p.h.Toast(plugin.Toast{Style: plugin.ToastInfo, Title: "hi"})
	}
	return plugin.Result{}, nil
}

func TestTasksAndToast(t *testing.T) {
	pr, hw := io.Pipe()
	hr, pw := io.Pipe()
	p := &taskPlugin{done: make(chan plugin.TaskResult, 2), ids: make(chan string, 2)}
	go ServeConn(p, pr, pw, io.Discard)
	var (
		mu     sync.Mutex
		specs  []protocol.RunProcessParams
		toasts []plugin.Toast
		c      *jsonrpc.Conn
		n      int
	)
	handle := func(_ context.Context, method string, params jsontext.Value, _ bool) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		switch method {
		case protocol.HostTaskRunProcess:
			var a protocol.RunProcessParams
			json.Unmarshal(params, &a)
			specs = append(specs, a)
			n++
			id := "t" + string(rune('0'+n))
			if n == 2 {
				// The task ends before the plugin learns its ID.
				c.Notify(protocol.NotifyTaskFinished, protocol.TaskFinishedParams{Result: plugin.TaskResult{ID: id, State: plugin.TaskFailed, ExitCode: 2}})
				time.Sleep(50 * time.Millisecond)
			}
			return protocol.RunProcessResult{ID: id}, nil
		case protocol.HostToast:
			var a protocol.ToastParams
			json.Unmarshal(params, &a)
			toasts = append(toasts, a.Toast)
		}
		return nil, nil
	}
	c = jsonrpc.NewConn(hr, hw, handle, nil)
	go c.Run()
	defer hw.Close()
	ctx := t.Context()
	if err := c.Call(ctx, protocol.MethodInitialize, protocol.InitializeParams{Protocol: 1, Locale: "en"}, nil); err != nil {
		t.Fatal(err)
	}
	act := func(target string) {
		t.Helper()
		if err := c.Call(ctx, protocol.MethodHandleAction, protocol.HandleActionParams{Action: plugin.ActionDo{Kind: plugin.ActionCallback, Target: target}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	act("task")
	if id := <-p.ids; id != "t1" {
		t.Fatalf("id %q", id)
	}
	c.Notify(protocol.NotifyTaskFinished, protocol.TaskFinishedParams{Result: plugin.TaskResult{ID: "t1", State: plugin.TaskSucceeded, LastLine: "ok"}})
	select {
	case r := <-p.done:
		if r.ID != "t1" || r.State != plugin.TaskSucceeded || r.LastLine != "ok" {
			t.Errorf("result %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnDone not called")
	}
	act("task")
	<-p.ids
	select {
	case r := <-p.done:
		if r.ID != "t2" || r.ExitCode != 2 {
			t.Errorf("early result %+v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnDone not called for a task that ended before its ID was known")
	}
	act("toast")
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(specs) != 2 || specs[0].Spec.Title != "Build" || specs[0].Spec.Queue != "q" || specs[0].Cmd.Argv[0] != "make" {
		t.Errorf("specs %+v", specs)
	}
	if len(toasts) != 1 || toasts[0].Title != "hi" {
		t.Errorf("toasts %+v", toasts)
	}
}
