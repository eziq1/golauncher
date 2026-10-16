package ai

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

func selectionItems(t *testing.T, p *Plugin, sel plugin.Selection, typed string) []plugin.Item {
	t.Helper()
	items, err := p.SelectionItems(t.Context(), plugin.SelectionQuery{Selection: sel, Text: typed})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func textSel(text string) plugin.Selection {
	return plugin.Selection{Text: text, App: plugin.AppInfo{Name: "notepad.exe"}, Source: plugin.SelectionUIA}
}

func ids(items []plugin.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func byID(t *testing.T, items []plugin.Item, id string) plugin.Item {
	t.Helper()
	for _, it := range items {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %q in %q", id, ids(items))
	return plugin.Item{}
}

// openFrom runs an item's primary action as the host does: ActionPush
// opens the chat view with the argument values.
func openFrom(t *testing.T, p *Plugin, it plugin.Item, instance string, args map[string]string) plugin.View {
	t.Helper()
	do := it.Actions[0].Do
	if do.Kind != plugin.ActionPush || do.Target != viewChat {
		t.Fatalf("primary action of %s = %+v", it.ID, do)
	}
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: do.Target, Instance: instance, Payload: do.Payload, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSelectionAIOff(t *testing.T) {
	fake := &backend.Fake{}
	p, h := newTestPlugin(t, fake, false)
	items := selectionItems(t, p, textSel("Hello world"), "")
	if len(items) != 1 || items[0].ID != "enable" || items[0].Title != "Use AI to translate and summarize the selected text" || items[0].Score != 0 {
		t.Fatalf("items = %+v", items)
	}
	if !slices.Contains(items[0].Keywords, "translate") {
		t.Errorf("keywords = %q", items[0].Keywords)
	}
	v := openFrom(t, p, items[0], "v1", nil)
	enable := v.Sections[0].Items[0].Actions[0]
	if enable.ID != "enable" || !strings.Contains(v.Detail.Markdown, "back at the search") || strings.Contains(v.Detail.Markdown, "Hello world") {
		t.Fatalf("off view = %+v", v)
	}
	r, err := p.HandleAction(t.Context(), enable.Do)
	if err != nil || string(r.Preferences[prefEnabled]) != "true" || !r.PopToRoot || r.Refresh {
		t.Fatalf("enable = %+v, %v", r, err)
	}
	if len(fake.Sends()) != 0 {
		t.Fatal("sent while off")
	}
	h.Prefs.SetValues(r.Preferences)
	// The chat opened from the prompt sends nothing once AI is on.
	openChat(t, p, "v1", chatPayload{})
	if len(fake.Sends()) != 0 {
		t.Fatalf("sends = %q", fake.Sends())
	}
	// Turning AI on from an ordinary chat still refreshes it.
	r, _ = p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: "enable"})
	if r.PopToRoot || !r.Refresh {
		t.Errorf("ordinary enable = %+v", r)
	}
}

func TestSelectionRows(t *testing.T) {
	p, _ := newTestPlugin(t, &backend.Fake{}, true)
	short := textSel("Let's move the meeting to Thursday.")
	items := selectionItems(t, p, short, "")
	if got := ids(items); !slices.Equal(got, []string{"translate", "ask", "explain", "rewrite", "reply"}) {
		t.Fatalf("short text rows = %q", got)
	}
	for _, it := range items {
		if it.Score != 0 || it.Icon.Name != "sparkles" || it.Subtitle != p.be.Name() || len(it.Keywords) == 0 {
			t.Errorf("row %+v", it)
		}
	}
	if it := byID(t, items, "translate"); it.Title != "Translate to Simplified Chinese" && it.Title != "Translate to English" {
		t.Errorf("translate title %q", it.Title)
	}
	ask := byID(t, items, "ask")
	if ask.Title != "Ask AI…" || len(ask.Arguments) != 1 || ask.Arguments[0].Name != "instruction" || ask.Arguments[0].Required ||
		ask.Arguments[0].Placeholder != "What should AI do? (optional)" {
		t.Errorf("ask = %+v", ask)
	}
	for id, title := range map[string]string{"explain": "Explain", "rewrite": "Rewrite", "reply": "Suggest Replies"} {
		if it := byID(t, items, id); it.Title != title {
			t.Errorf("%s title = %q", id, it.Title)
		}
	}
	for id, kw := range map[string][]string{"translate": {"translate", "翻译"}, "explain": {"explain", "解释"}, "rewrite": {"rewrite", "改写"},
		"reply": {"reply", "回复"}, "ask": {"ask", "问"}} {
		for _, k := range kw {
			if !slices.Contains(byID(t, items, id).Keywords, k) {
				t.Errorf("%s keywords lack %q", id, k)
			}
		}
	}

	// Summarize from 200 characters; the instruction row with typed text.
	long := textSel(strings.Repeat("word ", 41))
	items = selectionItems(t, p, long, "translate into Japanese")
	if got := ids(items); !slices.Equal(got, []string{"instruction", "translate", "ask", "explain", "summarize", "rewrite", "reply"}) {
		t.Fatalf("long text rows = %q", got)
	}
	in := items[0]
	if in.Title != "AI handles the selection: translate into Japanese" || in.Score != instructionScore {
		t.Errorf("instruction row = %+v", in)
	}
	if !slices.Contains(byID(t, items, "summarize").Keywords, "总结") {
		t.Errorf("summarize keywords = %q", byID(t, items, "summarize").Keywords)
	}
	if items := selectionItems(t, p, textSel(strings.Repeat("x", 199)), ""); slices.Contains(ids(items), "summarize") {
		t.Error("summarize offered for 199 characters")
	}
	if items := selectionItems(t, p, textSel("  \n"), "x"); items != nil {
		t.Errorf("blank selection: %q", ids(items))
	}
}

func TestTranslateTarget(t *testing.T) {
	tests := []struct {
		locale, pref, text, want, title string
	}{
		{"en", "", "Bonjour", langSimplified, "Translate to Simplified Chinese"}, // Latin: taken as English
		{"en", "", "Hello, how are you?", langSimplified, "Translate to Simplified Chinese"},
		{"en", "", "你好，今天怎么样？", langEnglish, "Translate to English"},
		{"en", "", "こんにちは、元気ですか", langEnglish, "Translate to English"},
		{"zh-CN", "", "Hello, how are you?", langSimplified, "翻译成简体中文"},
		{"zh-CN", "", "你好，今天怎么样？", langEnglish, "翻译成英文"},
		{"zh-CN", "", "こんにちは、元気ですか", langSimplified, "翻译成简体中文"},
		{"en", langJapanese, "Hello", langJapanese, "Translate to Japanese"},
		{"en", langJapanese, "こんにちは、元気ですか", langEnglish, "Translate to English"},
		{"en", langKorean, "안녕하세요", langEnglish, "Translate to English"},
		{"en", langRussian, "Привет, как дела?", langEnglish, "Translate to English"},
		{"en", langFrench, "Hello there", langFrench, "Translate to French"},
		{"zh-CN", langEnglish, "Hello", langSimplified, "翻译成简体中文"},
		{"en", "xx", "你好", langEnglish, "Translate to English"}, // unknown: automatic
	}
	for _, tt := range tests {
		fake := &backend.Fake{}
		p, h := newTestPlugin(t, fake, true)
		h.I18N.SetLocale(tt.locale)
		h.Prefs.SetValues(map[string]jsontext.Value{prefEnabled: jsontext.Value("true"), prefTranslateTo: plugin.MustPayload(tt.pref)})
		if got := p.translateTarget(tt.text); got != tt.want {
			t.Errorf("%s/%s %q: target %s, want %s", tt.locale, tt.pref, tt.text, got, tt.want)
		}
		it := byID(t, selectionItems(t, p, textSel(tt.text), ""), "translate")
		if it.Title != tt.title {
			t.Errorf("%s/%s %q: title %q, want %q", tt.locale, tt.pref, tt.text, it.Title, tt.title)
		}
		openFrom(t, p, it, "v1", nil)
		want := "Translate the quoted text into " + englishNames[tt.want] + ". Output only the translation.\n\n> "
		if s := fake.Sends(); len(s) != 1 || !strings.HasPrefix(s[0], want) {
			t.Errorf("%s/%s %q: sent %q", tt.locale, tt.pref, tt.text, s)
		}
	}
}

func TestSelectionMessage(t *testing.T) {
	fake := &backend.Fake{}
	p, _ := newTestPlugin(t, fake, true)
	sel := textSel("First line\r\n\r\nThird line\n")
	sel.Truncated = true
	v := openFrom(t, p, byID(t, selectionItems(t, p, sel, ""), "explain"), "v1", nil)
	want := "Explain the quoted text concisely.\n\n> First line\n>\n> Third line\n\n_(The quoted text was truncated to 25 characters.)_"
	if s := fake.Sends(); len(s) != 1 || s[0] != want {
		t.Fatalf("sent %q, want %q", s, want)
	}
	if !strings.Contains(v.Detail.Markdown, want) || !strings.Contains(v.Detail.Markdown, "You asked: Explain the quoted text") {
		t.Errorf("transcript:\n%s", v.Detail.Markdown)
	}
	if v.Title != "Explain: First line Third line" {
		t.Errorf("title = %q", v.Title)
	}
	list, _ := p.st.Sessions()
	if len(list) != 1 || list[0].Title != "Explain: First line Third line" {
		t.Errorf("stored sessions = %+v", list)
	}

	// Every fixed prompt.
	long := textSel(strings.Repeat("Some words here. ", 15))
	for id, prompt := range map[string]string{"summarize": promptSummarize, "rewrite": promptRewrite, "reply": promptReply} {
		fake := &backend.Fake{}
		p, _ := newTestPlugin(t, fake, true)
		openFrom(t, p, byID(t, selectionItems(t, p, long, ""), id), "v1", nil)
		if s := fake.Sends(); len(s) != 1 || !strings.HasPrefix(s[0], prompt+"\n\n> Some words here.") || strings.Contains(s[0], "truncated") {
			t.Errorf("%s sent %q", id, s)
		}
	}

	// A long selection gives a short title.
	fake = &backend.Fake{}
	p, _ = newTestPlugin(t, fake, true)
	v = openFrom(t, p, byID(t, selectionItems(t, p, long, ""), "rewrite"), "v1", nil)
	if v.Title != "Rewrite: Some words here. Some words h…" {
		t.Errorf("long title = %q", v.Title)
	}
}

func TestSelectionAskAndInstruction(t *testing.T) {
	sel := textSel("ping 8.8.8.8")
	// Ask AI… without an instruction: the default prompt.
	fake := &backend.Fake{}
	p, _ := newTestPlugin(t, fake, true)
	v := openFrom(t, p, byID(t, selectionItems(t, p, sel, ""), "ask"), "v1", nil)
	if s := fake.Sends(); len(s) != 1 || s[0] != "Explain the quoted text concisely.\n\n> ping 8.8.8.8" {
		t.Errorf("ask without instruction sent %q", s)
	}
	if v.Title != "Ask AI: ping 8.8.8.8" {
		t.Errorf("ask title = %q", v.Title)
	}
	// With the argument.
	fake = &backend.Fake{}
	p, _ = newTestPlugin(t, fake, true)
	openFrom(t, p, byID(t, selectionItems(t, p, sel, ""), "ask"), "v1", map[string]string{"instruction": " Is this safe? "})
	if s := fake.Sends(); len(s) != 1 || s[0] != "Is this safe?\n\n> ping 8.8.8.8" {
		t.Errorf("ask with instruction sent %q", s)
	}
	// The typed text as the instruction.
	fake = &backend.Fake{}
	p, _ = newTestPlugin(t, fake, true)
	v = openFrom(t, p, byID(t, selectionItems(t, p, sel, "what does it do"), "instruction"), "v1", nil)
	if s := fake.Sends(); len(s) != 1 || s[0] != "what does it do\n\n> ping 8.8.8.8" {
		t.Errorf("instruction sent %q", s)
	}
	if v.Title != "what does it do: ping 8.8.8.8" {
		t.Errorf("instruction title = %q", v.Title)
	}
	// Follow-ups are ordinary messages.
	act(t, p, "send", "v1", "and then?")
	if s := fake.Sends(); len(s) != 2 || s[1] != "and then?" {
		t.Errorf("follow-up sent %q", s)
	}
}

func TestSelectionReplaceTitle(t *testing.T) {
	paste := func(v plugin.View) plugin.Action {
		for _, a := range v.Sections[0].Items[0].Actions {
			if a.ID == "paste" {
				return a
			}
		}
		t.Fatalf("no paste action in %+v", v.Sections[0].Items[0].Actions)
		return plugin.Action{}
	}
	sel := textSel("teh quick brown fox")
	sel.Editable = true
	p, h := newTestPlugin(t, &backend.Fake{}, true)
	v := openFrom(t, p, byID(t, selectionItems(t, p, sel, ""), "rewrite"), "v1", nil)
	if a := paste(v); a.Title != "Replace Selected Text" || a.Shortcut != "Ctrl+Shift+V" || a.Do.Kind != plugin.ActionPaste ||
		!strings.HasPrefix(a.Do.Target, "You asked: Rewrite") {
		t.Errorf("paste = %+v", a)
	}
	h.I18N.SetLocale("zh-CN")
	if a := paste(openChat(t, p, "v1", chatPayload{})); a.Title != "替换选中的文字" {
		t.Errorf("zh paste = %q", a.Title)
	}
	h.I18N.SetLocale("en")
	// Not editable: the usual title; reopened from the history too.
	sel.Editable = false
	v = openFrom(t, p, byID(t, selectionItems(t, p, sel, ""), "rewrite"), "v2", nil)
	if a := paste(v); a.Title != "Paste Answer" {
		t.Errorf("not editable paste = %q", a.Title)
	}
	if a := paste(openChat(t, p, "v3", chatPayload{Session: "id-1"})); a.Title != "Replace Selected Text" {
		// Still in memory: the same chat.
		t.Errorf("same chat in another view = %q", a.Title)
	}
}

// TestTabUnchanged: Tab in the root search still sends only the typed text.
func TestTabUnchanged(t *testing.T) {
	p, _ := newTestPlugin(t, &backend.Fake{}, true)
	a, ok := p.TabAction(plugin.Query{Text: "hello"})
	var pl chatPayload
	if err := json.Unmarshal(a.Do.Payload, &pl); err != nil || !ok {
		t.Fatal(err)
	}
	if pl != (chatPayload{Text: "hello"}) {
		t.Errorf("tab payload = %+v", pl)
	}
}

func TestQuoteMessage(t *testing.T) {
	if got := quoteMessage("Do it.", "a\n\n  \nb", false); got != "Do it.\n\n> a\n>\n>\n> b" {
		t.Errorf("quoteMessage = %q", got)
	}
	if got := quoteMessage("Do it.", "中文", true); got != "Do it.\n\n> 中文\n\n_(The quoted text was truncated to 2 characters.)_" {
		t.Errorf("truncated = %q", got)
	}
}

func TestTranslatePreference(t *testing.T) {
	_, h := newTestPlugin(t, &backend.Fake{}, true)
	m := New(&backend.Fake{}).Manifest()
	if !slices.Contains(m.Permissions, plugin.PermSelection) {
		t.Errorf("permissions %q", m.Permissions)
	}
	l := h.I18N.Localizer(m.ID)
	i := slices.IndexFunc(m.Preferences, func(pr plugin.Preference) bool { return pr.Name == prefTranslateTo })
	if i < 0 {
		t.Fatal("no translateTo preference")
	}
	pr := m.Preferences[i]
	if pr.Type != plugin.PrefSelect || string(pr.Default) != `""` || len(pr.Options) != 10 || l.Resolve(pr.Title) != "Translate to" {
		t.Errorf("preference = %+v", pr)
	}
	for _, o := range pr.Options {
		if got := l.Resolve(o.Title); got == o.Title || got == "" {
			t.Errorf("option %q not translated", o.Title)
		}
		if _, ok := englishNames[o.Value]; !ok && o.Value != "" {
			t.Errorf("option %q has no English name", o.Value)
		}
	}
}
