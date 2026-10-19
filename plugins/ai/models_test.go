package ai

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Models and efforts made up for the tests (R109).
var testModels = backend.ModelList{Models: []backend.Model{
	{ID: "big", Name: "Big One", Efforts: []string{"low", "medium", "high", "xhigh"}, DefaultEffort: "medium"},
	{ID: "small", Name: "Small One", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "low"},
}}

// newEffortPlugin is the plugin with Claude Code and Codex as in the
// registry: Claude Code with --effort levels, Codex listing its models.
func newEffortPlugin(t *testing.T, claude, codex *backend.FakeInstalls) (*Plugin, *hostfake.Host) {
	t.Helper()
	if claude.Fake == nil {
		claude.Fake = &backend.Fake{}
	}
	if codex.Fake == nil {
		codex.Fake = &backend.Fake{}
	}
	codex.BackendName = "Codex (ChatGPT)"
	p := NewWithOptions(Options{Sync: true, HasProtocol: func(string) bool { return false }, Backends: []BackendOption{
		{ID: DefaultBackendID, Backend: claude, Efforts: true},
		{ID: "codex", Backend: codex, DynamicModels: true, Efforts: true},
		{ID: "gemini", Backend: &named{Fake: &backend.Fake{}, name: "Gemini CLI"}, Models: []plugin.Option{{Value: "g1", Title: "G1"}}},
	}})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true")})
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p, h
}

func optionValues(opts []plugin.Option) []string {
	var out []string
	for _, o := range opts {
		out = append(out, o.Value)
	}
	return out
}

// TestEffortPreferences: the effort preferences follow the model preference
// of their backend; Codex's model and effort choices come from the backend;
// Gemini gets none (R109).
func TestEffortPreferences(t *testing.T) {
	p, _ := newEffortPlugin(t, &backend.FakeInstalls{}, &backend.FakeInstalls{})
	var names []string
	for _, pr := range p.Manifest().Preferences {
		names = append(names, pr.Name)
		switch pr.Name {
		case "model.codex", "effort.codex", "effort.claude-code":
			if !pr.DynamicOptions || len(pr.Options) != 0 || pr.Type != plugin.PrefSelect || string(pr.Default) != `""` || pr.VisibleWhen != nil {
				t.Errorf("%s: %+v", pr.Name, pr)
			}
		case "model.gemini":
			if pr.DynamicOptions || len(pr.Options) != 2 {
				t.Errorf("%s: %+v", pr.Name, pr)
			}
		}
	}
	i := slices.Index(names, prefModel)
	if i < 0 || !slices.Equal(names[i:i+5], []string{prefModel, "effort.claude-code", "model.codex", "effort.codex", "model.gemini"}) ||
		slices.Contains(names, "effort.gemini") {
		t.Fatalf("preferences %q", names)
	}
}

// TestModelOptions: Codex's models, after "Default"; why only "Default"
// shows when they cannot be listed; a chosen model no longer listed stays.
func TestModelOptions(t *testing.T) {
	codex := &backend.FakeInstalls{ModelList: testModels}
	p, h := newEffortPlugin(t, &backend.FakeInstalls{}, codex)
	opts, err := p.PreferenceOptions(t.Context(), "model.codex")
	if err != nil || !slices.Equal(optionValues(opts), []string{"", "big", "small"}) || opts[0].Title != "Default" || opts[1].Title != "Big One" {
		t.Fatalf("options %+v, %v", opts, err)
	}
	// Chosen, but no longer listed: shown as it is, with a note.
	setPrefs(h, "model.codex", `"retired-model"`)
	opts, err = p.PreferenceOptions(t.Context(), "model.codex")
	if err != nil || !slices.Equal(optionValues(opts), []string{"", "big", "small", "retired-model"}) ||
		opts[3].Title != "retired-model (not in the current list)" {
		t.Fatalf("options %+v, %v", opts, err)
	}
	// The bundled list: shown, with a note.
	codex.ModelList.Bundled = true
	opts, err = p.PreferenceOptions(t.Context(), "model.codex")
	if len(opts) != 4 || err == nil || !strings.Contains(err.Error(), "built into it") {
		t.Fatalf("bundled: %+v, %v", opts, err)
	}
	setPrefs(h)
	for _, tt := range []struct {
		err  error
		want string
	}{
		{&backend.Error{Kind: backend.ErrNotInstalled}, "Codex (ChatGPT) is not installed, so only Default is offered."},
		{&backend.Error{Kind: backend.ErrVersion, Message: "old"}, "This version of Codex (ChatGPT) cannot list its models"},
		{&backend.Error{Kind: backend.ErrFailed, Message: "`codex debug models` failed: boom"}, "could not list its choices, so only Default is offered: `codex debug models` failed: boom"},
		{fmt.Errorf("codex: still listing the models: %w", context.DeadlineExceeded), "is still listing its models"},
	} {
		codex.ModelErr = tt.err
		opts, err := p.PreferenceOptions(t.Context(), "model.codex")
		if !slices.Equal(optionValues(opts), []string{""}) || err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%v: %+v, %v", tt.err, opts, err)
		}
	}
	// Other preferences have no dynamic options.
	if opts, err := p.PreferenceOptions(t.Context(), prefModel); opts != nil || err != nil {
		t.Errorf("model: %+v, %v", opts, err)
	}
}

// TestEffortOptions: the levels of the chosen model (with the default model,
// those every listed model takes), or of Claude Code's --effort; a chosen
// level outside them stays, marked as not passed.
func TestEffortOptions(t *testing.T) {
	claude := &backend.FakeInstalls{EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}}
	codex := &backend.FakeInstalls{ModelList: testModels}
	p, h := newEffortPlugin(t, claude, codex)
	for _, tt := range []struct {
		prefs []string
		want  []string
		note  string
	}{
		{nil, []string{"", "low", "medium", "high"}, ""},
		{[]string{"model.codex", `"big"`}, []string{"", "low", "medium", "high", "xhigh"}, ""},
		{[]string{"model.codex", `"small"`, "effort.codex", `"xhigh"`}, []string{"", "low", "medium", "high", "xhigh"}, ""},
		{[]string{"model.codex", `"retired-model"`}, []string{""}, "retired-model is not in the current list"},
	} {
		setPrefs(h, tt.prefs...)
		opts, err := p.PreferenceOptions(t.Context(), "effort.codex")
		if !slices.Equal(optionValues(opts), tt.want) || (err == nil) != (tt.note == "") || err != nil && !strings.Contains(err.Error(), tt.note) {
			t.Errorf("%q: %+v, %v", tt.prefs, opts, err)
		}
	}
	// The unsupported level is marked.
	setPrefs(h, "model.codex", `"small"`, "effort.codex", `"xhigh"`)
	opts, _ := p.PreferenceOptions(t.Context(), "effort.codex")
	if last := opts[len(opts)-1]; last.Title != "Extra high (not supported here: not passed)" || opts[1].Title != "Low" {
		t.Errorf("options %+v", opts)
	}

	// Claude Code: the levels of its --effort.
	opts, err := p.PreferenceOptions(t.Context(), "effort.claude-code")
	if err != nil || !slices.Equal(optionValues(opts), []string{"", "low", "medium", "high", "xhigh", "max"}) || opts[5].Title != "Max" {
		t.Fatalf("claude: %+v, %v", opts, err)
	}
	// An older version without --effort; one that cannot be checked.
	claude.EffortLevels = nil
	setPrefs(h, "effort.claude-code", `"high"`)
	opts, err = p.PreferenceOptions(t.Context(), "effort.claude-code")
	if !slices.Equal(optionValues(opts), []string{"", "high"}) || err == nil || !strings.Contains(err.Error(), "no way to choose the effort") {
		t.Fatalf("old claude: %+v, %v", opts, err)
	}
	claude.EffortErr = &backend.Error{Kind: backend.ErrNotInstalled}
	opts, err = p.PreferenceOptions(t.Context(), "effort.claude-code")
	if len(opts) != 2 || err == nil || !strings.Contains(err.Error(), "Claude Code is not installed") {
		t.Fatalf("no claude: %+v, %v", opts, err)
	}
}

// TestEffortInChat: a chat passes the effort only when the chosen model takes
// it (Codex); Claude Code gets the chosen level (its adapter leaves out what
// the CLI lacks). The footer shows the effort passed.
func TestEffortInChat(t *testing.T) {
	claude := &backend.FakeInstalls{EffortLevels: []string{"low", "medium", "high"}}
	codex := &backend.FakeInstalls{ModelList: testModels}
	p, h := newEffortPlugin(t, claude, codex)
	for i, tt := range []struct {
		model, effort, want string
	}{
		{"big", "xhigh", "xhigh"},
		{"small", "xhigh", ""}, // the model does not take it
		{"", "high", "high"},   // every listed model takes it
		{"", "xhigh", ""},      // not every listed model does
		{"retired-model", "high", ""},
		{"small", "", ""},
	} {
		setPrefs(h, prefBackend, `"codex"`, "model.codex", `"`+tt.model+`"`, "effort.codex", `"`+tt.effort+`"`)
		v := openChat(t, p, fmt.Sprintf("c%d", i), chatPayload{Text: "hi"})
		opens := codex.Opens()
		if got := opens[len(opens)-1]; got.Effort != tt.want || got.Model != tt.model {
			t.Errorf("%+v: opened with model %q effort %q", tt, got.Model, got.Effort)
		}
		if tt.want != "" && !strings.Contains(v.Detail.Metadata[0].Value, " · ") {
			t.Errorf("%+v: footer %+v", tt, v.Detail.Metadata)
		}
	}
	// The list cannot be had: no effort rather than a failed chat.
	codex.ModelErr = &backend.Error{Kind: backend.ErrFailed, Message: "offline"}
	setPrefs(h, prefBackend, `"codex"`, "model.codex", `"big"`, "effort.codex", `"high"`)
	v := openChat(t, p, "c-err", chatPayload{Text: "hi"})
	if opens := codex.Opens(); opens[len(opens)-1].Effort != "" || !strings.Contains(v.Detail.Markdown, "You asked: hi") {
		t.Errorf("without a list: %+v\n%s", opens[len(opens)-1], v.Detail.Markdown)
	}

	setPrefs(h, "effort.claude-code", `"high"`)
	v = openChat(t, p, "cl", chatPayload{Text: "hi"})
	if opens := claude.Opens(); len(opens) != 1 || opens[0].Effort != "high" || v.Detail.Metadata[0].Value != "fake-model · High" {
		t.Errorf("claude: %+v, footer %+v", opens, v.Detail.Metadata)
	}
}

// TestRedetectModels: "Re-detect" in the Backends section lists the models
// again.
func TestRedetectModels(t *testing.T) {
	claude, codex := &backend.FakeInstalls{}, &backend.FakeInstalls{ModelList: testModels}
	p, _ := newEffortPlugin(t, claude, codex)
	p.BackendReports(t.Context(), false)
	if _, n := codex.Listed(); n != 0 {
		t.Fatalf("redetected without refresh: %d", n)
	}
	p.BackendReports(t.Context(), true)
	if _, n := codex.Listed(); n != 1 {
		t.Fatalf("redetects %d", n)
	}
	if _, n := claude.Listed(); n != 0 {
		t.Fatalf("claude redetects %d", n)
	}
}
