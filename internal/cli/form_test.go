package cli

import (
	"context"
	"encoding/json/v2"
	"path/filepath"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// formPlugin has a form view and a command with arguments, for the session
// steps field and arg.
type formPlugin struct{}

func (formPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: "test.form", Name: "Form Test", Version: "1",
		Commands: []plugin.Command{
			{Name: "feedback", Title: "Send Feedback", Mode: plugin.ModeView},
			{Name: "greet", Title: "Greet Someone", Mode: plugin.ModeNoView, Arguments: []plugin.Argument{
				{Name: "name", Type: plugin.ArgText, Placeholder: "Name", Required: true},
				{Name: "greeting", Type: plugin.ArgDropdown, Placeholder: "Greeting",
					Options: []plugin.Option{{Value: "hello", Title: "Hello"}, {Value: "hi", Title: "Hi"}}},
			}},
		},
	}
}

func (formPlugin) Init(context.Context, plugin.Host) error { return nil }
func (formPlugin) Close() error                            { return nil }

func (formPlugin) View(context.Context, plugin.ViewRequest) (plugin.View, error) {
	return plugin.View{Kind: plugin.ViewForm, Title: "Feedback", Form: &plugin.Form{
		Fields: []plugin.Field{
			{ID: "subject", Type: plugin.FieldText, Title: "Subject", Required: true},
			{ID: "rating", Type: plugin.FieldNumber, Title: "Rating"},
			{ID: "subscribe", Type: plugin.FieldCheckbox, Title: "Subscribe"},
		},
		Submit: plugin.Action{ID: "send", Title: "Send", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "send"}},
	}}, nil
}

func (formPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	b, err := json.Marshal(a.Values, json.Deterministic(true))
	if err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Hide: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: a.Target, Message: string(b)}}, nil
}

func withFormPlugin(t *testing.T) string {
	t.Helper()
	fakeExtras = []plugin.Plugin{formPlugin{}}
	t.Cleanup(func() { fakeExtras = nil })
	s := filepath.Join(t.TempDir(), "s.json")
	if _, code := glctl(t, "session", "new", "-s", s); code != 0 {
		t.Fatal("session new failed")
	}
	return s
}

func steps(t *testing.T, s string, steps ...[]string) string {
	t.Helper()
	var out string
	for _, st := range steps {
		var code int
		out, code = glctl(t, append([]string{"session", st[0], "-s", s}, st[1:]...)...)
		if code != 0 {
			t.Fatalf("step %v failed:\n%s", st, out)
		}
	}
	return out
}

func TestSessionForm(t *testing.T) {
	s := withFormPlugin(t)
	out := steps(t, s,
		[]string{"type", "send feedback"},
		[]string{"key", "Enter"},
		[]string{"field", "rating", "lots"},
		[]string{"key", "Ctrl+Enter"},
	)
	golden(t, "session_form_invalid", out)
	out = steps(t, s,
		[]string{"field", "subject", "Great", "launcher"},
		[]string{"field", "rating", "5"},
		[]string{"key", "Tab"},
		[]string{"key", "Tab"},
		[]string{"key", "Space"},
		[]string{"key", "Ctrl+Enter"},
	)
	golden(t, "session_form_submit", out)
	if _, code := glctl(t, "session", "field", "-s", s, "nosuch", "x"); code == 0 {
		t.Fatal("setting an unknown field succeeded")
	}
}

func TestSessionArguments(t *testing.T) {
	s := withFormPlugin(t)
	out := steps(t, s,
		[]string{"type", "greet someone"},
		[]string{"key", "Enter"},
	)
	golden(t, "session_arg_missing", out)
	out = steps(t, s,
		[]string{"arg", "name", "Ada"},
		[]string{"arg", "greeting", "hi"},
		[]string{"key", "Enter"},
	)
	golden(t, "session_arg_run", out)
	if _, code := glctl(t, "session", "arg", "-s", s); code != 2 {
		t.Fatal("arg without a name is a usage error")
	}
}
