package ui_test

import (
	"slices"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/internal/ui"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai"
	"github.com/eziq1/golauncher/plugins/ai/actions"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// newActionsSettings is the settings on the AI plugin's page, whose "AI
// Actions" section edits the list of R103.
func newActionsSettings(t *testing.T, locale string) (*mui.Tester, *app.Env) {
	t.Helper()
	clip := &hostfake.Clipboard{}
	p := ai.NewWithOptions(ai.Options{Backend: &backend.Fake{}, Sync: true, HasProtocol: func(string) bool { return false }})
	env, err := app.Build(t.Context(), app.Options{Plugins: []plugin.Plugin{p}, Fake: true, Locale: locale,
		System: &app.Recorder{Clipboard: clip}, Clipboard: clip})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.Close() })
	if env.AIActions == nil {
		t.Fatal("no AI actions in the environment")
	}
	s := ui.NewSettings(env, &fakeHost{}, "1.0.0-test")
	tt := mui.NewTester(s.View, ui.SettingsWidth, 6000)
	s.ShowPage(ui.PagePlugins)
	tt.Frame()
	if err := tt.Click(map[string]string{"en": "Configure", "zh-CN": "配置"}[locale]); err != nil {
		t.Fatal(err)
	}
	settle(s, tt)
	return tt, env
}

// storedActions is the stored preference and the list it gives.
func storedActions(env *app.Env) (actions.Stored, []actions.Action) {
	st, _ := actions.Decode(env.Config.Get().Plugin(ai.ID).Preferences[actions.Pref])
	return st, actions.Merge(env.AIActions.BuiltinActions(), st)
}

func actionIDs(list []actions.Action) []string {
	var out []string
	for _, a := range list {
		out = append(out, a.ID)
	}
	return out
}

func clickText(t *testing.T, tt *mui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("%v in %q", err, tt.Texts())
	}
	tt.Frame()
}

// typeInto replaces the text of the field labeled label.
func typeInto(t *testing.T, tt *mui.Tester, label, text string) {
	t.Helper()
	clickText(t, tt, label)
	tt.Key(0, mui.KeyEnd)
	for range 120 {
		tt.Key(0, mui.KeyBackspace)
	}
	tt.Type(text)
	tt.Frame()
}

// TestSettingsActions: the AI Actions section lists the built-in actions;
// an action is turned off, moved, edited and restored; a custom action is
// added (its name and prompt are needed), and deleted.
func TestSettingsActions(t *testing.T) {
	tt, env := newActionsSettings(t, "en")
	for _, want := range []string{"AI Actions", "New Action", "Restore Defaults", "Translate to {lang}", "Explain", "Summarize",
		"Rewrite", "Suggest Replies", "Built-in", "Selected text · Opens a chat", "Selected text · at least 200 characters · Opens a chat"} {
		if !hasTextPart(tt, want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("Custom") {
		t.Error("a custom badge without custom actions")
	}
	if _, ok := tt.Find("Delete Explain"); ok {
		t.Error("a built-in action can be deleted")
	}

	// Off and on.
	clickText(t, tt, "Use Explain")
	if st, _ := storedActions(env); !st.Builtin["explain"].Disabled || st.Order != nil {
		t.Fatalf("stored %+v", st)
	}
	// Moved up.
	clickText(t, tt, "Move Explain up")
	if st, list := storedActions(env); !slices.Equal(actionIDs(list)[:2], []string{"explain", "translate"}) || st.Order == nil {
		t.Fatalf("order %q", actionIDs(list))
	}
	clickText(t, tt, "Move Explain down")
	if st, _ := storedActions(env); st.Order != nil {
		t.Fatalf("back in the default order, still stored %q", st.Order)
	}

	// A built-in action: only its name and prompt.
	clickText(t, tt, "Edit Rewrite")
	if !tt.HasText("Edit AI Action") || !hasTextPart(tt, "A built-in action") || tt.HasText("Use on") || !hasTextPart(tt, "{selection} the selected text") {
		t.Fatalf("dialog %q", tt.Texts())
	}
	typeInto(t, tt, "Name", "Polish")
	clickText(t, tt, "Save")
	if st, _ := storedActions(env); st.Builtin["rewrite"].Title != "Polish" || st.Builtin["rewrite"].Prompt != "" || tt.HasText("Edit AI Action") {
		t.Fatalf("stored %+v", st.Builtin)
	}

	// A new action: a name and a prompt are needed.
	clickText(t, tt, "New Action")
	if !tt.HasText("New AI Action") || !tt.HasText("Use on") || !tt.HasText("Minimum length") {
		t.Fatalf("dialog %q", tt.Texts())
	}
	clickText(t, tt, "Save")
	if !tt.HasText("Give the action a name.") {
		t.Fatalf("no name problem in %q", tt.Texts())
	}
	typeInto(t, tt, "Name", "Formal Email")
	clickText(t, tt, "Save")
	if !tt.HasText("Write the prompt.") {
		t.Fatalf("no prompt problem in %q", tt.Texts())
	}
	typeInto(t, tt, "Prompt", "Rewrite as a formal email: {selection}")
	clickText(t, tt, "Selected text and files")
	clickText(t, tt, "Shows the result")
	typeInto(t, tt, "Keywords", "mail, letter")
	clickText(t, tt, "Save")
	st, list := storedActions(env)
	if len(st.Custom) != 1 {
		t.Fatalf("stored %+v; texts %q", st, tt.Texts())
	}
	c := st.Custom[0]
	if c.ID != "custom-1" || c.Title != "Formal Email" || c.Prompt != "Rewrite as a formal email: {selection}" || c.Targets != actions.TargetBoth ||
		c.Result != actions.ModeResult || c.Keywords != "mail, letter" || c.Icon != "" || list[len(list)-1].ID != "custom-1" {
		t.Fatalf("custom %+v", c)
	}
	if !tt.HasText("Custom") || !hasTextPart(tt, "Selected text and files · Shows the result") {
		t.Fatalf("row %q", tt.Texts())
	}
	// Edited, and moved up among the built-in ones.
	clickText(t, tt, "Edit Formal Email")
	typeInto(t, tt, "Minimum length", "x")
	clickText(t, tt, "Save")
	if !tt.HasText("The minimum length is a whole number of characters.") {
		t.Fatalf("no length problem in %q", tt.Texts())
	}
	typeInto(t, tt, "Minimum length", "10")
	clickText(t, tt, "Save")
	clickText(t, tt, "Move Formal Email up")
	st, list = storedActions(env)
	if st.Custom[0].MinLength != 10 || actionIDs(list)[4] != "custom-1" {
		t.Fatalf("stored %+v, order %q", st.Custom, actionIDs(list))
	}

	// Restore Defaults, after a confirmation: the built-in actions as they
	// come; the custom one stays, after them.
	clickText(t, tt, "Restore Defaults")
	if !tt.HasText("Restore the built-in actions?") {
		t.Fatalf("no confirmation in %q", tt.Texts())
	}
	clickText(t, tt, "Cancel")
	if st, _ := storedActions(env); st.Builtin == nil {
		t.Fatal("restored without confirming")
	}
	clickText(t, tt, "Restore Defaults")
	clickText(t, tt, "Restore")
	st, list = storedActions(env)
	if st.Builtin != nil || st.Order != nil || len(st.Custom) != 1 || actionIDs(list)[5] != "custom-1" || !tt.HasText("Rewrite") {
		t.Fatalf("restored %+v, order %q", st, actionIDs(list))
	}

	// Deleted.
	clickText(t, tt, "Delete Formal Email")
	if st, _ := storedActions(env); len(st.Custom) != 0 || tt.HasText("Formal Email") {
		t.Fatalf("after delete %+v", st)
	}
}

// TestSettingsActionsChinese: the section in Chinese.
func TestSettingsActionsChinese(t *testing.T) {
	tt, _ := newActionsSettings(t, "zh-CN")
	for _, want := range []string{"AI 操作", "新建操作", "恢复默认", "翻译成{lang}", "内置", "选中文字 · 至少 200 字 · 打开对话"} {
		if !hasTextPart(tt, want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
	clickText(t, tt, "新建操作")
	for _, want := range []string{"新建 AI 操作", "名称", "提示词", "用于", "直接出结果", "占位符：{selection}"} {
		if !hasTextPart(tt, want) {
			t.Fatalf("no %q in %q", want, tt.Texts())
		}
	}
}
