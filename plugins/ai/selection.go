package ai

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Selected text actions (docs/selected-text.md §5, §6).

const (
	// prefTranslateTo is the target language of "Translate" ("" for the
	// UI language).
	prefTranslateTo = "translateTo"
	// argInstruction is the optional argument of "Ask AI…".
	argInstruction = "instruction"
	// minSummarizeRunes is the length from which "Summarize" is offered.
	minSummarizeRunes = 200
	// titleQuoteRunes is how much of the selection a chat title shows.
	titleQuoteRunes = 30
	// instructionScore ranks "AI handles the selection: <typed text>" when
	// text is typed: below clear matches, above weak ones (§5.2).
	instructionScore = 0.6
)

// Prompts sent to the model with the quoted text. They are not translated:
// the system prompt names the language to answer in.
const (
	promptTranslate = "Translate the quoted text into %s. Output only the translation."
	promptExplain   = "Explain the quoted text concisely."
	promptSummarize = "Summarize the quoted text in a few bullet points."
	promptRewrite   = "Rewrite the quoted text to be clearer and more natural, keeping its language and meaning. Output only the rewritten text."
	promptReply     = "The quoted text is a message I received. Suggest 3 short replies in its language."
	noteTruncated   = "_(The quoted text was truncated to %d characters.)_"
)

// Target languages of "Translate" (the values of prefTranslateTo).
const (
	langEnglish     = "en"
	langSimplified  = "zh-CN"
	langTraditional = "zh-TW"
	langJapanese    = "ja"
	langKorean      = "ko"
	langFrench      = "fr"
	langGerman      = "de"
	langSpanish     = "es"
	langRussian     = "ru"
)

// englishNames are the languages' names in the prompt.
var englishNames = map[string]string{
	langEnglish: "English", langSimplified: "Simplified Chinese", langTraditional: "Traditional Chinese",
	langJapanese: "Japanese", langKorean: "Korean", langFrench: "French", langGerman: "German",
	langSpanish: "Spanish", langRussian: "Russian",
}

// translateOptions are the options of prefTranslateTo.
func translateOptions() []plugin.Option {
	return []plugin.Option{
		{Value: "", Title: "%pref.translateTo.auto%"},
		{Value: langEnglish, Title: "%lang.en%"},
		{Value: langSimplified, Title: "%lang.zh-CN%"},
		{Value: langTraditional, Title: "%lang.zh-TW%"},
		{Value: langJapanese, Title: "%lang.ja%"},
		{Value: langKorean, Title: "%lang.ko%"},
		{Value: langFrench, Title: "%lang.fr%"},
		{Value: langGerman, Title: "%lang.de%"},
		{Value: langSpanish, Title: "%lang.es%"},
		{Value: langRussian, Title: "%lang.ru%"},
	}
}

// langName is a language's name in the UI language.
func langName(l plugin.Localizer, code string) string {
	switch code {
	case langSimplified:
		return l.T("lang.zh-CN")
	case langTraditional:
		return l.T("lang.zh-TW")
	case langJapanese:
		return l.T("lang.ja")
	case langKorean:
		return l.T("lang.ko")
	case langFrench:
		return l.T("lang.fr")
	case langGerman:
		return l.T("lang.de")
	case langSpanish:
		return l.T("lang.es")
	case langRussian:
		return l.T("lang.ru")
	}
	return l.T("lang.en")
}

// Scripts told apart by mainScript.
const (
	scriptNone     = ""
	scriptHan      = "han"
	scriptKana     = "kana" // Japanese: Kana, usually with Han
	scriptHangul   = "hangul"
	scriptCyrillic = "cyrillic"
	scriptLatin    = "latin"
	scriptOther    = "other"
)

// mainScript roughly tells which script text is mainly written in, by its
// letters. A Han, Kana or Hangul character counts like a short word (three
// letters of an alphabet).
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
// the language code. Latin text is taken as English only: it may well be
// French or German, and translating it into one of those is still useful.
func inLanguage(script, code string) bool {
	switch code {
	case langSimplified, langTraditional:
		return script == scriptHan
	case langJapanese:
		return script == scriptKana
	case langKorean:
		return script == scriptHangul
	case langRussian:
		return script == scriptCyrillic
	case langEnglish:
		return script == scriptLatin
	}
	return false
}

// translateTarget is the language "Translate" translates text into: the
// preference, else the UI language (Simplified Chinese in the Chinese UI,
// English otherwise); text already in it goes into English, and English
// text with English as the target into Simplified Chinese.
func (p *Plugin) translateTarget(text string) string {
	target := p.h.Preferences().String(prefTranslateTo)
	if _, ok := englishNames[target]; !ok {
		target = langEnglish
		if p.h.I18n().Locale() == langSimplified {
			target = langSimplified
		}
	}
	if !inLanguage(mainScript(text), target) {
		return target
	}
	if target == langEnglish {
		return langSimplified
	}
	return langEnglish
}

// quoteMessage is the message sent for a selection: the instruction, the
// text as a Markdown block quote, and a note when the text was truncated.
func quoteMessage(instruction, text string, truncated bool) string {
	n := utf8.RuneCountInString(text)
	var b strings.Builder
	b.WriteString(strings.TrimSpace(instruction))
	b.WriteString("\n\n")
	text = strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		if strings.TrimSpace(line) == "" {
			b.WriteByte('>')
		} else {
			b.WriteString("> " + line)
		}
	}
	if truncated {
		b.WriteString("\n\n" + fmt.Sprintf(noteTruncated, n))
	}
	return b.String()
}

// cutText is text on one line, cut to n characters.
func cutText(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	return strings.TrimSpace(string([]rune(text)[:n-1])) + "…"
}

// splitKeywords splits a comma-separated list of keywords.
func splitKeywords(s string) []string {
	var out []string
	for k := range strings.SplitSeq(s, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// SelectionItems implements plugin.SelectionProvider: with AI on, the
// actions on the selected text (translate, ask, explain, summarize, rewrite,
// suggest replies) and, with text typed, "AI handles the selection: <text>";
// with AI off, one row that opens the prompt to turn it on. Nothing is sent
// until the user picks an action.
func (p *Plugin) SelectionItems(ctx context.Context, q plugin.SelectionQuery) ([]plugin.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sel := q.Selection
	if strings.TrimSpace(sel.Text) == "" {
		return nil, nil
	}
	l := p.h.I18n()
	icon := plugin.Icon{Name: "sparkles"}
	if !p.enabled() {
		return []plugin.Item{{
			ID:       "enable",
			Title:    l.T("selection.enable"),
			Subtitle: l.T("plugin.name"),
			Icon:     icon,
			Keywords: splitKeywords(l.T("selection.enable.keywords")),
			Actions: []plugin.Action{{ID: "enable", Title: l.T("action.enable"),
				Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewChat, Payload: plugin.MustPayload(chatPayload{ToRoot: true})}}},
		}}, nil
	}
	sub := p.be.Name()
	row := func(id, title, name, keywords, instruction string) plugin.Item {
		pl := chatPayload{Quote: sel.Text, Instruction: instruction, Truncated: sel.Truncated, Editable: sel.Editable,
			Title: l.T("selection.chatTitle", "action", name, "text", cutText(sel.Text, titleQuoteRunes))}
		return plugin.Item{
			ID:       id,
			Title:    title,
			Subtitle: sub,
			Icon:     icon,
			Keywords: splitKeywords(keywords),
			Actions: []plugin.Action{{ID: "ask", Title: l.T("action.ask"),
				Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewChat, Payload: plugin.MustPayload(pl)}}},
		}
	}
	var items []plugin.Item
	if typed := strings.TrimSpace(q.Text); typed != "" {
		it := row("instruction", l.T("selection.instruction", "text", typed), cutText(typed, titleQuoteRunes), "", typed)
		it.Score = instructionScore
		items = append(items, it)
	}
	target := p.translateTarget(sel.Text)
	lang := langName(l, target)
	items = append(items, row("translate", l.T("selection.translate", "lang", lang), l.T("selection.translate.name"),
		l.T("selection.translate.keywords"), fmt.Sprintf(promptTranslate, englishNames[target])))
	ask := row("ask", l.T("selection.ask"), l.T("selection.ask.name"), l.T("selection.ask.keywords"), "")
	ask.Arguments = []plugin.Argument{{Name: argInstruction, Type: plugin.ArgText, Placeholder: l.T("selection.ask.placeholder")}}
	items = append(items,
		ask,
		row("explain", l.T("selection.explain"), l.T("selection.explain"), l.T("selection.explain.keywords"), promptExplain))
	if utf8.RuneCountInString(strings.TrimSpace(sel.Text)) >= minSummarizeRunes {
		items = append(items, row("summarize", l.T("selection.summarize"), l.T("selection.summarize"), l.T("selection.summarize.keywords"), promptSummarize))
	}
	items = append(items,
		row("rewrite", l.T("selection.rewrite"), l.T("selection.rewrite"), l.T("selection.rewrite.keywords"), promptRewrite),
		row("reply", l.T("selection.reply"), l.T("selection.reply"), l.T("selection.reply.keywords"), promptReply))
	return items, nil
}

// selectionMessage is the first message of a chat opened from a selection
// action: its instruction (for "Ask AI…" the argument typed, by default an
// explanation) and the quoted text.
func selectionMessage(pl chatPayload, args map[string]string) string {
	instruction := pl.Instruction
	if instruction == "" {
		instruction = strings.TrimSpace(args[argInstruction])
	}
	if instruction == "" {
		instruction = promptExplain
	}
	return quoteMessage(instruction, pl.Quote, pl.Truncated)
}
