package translator

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/eziq1/golauncher/plugins/translator/engines"
)

// Languages and the query syntax (docs/translator.md §2).

// language is a language the syntax names.
type language struct {
	// code is the canonical code (engines package).
	code string
	// english is its English name, for prompts (not shown).
	english string
	// codes are accepted as the first word ("ja 你好"); strict codes are
	// English words ("it", "id", "hi") and only accepted with ">" or in a
	// pair ("tr >it ciao", "en>it").
	codes, strict []string
	// names are Chinese names, accepted like codes ("日 你好").
	names []string
}

var languages = []language{
	{code: engines.English, english: "English", codes: []string{"en", "eng"}, names: []string{"英", "英文", "英语"}},
	{code: engines.Simplified, english: "Simplified Chinese", codes: []string{"zh", "zh-cn", "zh-hans", "cn", "chs"},
		names: []string{"中", "中文", "汉语", "简体", "简中", "简体中文"}},
	{code: engines.Traditional, english: "Traditional Chinese", codes: []string{"zh-tw", "zh-hant", "cht"},
		names: []string{"繁", "繁体", "繁中", "繁体中文", "正体"}},
	{code: engines.Japanese, english: "Japanese", codes: []string{"ja", "jp", "jpn"}, names: []string{"日", "日文", "日语"}},
	{code: engines.Korean, english: "Korean", codes: []string{"ko", "kr", "kor"}, names: []string{"韩", "韩文", "韩语"}},
	{code: "fr", english: "French", codes: []string{"fr", "fra"}, names: []string{"法", "法文", "法语"}},
	{code: "de", english: "German", codes: []string{"de", "deu", "ger"}, names: []string{"德", "德文", "德语"}},
	{code: "es", english: "Spanish", codes: []string{"es", "spa"}, names: []string{"西", "西语", "西班牙语"}},
	{code: engines.Russian, english: "Russian", codes: []string{"ru", "rus"}, names: []string{"俄", "俄文", "俄语"}},
	{code: "pt", english: "Portuguese", codes: []string{"pt", "por"}, names: []string{"葡", "葡语", "葡萄牙语"}},
	{code: "it", english: "Italian", codes: []string{"ita"}, strict: []string{"it"}, names: []string{"意", "意语", "意大利语"}},
	{code: "ar", english: "Arabic", codes: []string{"ar", "ara"}, names: []string{"阿", "阿语", "阿拉伯语"}},
	{code: "th", english: "Thai", codes: []string{"th", "tha"}, names: []string{"泰", "泰语"}},
	{code: "vi", english: "Vietnamese", codes: []string{"vi", "vie"}, names: []string{"越", "越南语"}},
	{code: "id", english: "Indonesian", codes: []string{"ind"}, strict: []string{"id"}, names: []string{"印尼", "印尼语"}},
	{code: "nl", english: "Dutch", codes: []string{"nl", "nld"}, names: []string{"荷", "荷兰语"}},
	{code: "pl", english: "Polish", codes: []string{"pl", "pol"}, names: []string{"波兰语"}},
	{code: "tr", english: "Turkish", codes: []string{"tr", "tur"}, names: []string{"土耳其语"}},
	{code: "uk", english: "Ukrainian", codes: []string{"uk", "ukr"}, names: []string{"乌克兰语"}},
	{code: "sv", english: "Swedish", codes: []string{"sv", "swe"}, names: []string{"瑞典语"}},
	{code: "hi", english: "Hindi", codes: []string{"hin"}, strict: []string{"hi"}, names: []string{"印地语"}},
}

// byCode finds a language by its canonical code.
func byCode(code string) (language, bool) {
	for _, l := range languages {
		if l.code == code {
			return l, true
		}
	}
	return language{}, false
}

// lookup finds the language a word names: a code or a Chinese name, and
// with strict also the codes that are English words.
func lookup(word string, strict bool) (language, bool) {
	w := strings.ToLower(strings.TrimSpace(word))
	if w == "" {
		return language{}, false
	}
	for _, l := range languages {
		if contains(l.codes, w) || contains(l.names, w) || strict && contains(l.strict, w) {
			return l, true
		}
	}
	return language{}, false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// englishName is a language's English name (for prompts), else its code.
func englishName(code string) string {
	if lang, ok := byCode(code); ok {
		return lang.english
	}
	return code
}

// prefixes are the keywords that start a translation (docs/translator.md
// §6: taken over from the web search).
var prefixes = []string{"tr", "fy", "翻译"}

// cutPrefix reports whether text starts with one of the prefixes followed
// by a space or the end, and returns the rest.
func cutPrefix(text string) (string, bool) {
	for _, p := range prefixes {
		if len(text) < len(p) || !strings.EqualFold(text[:len(p)], p) {
			continue
		}
		rest := text[len(p):]
		if r, _ := utf8.DecodeRuneInString(rest); rest != "" && !unicode.IsSpace(r) {
			continue
		}
		return strings.TrimSpace(rest), true
	}
	return "", false
}

// query is what to translate: the text with the languages given in the
// syntax ("" when not given: From detected, To automatic).
type query struct {
	From, To string
	Text     string
}

// parseQuery reads the language syntax at the start of s (the text after
// the prefix):
//
//	hello            automatic target
//	ja 你好          a code or a Chinese name ("日 你好") as the first word
//	>ja 你好         ">" and a code (also "→", "＞")
//	en>zh apple      source and target
func parseQuery(s string) query {
	s = strings.TrimSpace(s)
	var first, rest string
	if i := strings.IndexFunc(s, unicode.IsSpace); i >= 0 {
		first, rest = s[:i], strings.TrimSpace(s[i:])
	} else {
		first, rest = s, ""
	}
	norm := strings.NewReplacer("->", ">", "→", ">", "＞", ">", "》", ">").Replace(first)
	if from, to, ok := strings.Cut(norm, ">"); ok {
		tl, tok := lookup(to, true)
		var fl language
		fok := from == "" || strings.EqualFold(from, "auto")
		if !fok {
			fl, fok = lookup(from, true)
		}
		if tok && fok {
			return query{From: fl.code, To: tl.code, Text: rest}
		}
		return query{Text: s}
	}
	if l, ok := lookup(first, false); ok {
		switch {
		case rest != "":
			return query{To: l.code, Text: rest}
		case contains(l.codes, strings.ToLower(first)):
			// A code alone ("tr ja", the text comes next) is not sent; a
			// Chinese name alone ("tr 日语") is a word to translate.
			return query{To: l.code}
		}
	}
	return query{Text: s}
}

// Scripts told apart by mainScript (as in builtin.ai, R65).
const (
	scriptNone     = ""
	scriptHan      = "han"
	scriptKana     = "kana"
	scriptHangul   = "hangul"
	scriptCyrillic = "cyrillic"
	scriptLatin    = "latin"
	scriptOther    = "other"
)

// mainScript roughly tells which script text is mainly written in. A Han,
// Kana or Hangul character counts like a short word (three letters).
func mainScript(text string) string {
	var han, kana, hangul, cyrillic, latin, other int
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic++
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
	}{{cjkScript, cjk}, {scriptHangul, hangul * 3}, {scriptCyrillic, cyrillic}, {scriptLatin, latin}, {scriptOther, other}} {
		if s.n > n {
			best, n = s.script, s.n
		}
	}
	return best
}

// inLanguage reports whether text, written mainly in script, is roughly in
// the language code. Latin text is taken as English only (as in builtin.ai).
func inLanguage(script, code string) bool {
	switch code {
	case engines.Simplified, engines.Traditional:
		return script == scriptHan
	case engines.Japanese:
		return script == scriptKana
	case engines.Korean:
		return script == scriptHangul
	case engines.Russian:
		return script == scriptCyrillic
	case engines.English:
		return script == scriptLatin
	}
	return false
}

// baseTarget is the default target language: the preference, else the UI
// language (Simplified Chinese in the Chinese UI, English otherwise).
func baseTarget(pref, locale string) string {
	if _, ok := byCode(pref); ok {
		return pref
	}
	if locale == "zh" || strings.HasPrefix(strings.ToLower(locale), "zh-") {
		return engines.Simplified
	}
	return engines.English
}

// autoTarget is the language text goes into when none was given: base,
// unless the text is already in it — then English, or Simplified Chinese
// when base is English (the rule of builtin.ai's "Translate", R65). src is
// the source language when known (detected by an engine), else the script
// decides.
func autoTarget(base, src, text string) string {
	in := inLanguage(mainScript(text), base)
	if src != "" {
		in = src == base
	}
	switch {
	case !in:
		return base
	case base == engines.English:
		return engines.Simplified
	}
	return engines.English
}
