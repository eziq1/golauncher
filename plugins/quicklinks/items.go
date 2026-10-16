package quicklinks

import (
	"strings"

	"github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Callback names.
const (
	cbOpen          = "open"
	cbSearch        = "search"
	cbSave          = "save"
	cbDuplicate     = "duplicate"
	cbDelete        = "delete"
	cbToggle        = "toggle"
	cbSetDefault    = "setDefault"
	cbReset         = "reset"
	cbSaveEngine    = "saveEngine"
	cbDeleteEngine  = "deleteEngine"
	cbImport        = "import"
	cbExport        = "export"
	shortcutCopy    = "Ctrl+Shift+C"
	shortcutEdit    = "Ctrl+E"
	shortcutDup     = "Ctrl+D"
	shortcutDelete  = "Ctrl+X"
	shortcutDefault = "Ctrl+Shift+D"
)

// idPayload names a quicklink or an engine; Query is set when the text to
// put into {query} is already known (typed after the keyword).
type idPayload struct {
	ID    string `json:"id,omitempty"`
	Query string `json:"query,omitempty"`
}

func callback(target string, payload any) plugin.ActionDo {
	return plugin.ActionDo{Kind: plugin.ActionCallback, Target: target, Payload: plugin.MustPayload(payload)}
}

// builder builds items from one snapshot of the data.
type builder struct {
	p        *Plugin
	l        plugin.Localizer
	links    []Quicklink
	engines  []Engine
	browsers []shell.Browser
}

func (p *Plugin) newBuilder() *builder {
	return &builder{p: p, l: p.h.I18n(), links: p.linkList(), engines: p.engineList(), browsers: p.browserList()}
}

// urlActions open u, copy it and open it in each browser.
func (b *builder) urlActions(openTitle, u string) []plugin.Action {
	acts := []plugin.Action{
		{ID: "open", Title: openTitle, Do: openAction(u, "")},
		{ID: "copy", Title: b.l.T("action.copyURL"), Shortcut: shortcutCopy, Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: u}},
	}
	for i, br := range b.browsers {
		acts = append(acts, plugin.Action{ID: "browser" + string(rune('1'+i)), Title: b.l.T("action.openIn", "browser", br.Name),
			Do: openAction(br.Path, quoteArg(u))})
	}
	return acts
}

// urlItem opens a typed URL.
func (b *builder) urlItem(text, u string) plugin.Item {
	return plugin.Item{
		ID:        "url",
		Title:     b.l.T("url.title", "url", text),
		Subtitle:  u,
		Icon:      plugin.Icon{Name: "globe"},
		Group:     b.l.T("group.web"),
		Score:     1,
		Transient: true,
		Actions:   b.urlActions(b.l.T("action.openURL"), u),
	}
}

// searchURL is the URL searching e for query.
func searchURL(e Engine, query string) string {
	return parseLink(e.URL).resolve(values{query: query}).link
}

// searchItem searches e for query (typed after its keyword).
func (b *builder) searchItem(e Engine, query string) plugin.Item {
	u := searchURL(e, query)
	return plugin.Item{
		ID:        "search:" + e.ID,
		Title:     b.l.T("search.title", "engine", e.Name, "query", query),
		Subtitle:  u,
		Icon:      plugin.Icon{Name: "search"},
		Group:     b.l.T("group.web"),
		Score:     1,
		Transient: true,
		Actions:   b.urlActions(b.l.T("action.search"), u),
	}
}

// suggestionItems are the search suggestions of e, after its search item.
func (b *builder) suggestionItems(e Engine, res []string) []plugin.Item {
	var items []plugin.Item
	for i, s := range res {
		u := searchURL(e, s)
		items = append(items, plugin.Item{
			ID:        "suggest:" + s,
			Title:     s,
			Subtitle:  b.l.T("suggest.subtitle", "engine", e.Name),
			Icon:      plugin.Icon{Name: "search", Tint: plugin.TintMuted},
			Group:     b.l.T("group.web"),
			Score:     0.99 - 0.001*float64(i),
			Transient: true,
			Actions:   b.urlActions(b.l.T("action.search"), u),
		})
	}
	return items
}

// engineItem is an engine in the root search (its keyword typed alone):
// the search text is an inline argument.
func (b *builder) engineItem(e Engine) plugin.Item {
	return plugin.Item{
		ID:        "engine:" + e.ID,
		Title:     b.l.T("engine.item", "engine", e.Name),
		Subtitle:  strings.Join(e.Keywords, ", "),
		Icon:      plugin.Icon{Name: "search"},
		Keywords:  e.Keywords,
		Group:     b.l.T("group.web"),
		Accessory: b.l.T("engine.accessory"),
		Arguments: []plugin.Argument{{Name: argQuery, Type: plugin.ArgText, Placeholder: b.l.T("arg.search")}},
		Actions: []plugin.Action{
			{ID: "search", Title: b.l.T("action.search"), Do: callback(cbSearch, idPayload{ID: e.ID})},
			{ID: "home", Title: b.l.T("action.openHome"), Do: openAction(homepage(e.URL), "")},
		},
	}
}

// linkIcon is the icon of a quicklink by what it opens.
func linkIcon(t target) plugin.Icon {
	switch t.kind {
	case kindProgram:
		return plugin.Icon{Name: "app"}
	case kindPath:
		if strings.HasSuffix(t.link, `\`) || strings.HasSuffix(t.link, "/") || !strings.Contains(t.link[strings.LastIndexAny(t.link, `\/`)+1:], ".") {
			return plugin.Icon{Name: "folder"}
		}
		return plugin.Icon{Name: "file"}
	}
	return plugin.Icon{Name: "globe"}
}

// openLinkDo is the primary action of a quicklink: ActionOpen when it has
// no placeholders, else a callback that fills them in when it runs
// (docs/quicklinks.md §5.3).
func (b *builder) openLinkDo(q Quicklink, query string, known bool) plugin.ActionDo {
	hasQuery, dynamic := placeholders(q.Link)
	t := parseLink(q.Link)
	if !dynamic && (!hasQuery || known) {
		return t.resolve(values{query: query}).openDo(q.OpenWith)
	}
	return callback(cbOpen, idPayload{ID: q.ID, Query: query})
}

// linkItem is a quicklink in the root search (root) or in its list.
func (b *builder) linkItem(q Quicklink, root bool) plugin.Item {
	l := b.l
	t := parseLink(q.Link)
	hasQuery, dynamic := placeholders(q.Link)
	it := plugin.Item{
		ID:       "link:" + q.ID,
		Title:    q.Name,
		Subtitle: q.Link,
		Icon:     linkIcon(t),
		Detail:   b.linkDetail(q),
	}
	if q.Keyword != "" {
		it.Keywords = []string{q.Keyword}
		it.Accessory = q.Keyword
	}
	if root {
		it.Group = l.T("group.quicklinks")
	}
	if hasQuery {
		it.Arguments = []plugin.Argument{{Name: argQuery, Type: plugin.ArgText, Placeholder: l.T("arg.query"), Required: true}}
	}
	copyTarget := q.Link
	if !hasQuery && !dynamic {
		copyTarget = t.resolve(values{}).link
	}
	id := idPayload{ID: q.ID}
	it.Actions = []plugin.Action{
		{ID: "open", Title: l.T("action.open"), Do: b.openLinkDo(q, "", false)},
		{ID: "copy", Title: l.T("action.copyLink"), Shortcut: shortcutCopy, Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: copyTarget}},
		{ID: "edit", Title: l.T("action.edit"), Shortcut: shortcutEdit, Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewEdit, Payload: plugin.MustPayload(id)}},
		{ID: "duplicate", Title: l.T("action.duplicate"), Shortcut: shortcutDup, Do: callback(cbDuplicate, id)},
		{ID: "delete", Title: l.T("action.delete"), Shortcut: shortcutDelete, Style: plugin.StyleDestructive, Do: callback(cbDelete, id)},
	}
	return it
}

// linkQueryItem opens a quicklink with the text typed after its keyword.
func (b *builder) linkQueryItem(q Quicklink, query string) plugin.Item {
	t := parseLink(q.Link)
	_, dynamic := placeholders(q.Link)
	it := plugin.Item{
		ID:        "linkquery:" + q.ID,
		Title:     b.l.T("link.query", "name", q.Name, "query", query),
		Subtitle:  q.Link,
		Icon:      linkIcon(t),
		Group:     b.l.T("group.quicklinks"),
		Score:     1,
		Transient: true,
		Actions:   []plugin.Action{{ID: "open", Title: b.l.T("action.open"), Do: b.openLinkDo(q, query, true)}},
	}
	if !dynamic {
		it.Subtitle = t.resolve(values{query: query}).link
		if t.kind == kindURL && strings.TrimSpace(q.OpenWith) == "" {
			it.Actions = b.urlActions(b.l.T("action.open"), it.Subtitle)
		}
	}
	return it
}

// linkDetail describes a quicklink.
func (b *builder) linkDetail(q Quicklink) *plugin.Detail {
	l := b.l
	md := []plugin.Metadata{{Label: l.T("detail.link"), Value: q.Link}}
	kinds := map[linkKind]string{kindURL: l.T("detail.kindURL"), kindPath: l.T("detail.kindPath"), kindProgram: l.T("detail.kindProgram")}
	md = append(md, plugin.Metadata{Label: l.T("detail.kind"), Value: kinds[parseLink(q.Link).kind]})
	if q.Keyword != "" {
		md = append(md, plugin.Metadata{Label: l.T("detail.keyword"), Value: q.Keyword})
	}
	if q.OpenWith != "" {
		md = append(md, plugin.Metadata{Label: l.T("detail.openWith"), Value: q.OpenWith})
	}
	return &plugin.Detail{Metadata: md}
}
