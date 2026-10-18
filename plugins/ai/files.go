package ai

import (
	"context"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// FileItems implements plugin.FileActionsProvider (R97, docs/root-files.md):
// for the files handed to the root search, "Ask AI about <name>" opens a new
// chat with them in its draft (the first action of the section), the AI
// actions on files follow (R103) and, with text typed, "Ask AI: <text>"
// sends the text with them at once. With AI
// off, one row opens the prompt to turn it on, which keeps them. Nothing is
// read or sent until the user picks an action.
func (p *Plugin) FileItems(ctx context.Context, q plugin.FilesQuery) ([]plugin.Item, error) {
	if err := ctx.Err(); err != nil || len(q.Files) == 0 {
		return nil, err
	}
	l := p.h.I18n()
	paths := make([]string, 0, len(q.Files))
	for _, f := range q.Files {
		paths = append(paths, f.Path)
	}
	icon := plugin.Icon{Name: "sparkles"}
	push := func(pl chatPayload) []plugin.Action {
		pl.Files = paths
		return []plugin.Action{{ID: "ask", Title: l.T("action.ask"),
			Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewChat, Payload: plugin.MustPayload(pl)}}}
	}
	keywords := splitKeywords(l.T("files.ask.keywords"))
	typed := strings.TrimSpace(q.Text)
	if !p.enabled() {
		it := plugin.Item{ID: "files-enable", Title: l.T("files.enable"), Subtitle: l.T("plugin.name"), Icon: icon,
			Keywords: keywords, Actions: push(chatPayload{})}
		if typed == "" {
			it.Score = 1
		}
		return []plugin.Item{it}, nil
	}
	title := l.N("files.ask.many", len(q.Files))
	if len(q.Files) == 1 {
		title = l.T("files.ask.one", "name", q.Files[0].Name)
	}
	var items []plugin.Item
	if typed != "" {
		items = append(items, plugin.Item{ID: "files-instruction", Title: l.T("files.instruction", "text", typed), Subtitle: p.be.Name(),
			Icon: icon, Score: instructionScore, Actions: push(chatPayload{Text: typed})})
	}
	ask := plugin.Item{ID: "files-ask", Title: title, Subtitle: p.be.Name(), Icon: icon, Keywords: keywords, Actions: push(chatPayload{})}
	if typed == "" {
		ask.Score = 1 // the first action of the section
	}
	items = append(items, ask)
	// The AI actions on files (R103), in the list's order.
	names := make([]string, len(q.Files))
	for i, f := range q.Files {
		names[i] = f.Name
	}
	return append(items, p.fileActionItems(paths, strings.Join(names, ", "))...), nil
}
