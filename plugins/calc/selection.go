package calc

import (
	"context"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

var _ plugin.SelectionProvider = (*Plugin)(nil)

// SelectionItems implements plugin.SelectionProvider (docs/selected-text.md
// §6): when the selected text is a calculation or a conversion on one line,
// the same result items as in the root search. A plain number ("42") or a
// result equal to the text is no calculation. Pasting the result is offered
// only when the selection is editable, where it replaces the selected text.
func (p *Plugin) SelectionItems(ctx context.Context, q plugin.SelectionQuery) ([]plugin.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text := strings.TrimSpace(q.Selection.Text)
	if text == "" || len(text) > maxInput || strings.ContainsAny(text, "\r\n") {
		return nil, nil
	}
	r, c, err := p.evaluate(text, p.settings())
	if err != nil {
		return nil, nil
	}
	var items []plugin.Item
	if r == nil {
		if c.raw == text {
			return nil, nil
		}
		items = []plugin.Item{p.plainItem(c)}
	} else {
		for _, it := range p.resultItems(r, text) {
			if it.Actions[0].Do.Target == text || it.Title == text {
				continue // "10 km in km"
			}
			items = append(items, it)
		}
	}
	l := p.h.I18n()
	for i := range items {
		it := &items[i]
		it.Group = ""
		it.Score = 0
		if q.Text == "" {
			it.Score = 1 - float64(float64(i)*0.01)
		}
		var acts []plugin.Action
		for _, a := range it.Actions {
			if a.Do.Kind == plugin.ActionPaste {
				if !q.Selection.Editable {
					continue // nothing to replace
				}
				a.Title = l.T("action.pasteReplace")
			}
			acts = append(acts, a)
		}
		it.Actions = acts
	}
	return items, nil
}
