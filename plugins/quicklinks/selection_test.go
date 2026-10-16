package quicklinks

import (
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func (e *env) selection(text, typed string) []plugin.Item {
	e.t.Helper()
	items, err := e.p.SelectionItems(e.t.Context(), plugin.SelectionQuery{
		Selection: plugin.Selection{Text: text, App: plugin.AppInfo{Name: "msedge.exe"}, Source: plugin.SelectionUIA},
		Text:      typed,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return items
}

func itemIDs(items []plugin.Item) []string {
	var ids []string
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

func findItem(items []plugin.Item, id string) *plugin.Item {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func TestSelectionOpenLink(t *testing.T) {
	e := newEnv(t, Options{})
	items := e.selection("  github.com/golang/go \n", "")
	if got := itemIDs(items); !slices.Equal(got, []string{"open-link", "search", "translate"}) {
		t.Fatalf("ids = %q", got)
	}
	it := items[0]
	if it.Title != "Open Link" || it.Subtitle != "https://github.com/golang/go" || it.Score != 1 || !it.SubtitleSensitive ||
		it.Actions[0].Do.Kind != plugin.ActionOpen || it.Actions[0].Do.Target != "https://github.com/golang/go" ||
		it.Actions[1].Do.Kind != plugin.ActionCopyText {
		t.Errorf("open link = %+v", it)
	}
	// With text typed: no score, matched by the host.
	if it := e.selection("https://example.com/a?b=c", "open")[0]; it.ID != "open-link" || it.Score != 0 {
		t.Errorf("typed = %+v", it)
	}
	// Not a single URL.
	for _, s := range []string{"see github.com/golang/go for details", "github.com/a github.com/b", "hello"} {
		if findItem(e.selection(s, ""), "open-link") != nil {
			t.Errorf("%q offered as a link", s)
		}
	}
}

func TestSelectionSearch(t *testing.T) {
	e := newEnv(t, Options{})
	items := e.selection("golang\r\ngenerics\ttutorial", "")
	it := findItem(items, "search")
	if it == nil {
		t.Fatalf("no search in %q", itemIDs(items))
	}
	if it.Title != "Search Google" || it.Subtitle != "golang generics tutorial" || !it.SubtitleSensitive || it.Score != 0 {
		t.Errorf("search = %+v", it)
	}
	if got := it.Actions[0].Do.Target; got != "https://www.google.com/search?q=golang%20generics%20tutorial" {
		t.Errorf("search URL = %q", got)
	}
	if !slices.Contains(it.Keywords, "search") || !slices.Contains(it.Keywords, "Google") {
		t.Errorf("keywords = %q", it.Keywords)
	}
	// A second engine is the secondary action.
	e.h.Prefs.Set(prefSecond, plugin.MustPayload("baidu"))
	it = findItem(e.selection("golang", ""), "search")
	if len(it.Actions) < 2 || it.Actions[1].Title != "Search Baidu" || !strings.HasPrefix(it.Actions[1].Do.Target, "https://www.baidu.com/s?") {
		t.Errorf("second engine = %+v", it.Actions)
	}
	// Long text: no search; the subtitle is cut.
	if findItem(e.selection(strings.Repeat("a", maxSearchRunes+1), ""), "search") != nil {
		t.Error("search offered for long text")
	}
	if it := findItem(e.selection(strings.Repeat("字", maxSearchRunes), ""), "search"); it == nil || it.Subtitle != strings.Repeat("字", subtitleRunes)+"…" {
		t.Errorf("long subtitle = %+v", it)
	}
}

func TestSelectionTranslate(t *testing.T) {
	tests := []struct {
		locale, text, tl, title string
	}{
		{"en", "Bonjour tout le monde", "zh-CN", "Translate to Chinese on the Web"},
		{"en", "你好，世界", "en", "Translate to English on the Web"},
		{"en", "Привет", "en", "Translate to English on the Web"},
		{"zh-CN", "Hello world", "zh-CN", "网页翻译成中文"},
		{"zh-CN", "这是一个 test，你好", "en", "网页翻译成英文"},
		{"zh-CN", "こんにちは、世界", "zh-CN", "网页翻译成中文"},
	}
	for _, tt := range tests {
		e := newEnv(t, Options{})
		e.h.I18N.SetLocale(tt.locale)
		it := findItem(e.selection(tt.text, ""), "translate")
		if it == nil {
			t.Fatalf("%s %q: no translate item", tt.locale, tt.text)
		}
		u := it.Actions[0].Do.Target
		if it.Title != tt.title || !strings.HasPrefix(u, "https://translate.google.com/?sl=auto&tl="+tt.tl+"&text=") || it.Score != 0 {
			t.Errorf("%s %q: title %q url %q", tt.locale, tt.text, it.Title, u)
		}
		if a := it.Actions[1]; !strings.HasPrefix(a.Do.Target, "https://www.youdao.com/result?word=") {
			t.Errorf("youdao action = %+v", a)
		}
	}
	e := newEnv(t, Options{})
	if findItem(e.selection(strings.Repeat("a ", maxTranslateRunes), ""), "translate") != nil {
		t.Error("translate offered for long text")
	}
	if items := e.selection(" \n ", ""); items != nil {
		t.Errorf("blank selection: %q", itemIDs(items))
	}
}
