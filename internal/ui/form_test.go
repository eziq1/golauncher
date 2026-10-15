package ui_test

import (
	"context"
	"encoding/json/jsontext"
	"strings"
	"sync"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// formPlugin has a form view, a command with arguments and the preference
// extensions (groups, conditions, buttons, dynamic options).
type formPlugin struct {
	mu      sync.Mutex
	handled []plugin.ActionDo
	optsErr bool
}

func (*formPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: "form", Name: "Form Plugin", Version: "1",
		Commands: []plugin.Command{
			{Name: "feedback", Title: "Send Feedback", Mode: plugin.ModeView},
			{Name: "greet", Title: "Greet Person", Mode: plugin.ModeNoView, Arguments: []plugin.Argument{
				{Name: "name", Type: plugin.ArgText, Placeholder: "Name", Required: true},
				{Name: "tone", Type: plugin.ArgDropdown, Placeholder: "Tone", Options: []plugin.Option{{Value: "warm", Title: "Warm"}}},
			}},
		},
		Preferences: []plugin.Preference{
			{Name: "mode", Type: plugin.PrefSelect, Title: "Mode", Default: jsontext.Value(`"basic"`),
				Options: []plugin.Option{{Value: "basic", Title: "Basic"}, {Value: "advanced", Title: "Advanced"}}},
			{Name: "server", Type: plugin.PrefText, Title: "Server address", Group: "Connection",
				VisibleWhen: &plugin.Condition{Pref: "mode", Equals: jsontext.Value(`"advanced"`)}},
			{Name: "account", Type: plugin.PrefSelect, Title: "Account", Group: "Connection", DynamicOptions: true},
			{Name: "login", Type: plugin.PrefButton, Title: "Sign In", Description: "Connect your account.", Group: "Connection",
				Action: &plugin.ActionDo{Kind: plugin.ActionCallback, Target: "login"}},
		},
	}
}

func (*formPlugin) Init(context.Context, plugin.Host) error { return nil }
func (*formPlugin) Close() error                            { return nil }

func (*formPlugin) View(context.Context, plugin.ViewRequest) (plugin.View, error) {
	return plugin.View{Kind: plugin.ViewForm, Title: "Feedback", Form: &plugin.Form{
		Fields: []plugin.Field{
			{ID: "subject", Type: plugin.FieldText, Title: "Subject", Required: true, Info: "A short summary."},
			{ID: "body", Type: plugin.FieldTextArea, Title: "Body"},
			{ID: "urgent", Type: plugin.FieldCheckbox, Title: "Urgent"},
			{ID: "kind", Type: plugin.FieldDropdown, Title: "Kind", Default: jsontext.Value(`"bug"`),
				Options: []plugin.Option{{Value: "bug", Title: "Bug"}, {Value: "idea", Title: "Idea"}}},
			{ID: "due", Type: plugin.FieldDate, Title: "Due"},
			{ID: "file", Type: plugin.FieldFile, Title: "Attachment"},
			{ID: "token", Type: plugin.FieldPassword, Title: "Token"},
			{ID: "count", Type: plugin.FieldNumber, Title: "Count"},
		},
		Submit: plugin.Action{ID: "send", Title: "Send Feedback Now", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "send"}},
	}}, nil
}

func (p *formPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handled = append(p.handled, a)
	if a.Target == "login" {
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: "Signed in as Ada"}}, nil
	}
	return plugin.Result{PopToRoot: true}, nil
}

func (p *formPlugin) PreferenceOptions(context.Context, string) ([]plugin.Option, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.optsErr {
		return nil, context.DeadlineExceeded
	}
	return []plugin.Option{{Value: "a", Title: "Ada Lovelace"}}, nil
}

func (p *formPlugin) last() plugin.ActionDo {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.handled) == 0 {
		return plugin.ActionDo{}
	}
	return p.handled[len(p.handled)-1]
}

func newFormEnv(t *testing.T) (*app.Env, *formPlugin) {
	t.Helper()
	p := &formPlugin{}
	clip := &hostfake.Clipboard{}
	env, err := app.Build(t.Context(), app.Options{
		Plugins: []plugin.Plugin{p}, Fake: true, Locale: "en",
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	return env, p
}

func newFormLauncher(t *testing.T) (*launcherTest, *formPlugin) {
	t.Helper()
	env, p := newFormEnv(t)
	lt := &launcherTest{Env: env, t: t, host: &fakeHost{}}
	lt.l = ui.NewLauncher(env, lt.host, ui.NewDispatcher(t.Context(), nil))
	lt.tt = mui.NewTester(lt.l.View, ui.LauncherWidth, ui.LauncherHeight)
	lt.tt.SetSize(ui.LauncherWidth, 900) // the whole form, without scrolling
	lt.l.Shown()
	lt.step()
	return lt, p
}

func TestFormView(t *testing.T) {
	lt, p := newFormLauncher(t)
	lt.l.OpenCommand("form", "feedback")
	lt.step()
	lt.step()
	s := lt.Controller.Snapshot()
	if s.Form == nil {
		t.Fatalf("no form: %+v", s)
	}
	for _, want := range []string{"Feedback", "Subject *", "A short summary.", "Body", "Urgent", "Bug", "Attachment", "Token", "Send Feedback Now"} {
		lt.wantText(want)
	}
	if lt.tt.HasText("Search apps, commands, and more…") {
		t.Fatal("a form shows the search field")
	}

	// The first field has the focus; typing goes to it, Tab to the next.
	lt.typeText("Hello")
	lt.key(0, mui.KeyTab)
	if s := lt.Controller.Snapshot(); s.Form.Fields[0].Value != "Hello" || s.Form.Focused != "body" {
		t.Fatalf("form = %+v", s.Form)
	}
	lt.typeText("line")
	lt.key(0, mui.KeyEnter) // a new line, not the next field
	lt.typeText("two")
	if s := lt.Controller.Snapshot(); s.Form.Fields[1].Value != "line\ntwo" || s.Form.Focused != "body" {
		t.Fatalf("body = %q, focused %q", s.Form.Fields[1].Value, s.Form.Focused)
	}
	lt.key(mui.Shift, mui.KeyTab)
	if s := lt.Controller.Snapshot(); s.Form.Focused != "subject" {
		t.Fatalf("focused %q after Shift+Tab", s.Form.Focused)
	}
	// Enter in a single-line field moves on.
	lt.key(0, mui.KeyEnter)
	if s := lt.Controller.Snapshot(); s.Form.Focused != "body" {
		t.Fatalf("focused %q after Enter", s.Form.Focused)
	}
	// A click on the label toggles the check box.
	if err := lt.tt.Click("Urgent"); err != nil {
		t.Fatal(err)
	}
	lt.step()
	if s := lt.Controller.Snapshot(); s.Form.Fields[2].Value != "true" {
		t.Fatalf("urgent = %q", s.Form.Fields[2].Value)
	}

	// Invalid values are shown under the fields; nothing is submitted.
	if err := lt.Controller.SetField("count", "x"); err != nil {
		t.Fatal(err)
	}
	lt.step()
	lt.key(mui.Ctrl, mui.KeyEnter)
	lt.wantText("Enter a number.")
	if p.last().Target != "" {
		t.Fatal("an invalid form was submitted")
	}
	if err := lt.Controller.SetField("count", "2"); err != nil {
		t.Fatal(err)
	}
	lt.step()
	if err := lt.tt.Click("Send Feedback Now"); err != nil { // the footer button
		t.Fatal(err)
	}
	lt.step()
	got := p.last()
	if got.Target != "send" || string(got.Values["subject"]) != `"Hello"` || string(got.Values["urgent"]) != "true" || string(got.Values["count"]) != "2" {
		t.Fatalf("submitted %+v", got)
	}
	if s := lt.Controller.Snapshot(); s.Depth != 0 || s.Form != nil {
		t.Fatalf("after submit: depth %d", s.Depth)
	}
	lt.wantText("Search")
}

func TestFormEscape(t *testing.T) {
	lt, _ := newFormLauncher(t)
	lt.l.OpenCommand("form", "feedback")
	lt.step()
	lt.key(0, mui.KeyEscape)
	if s := lt.Controller.Snapshot(); s.Depth != 0 {
		t.Fatalf("depth %d after Escape", s.Depth)
	}
	if lt.host.hidden.Load() != 0 {
		t.Fatal("Escape in a form hid the launcher")
	}
}

func TestArgumentInputs(t *testing.T) {
	lt, p := newFormLauncher(t)
	lt.typeText("greet person")
	lt.wantText("Name")
	lt.wantText("Tone")
	// Enter without the required argument moves the focus into it.
	lt.key(0, mui.KeyEnter)
	if s := lt.Controller.Snapshot(); s.Arguments == nil || s.Arguments.Focused != "name" {
		t.Fatalf("arguments = %+v", s.Arguments)
	}
	lt.typeText("Ada")
	if s := lt.Controller.Snapshot(); s.Text != "greet person" || s.Arguments.Fields[0].Value != "Ada" {
		t.Fatalf("text %q, arguments %+v", s.Text, s.Arguments)
	}
	// Tab goes on to the next argument and back to the search field.
	lt.key(0, mui.KeyTab)
	lt.key(0, mui.KeyTab)
	if s := lt.Controller.Snapshot(); s.Arguments.Focused != "" {
		t.Fatalf("focused %q", s.Arguments.Focused)
	}
	lt.typeText("!")
	if s := lt.Controller.Snapshot(); s.Text != "greet person!" {
		t.Fatalf("text %q", s.Text)
	}
	lt.key(0, mui.KeyBackspace)
	lt.key(0, mui.KeyTab)
	lt.key(0, mui.KeyEnter)
	if got := p.last(); got.Kind != plugin.ActionRunCommand || string(got.Values["name"]) != `"Ada"` {
		t.Fatalf("handled %+v", got)
	}
}

func TestSettingsPreferenceExtensions(t *testing.T) {
	env, p := newFormEnv(t)
	s := ui.NewSettings(env, &fakeHost{}, "1.0.0-test")
	tt := mui.NewTester(s.View, ui.SettingsWidth, 1400)
	s.ShowPage(ui.PagePlugins)
	tt.Frame()
	if err := tt.Click("Configure"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	s.Wait()
	tt.Frame()
	for _, want := range []string{"Connection", "Sign In", "Connect your account.", "Account"} {
		if !tt.HasText(want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	// The server shows only in the advanced mode.
	if tt.HasText("Server address") {
		t.Fatal("a hidden preference shows")
	}
	env.Config.Update(func(c *config.Config) {
		c.Plugins = map[string]config.PluginConfig{"form": {Preferences: map[string]jsontext.Value{"mode": jsontext.Value(`"advanced"`), "account": jsontext.Value(`"a"`)}}}
	})
	tt.Frame()
	if !tt.HasText("Server address") {
		t.Fatalf("no server preference in advanced mode: %q", tt.Texts())
	}
	// Dynamic options are loaded when the page opens.
	if !tt.HasText("Ada Lovelace") {
		t.Fatalf("no dynamic option in %q", tt.Texts())
	}
	// The button runs its action and shows the toast next to it.
	if err := tt.Click("Sign In"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	s.Wait()
	tt.Frame()
	if !tt.HasText("Signed in as Ada") || p.last().Target != "login" {
		t.Fatalf("texts = %q", tt.Texts())
	}

	// A failure to load the options is shown.
	p.mu.Lock()
	p.optsErr = true
	p.mu.Unlock()
	s.ShowPage(ui.PagePlugins)
	tt.Frame()
	if err := tt.Click("Configure"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	s.Wait()
	tt.Frame()
	found := false
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "Could not load the options") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no error in %q", tt.Texts())
	}
}
