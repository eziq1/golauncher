package quicklinks

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Engine is a web search engine: built-in or defined by the user.
type Engine struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Keywords []string `json:"keywords"`
	// URL is the search URL with {query} where the search text goes.
	URL string `json:"url"`
	// Suggest is an OpenSearch suggestions URL with {query} (optional).
	Suggest  string `json:"suggest,omitempty"`
	Disabled bool   `json:"disabled,omitzero"`
	// Custom is set for engines defined by the user (not stored).
	Custom bool `json:"-"`
}

// builtinEngine is the definition of a built-in engine.
type builtinEngine struct {
	id string
	// name is the message key of the name, as "%key%".
	name     string
	keywords []string
	url      string
	// urlZh replaces url in the Simplified Chinese UI.
	urlZh   string
	suggest string
}

// OpenSearch suggestion endpoints (docs/quicklinks.md §7).
const (
	suggestGoogle = "https://suggestqueries.google.com/complete/search?client=firefox&q={query}"
)

// builtins are the built-in engines (docs/quicklinks.md §2.1), in the order
// they are listed.
var builtins = []builtinEngine{
	{id: "google", name: "%engine.google%", keywords: []string{"g", "谷歌"}, url: "https://www.google.com/search?q={query}", suggest: suggestGoogle},
	{id: "bing", name: "%engine.bing%", keywords: []string{"b", "必应"}, url: "https://www.bing.com/search?q={query}",
		suggest: "https://api.bing.com/osjson.aspx?query={query}"},
	{id: "baidu", name: "%engine.baidu%", keywords: []string{"bd", "百度"}, url: "https://www.baidu.com/s?ie=utf-8&wd={query}",
		suggest: "https://suggestion.baidu.com/su?action=opensearch&ie=utf-8&wd={query}"},
	{id: "duckduckgo", name: "%engine.duckduckgo%", keywords: []string{"ddg"}, url: "https://duckduckgo.com/?q={query}",
		suggest: "https://duckduckgo.com/ac/?type=list&q={query}"},
	{id: "github", name: "%engine.github%", keywords: []string{"gh"}, url: "https://github.com/search?q={query}"},
	{id: "stackoverflow", name: "%engine.stackoverflow%", keywords: []string{"so"}, url: "https://stackoverflow.com/search?q={query}"},
	{id: "youtube", name: "%engine.youtube%", keywords: []string{"yt"}, url: "https://www.youtube.com/results?search_query={query}",
		suggest: "https://suggestqueries.google.com/complete/search?client=firefox&ds=yt&q={query}"},
	{id: "bilibili", name: "%engine.bilibili%", keywords: []string{"bili", "b站"}, url: "https://search.bilibili.com/all?keyword={query}"},
	{id: "wikipedia", name: "%engine.wikipedia%", keywords: []string{"wiki", "维基"}, url: "https://en.wikipedia.org/wiki/Special:Search?search={query}",
		urlZh:   "https://zh.wikipedia.org/wiki/Special:Search?search={query}",
		suggest: "https://en.wikipedia.org/w/api.php?action=opensearch&format=json&search={query}"},
	{id: "zhihu", name: "%engine.zhihu%", keywords: []string{"zh", "zhihu", "知乎"}, url: "https://www.zhihu.com/search?type=content&q={query}"},
	{id: "douban", name: "%engine.douban%", keywords: []string{"db", "douban", "豆瓣"}, url: "https://www.douban.com/search?q={query}"},
	{id: "taobao", name: "%engine.taobao%", keywords: []string{"tb", "淘宝"}, url: "https://s.taobao.com/search?q={query}"},
	{id: "jd", name: "%engine.jd%", keywords: []string{"jd", "京东"}, url: "https://search.jd.com/Search?enc=utf-8&keyword={query}"},
	{id: "npm", name: "%engine.npm%", keywords: []string{"npm"}, url: "https://www.npmjs.com/search?q={query}"},
	{id: "gopkg", name: "%engine.gopkg%", keywords: []string{"go", "gopkg"}, url: "https://pkg.go.dev/search?q={query}"},
	{id: "mdn", name: "%engine.mdn%", keywords: []string{"mdn"}, url: "https://developer.mozilla.org/search?q={query}",
		urlZh: "https://developer.mozilla.org/zh-CN/search?q={query}"},
	{id: "translate", name: "%engine.translate%", keywords: []string{"gt", "translate"}, url: "https://translate.google.com/?sl=auto&tl=en&text={query}&op=translate",
		urlZh: "https://translate.google.com/?sl=auto&tl=zh-CN&text={query}&op=translate"},
	{id: "youdao", name: "%engine.youdao%", keywords: []string{"yd", "有道"}, url: "https://www.youdao.com/result?word={query}&lang=en"},
	{id: "maps", name: "%engine.maps%", keywords: []string{"map", "maps"}, url: "https://www.google.com/maps/search/{query}"},
	{id: "amap", name: "%engine.amap%", keywords: []string{"amap", "gd", "高德", "地图"}, url: "https://www.amap.com/search?query={query}"},
}

// builtinByID returns the definition of a built-in engine.
func builtinByID(id string) (builtinEngine, bool) {
	i := slices.IndexFunc(builtins, func(b builtinEngine) bool { return b.id == id })
	if i < 0 {
		return builtinEngine{}, false
	}
	return builtins[i], true
}

// isChinese reports whether a locale tag is Chinese.
func isChinese(locale string) bool {
	return locale == "zh" || strings.HasPrefix(strings.ToLower(locale), "zh-")
}

// autoEngine is the default engine when the user chose none: Bing in the
// Chinese UI (Google is usually unreachable in mainland China), Google
// otherwise (docs/quicklinks.md §3).
func autoEngine(locale string) string {
	if isChinese(locale) {
		return "bing"
	}
	return "google"
}

// normKeyword cleans a keyword for comparison.
func normKeyword(k string) string { return strings.ToLower(strings.TrimSpace(k)) }

// parseKeywords splits user input ("g, gg 谷歌") into keywords: separated
// by commas (also Chinese ones) or spaces, without duplicates.
func parseKeywords(s string) []string {
	var out []string
	for f := range strings.FieldsFuncSeq(s, func(r rune) bool { return r == ',' || r == '，' || r == '、' || unicode.IsSpace(r) }) {
		if k := normKeyword(f); k != "" && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// cutKeyword reports whether text starts with keyword kw and returns the
// rest. A keyword must be followed by a space or the end ("gh x" is not
// "g"), Chinese ones too: "百度网盘" and "有道云笔记" are app names, not
// searches (docs/quicklinks.md §2.2).
func cutKeyword(text, kw string) (string, bool) {
	kw = normKeyword(kw)
	if kw == "" || len(kw) > len(text) || !strings.EqualFold(text[:len(kw)], kw) {
		return "", false
	}
	rest := text[len(kw):]
	if r, _ := utf8.DecodeRuneInString(rest); rest != "" && !unicode.IsSpace(r) {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// match is a keyword found at the start of the search text.
type match struct {
	kw     string
	rest   string
	engine *Engine    // the engine whose keyword it is, or
	link   *Quicklink // the quicklink
}

// findKeyword returns the longest keyword that starts text among the
// quicklinks' and the enabled engines' keywords. On a tie quicklinks win,
// then custom engines, then built-in ones (docs/quicklinks.md §2.2).
func findKeyword(text string, links []Quicklink, engines []Engine) (match, bool) {
	var best match
	found := false
	consider := func(kw string, m match) {
		rest, ok := cutKeyword(text, kw)
		if !ok || (found && len(normKeyword(kw)) <= len(best.kw)) {
			return
		}
		m.kw, m.rest = normKeyword(kw), rest
		best, found = m, true
	}
	for i := range links {
		if links[i].Keyword != "" {
			consider(links[i].Keyword, match{link: &links[i]})
		}
	}
	for _, custom := range []bool{true, false} {
		for i := range engines {
			e := &engines[i]
			if e.Disabled || e.Custom != custom {
				continue
			}
			for _, kw := range e.Keywords {
				consider(kw, match{engine: e})
			}
		}
	}
	return best, found
}

// reservedPrefixes are the default prefixes of the other built-in plugins;
// the built-in engines' keywords must not take them (tested).
var reservedPrefixes = []string{
	"time", "tz", "timer", "计时", "倒计时", "remind", "提醒",
	"winget", "wg", "scoop", "sc", "ip", "ping", "dns", "port", "wifi", "tracert",
	"ps", "kill", "进程",
	"tr", "fy", "翻译", // builtin.translator (docs/translator.md §6)
}

// localName is the engine's name in the current language.
func (b builtinEngine) localName(l plugin.Localizer) string { return l.T(strings.Trim(b.name, "%")) }
