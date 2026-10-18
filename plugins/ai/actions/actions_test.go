package actions

import (
	"slices"
	"testing"
)

func defaults() []Action {
	return []Action{
		{ID: "translate", Title: "Translate to {lang}", Name: "Translate", Prompt: "Translate into {lang}."},
		{ID: "explain", Title: "Explain", Name: "Explain", Prompt: "Explain."},
		{ID: "summarize", Title: "Summarize", Name: "Summarize", Prompt: "Summarize.", MinLength: 200},
	}
}

func idsOf(list []Action) []string {
	var out []string
	for _, a := range list {
		out = append(out, a.ID)
	}
	return out
}

func find(t *testing.T, list []Action, id string) Action {
	t.Helper()
	for _, a := range list {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("no %q in %q", id, idsOf(list))
	return Action{}
}

// roundTrip stores list and merges it again, as the settings window and the
// plugin do.
func roundTrip(t *testing.T, defs, list []Action) ([]Action, Stored) {
	t.Helper()
	st := Diff(defs, list)
	got, ok := Decode(Encode(st))
	if !ok {
		t.Fatalf("stored %s does not decode", Encode(st))
	}
	return Merge(defs, got), got
}

func TestMergeDefaults(t *testing.T) {
	list := Merge(defaults(), Stored{})
	if got := idsOf(list); !slices.Equal(got, []string{"translate", "explain", "summarize"}) {
		t.Fatalf("ids %q", got)
	}
	for _, a := range list {
		if !a.Builtin || a.Disabled || a.Name == "" {
			t.Errorf("%+v", a)
		}
	}
	// Nothing changed: nothing stored.
	if st := Diff(defaults(), list); st.Order != nil || st.Builtin != nil || st.Custom != nil {
		t.Errorf("stored %+v", st)
	}
	for _, raw := range []string{"", "null", "{"} {
		st, ok := Decode([]byte(raw))
		if ok != (raw != "{") || len(Merge(defaults(), st)) != 3 {
			t.Errorf("%q: %+v %v", raw, st, ok)
		}
	}
}

func TestMergeChangedBuiltin(t *testing.T) {
	list := Merge(defaults(), Stored{})
	list[1].Title, list[1].Prompt = "Explain simply", "Explain like I'm five."
	list[2].Disabled = true
	got, st := roundTrip(t, defaults(), list)
	if o := st.Builtin["explain"]; o.Title != "Explain simply" || o.Prompt != "Explain like I'm five." || o.Disabled {
		t.Errorf("stored override %+v", o)
	}
	if o := st.Builtin["summarize"]; o != (Override{Disabled: true}) {
		t.Errorf("only the switch is stored: %+v", o)
	}
	if _, ok := st.Builtin["translate"]; ok || st.Order != nil {
		t.Errorf("stored %+v", st)
	}
	e := find(t, got, "explain")
	if e.Title != "Explain simply" || e.Name != "" || e.Prompt != "Explain like I'm five." || !e.Builtin {
		t.Errorf("explain %+v", e)
	}
	if s := find(t, got, "summarize"); !s.Disabled || s.MinLength != 200 {
		t.Errorf("summarize %+v", s)
	}
	// An unchanged built-in follows a new version's default prompt.
	defs := defaults()
	defs[0].Prompt = "Translate better into {lang}."
	if tr := find(t, Merge(defs, st), "translate"); tr.Prompt != "Translate better into {lang}." {
		t.Errorf("translate %+v", tr)
	}
}

func TestMergeOrderAndNewBuiltin(t *testing.T) {
	list := Merge(defaults(), Stored{})
	list = append(list, Action{ID: NewID(list), Title: "Formal email", Prompt: "Rewrite as a formal email: {selection}", Result: ModeResult})
	if list[3].ID != "custom-1" {
		t.Fatalf("new id %q", list[3].ID)
	}
	// Only the custom action added: the default order, not stored.
	if _, st := roundTrip(t, defaults(), list); st.Order != nil || len(st.Custom) != 1 {
		t.Fatalf("stored %+v", st)
	}
	if !Move(list, 3, -1) || !Move(list, 0, 1) || Move(list, 0, -1) || Move(list, 3, 1) {
		t.Fatal("Move")
	}
	got, st := roundTrip(t, defaults(), list)
	want := []string{"explain", "translate", "custom-1", "summarize"}
	if !slices.Equal(idsOf(got), want) || !slices.Equal(st.Order, want) {
		t.Fatalf("order %q, stored %q", idsOf(got), st.Order)
	}
	if c := find(t, got, "custom-1"); c.Builtin || !c.ResultMode() || c.Title != "Formal email" {
		t.Errorf("custom %+v", c)
	}
	// A later version adds a built-in action between translate and
	// summarize: it shows after translate, where it is by default.
	defs := slices.Insert(defaults(), 1, Action{ID: "proofread", Title: "Proofread", Prompt: "Proofread."})
	if got := idsOf(Merge(defs, st)); !slices.Equal(got, []string{"explain", "translate", "proofread", "custom-1", "summarize"}) {
		t.Errorf("with a new built-in: %q", got)
	}
	// One added first.
	defs = append([]Action{{ID: "first", Title: "First", Prompt: "x"}}, defaults()...)
	if got := idsOf(Merge(defs, st)); got[0] != "first" {
		t.Errorf("with a new first built-in: %q", got)
	}
	// A built-in action removed in a later version disappears.
	if got := idsOf(Merge(defaults()[:2], st)); slices.Contains(got, "summarize") {
		t.Errorf("removed built-in: %q", got)
	}
}

func TestDeleteAndRestore(t *testing.T) {
	list := Merge(defaults(), Stored{})
	list = append(list,
		Action{ID: "custom-1", Title: "A", Prompt: "a"},
		Action{ID: "custom-2", Title: "B", Prompt: "b", Targets: TargetFiles})
	list[0].Disabled, list[1].Title = true, "Explain!"
	Move(list, 4, -1) // B before A
	Move(list, 0, 1)  // explain first
	got, _ := roundTrip(t, defaults(), list)
	if g := idsOf(got); !slices.Equal(g, []string{"explain", "translate", "summarize", "custom-2", "custom-1"}) {
		t.Fatalf("before: %q", g)
	}
	// Deleting a custom action.
	i := slices.IndexFunc(got, func(a Action) bool { return a.ID == "custom-1" })
	got, st := roundTrip(t, defaults(), slices.Delete(got, i, i+1))
	if slices.Contains(idsOf(got), "custom-1") || len(st.Custom) != 1 {
		t.Fatalf("after delete: %q %+v", idsOf(got), st)
	}
	// Restore Defaults: the built-in actions as they come, the custom ones kept.
	got, st = roundTrip(t, defaults(), Restore(defaults(), got))
	if g := idsOf(got); !slices.Equal(g, []string{"translate", "explain", "summarize", "custom-2"}) {
		t.Fatalf("restored: %q", g)
	}
	if st.Builtin != nil || st.Order != nil || find(t, got, "translate").Disabled || find(t, got, "explain").Title != "Explain" {
		t.Errorf("restored: %+v", st)
	}
	if c := find(t, got, "custom-2"); c.Targets != TargetFiles || c.OnSelection() || !c.OnFiles() {
		t.Errorf("custom kept: %+v", c)
	}
	// A custom action cannot take a built-in ID.
	if got := Merge(defaults(), Stored{Custom: []Action{{ID: "explain", Title: "X", Prompt: "x"}}}); len(got) != 3 || !find(t, got, "explain").Builtin {
		t.Errorf("custom with a built-in ID: %+v", got)
	}
}

func TestExpandAndCheck(t *testing.T) {
	v := Vars{Selection: "say {lang}", Lang: "French", Input: "short", App: "notepad", Date: "2026-10-18"}
	got := Expand("In {lang} ({input}), from {app} on {date}: {selection} {unknown}", v)
	if want := "In French (short), from notepad on 2026-10-18: say {lang} {unknown}"; got != want {
		t.Errorf("Expand = %q, want %q", got, want)
	}
	if !UsesSelection("a {selection}") || UsesSelection("a {lang}") || !UsesInput("{input}") {
		t.Error("Uses*")
	}
	for a, want := range map[*Action]Problem{
		{Title: " ", Prompt: "x"}: NoTitle, {Title: "x", Prompt: "\n"}: NoPrompt, {Title: "x", Prompt: "y"}: NoProblem,
	} {
		if got := Check(*a); got != want {
			t.Errorf("Check(%+v) = %q", *a, got)
		}
	}
	if (Action{Icon: "nope"}).IconName() != DefaultIcon || (Action{Icon: "code"}).IconName() != "code" {
		t.Error("IconName")
	}
	if got := (Action{Keywords: " a, ,b ,"}).KeywordList(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("KeywordList %q", got)
	}
}
