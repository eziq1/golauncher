package core_test

import (
	"context"
	"encoding/json/v2"
	"reflect"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// asker stands in for builtin.ai: it answers ActionAskAI with a push of
// its chat view carrying the request.
type asker struct{ got []plugin.AskAI }

func (a *asker) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "fake.ai", Name: "AI", Version: "1"}
}
func (a *asker) Init(context.Context, plugin.Host) error { return nil }
func (a *asker) Close() error                            { return nil }
func (a *asker) AskAI(_ context.Context, q plugin.AskAI) (plugin.Result, error) {
	a.got = append(a.got, q)
	return plugin.Result{Push: &plugin.ViewRequest{ID: "chat", Payload: plugin.MustPayload(q)}}, nil
}

func buildEnv(t *testing.T, ps ...plugin.Plugin) *app.Env {
	t.Helper()
	env, err := app.Build(t.Context(), app.Options{Plugins: ps, Fake: true, Locale: "en", System: &app.Recorder{},
		Clipboard: &hostfake.Clipboard{}, Now: func() time.Time { return hostfake.Fixed }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	return env
}

// TestAskAI: an ActionAskAI of one plugin (the translator) is handed to the
// plugin implementing plugin.AIAsker, whose view is pushed; without one (or
// with it turned off) a toast says AI is not available.
func TestAskAI(t *testing.T) {
	a := &asker{}
	env := buildEnv(t, &testPlugin{}, a)
	q := plugin.AskAI{Instruction: "Translate the quoted text into Japanese.", Text: "hello", Title: "Translate: hello"}
	act := plugin.Action{ID: "ai", Do: plugin.ActionDo{Kind: plugin.ActionAskAI, Payload: plugin.MustPayload(q)}}
	out, err := env.Engine.Run(t.Context(), core.Entry{Plugin: "test"}, act, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.got) != 1 || !reflect.DeepEqual(a.got[0], q) {
		t.Fatalf("asked %+v", a.got)
	}
	var got plugin.AskAI
	if out.Push == nil || out.Push.Plugin != "fake.ai" || out.Push.Request.ID != "chat" || json.Unmarshal(out.Push.Request.Payload, &got) != nil || !reflect.DeepEqual(got, q) {
		t.Fatalf("outcome %+v", out)
	}
	// The AI plugin turned off.
	if err := env.Config.Update(func(c *config.Config) {
		c.Plugins = map[string]config.PluginConfig{"fake.ai": {Disabled: true}}
	}); err != nil {
		t.Fatal(err)
	}
	env.Controller.Wait()
	out, err = env.Engine.Run(t.Context(), core.Entry{Plugin: "test"}, act, false)
	if err != nil || out.Push != nil || out.Toast == nil || out.Toast.Title != "AI is not available" || len(a.got) != 1 {
		t.Errorf("without AI: %+v %v", out, err)
	}
	// A bad payload is an error.
	if _, err := env.Engine.Run(t.Context(), core.Entry{Plugin: "test"}, plugin.Action{Do: plugin.ActionDo{Kind: plugin.ActionAskAI, Payload: []byte(`"x"`)}}, false); err == nil {
		t.Error("bad payload accepted")
	}
}
