package network

import (
	"github.com/eziq1/golauncher/pkg/plugin"
)

// tracert, the DNS cache flush and the IP renewal run as background tasks
// (docs/background-tasks.md): the action returns at once and Background
// Tasks shows their output.

// tracertRoot is the root search for the tracert prefix.
func (p *Plugin) tracertRoot(rest string) []plugin.Item {
	l := p.h.I18n()
	if rest == "" {
		return []plugin.Item{p.hint("tracert-hint", l.T("hint.tracert.title"), l.T("hint.tracert.subtitle"))}
	}
	host, prob := parseHost(rest, false)
	if prob != nil {
		return []plugin.Item{p.problemItem("tracert-invalid", prob)}
	}
	return []plugin.Item{{
		ID: "tracert:" + host, Title: l.T("tracert.item", "host", host), Subtitle: l.T("tracert.itemSubtitle"),
		Icon:    plugin.Icon{Name: "network"},
		Actions: []plugin.Action{callback("tracert", l.T("action.tracert"), cbTracert, hostPayload{Host: host}, "")},
	}}
}

// runTracert starts tracert as a task and opens it.
func (p *Plugin) runTracert(host string) plugin.Result {
	l := p.h.I18n()
	h, prob := parseHost(host, false)
	if prob != nil {
		return failure(prob.msg(l), "")
	}
	id := p.h.RunProcess(plugin.TaskSpec{Title: l.T("task.tracert", "host", h), Icon: plugin.Icon{Name: "network"}},
		plugin.ProcessSpec{Argv: []string{"tracert", h}})
	return plugin.Result{Do: &plugin.ActionDo{Kind: plugin.ActionShowTask, Target: id}}
}

// runFlushDNS empties the DNS cache with ipconfig /flushdns.
func (p *Plugin) runFlushDNS() plugin.Result {
	l := p.h.I18n()
	title := l.T("task.flushDNS")
	p.h.RunProcess(plugin.TaskSpec{Title: title, Icon: plugin.Icon{Name: "globe"}, Queue: taskQueue, Notify: plugin.NotifyAlways},
		plugin.ProcessSpec{Argv: []string{"ipconfig", "/flushdns"}})
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.running", "task", title)}}
}

// runRenewIP releases and renews the DHCP leases of all adapters. The
// command is destructive (the connection drops for a moment), so the host
// asks for confirmation before it runs.
func (p *Plugin) runRenewIP() plugin.Result {
	l := p.h.I18n()
	title := l.T("task.renewIP")
	const line = "ipconfig /release && ipconfig /renew"
	p.h.RunProcess(plugin.TaskSpec{Title: title, Icon: plugin.Icon{Name: "network"}, Queue: taskQueue, Notify: plugin.NotifyAlways},
		plugin.ProcessSpec{Argv: []string{"cmd.exe", "/d", "/c", line}, CommandLine: line})
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.running", "task", title)}}
}
