package network

import (
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	winnet "github.com/eziq1/golauncher/internal/winapi/network"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// cbRecheck pulls the port view again, which checks the remote port again.
const cbRecheck = "recheck"

// killPayload is the payload of the kill callback. Created (the creation
// time) guards against a PID reused by another process since the list was
// shown, checked on the handle that ends the process
// (docs/process-manager.md §5.4); without it (older data) Name does.
type killPayload struct {
	PID     uint32 `json:"pid"`
	Name    string `json:"name,omitempty"`
	Created int64  `json:"created,omitzero"`
}

// owner is a process with its sockets on one port (or on all ports).
type owner struct {
	proc  winnet.Process
	port  uint16
	socks []winnet.Socket
}

// owners groups the sockets on port (0: every port) by process and port,
// ordered by port and process name.
func (p *Plugin) owners(ctx context.Context, port uint16) ([]owner, error) {
	socks, err := p.b.Sockets(ctx)
	if err != nil {
		return nil, err
	}
	procs := map[uint32]winnet.Process{}
	byKey := map[string]*owner{}
	var out []*owner
	for _, s := range socks {
		if port != 0 && s.Local.Port() != port {
			continue
		}
		key := fmt.Sprintf("%d/%d", s.Local.Port(), s.PID)
		o, ok := byKey[key]
		if !ok {
			pr, ok := procs[s.PID]
			if !ok {
				pr = p.b.Process(s.PID)
				procs[s.PID] = pr
			}
			o = &owner{proc: pr, port: s.Local.Port()}
			byKey[key] = o
			out = append(out, o)
		}
		if !slices.Contains(o.socks, s) {
			o.socks = append(o.socks, s)
		}
	}
	slices.SortStableFunc(out, func(a, b *owner) int {
		return cmp.Or(cmp.Compare(a.port, b.port), cmp.Compare(strings.ToLower(a.proc.Name), strings.ToLower(b.proc.Name)), cmp.Compare(a.proc.PID, b.proc.PID))
	})
	res := make([]owner, len(out))
	for i, o := range out {
		res[i] = *o
	}
	return res, nil
}

// ownerItem is the item of a process listening on a port. With list set
// the port leads the title (the list of all ports).
func (p *Plugin) ownerItem(o owner, list bool) plugin.Item {
	l := p.h.I18n()
	name := cmp.Or(o.proc.Name, l.T("port.unknownProcess"))
	var socks []string
	for _, s := range o.socks {
		socks = append(socks, strings.ToUpper(s.Proto)+" "+s.Local.String())
	}
	it := plugin.Item{
		ID:        fmt.Sprintf("owner:%d:%d", o.port, o.proc.PID),
		Title:     l.T("port.owner", "name", name, "pid", o.proc.PID),
		Subtitle:  strings.Join(socks, ", "),
		Icon:      plugin.Icon{Name: "hash"},
		Accessory: l.T("port.listening"),
		Detail: &plugin.Detail{Metadata: []plugin.Metadata{
			{Label: l.T("meta.process"), Value: name},
			{Label: l.T("meta.pid"), Value: strconv.Itoa(int(o.proc.PID))},
			{Label: l.T("meta.path"), Value: o.proc.Path},
			{Label: l.T("meta.sockets"), Value: strings.Join(socks, "\n")},
		}},
	}
	if list {
		it.Title = l.T("port.listItem", "port", o.port, "name", name, "pid", o.proc.PID)
		it.Accessory = ""
	}
	it.Actions = append(it.Actions, copyAction("copy-pid", l.T("action.copyPID"), strconv.Itoa(int(o.proc.PID)), ""))
	if o.proc.Path != "" {
		it.Actions = append(it.Actions, plugin.Action{ID: "reveal", Title: l.T("action.reveal"), Shortcut: "Ctrl+Shift+F",
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: o.proc.Path, Payload: plugin.MustPayload(plugin.OpenOptions{Reveal: true})}})
	}
	if o.proc.PID > 4 && o.proc.PID != uint32(os.Getpid()) { // not the system or the launcher itself
		kill := callback("kill", l.T("action.kill", "name", name), cbKill, killPayload{PID: o.proc.PID, Name: o.proc.Name, Created: o.proc.Created}, "Ctrl+X")
		kill.Style = plugin.StyleDestructive
		it.Actions = append(it.Actions, kill)
	}
	return it
}

// localPortItems are the items of the processes listening on port.
func (p *Plugin) localPortItems(ctx context.Context, port uint16) []plugin.Item {
	l := p.h.I18n()
	owners, err := p.owners(ctx, port)
	if err != nil {
		return []plugin.Item{{ID: "port-error", Title: l.T("port.failed"), Subtitle: err.Error(),
			Icon: plugin.Icon{Name: "alert", Tint: plugin.TintDanger}}}
	}
	if len(owners) == 0 {
		return []plugin.Item{{ID: fmt.Sprintf("port-free:%d", port), Title: l.T("port.free", "port", port),
			Subtitle: l.T("port.freeSubtitle"), Icon: plugin.Icon{Name: "check", Tint: plugin.TintSuccess},
			Actions: []plugin.Action{copyAction("copy", l.T("action.copyPort"), strconv.Itoa(int(port)), "")}}}
	}
	var items []plugin.Item
	for _, o := range owners {
		items = append(items, p.ownerItem(o, false))
	}
	return items
}

// portRoot is the root search for the port prefix.
func (p *Plugin) portRoot(ctx context.Context, rest string) []plugin.Item {
	l := p.h.I18n()
	t, prob := parsePortTarget(rest)
	switch {
	case prob != nil:
		return []plugin.Item{p.problemItem("port-invalid", prob)}
	case rest == "":
		return []plugin.Item{
			p.hint("port-hint", l.T("hint.port.title"), l.T("hint.port.subtitle")),
			{ID: "port-all", Title: l.T("port.all"), Icon: plugin.Icon{Name: "hash"},
				Actions: []plugin.Action{push("open", l.T("action.showPorts"), toolPort, portTarget{}, "")}},
		}
	case t.Host != "":
		return []plugin.Item{{ID: "port-check:" + t.address(), Title: l.T("port.check", "address", t.address()),
			Subtitle: l.T("port.checkSubtitle"), Icon: plugin.Icon{Name: "hash"},
			Actions: []plugin.Action{push("check", l.T("action.check"), toolPort, t, "")}}}
	}
	ctx, cancel := context.WithTimeout(ctx, rootTimeout)
	defer cancel()
	return p.localPortItems(ctx, t.Port)
}

// portView lists every listening port, the owners of one port, or checks
// a remote port.
func (p *Plugin) portView(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	l := p.h.I18n()
	var t portTarget
	var prob *problem
	if len(req.Payload) > 0 {
		if err := json.Unmarshal(req.Payload, &t); err != nil {
			return plugin.View{}, err
		}
	} else {
		t, prob = parsePortTarget(req.Arguments["target"])
	}
	v := plugin.View{Kind: plugin.ViewList, Title: l.T("view.port.titleAll"), Placeholder: l.T("view.port.placeholder"),
		FilterLocally: true, ShowDetail: true, EmptyText: l.T("view.port.empty")}
	switch {
	case prob != nil:
		v.Sections = []plugin.Section{{Items: []plugin.Item{p.problemItem("port-invalid", prob)}}}
	case t.Host != "":
		v.Title = l.T("view.port.titleCheck", "address", t.address())
		v.ShowDetail = false
		v.Sections = []plugin.Section{{Items: []plugin.Item{p.checkItem(ctx, t)}}}
	case t.Port != 0:
		v.Title = l.T("view.port.titleLocal", "port", t.Port)
		v.Sections = []plugin.Section{{Items: p.localPortItems(ctx, t.Port)}}
	default:
		owners, err := p.owners(ctx, 0)
		if err != nil {
			return plugin.View{}, err
		}
		var items []plugin.Item
		for _, o := range owners {
			items = append(items, p.ownerItem(o, true))
		}
		v.Sections = []plugin.Section{{Items: items}}
	}
	return v, nil
}

// checkItem connects to a remote port and describes the outcome.
func (p *Plugin) checkItem(ctx context.Context, t portTarget) plugin.Item {
	l := p.h.I18n()
	addr := t.address()
	elapsed, remote, err := p.b.Dial(ctx, addr, dialTimeout)
	it := plugin.Item{ID: "check:" + addr}
	if err != nil {
		it.Title = l.T("port.closed", "address", addr)
		it.Subtitle = err.Error()
		it.Icon = plugin.Icon{Name: "alert", Tint: plugin.TintDanger}
	} else {
		it.Title = l.T("port.open", "address", addr)
		it.Subtitle = l.T("port.openSubtitle", "time", p.ms(elapsed), "remote", remote)
		it.Icon = plugin.Icon{Name: "check", Tint: plugin.TintSuccess}
	}
	it.Actions = []plugin.Action{
		callback("again", l.T("action.checkAgain"), cbRecheck, nil, "Ctrl+R"),
		copyAction("copy", l.T("action.copyResult"), it.Title+"\n"+it.Subtitle, ""),
	}
	return it
}

// kill ends a process after the host asked for confirmation.
func (p *Plugin) kill(pl killPayload) plugin.Result {
	l := p.h.I18n()
	name := cmp.Or(pl.Name, l.T("port.unknownProcess"))
	// With the creation time the backend checks the instance itself;
	// without it (older data) the name must not have changed.
	if pl.Created == 0 {
		if cur := p.b.Process(pl.PID); pl.Name != "" && cur.Name != "" && !strings.EqualFold(cur.Name, pl.Name) {
			return failure(l.T("kill.changed", "name", name, "pid", pl.PID), "")
		}
	}
	switch err := p.b.Kill(pl.PID, pl.Created); {
	case errors.Is(err, winnet.ErrNeedsAdmin):
		return failure(l.T("kill.failed", "name", name), l.T("kill.needsAdmin"))
	case errors.Is(err, winnet.ErrProtected):
		return failure(l.T("kill.protected", "name", name), "")
	case errors.Is(err, winnet.ErrNotFound):
		r := failure(l.T("kill.gone", "name", name), "")
		r.Refresh = true
		return r
	case err != nil:
		return failure(l.T("kill.failed", "name", name), err.Error())
	}
	return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("kill.done", "name", name, "pid", pl.PID)}}
}
