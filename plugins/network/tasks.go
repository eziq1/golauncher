package network

import (
	"net/netip"

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
	argv := []string{"tracert", h}
	if p.b.Mac() {
		argv = []string{"/usr/sbin/traceroute", h}
		if a, err := netip.ParseAddr(h); err == nil && a.Is6() && !a.Is4In6() {
			argv[0] = "/usr/sbin/traceroute6"
		}
	}
	id := p.h.RunProcess(plugin.TaskSpec{Title: l.T("task.tracert", "host", h), Icon: plugin.Icon{Name: "network"}},
		plugin.ProcessSpec{Argv: argv})
	return plugin.Result{Do: &plugin.ActionDo{Kind: plugin.ActionShowTask, Target: id}}
}

// macFlushDNS is what flushes the DNS cache of macOS, as root.
const macFlushDNS = "/usr/bin/dscacheutil -flushcache; /usr/bin/killall -HUP mDNSResponder"

// runFlushDNS empties the DNS cache with ipconfig /flushdns. It needs
// administrator rights (R68), so unless golauncher has them the task asks
// for them with a UAC prompt. On macOS osascript runs macFlushDNS after
// macOS asked for an administrator's password.
func (p *Plugin) runFlushDNS() plugin.Result {
	l := p.h.I18n()
	title := l.T("task.flushDNS")
	spec := plugin.TaskSpec{Title: title, Icon: plugin.Icon{Name: "globe"}, Queue: taskQueue, Notify: plugin.NotifyAlways}
	if p.b.Mac() {
		cmd := plugin.ProcessSpec{Argv: adminScriptArgv(macFlushDNS, l.T("flushDNS.prompt")), CommandLine: macFlushDNS}
		if p.b.Elevated() {
			cmd = plugin.ProcessSpec{Argv: []string{"/bin/sh", "-c", macFlushDNS}, CommandLine: macFlushDNS}
		}
		p.h.RunProcess(spec, cmd)
		if p.b.Elevated() {
			return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.running", "task", title)}}
		}
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.runningAdmin", "task", title),
			Message: l.T("toast.runningAdminHint")}}
	}
	return p.runAdmin(spec, plugin.ProcessSpec{Argv: []string{"ipconfig", "/flushdns"}})
}

// adminScriptArgv is the osascript command line that runs the shell
// command cmd as root once macOS asked for an administrator's password,
// with prompt. Both are passed as arguments, never pasted into the script.
func adminScriptArgv(cmd, prompt string) []string {
	const script = `on run argv
do shell script (item 1 of argv) with prompt (item 2 of argv) with administrator privileges
end run`
	return []string{"/usr/bin/osascript", "-e", script, cmd, prompt}
}

// runRenewIP releases and renews the DHCP leases of all adapters. The
// command is destructive (the connection drops for a moment), so the host
// asks for confirmation before it runs; like the DNS flush it needs
// administrator rights.
func (p *Plugin) runRenewIP() plugin.Result {
	title := p.h.I18n().T("task.renewIP")
	const line = "ipconfig /release && ipconfig /renew"
	return p.runAdmin(plugin.TaskSpec{Title: title, Icon: plugin.Icon{Name: "network"}, Queue: taskQueue, Notify: plugin.NotifyAlways},
		plugin.ProcessSpec{Argv: []string{"cmd.exe", "/d", "/c", line}, CommandLine: line})
}

// runAdmin starts an ipconfig task that needs administrator rights: with
// Elevate unless golauncher runs as administrator (then it runs as usual,
// with its output). The toast says a UAC prompt follows.
func (p *Plugin) runAdmin(spec plugin.TaskSpec, cmd plugin.ProcessSpec) plugin.Result {
	l := p.h.I18n()
	cmd.Elevate = !p.b.Elevated()
	p.h.RunProcess(spec, cmd)
	if cmd.Elevate {
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.runningAdmin", "task", spec.Title),
			Message: l.T("toast.runningAdminHint")}}
	}
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.running", "task", spec.Title)}}
}
