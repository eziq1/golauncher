package ai

import (
	"encoding/json/v2"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// historyView lists the chats kept, the most recent first; old ones are
// pruned first.
func (p *Plugin) historyView() plugin.View {
	p.prune()
	l := p.h.I18n()
	v := plugin.View{Kind: plugin.ViewList, Title: l.T("command.history.title"), Placeholder: l.T("history.placeholder"),
		EmptyText: l.T("history.empty"), FilterLocally: true}
	list, err := p.st.Sessions()
	if err != nil {
		v.EmptyText = err.Error()
		return v
	}
	now := p.h.Now()
	var items []plugin.Item
	for _, s := range list {
		if s.Messages == 0 {
			continue
		}
		del := plugin.MustPayload(s.ID)
		items = append(items, plugin.Item{
			ID:        s.ID,
			Title:     s.Title,
			Subtitle:  l.T("history.subtitle", "time", l.RelativeTime(s.Updated, now), "backend", p.backendName(s)),
			Icon:      plugin.Icon{Name: "message-square"},
			Accessory: l.N("history.messages", s.Messages),
			Actions: []plugin.Action{
				{ID: "open", Title: l.T("history.open"),
					Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewChat, Payload: plugin.MustPayload(chatPayload{Session: s.ID})}},
				{ID: "delete", Title: l.T("history.delete"), Shortcut: "Ctrl+X", Style: plugin.StyleDestructive,
					Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "delete", Payload: del}},
				{ID: "clear", Title: l.T("history.clear"), Style: plugin.StyleDestructive,
					Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "clear"}},
			},
		})
	}
	if len(items) > 0 {
		v.Sections = []plugin.Section{{Items: items}}
	}
	return v
}

// historyAction deletes one chat or all of them.
func (p *Plugin) historyAction(a plugin.ActionDo) (plugin.Result, error) {
	l := p.h.I18n()
	var id string
	if a.Target == "delete" {
		if err := json.Unmarshal(a.Payload, &id); err != nil {
			return plugin.Result{}, err
		}
	}
	list, err := p.st.Sessions()
	if err != nil {
		return plugin.Result{}, err
	}
	var ids []string
	for _, s := range list {
		if a.Target == "clear" || s.ID == id {
			ids = append(ids, s.ID)
			p.forget(s.ID)
			p.be.forget(s) // the CLI's session ID of the chat (R72)
		}
	}
	if a.Target == "delete" && len(ids) == 0 {
		ids = []string{id} // not in the index: delete what may be left
		p.forget(id)
	}
	n := len(ids)
	if a.Target == "clear" {
		var err error
		if n, err = p.st.Clear(); err != nil {
			return plugin.Result{}, err
		}
	} else if err := p.st.Delete(ids[0]); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.N("toast.deleted", n)}}, nil
}

// forget stops and drops a live chat that is deleted.
func (p *Plugin) forget(id string) {
	p.mu.Lock()
	ch := p.chats[id]
	delete(p.chats, id)
	for inst, sid := range p.views {
		if sid == id {
			delete(p.views, inst)
		}
	}
	p.mu.Unlock()
	if ch == nil {
		return
	}
	p.stopChat(ch)
	p.mu.Lock()
	conv := ch.conv
	p.mu.Unlock()
	if conv != nil {
		_ = conv.Close()
	}
}
