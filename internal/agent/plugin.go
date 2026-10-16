package agent

import (
	"context"
	"fmt"
	"strconv"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// PluginID is the ID of the host-provided plugin with the "AI Activity"
// command, which shows the activity log (docs/ai.md §4.4).
const PluginID = "builtin.agent"

// ViewActivity is the activity view, also the command name.
const ViewActivity = "activity"

const callbackClear = "clear"

// Plugin is builtin.agent. Its strings are in the host's catalog.
type Plugin struct {
	log *Activity
	h   plugin.Host
}

var (
	_ plugin.ViewProvider  = (*Plugin)(nil)
	_ plugin.ActionHandler = (*Plugin)(nil)
)

// NewPlugin returns the plugin showing log.
func NewPlugin(log *Activity) *Plugin { return &Plugin{log: log} }

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	icon := plugin.Icon{Name: "sparkles"}
	return plugin.Manifest{
		ID:          PluginID,
		Name:        "%agent.plugin.name%",
		Description: "%agent.plugin.description%",
		Version:     "1.0.0",
		Icon:        icon,
		Commands: []plugin.Command{{
			Name: ViewActivity, Title: "%agent.command.title%", Mode: plugin.ModeView, Icon: icon,
			Keywords: []string{"%agent.command.keywords%"},
		}},
	}
}

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	if p.log != nil {
		p.log.OnChange(func() { h.Invalidate(ViewActivity) })
	}
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close() error { return nil }

// View implements plugin.ViewProvider.
func (p *Plugin) View(_ context.Context, req plugin.ViewRequest) (plugin.View, error) {
	if req.ID != ViewActivity {
		return plugin.View{}, fmt.Errorf("agent: unknown view %q", req.ID)
	}
	l := p.h.I18n()
	v := plugin.View{Kind: plugin.ViewList, Title: l.T("agent.view.title"), Placeholder: l.T("agent.view.placeholder"),
		EmptyText: l.T("agent.view.empty"), FilterLocally: true, ShowDetail: true}
	if p.log == nil {
		return v, nil
	}
	clear := plugin.Action{ID: "clear", Title: l.T("agent.action.clear"), Style: plugin.StyleDestructive,
		Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackClear}}
	now := p.h.Now()
	var items []plugin.Item
	for i, e := range p.log.Entries() {
		outcome := outcomeText(l, e.Outcome)
		title := e.Summary
		if title == "" {
			title = e.Tool
		}
		icon := plugin.Icon{Name: "check", Tint: plugin.TintSuccess}
		switch e.Outcome {
		case OutcomeDeclined, OutcomeRefused:
			icon = plugin.Icon{Name: "circle-stop", Tint: plugin.TintMuted}
		case OutcomeError:
			icon = plugin.Icon{Name: "alert", Tint: plugin.TintDanger}
		}
		md := []plugin.Metadata{
			{Label: l.T("agent.meta.time"), Value: e.Time.Local().Format("2006-01-02 15:04:05")},
			{Label: l.T("agent.meta.client"), Value: e.Client},
			{Label: l.T("agent.meta.tool"), Value: e.Tool},
			{Label: l.T("agent.meta.outcome"), Value: outcome},
		}
		if e.Detail != "" {
			md = append(md, plugin.Metadata{Label: l.T("agent.meta.detail"), Value: e.Detail})
		}
		items = append(items, plugin.Item{
			ID:        strconv.Itoa(i),
			Title:     title,
			Subtitle:  e.Client + " · " + e.Tool + " · " + outcome,
			Accessory: l.RelativeTime(e.Time, now),
			Icon:      icon,
			Detail:    &plugin.Detail{Metadata: md},
			Actions:   []plugin.Action{clear},
		})
	}
	if len(items) > 0 {
		v.Sections = []plugin.Section{{Items: items}}
	}
	return v, nil
}

// HandleAction implements plugin.ActionHandler.
func (p *Plugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	if a.Target != callbackClear || p.log == nil {
		return plugin.Result{}, nil
	}
	n, err := p.log.Clear()
	if err != nil {
		return plugin.Result{}, err
	}
	l := p.h.I18n()
	return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.N("agent.toast.cleared", n)}}, nil
}

// outcomeText names an outcome.
func outcomeText(l plugin.Localizer, o Outcome) string {
	switch o {
	case OutcomeDeclined:
		return l.T("agent.outcome.declined")
	case OutcomeRefused:
		return l.T("agent.outcome.refused")
	case OutcomeError:
		return l.T("agent.outcome.error")
	}
	return l.T("agent.outcome.ok")
}
