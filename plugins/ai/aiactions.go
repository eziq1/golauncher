package ai

import (
	"cmp"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/actions"
)

// Custom AI actions (R103, docs/ai-actions.md): the rows on the selected
// text and on the files handed to the root search come from the merged list
// of package actions; an action either opens a chat or shows only its
// answer (the result view).

// argInput is the argument of an action whose prompt uses {input}.
const argInput = "input"

var _ actions.Provider = (*Plugin)(nil)

// BuiltinActions implements actions.Provider: the built-in actions, with
// their titles and keywords in the current UI language. Their prompts are
// the same as before R103, with placeholders.
func (p *Plugin) BuiltinActions() []actions.Action {
	if p.h == nil {
		return nil
	}
	l := p.h.I18n()
	return []actions.Action{
		{ID: "translate", Title: l.T("selection.translate", "lang", "{lang}"), Name: l.T("selection.translate.name"),
			Prompt: promptTranslate, Keywords: l.T("selection.translate.keywords")},
		{ID: "explain", Title: l.T("selection.explain"), Name: l.T("selection.explain"),
			Prompt: promptExplain, Keywords: l.T("selection.explain.keywords")},
		{ID: "summarize", Title: l.T("selection.summarize"), Name: l.T("selection.summarize"),
			Prompt: promptSummarize, Keywords: l.T("selection.summarize.keywords"), MinLength: minSummarizeRunes},
		{ID: "rewrite", Title: l.T("selection.rewrite"), Name: l.T("selection.rewrite"),
			Prompt: promptRewrite, Keywords: l.T("selection.rewrite.keywords")},
		{ID: "reply", Title: l.T("selection.reply"), Name: l.T("selection.reply"),
			Prompt: promptReply, Keywords: l.T("selection.reply.keywords")},
	}
}

// actionList is the merged list of actions: the built-in ones with the
// user's changes, and the user's own.
func (p *Plugin) actionList() []actions.Action {
	raw, _ := p.h.Preferences().Raw(actions.Pref)
	st, ok := actions.Decode(raw)
	if !ok {
		p.h.Logger().Warn("ai actions: the stored list is malformed; the built-in actions are used")
	}
	return actions.Merge(p.BuiltinActions(), st)
}

// promptVars are the values of an action's placeholders known when its row
// is built; {selection} and {input} are added when it runs.
type promptVars struct {
	Lang string `json:"lang,omitempty"`
	App  string `json:"app,omitempty"`
	Date string `json:"date,omitempty"`
}

// appName is the name of the app the text was selected in, as {app} gives
// it: the executable's name without ".exe". Both separators are cut, so a
// Windows path gives the same name whatever the OS.
func appName(exe string) string {
	name := exe[strings.LastIndexAny(exe, `/\`)+1:]
	if strings.EqualFold(filepath.Ext(name), ".exe") {
		name = name[:len(name)-4]
	}
	return name
}

// today is {date}.
func (p *Plugin) today() string { return p.h.Now().Format(time.DateOnly) }

// setPrompt puts an action's prompt into a payload. A prompt using neither
// {selection} nor {input} is filled in now, so that the built-in actions'
// payloads are what they were before R103; the others are filled in when
// the action runs.
func setPrompt(pl *chatPayload, prompt string, v promptVars, files bool) {
	if actions.UsesSelection(prompt) || actions.UsesInput(prompt) {
		pl.Prompt, pl.Vars = prompt, &v
		return
	}
	text := strings.TrimSpace(actions.Expand(prompt, actions.Vars{Lang: v.Lang, App: v.App, Date: v.Date}))
	if files {
		pl.Text = text
	} else {
		pl.Instruction = text
	}
}

// promptMessage is the first message of an action whose prompt is filled
// in when it runs: the prompt with its placeholders replaced, followed by
// the selected text as a block quote unless the prompt places it.
func promptMessage(pl chatPayload, args map[string]string) string {
	var v actions.Vars
	if pl.Vars != nil {
		v.Lang, v.App, v.Date = pl.Vars.Lang, pl.Vars.App, pl.Vars.Date
	}
	v.Input = strings.TrimSpace(args[argInput])
	quote := strings.TrimRight(strings.ReplaceAll(pl.Quote, "\r\n", "\n"), "\n")
	v.Selection = quote
	msg := strings.TrimSpace(actions.Expand(pl.Prompt, v))
	switch {
	case strings.TrimSpace(pl.Quote) == "": // files
		return msg
	case actions.UsesSelection(pl.Prompt):
		if pl.Truncated {
			msg += "\n\n" + fmt.Sprintf(noteTruncated, utf8.RuneCountInString(pl.Quote))
		}
		return msg
	}
	if msg == "" {
		msg = promptExplain
	}
	return quoteMessage(msg, pl.Quote, pl.Truncated)
}

// actionItem is the row of an action. pl is the payload of the chat it
// opens, without the prompt; title and name are the row's title and the
// action's name in the chat's title.
func (p *Plugin) actionItem(id string, a actions.Action, title string, pl chatPayload) plugin.Item {
	l := p.h.I18n()
	pl.Result = a.ResultMode()
	it := plugin.Item{
		ID:       id,
		Title:    title,
		Subtitle: p.be.Name(),
		Icon:     plugin.Icon{Name: a.IconName()},
		Keywords: a.KeywordList(),
		Actions: []plugin.Action{{ID: "ask", Title: l.T("action.ask"),
			Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewChat, Payload: plugin.MustPayload(pl)}}},
	}
	if actions.UsesInput(a.Prompt) {
		it.Arguments = []plugin.Argument{{Name: argInput, Type: plugin.ArgText, Placeholder: l.T("actions.input.placeholder")}}
	}
	return it
}

// selectionActionItems are the rows of the actions on the selected text:
// the enabled ones, in the list's order, leaving out those asking for a
// longer selection.
func (p *Plugin) selectionActionItems(sel plugin.Selection) []plugin.Item {
	l := p.h.I18n()
	target := p.translateTarget(sel.Text)
	v := promptVars{Lang: englishNames[target], App: appName(sel.App.Name), Date: p.today()}
	n := utf8.RuneCountInString(strings.TrimSpace(sel.Text))
	var items []plugin.Item
	for _, a := range p.actionList() {
		if a.Disabled || !a.OnSelection() || n < a.MinLength {
			continue
		}
		title := actions.Expand(a.Title, actions.Vars{Lang: langName(l, target), App: v.App, Date: v.Date})
		pl := chatPayload{Quote: sel.Text, Truncated: sel.Truncated, Editable: sel.Editable,
			Title: l.T("selection.chatTitle", "action", cmp.Or(a.Name, title), "text", cutText(sel.Text, titleQuoteRunes))}
		setPrompt(&pl, a.Prompt, v, false)
		items = append(items, p.actionItem(a.ID, a, title, pl))
	}
	return items
}

// fileActionItems are the rows of the actions on files, for the files
// handed to the root search (paths; names for the chat's title).
func (p *Plugin) fileActionItems(paths []string, names string) []plugin.Item {
	l := p.h.I18n()
	target := p.translateTarget("")
	v := promptVars{Lang: englishNames[target], Date: p.today()}
	var items []plugin.Item
	for _, a := range p.actionList() {
		if a.Disabled || !a.OnFiles() {
			continue
		}
		title := actions.Expand(a.Title, actions.Vars{Lang: langName(l, target), Date: v.Date})
		pl := chatPayload{Files: paths, Title: l.T("selection.chatTitle", "action", cmp.Or(a.Name, title), "text", cutText(names, titleQuoteRunes))}
		setPrompt(&pl, a.Prompt, v, true)
		items = append(items, p.actionItem("files-"+a.ID, a, title, pl))
	}
	return items
}

// logAction records that an action ran: counts only, never its prompt or
// the text (docs/ai-actions.md §5).
func (p *Plugin) logAction(pl chatPayload) {
	p.h.Logger().Info("ai action", "result", pl.Result, "chars", utf8.RuneCountInString(pl.Quote), "files", len(pl.Files))
}

// renderResult builds the result view of ch (docs/ai-actions.md §4): only
// the answer, as it is written; Enter pastes it (replacing an editable
// selection), Ctrl+C copies it, Tab turns the view into the chat to follow
// up. p.mu must be held.
func (p *Plugin) renderResult(ch *chat, instance string) plugin.View {
	l := p.h.I18n()
	title := cmp.Or(ch.sess.Title, l.T("chat.title"))
	inst := plugin.MustPayload(instancePayload{Instance: instance})
	cb := func(id, title, shortcut string) plugin.Action {
		return plugin.Action{ID: id, Title: title, Shortcut: shortcut, Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: id, Payload: inst}}
	}
	toChat := cb("tochat", l.T("action.toChat"), "Tab")
	var md strings.Builder
	var acts []plugin.Action
	if ch.busy {
		for _, s := range ch.steps {
			md.WriteString("_" + p.stepLine(s) + "_\n\n")
		}
		if text := ch.partial.String(); text != "" {
			md.WriteString(text + "▍")
		} else {
			md.WriteString("_" + l.T("chat.thinking") + "_")
		}
		acts = []plugin.Action{cb("stop", l.T("action.stop"), ""), toChat}
	} else {
		var last Message
		if n := len(ch.msgs); n > 0 && ch.msgs[n-1].Role == RoleAssistant {
			last = ch.msgs[n-1]
		}
		if last.Text != "" {
			md.WriteString(last.Text)
		}
		if last.Stopped {
			md.WriteString("\n\n_" + l.T("chat.stopped") + "_")
		}
		if last.Error != "" {
			md.WriteString("\n\n" + p.errorMarkdown(ch.sess, last.Error, last.Program))
		}
		if strings.TrimSpace(md.String()) == "" {
			md.WriteString("_" + l.T("result.empty") + "_")
		}
		if last.Text != "" {
			paste := l.T("action.paste")
			if ch.editable {
				paste = l.T("action.replaceSelection")
			}
			acts = append(acts,
				plugin.Action{ID: "paste", Title: paste, Do: plugin.ActionDo{Kind: plugin.ActionPaste, Target: last.Text}},
				plugin.Action{ID: "copy", Title: l.T("action.copy"), Shortcut: "Ctrl+C", Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: last.Text}},
			)
		}
		acts = append(acts, toChat)
	}
	acts = append(acts, plugin.Action{ID: "history", Title: l.T("command.history.title"), Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewHistory}})
	return plugin.View{
		Kind:        plugin.ViewDetail,
		Title:       title,
		Placeholder: l.T("result.placeholder"),
		Detail:      &plugin.Detail{Markdown: strings.TrimSpace(md.String()), Metadata: p.metadata(ch)},
		Sections:    []plugin.Section{{Items: []plugin.Item{{ID: "result", Title: title, Icon: plugin.Icon{Name: "sparkles"}, Actions: acts}}}},
		KeepOnHide:  true,
	}
}

// toChat turns a result view into the chat (Tab): the same chat, now shown
// with its messages; text typed meanwhile is sent as a follow-up.
func (p *Plugin) toChat(instance, text string) plugin.Result {
	p.mu.Lock()
	if ch := p.chats[p.views[instance]]; ch != nil {
		ch.result = false
	}
	p.mu.Unlock()
	r := plugin.Result{Refresh: true}
	if strings.TrimSpace(text) != "" {
		r = p.send(instance, text)
		r.Refresh = true
	}
	return r
}
