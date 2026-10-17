package translator

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/eziq1/golauncher/internal/winapi/tts"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/translator/engines"
)

// Callbacks.
const cbSpeak = "speak"

// maxSpeakRunes is the longest text read aloud.
const maxSpeakRunes = 5000

// promptTranslate is the instruction of "Translate with AI" (not
// translated: the AI chat's system prompt names the language to answer in).
const promptTranslate = "Translate the quoted text into %s. Output only the translation."

// speakPayload is the payload of cbSpeak.
type speakPayload struct {
	Text string `json:"text"`
	Lang string `json:"lang,omitempty"`
}

// speakActions read the translation or the original aloud (docs/translator.md
// §5): only when picked, with the system's voices, offline.
func (p *Plugin) speakActions(tr, to, text, from string) []plugin.Action {
	l := p.h.I18n()
	var acts []plugin.Action
	add := func(id, title, s, lang string) {
		if strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > maxSpeakRunes {
			return
		}
		// Read-only for an AI: speaking changes nothing on the computer.
		acts = append(acts, plugin.Action{ID: id, Title: title, ReadOnly: true,
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbSpeak, Payload: plugin.MustPayload(speakPayload{Text: s, Lang: lang})}})
	}
	add("speak", l.T("action.speak"), tr, to)
	add("speak-original", l.T("action.speakOriginal"), text, from)
	return acts
}

// askAIAction hands the text to the AI chat with the instruction to
// translate it (plugin.ActionAskAI; with AI off the chat asks to turn it on).
func (p *Plugin) askAIAction(text, to string) plugin.Action {
	l := p.h.I18n()
	return plugin.Action{ID: "ai", Title: l.T("action.ai"), Do: plugin.ActionDo{Kind: plugin.ActionAskAI,
		Payload: plugin.MustPayload(plugin.AskAI{Instruction: fmt.Sprintf(promptTranslate, englishName(to)), Text: text,
			Title: l.T("ai.title", "text", oneLine(text, 30))})}}
}

// HandleAction implements plugin.ActionHandler.
func (p *Plugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	switch a.Target {
	case cbSpeak:
		var sp speakPayload
		if err := json.Unmarshal(a.Payload, &sp); err != nil {
			return plugin.Result{}, err
		}
		p.speak(sp)
		return plugin.Result{}, nil
	case cbUse, cbForget, cbClearHistory:
		return p.historyAction(a)
	}
	return plugin.Result{}, fmt.Errorf("%w %q", errUnknownAction, a.Target)
}

var errUnknownAction = errors.New("translator: unknown action")

// speak reads text aloud in the background, stopping what is being read.
func (p *Plugin) speak(sp speakPayload) {
	p.stopSpeaking()
	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.speaking = cancel
	p.mu.Unlock()
	p.speech.Add(1)
	p.h.Go(func() {
		defer p.speech.Done()
		defer cancel()
		err := p.opts.Speaker.Speak(ctx, sp.Text, sp.Lang)
		if err == nil || ctx.Err() != nil {
			return
		}
		p.h.Logger().Info("translator: speech failed", "chars", utf8.RuneCountInString(sp.Text), "lang", sp.Lang, "err", errors.Is(err, tts.ErrUnsupported))
		l := p.h.I18n()
		t := plugin.Toast{Style: plugin.ToastFailure, Title: l.T("toast.speakFailed")}
		if errors.Is(err, tts.ErrUnsupported) {
			t.Title = l.T("toast.speakUnsupported")
		}
		p.h.Toast(t)
	})
}

// stopSpeaking stops the speech going on, if any.
func (p *Plugin) stopSpeaking() {
	p.mu.Lock()
	stop := p.speaking
	p.speaking = nil
	p.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// dictItem is the dictionary entry of a single word, from the first engine
// that gave one.
func (p *Plugin) dictItem(j job, rows []row) (plugin.Item, bool) {
	var d *engines.Dict
	var r row
	for _, x := range rows {
		if x.done && x.err == nil && !x.res.Dict.Empty() {
			d, r = x.res.Dict, x
			break
		}
	}
	if d == nil {
		return plugin.Item{}, false
	}
	l := p.h.I18n()
	word := strings.TrimSpace(j.req.Text)
	title := word
	if d.Phonetic != "" {
		title = l.T("dict.title", "word", word, "phonetic", d.Phonetic)
	}
	var parts []string
	var md strings.Builder
	md.WriteString("**" + mdEscaper.Replace(word) + "**")
	if d.Phonetic != "" {
		md.WriteString("  /" + mdEscaper.Replace(d.Phonetic) + "/")
	}
	md.WriteString("\n\n")
	for _, e := range d.Entries {
		parts = append(parts, strings.TrimSpace(e.POS+" "+strings.Join(e.Meanings, l.T("dict.separator"))))
		md.WriteString("- ")
		if e.POS != "" {
			md.WriteString("*" + mdEscaper.Replace(e.POS) + "* ")
		}
		md.WriteString(mdEscaper.Replace(strings.Join(e.Meanings, l.T("dict.separator"))) + "\n")
	}
	if len(d.Definitions) > 0 {
		md.WriteString("\n")
		for _, e := range d.Definitions {
			if e.POS != "" {
				md.WriteString("*" + mdEscaper.Replace(e.POS) + "*\n\n")
			}
			for i, m := range e.Meanings {
				fmt.Fprintf(&md, "%d. %s\n", i+1, mdEscaper.Replace(m))
			}
			md.WriteString("\n")
		}
		if len(parts) == 0 {
			parts = append(parts, d.Definitions[0].Meanings[0])
		}
	}
	summary := strings.Join(parts, " · ")
	it := plugin.Item{
		ID:       "dict",
		Title:    title,
		Subtitle: oneLine(l.T("dict.subtitle", "engine", engineName(l, r.engine), "meanings", summary), titleRunes),
		Icon:     plugin.Icon{Name: "text"},
		Detail:   &plugin.Detail{Markdown: strings.TrimSpace(md.String())},
		Actions: append([]plugin.Action{
			{ID: "copy", Title: l.T("action.copyDict"), Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: strings.TrimSpace(word + " " + d.Phonetic + "\n" + strings.Join(parts, "\n"))}},
		}, p.speakActions("", "", word, r.res.From)...),
	}
	return it, true
}
