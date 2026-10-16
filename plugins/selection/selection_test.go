package selection

import (
	"encoding/json/v2"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

func newPlugin(t *testing.T) (*Plugin, *hostfake.Host) {
	t.Helper()
	p := New()
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p, h
}

func sel(text string) plugin.Selection {
	return plugin.Selection{Text: text, App: plugin.AppInfo{Name: "Notepad.exe"}, Source: plugin.SelectionUIA}
}

func preview(t *testing.T, p *Plugin, q plugin.SelectionQuery) plugin.Item {
	t.Helper()
	items, err := p.SelectionItems(t.Context(), q)
	if err != nil || len(items) != 1 {
		t.Fatalf("SelectionItems = %+v, %v", items, err)
	}
	return items[0]
}

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.ID != "builtin.selection" || !slices.Equal(m.Permissions, []plugin.Permission{plugin.PermSelection}) || len(m.Commands) != 0 {
		t.Errorf("manifest = %+v", m)
	}
}

func TestPreviewTitle(t *testing.T) {
	long := strings.Repeat("abcdefghij", 7)
	tests := []struct{ in, want string }{
		{"hello world", "hello world"},
		{"\n\n   \n  first\t\tline  here \nsecond", "first line here"},
		{long, long[:60] + "…"},
		{strings.Repeat("中", 61), strings.Repeat("中", 60) + "…"},
		{strings.Repeat("中", 60), strings.Repeat("中", 60)},
		{strings.Repeat("a", 59) + " b", strings.Repeat("a", 59) + "…"},
	}
	for _, tt := range tests {
		if got := previewTitle(tt.in); got != tt.want {
			t.Errorf("previewTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCountWords(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"Hello, world!", 2},
		{"Let's move the meeting to Thursday.", 6},
		{"你好，世界", 4},
		{"我用 Go 写了 3 个程序", 9},
		{"日本語のテキスト", 8},
		{"한국어 문장", 5},
		{"party 🎉🎉 time", 2},
		{"🎉", 0},
		{"3.14 and 42", 4},
		{"version-2 build_7", 4},
		{"café naïve", 2},
	}
	for _, tt := range tests {
		if got := countWords(tt.in); got != tt.want {
			t.Errorf("countWords(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestCountLinesAndCodeBlock(t *testing.T) {
	for in, want := range map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\r\nb\r\n\r\nc": 4} {
		if got := countLines(in); got != want {
			t.Errorf("countLines(%q) = %d, want %d", in, got, want)
		}
	}
	if got := codeBlock("x"); got != "```\nx\n```" {
		t.Errorf("codeBlock = %q", got)
	}
	if got := codeBlock("a ```` b ``\n"); got != "`````\na ```` b ``\n`````" {
		t.Errorf("codeBlock with backticks = %q", got)
	}
}

func TestSelectionItems(t *testing.T) {
	p, h := newPlugin(t)
	it := preview(t, p, plugin.SelectionQuery{Selection: sel("Let's move the meeting\nto Thursday afternoon.")})
	if it.ID != "preview" || it.Title != "Let's move the meeting" || it.Subtitle != "Notepad · 7 words" ||
		!it.SubtitleSensitive || !it.Sensitive || it.Score != 1 {
		t.Errorf("preview = %+v", it)
	}
	for _, kw := range []string{"selected", "selection", "word count"} {
		if !slices.Contains(it.Keywords, kw) {
			t.Errorf("keywords %q lack %q", it.Keywords, kw)
		}
	}
	if len(it.Actions) != 1 || it.Actions[0].Do.Kind != plugin.ActionPush || it.Actions[0].Do.Target != viewText {
		t.Fatalf("actions = %+v", it.Actions)
	}

	// With text typed, no score: the host matches the keywords.
	it = preview(t, p, plugin.SelectionQuery{Selection: sel("one"), Text: "word"})
	if it.Score != 0 || it.ID != "preview" || it.Subtitle != "Notepad · 1 word" {
		t.Errorf("typed: %+v", it)
	}

	// Truncated, no app.
	s := sel("x y")
	s.Truncated = true
	s.App = plugin.AppInfo{}
	if it := preview(t, p, plugin.SelectionQuery{Selection: s}); it.Subtitle != "2 words · Truncated" {
		t.Errorf("truncated subtitle = %q", it.Subtitle)
	}

	// Nothing for blank text.
	if items, _ := p.SelectionItems(t.Context(), plugin.SelectionQuery{Selection: sel(" \n ")}); items != nil {
		t.Errorf("blank selection: %+v", items)
	}

	// In Chinese.
	h.I18N.SetLocale("zh-CN")
	s = sel("你好，世界")
	s.Truncated = true
	it = preview(t, p, plugin.SelectionQuery{Selection: s})
	if it.Subtitle != "Notepad · 4 字 · 已截断" {
		t.Errorf("zh subtitle = %q", it.Subtitle)
	}
	for _, kw := range []string{"字数", "选中"} {
		if !slices.Contains(it.Keywords, kw) {
			t.Errorf("zh keywords %q lack %q", it.Keywords, kw)
		}
	}
}

func TestTextView(t *testing.T) {
	p, _ := newPlugin(t)
	s := plugin.Selection{Text: "Hello ```code``` world\nsecond line\n", App: plugin.AppInfo{Name: "Weixin.exe"},
		Source: plugin.SelectionClipboard, Truncated: true, Editable: true}
	it := preview(t, p, plugin.SelectionQuery{Selection: s})
	do := it.Actions[0].Do
	// The selection is gone when the view opens: the payload has it all.
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: do.Target, Payload: do.Payload})
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != plugin.ViewList || !v.ShowDetail || v.Title != "Selected Text" || len(v.Sections) != 1 || len(v.Sections[0].Items) != 1 {
		t.Fatalf("view = %+v", v)
	}
	vi := v.Sections[0].Items[0]
	if !vi.Sensitive || vi.Detail == nil {
		t.Fatalf("item = %+v", vi)
	}
	if want := "````\nHello ```code``` world\nsecond line\n````"; vi.Detail.Markdown != want {
		t.Errorf("markdown = %q, want %q", vi.Detail.Markdown, want)
	}
	got := map[string]string{}
	for _, m := range vi.Detail.Metadata {
		got[m.Label] = m.Value
	}
	want := map[string]string{"App": "Weixin", "Words": "5", "Characters": "35", "Lines": "2", "Method": "Copied", "Truncated": "Yes", "Editable": "Yes"}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("metadata %s = %q, want %q (all: %v)", k, got[k], w, got)
		}
	}
	if _, err := p.View(t.Context(), plugin.ViewRequest{ID: "other"}); err == nil {
		t.Error("unknown view accepted")
	}
}

// TestLocales checks that both catalogs have the same keys.
func TestLocales(t *testing.T) {
	keys := func(name string) []string {
		data, err := fs.ReadFile(New().Locales(), name)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		var out []string
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	if en, zh := keys("en.json"), keys("zh-CN.json"); !slices.Equal(en, zh) {
		t.Errorf("en.json keys %q, zh-CN.json keys %q", en, zh)
	}
}
