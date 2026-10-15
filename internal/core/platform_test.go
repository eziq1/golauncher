package core_test

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// platPlugin exercises the M5 platform features: invalidation, pushed
// views, forms, arguments and the preference extensions.
type platPlugin struct {
	mu        sync.Mutex
	h         plugin.Host
	views     []plugin.ViewRequest // the View calls
	roots     int                  // the RootItems calls
	rootItems []string
	liveItems []string
	block     chan struct{} // when set, View("live") waits for it
	handled   []plugin.ActionDo
	fieldErrs map[string]string
	optsErr   error
}

func (p *platPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID: "plat", Name: "Platform", Version: "1",
		Commands: []plugin.Command{
			{Name: "live", Title: "Live View", Mode: plugin.ModeView},
			{Name: "form", Title: "Feedback Form", Mode: plugin.ModeView},
			{Name: "greet", Title: "Greet Person", Mode: plugin.ModeView, Arguments: []plugin.Argument{
				{Name: "name", Type: plugin.ArgText, Placeholder: "Name", Required: true},
				{Name: "tone", Type: plugin.ArgDropdown, Placeholder: "Tone", Options: []plugin.Option{{Value: "warm", Title: "Warm"}, {Value: "cool", Title: "Cool"}}},
			}},
			{Name: "shout", Title: "Shout Text", Mode: plugin.ModeNoView, Arguments: []plugin.Argument{
				{Name: "text", Type: plugin.ArgText, Placeholder: "Text", Required: true},
			}},
		},
		Preferences: []plugin.Preference{
			{Name: "mode", Type: plugin.PrefSelect, Title: "Mode", Default: jsontext.Value(`"basic"`),
				Options: []plugin.Option{{Value: "basic", Title: "Basic"}, {Value: "advanced", Title: "Advanced"}}},
			{Name: "server", Type: plugin.PrefText, Title: "Server", Group: "Connection",
				VisibleWhen: &plugin.Condition{Pref: "mode", Equals: jsontext.Value(`"advanced"`)}},
			{Name: "account", Type: plugin.PrefSelect, Title: "Account", Group: "Connection", DynamicOptions: true},
			{Name: "login", Type: plugin.PrefButton, Title: "Sign In", Group: "Connection",
				Action: &plugin.ActionDo{Kind: plugin.ActionCallback, Target: "login"}},
		},
	}
}

func (p *platPlugin) Init(_ context.Context, h plugin.Host) error {
	p.mu.Lock()
	p.h = h
	p.mu.Unlock()
	return nil
}
func (p *platPlugin) Close() error { return nil }

func (p *platPlugin) host() plugin.Host {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.h
}

func (p *platPlugin) RootItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.roots++
	var out []plugin.Item
	for _, id := range p.rootItems {
		it := plugin.Item{ID: id, Title: "Item " + id, Actions: []plugin.Action{
			{ID: "copy", Title: "Copy", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: id}},
		}}
		if id == "args" {
			it.Arguments = []plugin.Argument{{Name: "suffix", Type: plugin.ArgText, Placeholder: "Suffix"}}
			it.Actions = append([]plugin.Action{{ID: "run", Title: "Run", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "args"}}}, it.Actions...)
		}
		out = append(out, it)
	}
	return out, nil
}

func listView(title string, ids []string) plugin.View {
	var items []plugin.Item
	for _, id := range ids {
		items = append(items, plugin.Item{ID: id, Title: id, Actions: []plugin.Action{
			{ID: "copy", Title: "Copy", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: id}},
		}})
	}
	return plugin.View{Kind: plugin.ViewList, Title: title, Sections: []plugin.Section{{Items: items}}}
}

func (p *platPlugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	p.mu.Lock()
	p.views = append(p.views, req)
	block, live := p.block, slices.Clone(p.liveItems)
	p.mu.Unlock()
	switch req.ID {
	case "live":
		if block != nil {
			select {
			case <-block:
			case <-ctx.Done():
				return plugin.View{}, ctx.Err()
			}
		}
		return listView("Live", live), nil
	case "form":
		return plugin.View{Kind: plugin.ViewForm, Title: "Feedback", Form: &plugin.Form{
			Fields: []plugin.Field{
				{ID: "subject", Type: plugin.FieldText, Title: "Subject", Required: true},
				{ID: "count", Type: plugin.FieldNumber, Title: "Count", Default: jsontext.Value("3")},
				{ID: "body", Type: plugin.FieldTextArea, Title: "Body"},
				{ID: "urgent", Type: plugin.FieldCheckbox, Title: "Urgent"},
				{ID: "kind", Type: plugin.FieldDropdown, Title: "Kind", Default: jsontext.Value(`"bug"`),
					Options: []plugin.Option{{Value: "bug", Title: "Bug"}, {Value: "idea", Title: "Idea"}}},
				{ID: "due", Type: plugin.FieldDate, Title: "Due"},
			},
			Submit: plugin.Action{ID: "send", Title: "Send", Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "send"}},
		}}, nil
	case "greet":
		return plugin.View{Kind: plugin.ViewDetail, Title: "Hello " + req.Arguments["name"]}, nil
	}
	return plugin.View{}, fmt.Errorf("no view %q", req.ID)
}

func (p *platPlugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handled = append(p.handled, a)
	switch a.Target {
	case "send":
		if p.fieldErrs != nil {
			return plugin.Result{FieldErrors: p.fieldErrs}, nil
		}
		return plugin.Result{PopToRoot: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: "Sent"}}, nil
	case "login":
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: "Signed in"}}, nil
	}
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: "ok"}}, nil
}

func (p *platPlugin) PreferenceOptions(_ context.Context, name string) ([]plugin.Option, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.optsErr != nil {
		return nil, p.optsErr
	}
	return []plugin.Option{{Value: "a1", Title: "Account " + name}}, nil
}

func (p *platPlugin) viewCalls(id string) []plugin.ViewRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []plugin.ViewRequest
	for _, r := range p.views {
		if r.ID == id {
			out = append(out, r)
		}
	}
	return out
}

func (p *platPlugin) set(fn func(p *platPlugin)) {
	p.mu.Lock()
	fn(p)
	p.mu.Unlock()
}

func (p *platPlugin) lastHandled() plugin.ActionDo {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.handled) == 0 {
		return plugin.ActionDo{}
	}
	return p.handled[len(p.handled)-1]
}

func newPlat(t *testing.T) (*app.Env, *platPlugin) {
	t.Helper()
	p := &platPlugin{rootItems: []string{"a", "b", "c"}, liveItems: []string{"one", "two", "three"}}
	return buildWith(t, p), p
}

func open(t *testing.T, env *app.Env, name string) core.Snapshot {
	t.Helper()
	if _, err := env.Controller.OpenCommand(t.Context(), "plat", name); err != nil {
		t.Fatal(err)
	}
	env.Controller.Wait()
	return env.Controller.Snapshot()
}

func press(t *testing.T, env *app.Env, key string) core.Outcome {
	t.Helper()
	out, err := env.Controller.Key(t.Context(), key)
	if err != nil {
		t.Fatalf("Key(%s): %v", key, err)
	}
	env.Controller.Wait()
	return out
}

func selectedTitle(s core.Snapshot) string {
	e, _ := s.SelectedEntry()
	return e.Title
}

func TestInvalidateCoalescesAndKeepsSelection(t *testing.T) {
	env, p := newPlat(t)
	s := open(t, env, "live")
	if got := titles(s); strings.Join(got, ",") != "one,two,three" {
		t.Fatalf("live view = %v", got)
	}
	press(t, env, "Down")
	press(t, env, "Down") // "three"
	calls := len(p.viewCalls("live"))

	// A burst of invalidations is one refresh; the new item above the
	// selection does not move it off "three".
	p.set(func(p *platPlugin) { p.liveItems = []string{"zero", "one", "two", "three"} })
	for range 10 {
		p.host().Invalidate("live")
	}
	env.Controller.Wait()
	if got := len(p.viewCalls("live")) - calls; got != 1 {
		t.Fatalf("%d refreshes for a burst", got)
	}
	s = env.Controller.Snapshot()
	if selectedTitle(s) != "three" || s.Selected != 3 || s.Loading {
		t.Fatalf("selected %q (%d), loading %v", selectedTitle(s), s.Selected, s.Loading)
	}

	// Other views of the plugin, and other plugins, are left alone.
	calls = len(p.viewCalls("live"))
	p.host().Invalidate("form")
	env.Services.OnInvalidate("other", "")
	env.Controller.Wait()
	if got := len(p.viewCalls("live")) - calls; got != 0 {
		t.Fatalf("%d refreshes for another view", got)
	}
	// "" is every view of the plugin.
	p.host().Invalidate("")
	env.Controller.Wait()
	if got := len(p.viewCalls("live")) - calls; got != 1 {
		t.Fatalf("%d refreshes for all views", got)
	}

	// When the selected entry is gone, the row at the same index stays selected.
	p.set(func(p *platPlugin) { p.liveItems = []string{"zero", "one", "two", "four"} })
	env.Controller.Refresh(t.Context())
	env.Controller.Wait()
	if s := env.Controller.Snapshot(); selectedTitle(s) != "four" {
		t.Fatalf("selected %q", selectedTitle(s))
	}
}

func TestInvalidateWaitsForSearchInFlight(t *testing.T) {
	env, p := newPlat(t)
	open(t, env, "live")
	block := make(chan struct{})
	p.set(func(p *platPlugin) { p.block = block })
	calls := len(p.viewCalls("live"))
	env.Controller.SetText(t.Context(), "x") // a search in flight
	p.host().Invalidate("live")
	time.Sleep(120 * time.Millisecond) // the coalescing delay has passed
	if got := len(p.viewCalls("live")) - calls; got != 1 {
		t.Fatalf("the invalidation canceled the search in flight: %d calls", got)
	}
	waited := make(chan struct{})
	go func() { env.Controller.Wait(); close(waited) }()
	select {
	case <-waited:
		t.Fatal("Wait returned with an invalidation pending")
	case <-time.After(20 * time.Millisecond):
	}
	p.set(func(p *platPlugin) { p.block = nil })
	close(block)
	<-waited
	if got := len(p.viewCalls("live")) - calls; got != 2 {
		t.Fatalf("no refresh after the search finished: %d calls", got)
	}
}

func TestInvalidateRoot(t *testing.T) {
	env, p := newPlat(t)
	env.Controller.SetText(t.Context(), "item")
	env.Controller.Wait()
	press(t, env, "Down") // "Item b"
	p.set(func(p *platPlugin) { p.rootItems = []string{"0", "a", "b", "c"} })
	p.host().Invalidate("anything") // the root shows all of a root provider's results
	env.Controller.Wait()
	s := env.Controller.Snapshot()
	if got := titles(s); len(got) != 4 || selectedTitle(s) != "Item b" {
		t.Fatalf("root = %v, selected %q", got, selectedTitle(s))
	}
	// Typing selects the best match again.
	s = func() core.Snapshot { env.Controller.SetText(t.Context(), "item "); env.Controller.Wait(); return env.Controller.Snapshot() }()
	if s.Selected != 0 {
		t.Fatalf("selected %d after typing", s.Selected)
	}
}

func TestRenderPush(t *testing.T) {
	env, p := newPlat(t)
	var mu sync.Mutex
	var closedViews []string
	env.Engine.OnViewClosed(func(pluginID, instance string) {
		mu.Lock()
		closedViews = append(closedViews, pluginID+"/"+instance)
		mu.Unlock()
	})
	open(t, env, "live")
	reqs := p.viewCalls("live")
	inst := reqs[0].Instance
	if inst == "" || reqs[len(reqs)-1].Instance != inst {
		t.Fatalf("instances = %+v", reqs)
	}
	press(t, env, "Down") // "two"

	// A burst of pushes is applied at most every 16ms, ending with the last.
	var changes atomic.Int32
	env.Controller.OnChange(func() { changes.Add(1) })
	start := time.Now()
	for i := range 20 {
		p.host().Render(inst, listView(fmt.Sprint("Push ", i), []string{"new", "one", "two"}))
	}
	env.Controller.Wait()
	if n, max := changes.Load(), int32(time.Since(start)/(16*time.Millisecond))+2; n > max {
		t.Fatalf("%d pushes applied, want at most %d", n, max)
	}
	s := env.Controller.Snapshot()
	if s.Title != "Push 19" || selectedTitle(s) != "two" {
		t.Fatalf("after pushes: title %q, selected %q", s.Title, selectedTitle(s))
	}
	// Pushes for unknown instances or other plugins are dropped.
	p.host().Render("v999", listView("Stale", nil))
	env.Services.OnRender("other", inst, listView("Other", nil))
	env.Controller.Wait()
	if s := env.Controller.Snapshot(); s.Title != "Push 19" {
		t.Fatalf("title = %q", s.Title)
	}

	// A closed instance takes no pushes, and the hook hears of it.
	press(t, env, "Escape")
	p.host().Render(inst, listView("Late", []string{"x"}))
	env.Controller.Wait()
	if s := env.Controller.Snapshot(); s.Depth != 0 || s.Title != "" {
		t.Fatalf("a push reached a closed view: %+v", s)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := slices.Clone(closedViews)
		mu.Unlock()
		if slices.Equal(got, []string{"plat/" + inst}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("closed = %q", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Opening it again is a new instance.
	open(t, env, "live")
	if r := p.viewCalls("live"); r[len(r)-1].Instance == inst {
		t.Fatal("the instance was reused")
	}
}

func TestPushDropsWhileClosingRace(t *testing.T) {
	env, p := newPlat(t)
	open(t, env, "live")
	inst := p.viewCalls("live")[0].Instance
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 200 {
			p.host().Render(inst, listView(fmt.Sprint(i), []string{"a"}))
		}
	})
	wg.Go(func() {
		for range 20 {
			env.Controller.Key(t.Context(), "Escape")
			env.Controller.OpenCommand(t.Context(), "plat", "live")
		}
	})
	wg.Wait()
	env.Controller.Wait()
}

func TestFormView(t *testing.T) {
	env, p := newPlat(t)
	s := open(t, env, "form")
	if s.Form == nil || len(s.Form.Fields) != 6 || s.Form.Focused != "subject" || s.Form.Submit != "Send" {
		t.Fatalf("form = %+v", s.Form)
	}
	if f := s.Form.Fields; f[1].Value != "3" || f[3].Value != "false" || f[4].Value != "bug" {
		t.Fatalf("defaults = %+v", f)
	}
	// Tab, Shift+Tab and Enter move between the fields.
	press(t, env, "Tab")
	press(t, env, "Tab")
	if got := env.Controller.Snapshot().Form.Focused; got != "body" {
		t.Fatalf("focused %q after Tab Tab", got)
	}
	press(t, env, "Enter") // a new line in the text area, not the next field
	press(t, env, "Shift+Tab")
	press(t, env, "Enter")
	if got := env.Controller.Snapshot().Form.Focused; got != "body" {
		t.Fatalf("focused %q", got)
	}
	// Backspace in a form does not go back.
	press(t, env, "Backspace")
	if env.Controller.Snapshot().Depth != 1 {
		t.Fatal("Backspace left the form")
	}

	// The host checks required fields, numbers and dates first.
	if err := env.Controller.SetField("count", "many"); err != nil {
		t.Fatal(err)
	}
	if err := env.Controller.SetField("due", "tomorrow"); err != nil {
		t.Fatal(err)
	}
	press(t, env, "Ctrl+Enter")
	s = env.Controller.Snapshot()
	errs := map[string]string{}
	for _, f := range s.Form.Fields {
		if f.Error != "" {
			errs[f.ID] = f.Error
		}
	}
	if len(errs) != 3 || errs["subject"] != "This field is required." || errs["count"] != "Enter a number." || s.Form.Focused != "subject" {
		t.Fatalf("errors = %v, focused %q", errs, s.Form.Focused)
	}
	if p.lastHandled().Kind != "" {
		t.Fatal("an invalid form was submitted")
	}
	if err := env.Controller.SetField("nope", "x"); err == nil {
		t.Fatal("SetField of an unknown field succeeded")
	}
	for id, v := range map[string]string{"subject": "Hi", "count": "4.5", "due": "2026-10-20", "body": "a\nb"} {
		if err := env.Controller.SetField(id, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.Controller.FocusField("urgent"); err != nil {
		t.Fatal(err)
	}
	press(t, env, "Space") // toggles the checkbox

	// The plugin may reject the values: the form stays open.
	p.set(func(p *platPlugin) { p.fieldErrs = map[string]string{"subject": "Too short"} })
	out := press(t, env, "Ctrl+Enter")
	s = env.Controller.Snapshot()
	if s.Depth != 1 || s.Form == nil || s.Form.Fields[0].Error != "Too short" || out.PopToRoot {
		t.Fatalf("after FieldErrors: %+v", s)
	}
	got := p.lastHandled()
	want := map[string]string{"subject": `"Hi"`, "count": "4.5", "body": `"a\nb"`, "urgent": "true", "kind": `"bug"`, "due": `"2026-10-20"`}
	for k, v := range want {
		if string(got.Values[k]) != v {
			t.Errorf("value %s = %s, want %s", k, got.Values[k], v)
		}
	}
	// Typed values survive a refresh of the form.
	env.Controller.Refresh(t.Context())
	env.Controller.Wait()
	if s := env.Controller.Snapshot(); s.Form.Fields[0].Value != "Hi" {
		t.Fatalf("value lost on refresh: %+v", s.Form.Fields[0])
	}

	// Enter in the last field submits.
	p.set(func(p *platPlugin) { p.fieldErrs = nil })
	if err := env.Controller.FocusField("due"); err != nil {
		t.Fatal(err)
	}
	out = press(t, env, "Enter")
	if s := env.Controller.Snapshot(); !out.PopToRoot || s.Depth != 0 || s.Toast == nil || s.Toast.Title != "Sent" {
		t.Fatalf("after submit: %+v, %+v", out, s)
	}
	if err := env.Controller.SetField("subject", "x"); err == nil {
		t.Fatal("SetField outside a form succeeded")
	}
}

func TestFormEscape(t *testing.T) {
	env, _ := newPlat(t)
	open(t, env, "form")
	env.Controller.SetText(t.Context(), "ignored")
	if s := env.Controller.Snapshot(); s.Text != "" {
		t.Fatalf("a form took search text %q", s.Text)
	}
	press(t, env, "Escape")
	if s := env.Controller.Snapshot(); s.Depth != 0 {
		t.Fatalf("depth %d after Escape", s.Depth)
	}
}

func TestCommandArguments(t *testing.T) {
	env, p := newPlat(t)
	env.Controller.SetText(t.Context(), "greet person")
	env.Controller.Wait()
	s := env.Controller.Snapshot()
	if s.Arguments == nil || s.Arguments.Entry != "plat/cmd:greet" || len(s.Arguments.Fields) != 2 || s.Arguments.Focused != "" {
		t.Fatalf("arguments = %+v", s.Arguments)
	}
	// Enter without the required argument focuses it instead of running.
	press(t, env, "Enter")
	if s := env.Controller.Snapshot(); s.Depth != 0 || s.Arguments.Focused != "name" {
		t.Fatalf("after Enter: depth %d, arguments %+v", s.Depth, s.Arguments)
	}
	// Tab cycles through the arguments and back to the search field.
	press(t, env, "Tab")
	if got := env.Controller.Snapshot().Arguments.Focused; got != "tone" {
		t.Fatalf("focused %q", got)
	}
	press(t, env, "Tab")
	if got := env.Controller.Snapshot().Arguments.Focused; got != "" {
		t.Fatalf("focused %q", got)
	}
	press(t, env, "Shift+Tab")
	if got := env.Controller.Snapshot().Arguments.Focused; got != "tone" {
		t.Fatalf("focused %q", got)
	}
	press(t, env, "Escape") // back to the search field, text kept
	if s := env.Controller.Snapshot(); s.Arguments.Focused != "" || s.Text != "greet person" {
		t.Fatalf("after Escape: %+v", s)
	}
	if err := env.Controller.SetArgument("bogus", "x"); err == nil {
		t.Fatal("SetArgument of an unknown argument succeeded")
	}
	if err := env.Controller.SetArgument("name", "Ada"); err != nil {
		t.Fatal(err)
	}
	if err := env.Controller.SetArgument("tone", "warm"); err != nil {
		t.Fatal(err)
	}
	press(t, env, "Enter")
	s = env.Controller.Snapshot()
	if s.Depth != 1 || s.Title != "Hello Ada" {
		t.Fatalf("view = %+v", s)
	}
	reqs := p.viewCalls("greet")
	if args := reqs[len(reqs)-1].Arguments; args["name"] != "Ada" || args["tone"] != "warm" {
		t.Fatalf("arguments = %v", args)
	}

	// A no-view command gets them in ActionDo.Values.
	env.Controller.Reset(t.Context())
	env.Controller.SetText(t.Context(), "shout text")
	env.Controller.Wait()
	if err := env.Controller.SetArgument("text", "hey"); err != nil {
		t.Fatal(err)
	}
	press(t, env, "Enter")
	if got := p.lastHandled(); got.Kind != plugin.ActionRunCommand || string(got.Values["text"]) != `"hey"` {
		t.Fatalf("handled = %+v", got)
	}

	// Arguments belong to the entry they were typed for.
	env.Controller.SetText(t.Context(), "greet person")
	env.Controller.Wait()
	if s := env.Controller.Snapshot(); s.Arguments.Fields[0].Value != "" {
		t.Fatalf("arguments leaked: %+v", s.Arguments)
	}
}

func TestItemArguments(t *testing.T) {
	env, p := newPlat(t)
	p.set(func(p *platPlugin) { p.rootItems = []string{"args"} })
	env.Controller.SetText(t.Context(), "item args")
	env.Controller.Wait()
	if err := env.Controller.SetArgument("suffix", "!"); err != nil {
		t.Fatal(err)
	}
	press(t, env, "Enter")
	if got := p.lastHandled(); got.Target != "args" || string(got.Values["suffix"]) != `"!"` {
		t.Fatalf("handled = %+v", got)
	}
	// Optional arguments do not hold the action back.
	if _, err := env.Controller.RunAction(t.Context(), "plat/args", "copy"); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCommandWithRequiredArguments(t *testing.T) {
	env, p := newPlat(t)
	s := open(t, env, "greet")
	if s.Depth != 0 || selectedTitle(s) != "Greet Person" || s.Arguments == nil || s.Arguments.Focused != "name" {
		t.Fatalf("snapshot = %+v", s)
	}
	if len(p.viewCalls("greet")) != 0 {
		t.Fatal("ran without its arguments")
	}
}

func TestPreferenceExtensions(t *testing.T) {
	env, p := newPlat(t)
	i := slices.IndexFunc(env.Engine.Plugins(), func(p core.PluginInfo) bool { return p.ID == "plat" })
	info := env.Engine.Plugins()[i]
	prefs := info.Preferences
	if prefs[1].Group != "Connection" || prefs[3].Action == nil || !prefs[2].DynamicOptions || prefs[1].VisibleWhen == nil {
		t.Fatalf("preferences = %+v", prefs)
	}
	values := map[string]jsontext.Value{}
	if core.PrefVisible(prefs, values, prefs[1]) {
		t.Fatal("server shown with the default mode")
	}
	values["mode"] = jsontext.Value(` "advanced" `)
	if !core.PrefVisible(prefs, values, prefs[1]) || !core.PrefVisible(prefs, values, prefs[0]) {
		t.Fatal("server hidden in advanced mode")
	}

	opts, err := env.Engine.PreferenceOptions(t.Context(), "plat", "account")
	if err != nil || len(opts) != 1 || opts[0].Title != "Account account" {
		t.Fatalf("options = %+v, %v", opts, err)
	}
	if _, err := env.Engine.PreferenceOptions(t.Context(), "plat", "mode"); err == nil {
		t.Fatal("options of a static preference")
	}
	p.set(func(p *platPlugin) { p.optsErr = errors.New("offline") })
	if _, err := env.Engine.PreferenceOptions(t.Context(), "plat", "account"); err == nil || err.Error() != "offline" {
		t.Fatalf("err = %v", err)
	}

	out, err := env.Engine.RunPreferenceAction(t.Context(), "plat", "login")
	if err != nil || out.Toast == nil || out.Toast.Title != "Signed in" {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if _, err := env.Engine.RunPreferenceAction(t.Context(), "plat", "mode"); err == nil {
		t.Fatal("ran a preference that is not a button")
	}
	if _, err := env.Engine.RunPreferenceAction(t.Context(), "nope", "login"); err == nil {
		t.Fatal("ran a button of an unknown plugin")
	}
}
