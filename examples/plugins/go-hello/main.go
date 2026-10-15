// Command go-hello is an example external golauncher plugin written in Go.
// It is an ordinary plugin.Plugin run by rpcserve.Serve; the same type
// could be compiled into the launcher instead.
//
// Build it next to its manifest and point golauncher at the directory:
//
//	go build -o examples/plugins/go-hello/go-hello.exe ./examples/plugins/go-hello
//	$env:GOLAUNCHER_PLUGIN_PATH = "$PWD\examples\plugins\go-hello"
//	glctl query hello --plugins examples/plugins/go-hello
//
// It shows a root item, a list view with copy actions and a callback, a
// no-view command, and uses the plugin's private storage.
package main

import (
	"context"
	"embed"
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"log"
	"strconv"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/rpcserve"
)

//go:embed plugin.json locales/*.json
var files embed.FS

func main() {
	if err := rpcserve.Serve(&Hello{}); err != nil {
		log.Fatal(err)
	}
}

// Hello is the plugin.
type Hello struct{ h plugin.Host }

// Manifest returns the manifest from plugin.json, so it is written once.
func (p *Hello) Manifest() plugin.Manifest {
	var m plugin.Manifest
	data, _ := files.ReadFile("plugin.json")
	_ = json.Unmarshal(data, &m)
	return m
}

func (p *Hello) Init(_ context.Context, h plugin.Host) error { p.h = h; return nil }
func (p *Hello) Close() error                                { return nil }

func (p *Hello) Locales() fs.FS {
	sub, _ := fs.Sub(files, "locales")
	return sub
}

const countKey = "count"

func (p *Hello) count() int {
	v, ok, err := p.h.Storage().Get(countKey)
	if err != nil || !ok {
		return 0
	}
	n, _ := strconv.Atoi(string(v))
	return n
}

func (p *Hello) countAction() plugin.Action {
	return plugin.Action{
		ID:    "count",
		Title: p.h.I18n().T("action.count"),
		Do:    plugin.ActionDo{Kind: plugin.ActionCallback, Target: "count"},
	}
}

// RootItems implements plugin.RootProvider.
func (p *Hello) RootItems(_ context.Context, _ plugin.Query) ([]plugin.Item, error) {
	l := p.h.I18n()
	return []plugin.Item{{
		ID:       "hello",
		Title:    l.T("root.title"),
		Subtitle: l.N("root.subtitle", p.count()),
		Keywords: []string{"hello", "go"},
		Group:    l.T("group"),
		Actions: []plugin.Action{
			{ID: "open", Title: l.T("view.title"), Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: "greetings"}},
			p.countAction(),
		},
	}}, nil
}

var greetings = []struct{ lang, text string }{
	{"en", "Hello"}, {"fr", "Bonjour"}, {"de", "Hallo"}, {"es", "Hola"},
	{"ja", "こんにちは"}, {"zh", "你好"},
}

// View implements plugin.ViewProvider.
func (p *Hello) View(_ context.Context, req plugin.ViewRequest) (plugin.View, error) {
	if req.ID != "greetings" {
		return plugin.View{}, fmt.Errorf("unknown view %q", req.ID)
	}
	l := p.h.I18n()
	var items []plugin.Item
	for _, g := range greetings {
		items = append(items, plugin.Item{
			ID:        g.lang,
			Title:     g.text,
			Accessory: g.lang,
			Actions: []plugin.Action{
				{ID: "copy", Title: l.T("action.copy"), Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: g.text}},
				p.countAction(),
			},
		})
	}
	return plugin.View{
		Kind:          plugin.ViewList,
		Title:         l.T("view.title"),
		Placeholder:   l.T("view.placeholder"),
		FilterLocally: true,
		Sections:      []plugin.Section{{Items: items}},
	}, nil
}

// HandleAction implements plugin.ActionHandler: the count callback and the
// count no-view command both store the incremented counter.
func (p *Hello) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	if a.Target != "count" {
		return plugin.Result{}, fmt.Errorf("unknown action %q", a.Target)
	}
	n := p.count() + 1
	if err := p.h.Storage().Set(countKey, []byte(strconv.Itoa(n))); err != nil {
		return plugin.Result{}, err
	}
	p.h.Invalidate("")
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: p.h.I18n().N("toast.counted", n)}}, nil
}
