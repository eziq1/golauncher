package rpcserve

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/jsonrpc"
	"github.com/eziq1/golauncher/pkg/plugin/protocol"
)

type testPlugin struct {
	h      plugin.Host
	events chan string
}

func (p *testPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "t", Preferences: []plugin.Preference{{Name: "greeting", Type: plugin.PrefText, Default: jsontext.Value(`"hi"`)}}}
}
func (p *testPlugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	h.Events().Subscribe(plugin.EventClipboardChanged, func() { p.events <- plugin.EventClipboardChanged })
	h.Events().Subscribe(plugin.EventConfigChanged, func() { p.events <- plugin.EventConfigChanged })
	return nil
}
func (p *testPlugin) Close() error { return nil }

func (p *testPlugin) RootItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	if q.Text == "panic" {
		panic("boom")
	}
	title := p.h.Preferences().String("greeting") + " " + q.Text
	if q.Context != nil {
		title += " in " + q.Context.Folder
	}
	return []plugin.Item{{ID: "1", Title: title}}, nil
}

func (p *testPlugin) ContextItems(_ context.Context, c plugin.Context) ([]plugin.Item, error) {
	return []plugin.Item{{ID: "here", Title: c.Kind + ":" + c.Folder + ":" + strings.Join(c.Selected, ",")}}, nil
}

func (p *testPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	switch a.Target {
	case "store":
		if err := p.h.Storage().Set("bin", []byte{0xff, 0, 1}); err != nil {
			return plugin.Result{}, err
		}
		v, ok, err := p.h.Storage().Get("bin")
		if err != nil || !ok || string(v) != string([]byte{0xff, 0, 1}) {
			return plugin.Result{}, errors.New("storage round trip failed")
		}
	case "render":
		p.h.Render("v1", plugin.View{Title: "a"})
		p.h.Render("v1", plugin.View{Title: "b"})
	}
	return plugin.Result{Hide: true}, nil
}

// fakeHost is the launcher side of the connection.
type fakeHost struct {
	mu      sync.Mutex
	kv      map[string]string
	renders []protocol.RenderParams
	subs    []string
}

func (h *fakeHost) handle(_ context.Context, method string, params jsontext.Value, notify bool) (any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch method {
	case protocol.HostStorageSet:
		var a protocol.StorageSetParams
		json.Unmarshal(params, &a)
		h.kv[a.Key] = a.Base64
		return nil, nil
	case protocol.HostStorageGet:
		var a protocol.StorageKeyParams
		json.Unmarshal(params, &a)
		v, ok := h.kv[a.Key]
		return protocol.StorageGetResult{Found: ok, StorageValue: protocol.StorageValue{Base64: v}}, nil
	case protocol.HostSubscribe:
		var a protocol.SubscribeParams
		json.Unmarshal(params, &a)
		h.subs = append(h.subs, a.Event)
		return nil, nil
	case protocol.NotifyRender:
		var a protocol.RenderParams
		json.Unmarshal(params, &a)
		h.renders = append(h.renders, a)
		return nil, nil
	}
	return nil, nil
}

func TestServe(t *testing.T) {
	pr, hw := io.Pipe()
	hr, pw := io.Pipe()
	p := &testPlugin{events: make(chan string, 10)}
	served := make(chan error, 1)
	go func() { served <- ServeConn(p, pr, pw, io.Discard) }()
	fh := &fakeHost{kv: map[string]string{}}
	c := jsonrpc.NewConn(hr, hw, fh.handle, nil)
	go c.Run()
	ctx := t.Context()

	var items []plugin.Item
	if err := c.Call(ctx, protocol.MethodRootItems, protocol.RootItemsParams{Query: "x"}, &items); err == nil {
		t.Fatal("call before initialize succeeded")
	}
	var init protocol.InitializeResult
	if err := c.Call(ctx, protocol.MethodInitialize, protocol.InitializeParams{Protocol: 1, Locale: "en", DataDir: t.TempDir(), CacheDir: t.TempDir()}, &init); err != nil {
		t.Fatal(err)
	}
	want := protocol.Capabilities{RootItems: true, Actions: true, Render: true, ContextItems: true}
	if init.Protocol != protocol.Version || init.Capabilities != want {
		t.Fatalf("initialize = %+v", init)
	}
	if err := c.Call(ctx, protocol.MethodRootItems, protocol.RootItemsParams{Query: "x"}, &items); err != nil || items[0].Title != "hi x" {
		t.Fatalf("rootItems = %+v, %v", items, err)
	}
	cur := plugin.Context{Kind: plugin.ContextExplorer, Folder: `C:\tmp`, Selected: []string{`C:\tmp\a`, `C:\tmp\b`}}
	if err := c.Call(ctx, protocol.MethodRootItems, protocol.RootItemsParams{Query: "x", Context: &cur}, &items); err != nil || items[0].Title != `hi x in C:\tmp` {
		t.Fatalf("rootItems with context = %+v, %v", items, err)
	}
	if err := c.Call(ctx, protocol.MethodContextItems, protocol.ContextItemsParams{Context: cur}, &items); err != nil || items[0].Title != `explorer:C:\tmp:C:\tmp\a,C:\tmp\b` {
		t.Fatalf("contextItems = %+v, %v", items, err)
	}
	if err := c.Call(ctx, protocol.MethodRootItems, protocol.RootItemsParams{Query: "panic"}, &items); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("panic: %v", err)
	}
	if err := c.Call(ctx, protocol.MethodView, protocol.ViewParams{}, nil); !isCode(err, jsonrpc.CodeMethodNotFound) {
		t.Fatalf("view without ViewProvider: %v", err)
	}

	// Preferences change.
	c.Notify(protocol.NotifyPreferencesChanged, protocol.PreferencesChangedParams{Preferences: map[string]jsontext.Value{"greeting": jsontext.Value(`"yo"`)}})
	waitEvent(t, p.events, plugin.EventConfigChanged)
	c.Call(ctx, protocol.MethodRootItems, protocol.RootItemsParams{Query: "x"}, &items)
	if items[0].Title != "yo x" {
		t.Fatalf("after preferencesChanged: %q", items[0].Title)
	}

	// Events: subscribed once with the host, then delivered.
	time.Sleep(50 * time.Millisecond)
	fh.mu.Lock()
	subs := fh.subs
	fh.mu.Unlock()
	if len(subs) != 1 || subs[0] != plugin.EventClipboardChanged {
		t.Fatalf("subscriptions = %v", subs)
	}
	c.Notify(protocol.NotifyEvent, protocol.EventParams{Name: plugin.EventClipboardChanged})
	waitEvent(t, p.events, plugin.EventClipboardChanged)

	// Binary storage and renders with increasing versions.
	var r plugin.Result
	for _, target := range []string{"store", "render"} {
		if err := c.Call(ctx, protocol.MethodHandleAction, protocol.HandleActionParams{Action: plugin.ActionDo{Kind: plugin.ActionCallback, Target: target}}, &r); err != nil || !r.Hide {
			t.Fatalf("%s: %+v %v", target, r, err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	fh.mu.Lock()
	renders := fh.renders
	fh.mu.Unlock()
	if len(renders) != 2 || renders[0].Version >= renders[1].Version || renders[1].View.Title != "b" {
		t.Fatalf("renders = %+v", renders)
	}

	if err := c.Call(ctx, protocol.MethodShutdown, struct{}{}, nil); err != nil {
		t.Fatal(err)
	}
	hw.Close()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func isCode(err error, code int64) bool {
	e, ok := errors.AsType[*jsonrpc.Error](err)
	return ok && e.Code == code
}

func waitEvent(t *testing.T, ch chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("event %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s event", want)
	}
}

func TestUnavailableInit(t *testing.T) {
	pr, hw := io.Pipe()
	hr, pw := io.Pipe()
	go ServeConn(unavailable{}, pr, pw, io.Discard)
	c := jsonrpc.NewConn(hr, hw, nil, nil)
	go c.Run()
	defer hw.Close()
	err := c.Call(t.Context(), protocol.MethodInitialize, protocol.InitializeParams{Protocol: 1}, nil)
	e, ok := errors.AsType[*jsonrpc.Error](err)
	if !ok || e.Code != jsonrpc.CodeUnavailable || e.Message != "no backend" {
		t.Fatalf("err = %#v", err)
	}
}

type unavailable struct{}

func (unavailable) Manifest() plugin.Manifest { return plugin.Manifest{ID: "u"} }
func (unavailable) Init(context.Context, plugin.Host) error {
	return fmt.Errorf("no backend: %w", plugin.ErrUnavailable)
}
func (unavailable) Close() error { return nil }
