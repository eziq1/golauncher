package translator

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// selectionScore ranks the instant translation in the "Selected Text"
// section while nothing is typed: a clear hit, after an URL's "Open Link".
const selectionScore = 0.9

// SelectionItems implements plugin.SelectionProvider: with the "selection"
// preference on (off by default, docs/translator.md §4), one row with the
// translation of the selected text by the first engine that answers. It
// sends the selected text when the launcher opens, before the user picks
// anything, which is why it is off by default.
func (p *Plugin) SelectionItems(ctx context.Context, q plugin.SelectionQuery) ([]plugin.Item, error) {
	if !p.h.Preferences().Bool(prefSelection) {
		return nil, nil
	}
	text := strings.TrimSpace(q.Selection.Text)
	if text == "" || utf8.RuneCountInString(text) > maxSelectionRunes {
		return nil, nil
	}
	j := p.newJob(query{Text: text})
	rows := p.sel.get(j, false, p.opts.Sync)
	o := origin{selection: true, editable: q.Selection.Editable}
	items := p.rowItems(j, rows, o)
	if len(items) == 0 {
		return nil, ctx.Err()
	}
	// The first translation, else the first row (pending or failed).
	it := items[0]
	for _, x := range items {
		if strings.HasPrefix(x.ID, "result:") {
			it = x
			break
		}
	}
	l := p.h.I18n()
	it.ID = "selection"
	it.Keywords = splitList(l.T("selection.keywords"))
	it.Subtitle = l.T("selection.subtitle", "subtitle", it.Subtitle)
	if q.Text == "" {
		it.Score = selectionScore
	}
	return []plugin.Item{it}, ctx.Err()
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
