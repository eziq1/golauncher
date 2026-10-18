package ai

import (
	"encoding/json/jsontext"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/actions"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// setActions stores the list of AI actions (R103) as the settings window
// does, with AI on.
func setActions(t *testing.T, h *hostfake.Host, st actions.Stored) {
	t.Helper()
	h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true"), actions.Pref: actions.Encode(st)})
}

// TestBuiltinActionsUnchanged: the built-in actions are those before R103,
// with the same prompts, and nothing is stored for them.
func TestBuiltinActionsUnchanged(t *testing.T) {
	p, _ := newTestPlugin(t, &backend.Fake{}, true)
	defs := p.BuiltinActions()
	if got := idsOf(defs); !slices.Equal(got, []string{"translate", "explain", "summarize", "rewrite", "reply"}) {
		t.Fatalf("built-in actions %q", got)
	}
	for _, a := range defs {
		if a.OnFiles() || a.ResultMode() || a.IconName() != "sparkles" || a.Name == "" || a.KeywordList() == nil {
			t.Errorf("%+v", a)
		}
	}
	if defs[0].Title != "Translate to {lang}" || defs[2].MinLength != 200 {
		t.Errorf("translate %+v, summarize %+v", defs[0], defs[2])
	}
}

func idsOf(list []actions.Action) []string {
	var out []string
	for _, a := range list {
		out = append(out, a.ID)
	}
	return out
}

// TestSelectionRowsFollowTheList: the rows come from the merged list: only
// the enabled actions on the selected text, in its order, with "Ask AI…"
// second; a custom action's title, icon and keywords; the minimum length.
func TestSelectionRowsFollowTheList(t *testing.T) {
	p, h := newTestPlugin(t, &backend.Fake{}, true)
	setActions(t, h, actions.Stored{
		Order:   []string{"rewrite", "custom-1", "translate", "explain", "summarize", "custom-2", "custom-3", "reply"},
		Builtin: map[string]actions.Override{"explain": {Disabled: true}, "rewrite": {Title: "Polish"}},
		Custom: []actions.Action{
			{ID: "custom-1", Title: "Formal Email", Prompt: "Make it a formal email.", Keywords: "mail, 邮件", Icon: "message-square"},
			{ID: "custom-2", Title: "Review Code", Prompt: "Review it.", Targets: actions.TargetFiles},
			{ID: "custom-3", Title: "Long Only", Prompt: "Shorten it.", Targets: actions.TargetBoth, MinLength: 50},
		},
	})
	items := selectionItems(t, p, textSel("Let's move the meeting to Thursday."), "")
	if got := ids(items); !slices.Equal(got, []string{"rewrite", "ask", "custom-1", "translate", "reply"}) {
		t.Fatalf("rows %q", got)
	}
	if it := byID(t, items, "rewrite"); it.Title != "Polish" {
		t.Errorf("renamed built-in %q", it.Title)
	}
	c := byID(t, items, "custom-1")
	if c.Title != "Formal Email" || c.Icon.Name != "message-square" || !slices.Equal(c.Keywords, []string{"mail", "邮件"}) || c.Subtitle != p.be.Name() || len(c.Arguments) != 0 {
		t.Errorf("custom row %+v", c)
	}
	// Long enough for custom-3 (and summarize, from 200).
	items = selectionItems(t, p, textSel(strings.Repeat("word ", 41)), "x")
	if got := ids(items); !slices.Equal(got, []string{"instruction", "rewrite", "ask", "custom-1", "translate", "summarize", "custom-3", "reply"}) {
		t.Fatalf("long rows %q", got)
	}
	// The chat's title: the custom action's name; a renamed built-in's new name.
	fake := &backend.Fake{}
	p, h = newTestPlugin(t, fake, true)
	setActions(t, h, actions.Stored{Custom: []actions.Action{{ID: "custom-1", Title: "Formal Email", Prompt: "Make it a formal email."}}})
	v := openFrom(t, p, byID(t, selectionItems(t, p, textSel("see you"), ""), "custom-1"), "v1", nil)
	if v.Title != "Formal Email: see you" || fake.Sends()[0] != "Make it a formal email.\n\n> see you" {
		t.Errorf("title %q, sent %q", v.Title, fake.Sends())
	}
	// Everything off: only "Ask AI…".
	setActions(t, h, actions.Stored{Builtin: map[string]actions.Override{
		"translate": {Disabled: true}, "explain": {Disabled: true}, "summarize": {Disabled: true}, "rewrite": {Disabled: true}, "reply": {Disabled: true}}})
	if got := ids(selectionItems(t, p, textSel("x"), "")); !slices.Equal(got, []string{"ask"}) {
		t.Errorf("all off: %q", got)
	}
}

// TestActionPlaceholders: {selection} places the text (no quote added),
// {lang} is the target language of R65, {input} the argument typed, {app}
// the app's name and {date} today; text put in is not expanded again.
func TestActionPlaceholders(t *testing.T) {
	fake := &backend.Fake{}
	p, h := newTestPlugin(t, fake, true)
	h.I18N.SetLocale("zh-CN")
	setActions(t, h, actions.Stored{Custom: []actions.Action{
		{ID: "custom-1", Title: "To {lang}", Prompt: "Into {lang}, {input}, for {app} on {date}:\n\n{selection}"},
		{ID: "custom-2", Title: "Plain", Prompt: "In {lang} please."},
	}})
	sel := textSel("Hello {input} world\r\n")
	sel.Truncated = true
	items := selectionItems(t, p, sel, "")
	it := byID(t, items, "custom-1")
	if it.Title != "To 简体中文" || len(it.Arguments) != 1 || it.Arguments[0].Name != argInput || it.Arguments[0].Placeholder != "补充要求（可留空）" {
		t.Fatalf("row %+v", it)
	}
	openFrom(t, p, it, "v1", map[string]string{argInput: " keep it short "})
	date := h.Now().Format("2006-01-02")
	want := "Into Simplified Chinese, keep it short, for notepad on " + date + ":\n\nHello {input} world\n\n_(The quoted text was truncated to 21 characters.)_"
	if s := fake.Sends(); len(s) != 1 || s[0] != want {
		t.Fatalf("sent %q\nwant %q", s, want)
	}
	// Chinese text: into English (R65); no {selection}: quoted as before.
	openFrom(t, p, byID(t, selectionItems(t, p, textSel("你好，世界"), ""), "custom-2"), "v2", nil)
	if s := fake.Sends(); len(s) != 2 || s[1] != "In English please.\n\n> 你好，世界" {
		t.Fatalf("sent %q", s)
	}
	// A built-in action's prompt can be changed, with placeholders.
	setActions(t, h, actions.Stored{Builtin: map[string]actions.Override{"translate": {Prompt: "Into {lang}: {selection}"}}})
	openFrom(t, p, byID(t, selectionItems(t, p, textSel("hi there"), ""), "translate"), "v3", nil)
	if s := fake.Sends(); s[2] != "Into Simplified Chinese: hi there" {
		t.Fatalf("sent %q", s[2])
	}
	if appName(`C:\Apps\Code.EXE`) != "Code" || appName("") != "" || appName("Safari") != "Safari" {
		t.Error("appName")
	}
}

// TestFileActions: an action on files is offered for the files handed to
// the root search, after "Ask AI about…", and sends its prompt with the
// files attached; actions on the selected text only are not.
func TestFileActions(t *testing.T) {
	fake := &backend.Fake{}
	p, h := newTestPlugin(t, fake, true)
	setActions(t, h, actions.Stored{Custom: []actions.Action{
		{ID: "custom-1", Title: "Review Code", Prompt: "Review these files on {date}.", Targets: actions.TargetFiles, Icon: "code"},
		{ID: "custom-2", Title: "Only Text", Prompt: "x"},
		{ID: "custom-3", Title: "Summarize Both", Prompt: "Summarize {selection}", Targets: actions.TargetBoth, Result: actions.ModeResult},
	}})
	f := writeFile(t, t.TempDir(), "main.go", []byte("package main\n"))
	items, err := p.FileItems(t.Context(), plugin.FilesQuery{Files: []plugin.FileInfo{{Path: f, Name: "main.go"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(items); !slices.Equal(got, []string{"files-ask", "files-custom-1", "files-custom-3"}) {
		t.Fatalf("rows %q", got)
	}
	it := byID(t, items, "files-custom-1")
	if it.Title != "Review Code" || it.Icon.Name != "code" || it.Score != 0 {
		t.Errorf("row %+v", it)
	}
	v := openFrom(t, p, it, "v1", nil)
	sends, att := fake.Sends(), fake.Attached()
	if len(sends) != 1 || sends[0] != "Review these files on "+h.Now().Format("2006-01-02")+"." || len(att[0]) != 1 || att[0][0].Name != "main.go" {
		t.Fatalf("sent %q with %+v", sends, att)
	}
	if v.Title != "Review Code: main.go" {
		t.Errorf("title %q", v.Title)
	}
	// {selection} is empty for files; the result mode applies.
	v = openFrom(t, p, byID(t, items, "files-custom-3"), "v2", nil)
	if s := fake.Sends(); s[1] != "Summarize" || v.Sections[0].Items[0].ID != "result" {
		t.Errorf("sent %q, view %+v", s, v.Sections[0].Items[0])
	}
}

// resultAction is the action with id in a result view.
func resultAction(t *testing.T, v plugin.View, id string) plugin.Action {
	t.Helper()
	for _, a := range v.Sections[0].Items[0].Actions {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("no action %q in %+v", id, v.Sections[0].Items[0].Actions)
	return plugin.Action{}
}

// TestResultView: an action in the result mode shows only the answer, as
// it is written; Enter replaces the selected text (or pastes the answer),
// Ctrl+C copies it, Tab turns the view into the chat to follow up, which is
// stored in the history like any chat.
func TestResultView(t *testing.T) {
	fake := &backend.Fake{Reply: func(string) []string { return []string{"Dear team, ", "the meeting moves to Thursday."} }}
	p, h := newTestPlugin(t, fake, true)
	setActions(t, h, actions.Stored{Custom: []actions.Action{
		{ID: "custom-1", Title: "Formal Email", Prompt: "Rewrite as a formal email: {selection}", Result: actions.ModeResult},
	}})
	sel := textSel("meeting thu")
	sel.Editable = true
	it := byID(t, selectionItems(t, p, sel, ""), "custom-1")

	// While the answer is written: the text so far, Stop first.
	p.mu.Lock()
	ch := &chat{sess: Session{ID: "w", Title: "Formal Email: meeting thu"}, msgs: []Message{{Role: RoleUser, Text: "q"}}, busy: true, result: true}
	ch.partial.WriteString("Dear team, ")
	v := p.render(ch, "w1")
	p.mu.Unlock()
	if v.Detail.Markdown != "Dear team, ▍" || v.Sections[0].Items[0].Actions[0].ID != "stop" || strings.Contains(v.Detail.Markdown, "**") {
		t.Fatalf("streaming %q, actions %+v", v.Detail.Markdown, v.Sections[0].Items[0].Actions)
	}
	ch.partial.Reset()
	p.mu.Lock()
	v = p.render(ch, "w1")
	p.mu.Unlock()
	if v.Detail.Markdown != "_Thinking…_" {
		t.Errorf("before the first piece: %q", v.Detail.Markdown)
	}

	v = openFrom(t, p, it, "v1", nil)
	answer := "Dear team, the meeting moves to Thursday."
	if v.Detail.Markdown != answer || v.Title != "Formal Email: meeting thu" || v.Escape != nil || !v.KeepOnHide {
		t.Fatalf("result view %+v", v)
	}
	if s := fake.Sends(); len(s) != 1 || s[0] != "Rewrite as a formal email: meeting thu" {
		t.Errorf("sent %q", s)
	}
	enter := v.Sections[0].Items[0].Actions[0]
	if enter.ID != "paste" || enter.Title != "Replace Selected Text" || enter.Do.Kind != plugin.ActionPaste || enter.Do.Target != answer {
		t.Errorf("Enter = %+v", enter)
	}
	if c := resultAction(t, v, "copy"); c.Shortcut != "Ctrl+C" || c.Do.Kind != plugin.ActionCopyText || c.Do.Target != answer {
		t.Errorf("copy = %+v", c)
	}
	tab := resultAction(t, v, "tochat")
	if tab.Shortcut != "Tab" || tab.Do.Kind != plugin.ActionCallback {
		t.Errorf("tab = %+v", tab)
	}
	// Saved in the history like any chat.
	if list, _ := p.st.Sessions(); len(list) != 1 || list[0].Title != "Formal Email: meeting thu" {
		t.Errorf("history %+v", list)
	}

	// Tab: the chat, with a follow-up typed meanwhile.
	do := tab.Do
	do.Query = "and shorter"
	r, err := p.HandleAction(t.Context(), do)
	if err != nil || !r.Refresh {
		t.Fatalf("tab = %+v, %v", r, err)
	}
	v = openChat(t, p, "v1", chatPayload{})
	if v.Sections[0].Items[0].ID != "chat" || !strings.Contains(v.Detail.Markdown, "**You**") || len(fake.Sends()) != 2 || fake.Sends()[1] != "and shorter" {
		t.Fatalf("after Tab: %q, sent %q", v.Detail.Markdown, fake.Sends())
	}

	// Not editable: Enter pastes; reopened from the history: an ordinary chat.
	sel.Editable = false
	v = openFrom(t, p, byID(t, selectionItems(t, p, sel, ""), "custom-1"), "v2", nil)
	if a := v.Sections[0].Items[0].Actions[0]; a.ID != "paste" || a.Title != "Paste Answer" {
		t.Errorf("not editable: %+v", a)
	}

	// A failed answer: the error, and Tab to retry in the chat.
	fake2 := &backend.Fake{SendErr: errors.New("boom")}
	p, h = newTestPlugin(t, fake2, true)
	setActions(t, h, actions.Stored{Custom: []actions.Action{{ID: "custom-1", Title: "F", Prompt: "p", Result: actions.ModeResult}}})
	v = openFrom(t, p, byID(t, selectionItems(t, p, textSel("x"), ""), "custom-1"), "v1", nil)
	if a := v.Sections[0].Items[0].Actions; a[0].ID != "tochat" || !strings.Contains(v.Detail.Markdown, "**") {
		t.Errorf("failed: %q %+v", v.Detail.Markdown, a)
	}
}
