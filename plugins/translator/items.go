package translator

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/translator/engines"
)

const (
	// maxWebRunes is the longest text put into a web translator's URL.
	maxWebRunes = 5000
	// titleRunes is how much of a translation a row's title shows.
	titleRunes = 200
)

// origin is where rows are shown, which changes their actions.
type origin struct {
	root bool
	// selection: the selected text's row; editable when pasting replaces it.
	selection, editable bool
}

// viewPayload opens the translate view with a text (Swap Languages, Show
// All Engines).
type viewPayload struct {
	Text string `json:"text,omitempty"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// errorKind names an error for the log and picks its message.
func errorKind(err error) string {
	var ne net.Error
	switch {
	case err == nil:
		return ""
	case errors.Is(err, engines.ErrQuota):
		return "quota"
	case errors.Is(err, engines.ErrAuth):
		return "auth"
	case errors.Is(err, engines.ErrLanguage):
		return "language"
	case errors.Is(err, engines.ErrNoKey):
		return "noKey"
	case errors.Is(err, engines.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, errOffline), errors.As(err, &ne):
		return "network"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return "network"
	}
	return "failed"
}

// rowItems are the items of the rows of j. A fallback row that answered
// replaces the rows of the engines that failed.
func (p *Plugin) rowItems(j job, rows []row, o origin) []plugin.Item {
	l := p.h.I18n()
	group := ""
	if o.root {
		group = l.T("group")
	}
	var show []row
	for _, r := range rows {
		if r.fallback && r.err == nil {
			show = []row{r}
			break
		}
		show = append(show, r)
	}
	var items []plugin.Item
	for i, r := range show {
		it := p.rowItem(j, r, o)
		it.Group = group
		if o.root {
			it.Score = 1 - 0.001*float64(i)
		}
		it.Transient = true
		items = append(items, it)
	}
	return items
}

// rowItem is one engine's row.
func (p *Plugin) rowItem(j job, r row, o origin) plugin.Item {
	l := p.h.I18n()
	name := engineName(l, r.engine)
	text := j.req.Text
	switch {
	case !r.done:
		return plugin.Item{
			ID:       "pending:" + r.engine,
			Title:    l.T("row.pending"),
			Subtitle: l.T("row.subtitle", "engine", name, "from", langName(l, j.req.From), "to", langName(l, r.to)),
			Icon:     plugin.Icon{Name: "languages", Tint: plugin.TintMuted},
			Actions:  p.webActions(text, j.req.From, r.to),
		}
	case r.err != nil:
		return plugin.Item{
			ID:       "error:" + r.engine,
			Title:    errorText(l, errorKind(r.err), name),
			Subtitle: name,
			Icon:     plugin.Icon{Name: "alert", Tint: plugin.TintDanger},
			Actions:  p.webActions(text, j.req.From, r.to),
		}
	}
	from := r.res.From
	sub := l.T("row.subtitle", "engine", name, "from", langName(l, from), "to", langName(l, r.to))
	if r.fallback && len(r.failed) > 0 {
		var names []string
		for _, id := range r.failed {
			names = append(names, engineName(l, id))
		}
		sub = l.T("row.fallback", "subtitle", sub, "failed", strings.Join(names, ", "))
	}
	tr := r.res.Text
	it := plugin.Item{
		ID:       "result:" + r.engine,
		Title:    oneLine(tr, titleRunes),
		Subtitle: sub,
		Icon:     plugin.Icon{Name: "languages"},
		Detail:   p.detail(j, r),
	}
	it.Actions = p.resultActions(tr, text, from, r.to, o)
	return it
}

// resultActions are the actions of a translation: copy (Enter), paste or
// replace the selection (Ctrl+Enter), swap the languages, open it in a web
// translator.
func (p *Plugin) resultActions(tr, text, from, to string, o origin) []plugin.Action {
	l := p.h.I18n()
	paste := plugin.Action{ID: "paste", Title: l.T("action.paste"), Do: plugin.ActionDo{Kind: plugin.ActionPaste, Target: tr}}
	if o.selection && o.editable {
		paste.ID, paste.Title = "replace", l.T("action.replace")
	}
	acts := []plugin.Action{
		{ID: "copy", Title: l.T("action.copy"), Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: tr}},
		paste,
	}
	if o.selection {
		acts = append(acts, plugin.Action{ID: "all", Title: l.T("action.allEngines"),
			Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewTranslate, Payload: plugin.MustPayload(viewPayload{Text: text})}})
	}
	if from != "" && from != to {
		acts = append(acts, plugin.Action{ID: "swap", Title: l.T("action.swap"),
			Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewTranslate, Payload: plugin.MustPayload(viewPayload{Text: tr, From: to, To: from})}})
	}
	acts = append(acts, plugin.Action{ID: "copy-original", Title: l.T("action.copyOriginal"), Shortcut: "Ctrl+Shift+C",
		Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: text}})
	return append(acts, p.webActions(text, from, to)...)
}

// webActions open the text in Google Translate, DeepL and Youdao's websites.
func (p *Plugin) webActions(text, from, to string) []plugin.Action {
	if utf8.RuneCountInString(text) > maxWebRunes {
		return nil
	}
	l := p.h.I18n()
	open := func(id, u string) plugin.Action {
		return plugin.Action{ID: "web-" + id, Title: l.T("action.openIn", "site", webName(l, id)),
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: u}}
	}
	return []plugin.Action{
		open("google", googleWebURL(text, from, to)),
		open("deepl", deeplWebURL(text, from, to)),
		open("youdao", youdaoWebURL(text)),
	}
}

// googleWebURL opens Google Translate's website.
func googleWebURL(text, from, to string) string {
	if from == "" {
		from = "auto"
	}
	return "https://translate.google.com/?sl=" + url.QueryEscape(from) + "&tl=" + url.QueryEscape(to) +
		"&text=" + url.QueryEscape(text) + "&op=translate"
}

// deeplWebURL opens DeepL's website (its codes: "zh", "en", …).
func deeplWebURL(text, from, to string) string {
	code := func(c string) string {
		if c == "" {
			return "auto"
		}
		if c == engines.Traditional {
			return "zh-hant"
		}
		c, _, _ = strings.Cut(c, "-")
		return c
	}
	return "https://www.deepl.com/translator#" + code(from) + "/" + code(to) + "/" + url.PathEscape(text)
}

// youdaoWebURL opens Youdao's website.
func youdaoWebURL(text string) string {
	return "https://www.youdao.com/result?word=" + url.QueryEscape(text) + "&lang=en"
}

// detail is the full text of a translation and of the original.
func (p *Plugin) detail(j job, r row) *plugin.Detail {
	l := p.h.I18n()
	var b strings.Builder
	b.WriteString(markdownText(r.res.Text))
	b.WriteString("\n\n---\n\n")
	b.WriteString(markdownText(j.req.Text))
	return &plugin.Detail{Markdown: b.String(), Metadata: []plugin.Metadata{
		{Label: l.T("detail.engine"), Value: engineName(l, r.engine)},
		{Label: l.T("detail.from"), Value: langName(l, r.res.From)},
		{Label: l.T("detail.to"), Value: langName(l, r.to)},
	}}
}

// markdownText shows text as is in Markdown: line breaks kept, characters
// with a meaning escaped.
func markdownText(s string) string {
	var b strings.Builder
	for i, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if i > 0 {
			b.WriteString("  \n")
		}
		if t := strings.TrimLeft(line, " "); t != "" && strings.ContainsRune("#>-+=|", rune(t[0])) {
			b.WriteByte('\\')
		}
		b.WriteString(mdEscaper.Replace(line))
	}
	return b.String()
}

var mdEscaper = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "[", `\[`, "`", "\\`", "<", `\<`)

// oneLine is text on one line, cut to n characters.
func oneLine(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	return strings.TrimSpace(string([]rune(text)[:n-1])) + "…"
}

// hintItem is shown for a prefix with languages but no text yet ("tr ja").
func (p *Plugin) hintItem(q query) plugin.Item {
	l := p.h.I18n()
	to := q.To
	if to == "" {
		to = baseTarget(p.settings().target, l.Locale())
	}
	return plugin.Item{
		ID:        "hint",
		Title:     l.T("hint.title", "to", langName(l, to)),
		Subtitle:  l.T("hint.subtitle"),
		Icon:      plugin.Icon{Name: "languages"},
		Group:     l.T("group"),
		Score:     1,
		Transient: true,
		Actions: []plugin.Action{{ID: "open", Title: l.T("action.openView"),
			Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewTranslate, Payload: plugin.MustPayload(viewPayload{From: q.From, To: q.To})}}},
	}
}
