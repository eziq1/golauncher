package quicklinks

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Limits of the selection actions (docs/selected-text.md §6): longer text
// makes no useful search, and a longer translation does not fit in a URL.
const (
	maxSearchRunes    = 300
	maxTranslateRunes = 2000
	// subtitleRunes is how much of the text a subtitle shows.
	subtitleRunes = 60
)

// Translation URLs; {query} is the text and {tl} the target language.
const (
	googleTranslateURL = "https://translate.google.com/?sl=auto&tl={tl}&text={query}&op=translate"
	youdaoURL          = "https://www.youdao.com/result?word={query}&lang=en"
)

var _ plugin.SelectionProvider = (*Plugin)(nil)

// SelectionItems implements plugin.SelectionProvider (docs/selected-text.md
// §6): "Open Link" when the selection is one URL, a web search with the
// default engine (and the second one as another action), and a web
// translation.
func (p *Plugin) SelectionItems(ctx context.Context, q plugin.SelectionQuery) ([]plugin.Item, error) {
	text := strings.TrimSpace(q.Selection.Text)
	if text == "" {
		return nil, nil
	}
	b := p.newBuilder()
	l := b.l
	var items []plugin.Item
	if u, ok := DetectURL(text); ok {
		it := plugin.Item{
			ID:                "open-link",
			Title:             l.T("selection.openLink"),
			Subtitle:          u,
			SubtitleSensitive: true,
			Icon:              plugin.Icon{Name: "globe"},
			Keywords:          splitList(l.T("selection.openLink.keywords")),
			Transient:         true,
			Actions:           b.urlActions(l.T("action.openURL"), u),
		}
		if q.Text == "" {
			it.Score = 1
		}
		items = append(items, it)
	}
	oneLine := strings.Join(strings.Fields(text), " ")
	if n := utf8.RuneCountInString(oneLine); n <= maxSearchRunes {
		if engines := p.defaultEngines(); len(engines) > 0 {
			items = append(items, b.selectionSearchItem(engines, oneLine))
		}
	}
	if utf8.RuneCountInString(text) <= maxTranslateRunes {
		items = append(items, b.translateItem(text))
	}
	return items, ctx.Err()
}

// selectionSearchItem searches the first engine for the text, the second one
// (if any) as the secondary action.
func (b *builder) selectionSearchItem(engines []Engine, text string) plugin.Item {
	l := b.l
	first := engines[0]
	acts := []plugin.Action{{ID: "search", Title: l.T("action.search"), Do: openAction(searchURL(first, text), "")}}
	keywords := splitList(l.T("selection.search.keywords"))
	keywords = append(keywords, first.Name)
	for _, e := range engines[1:] {
		acts = append(acts, plugin.Action{ID: "search:" + e.ID, Title: l.T("selection.searchWith", "engine", e.Name),
			Do: openAction(searchURL(e, text), "")})
		keywords = append(keywords, e.Name)
	}
	acts = append(acts, plugin.Action{ID: "copy", Title: l.T("action.copyURL"), Shortcut: shortcutCopy,
		Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: searchURL(first, text)}})
	return plugin.Item{
		ID:                "search",
		Title:             l.T("engine.item", "engine", first.Name),
		Subtitle:          cutRunes(text, subtitleRunes),
		SubtitleSensitive: true,
		Icon:              plugin.Icon{Name: "search"},
		Keywords:          keywords,
		Transient:         true,
		Actions:           acts,
	}
}

// translateItem translates the text with Google Translate, into the UI
// language or, for text already in it, into the other of English and
// Simplified Chinese; Youdao is another action.
func (b *builder) translateItem(text string) plugin.Item {
	l := b.l
	tl, lang := "en", l.T("selection.lang.en")
	if translateToChinese(l.Locale(), text) {
		tl, lang = "zh-CN", l.T("selection.lang.zh")
	}
	google := searchURL(Engine{URL: strings.ReplaceAll(googleTranslateURL, "{tl}", tl)}, text)
	youdao := searchURL(Engine{URL: youdaoURL}, text)
	return plugin.Item{
		ID:        "translate",
		Title:     l.T("selection.translate", "lang", lang),
		Subtitle:  l.T("engine.translate"),
		Icon:      plugin.Icon{Name: "globe"},
		Keywords:  splitList(l.T("selection.translate.keywords")),
		Transient: true,
		Actions: []plugin.Action{
			{ID: "google", Title: l.T("selection.translateWith", "engine", l.T("engine.translate")), Do: openAction(google, "")},
			{ID: "youdao", Title: l.T("selection.translateWith", "engine", l.T("engine.youdao")), Do: openAction(youdao, "")},
		},
	}
}

// translateToChinese reports whether a web translation of text goes into
// Simplified Chinese: in the Chinese UI unless the text is mainly Chinese
// already, in other UIs when the text is mainly English (Latin script).
func translateToChinese(locale, text string) bool {
	script := mainScript(text)
	if isChinese(locale) {
		return script != scriptHan
	}
	return script == scriptLatin
}

// Scripts told apart by mainScript.
const (
	scriptNone  = ""
	scriptHan   = "han"
	scriptKana  = "kana" // Japanese: Kana, usually with Han
	scriptLatin = "latin"
	scriptOther = "other"
)

// mainScript roughly tells which script text is mainly written in, by its
// letters: a Han, Kana or Hangul character counts like a short word (three
// Latin letters).
func mainScript(text string) string {
	var han, kana, latin, other int
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		case unicode.Is(unicode.Latin, r):
			latin++
		case unicode.IsLetter(r):
			other++
		}
	}
	cjk, cjkScript := han*3, scriptHan
	if kana > 0 {
		cjk, cjkScript = (han+kana)*3, scriptKana
	}
	best, n := scriptNone, 0
	for _, s := range []struct {
		script string
		n      int
	}{{cjkScript, cjk}, {scriptLatin, latin}, {scriptOther, other}} {
		if s.n > n {
			best, n = s.script, s.n
		}
	}
	return best
}

// cutRunes cuts s to n characters, with "…" when it was longer.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimRightFunc(string([]rune(s)[:n]), unicode.IsSpace) + "…"
}

// splitList splits a comma-separated list of keywords.
func splitList(s string) []string {
	var out []string
	for k := range strings.SplitSeq(s, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}
