package translator

import (
	"context"
	"encoding/json/v2"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// View implements plugin.ViewProvider: the "Translate" command's view,
// whose search field is the text to translate (with the same language
// syntax as after the prefix). Opened with a text (Swap Languages, Show
// All Engines, an AI's run_command with a query), its first rendering waits
// for the answers instead of debouncing, so that what is opened is
// complete; typing afterwards is debounced as in the root search.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	l := p.h.I18n()
	v := plugin.View{Kind: plugin.ViewList, Title: l.T("view.title"), Placeholder: l.T("view.placeholder"), ShowDetail: true,
		EmptyText: l.T("view.empty")}
	if req.ID != viewTranslate {
		return v, nil
	}
	var pl viewPayload
	if len(req.Payload) > 0 {
		_ = json.Unmarshal(req.Payload, &pl)
	}
	q := query{From: pl.From, To: pl.To, Text: pl.Text}
	if typed := strings.TrimSpace(req.Query.Text); typed != "" {
		tq := parseQuery(typed)
		if tq.To == "" && tq.From == "" {
			// Plain text keeps the languages the view was opened with.
			tq.From, tq.To = q.From, q.To
		}
		q = tq
	}
	p.mu.Lock()
	first := !p.seen[req.Instance]
	if req.Instance != "" {
		p.seen[req.Instance] = true
	}
	p.mu.Unlock()
	if strings.TrimSpace(q.Text) == "" {
		p.typing.stop()
		if q.To != "" {
			v.EmptyText = l.T("hint.title", "to", langName(l, q.To))
		}
		return v, nil
	}
	j := p.newJob(q)
	rows := p.typing.get(j, true, first || p.opts.Sync)
	v.Sections = []plugin.Section{{Items: p.rowItems(j, rows, origin{})}}
	return v, ctx.Err()
}
