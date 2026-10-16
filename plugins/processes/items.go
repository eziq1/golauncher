package processes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/process"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Where an item is shown: its actions differ.
type itemContext int

const (
	ctxRoot  itemContext = iota // "ps " in the root search
	ctxView                     // the Processes view
	ctxGroup                    // a process in the group view
	ctxKill                     // "kill" in the root search
	ctxHung                     // the Not Responding view
)

// target is the payload of the callbacks on a process or an app.
type target struct {
	ID process.ID `json:"id,omitzero"`
	// Group acts on the whole app whose root is ID.
	Group bool    `json:"group,omitzero"`
	Name  string  `json:"name,omitempty"`
	HWND  uintptr `json:"hwnd,omitzero"`
	Tree  bool    `json:"tree,omitzero"`
	Sort  string  `json:"sort,omitempty"`
}

func callback(id, title, cb string, pl target, shortcut string) plugin.Action {
	return plugin.Action{ID: id, Title: title, Shortcut: shortcut,
		Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: cb, Payload: plugin.MustPayload(pl)}}
}

func copyAction(id, title, text, shortcut string) plugin.Action {
	return plugin.Action{ID: id, Title: title, Shortcut: shortcut, Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: text}}
}

// --- Rows -------------------------------------------------------------------------

// summaryItem is the system's total use: "CPU 23% · Memory 58% (11.2 /
// 31.7 GB) · 312 processes".
func (p *Plugin) summaryItem(m *model) plugin.Item {
	l := p.h.I18n()
	s := m.snap
	used := s.MemTotal - min(s.MemAvail, s.MemTotal)
	n := len(m.procs)
	it := plugin.Item{
		ID: "summary",
		Title: l.N("summary.title", n, "cpu", p.percent(m.cpu), "memory", p.percent(s.MemoryUsed()),
			"used", formatBytes(used), "total", formatBytes(s.MemTotal)),
		Icon: plugin.Icon{Name: "activity"},
		Accessories: []plugin.Accessory{
			{Text: l.T("accessory.cpu", "value", p.percent(m.cpu)), Meter: meter(m.cpu, 100), Load: true},
			{Text: l.T("accessory.memory", "value", formatBytes(used)), Meter: meter(s.MemoryUsed(), 100), Load: true},
		},
		Actions: []plugin.Action{{ID: "open", Title: l.T("action.openView"), Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: cmdProcesses}}},
	}
	return it
}

// meter is v/full as a bar, nil when v is unknown.
func meter(v, full float64) *float64 {
	if v < 0 || full <= 0 {
		return nil
	}
	f := min(v/full, 1)
	return &f
}

// title is the name of a row: the root's display name and, for an app of
// several processes, their number.
func (r *row) title(l plugin.Localizer) string {
	if len(r.members) > 1 {
		return l.T("row.titleGroup", "name", r.root.display(), "count", len(r.members))
	}
	return r.root.display()
}

// rowItem is the item of an app or process.
func (p *Plugin) rowItem(m *model, r *row, where itemContext) plugin.Item {
	l := p.h.I18n()
	it := plugin.Item{
		ID:          r.id(),
		Title:       r.title(l),
		Icon:        p.iconOf(r.root),
		Accessories: p.usageAccessories(m, r.cpu, r.mem),
		Detail:      p.detail(m, r, r.root),
		Actions:     p.actions(m, r, nil, where),
	}
	p.subtitle(&it, r.windows(), r.root)
	if r.hung() {
		it.Accessory = l.T("row.hung")
		it.Icon = plugin.Icon{Name: "alert", Tint: plugin.TintDanger}
	}
	return it
}

// killItem is the item of "kill <name>": Enter ends the task.
func (p *Plugin) killItem(m *model, r *row) plugin.Item {
	it := p.rowItem(m, r, ctxKill)
	if !r.endable() && r.root.class != process.ClassShell {
		it.Accessory = p.h.I18n().T("row.protected")
	}
	return it
}

// memberItem is a process in the group view, indented by its depth in the
// tree.
func (p *Plugin) memberItem(m *model, r *row, x *proc) plugin.Item {
	l := p.h.I18n()
	title := x.display()
	if d := r.depth[x.ID]; d > 0 {
		title = strings.Repeat("  ", d-1) + "└ " + title
	}
	it := plugin.Item{
		ID:          itemID(x.ID),
		Title:       title,
		Icon:        p.iconOf(x),
		Accessories: p.usageAccessories(m, x.usage.CPU, x.PrivateWorkingSet),
		Detail:      p.detail(m, nil, x),
		Actions:     p.actions(m, r, x, ctxGroup),
	}
	p.subtitle(&it, x.windows, x)
	if x.hung() {
		it.Accessory = l.T("row.hung")
		it.Icon = plugin.Icon{Name: "alert", Tint: plugin.TintDanger}
	}
	return it
}

// subtitle shows the first window title (never given to an AI), else the
// executable name and PID.
func (p *Plugin) subtitle(it *plugin.Item, wins []process.Window, x *proc) {
	if len(wins) > 0 && wins[0].Title != "" {
		it.Subtitle, it.SubtitleSensitive = wins[0].Title, true
		return
	}
	it.Subtitle = p.h.I18n().T("row.subtitle", "name", x.Name, "pid", x.PID)
}

// usageAccessories are the CPU and memory values with their bars.
func (p *Plugin) usageAccessories(m *model, cpu float64, mem uint64) []plugin.Accessory {
	l := p.h.I18n()
	full := 100.0
	if p.cpuMode() == process.CPUPerCore {
		full *= float64(max(m.snap.CPUs, 1))
	}
	return []plugin.Accessory{
		{Text: l.T("accessory.cpu", "value", p.percent(cpu)), Meter: meter(cpu, full), Load: true},
		{Text: l.T("accessory.memory", "value", formatBytes(mem)), Meter: meter(float64(mem), float64(m.snap.MemTotal)), Load: true},
	}
}

// iconOf is the icon of a process: its executable's icon once drawn (in
// the background, then the list is pulled again), else the app icon.
func (p *Plugin) iconOf(x *proc) plugin.Icon {
	path := x.info.Path
	if path == "" {
		return plugin.Icon{Name: "app"}
	}
	p.mu.Lock()
	png, known := p.icons[path]
	if !known && !p.closed {
		p.icons[path] = ""
		p.goBackground(func() { p.drawIcon(path) })
	}
	p.mu.Unlock()
	if png == "" {
		return plugin.Icon{Name: "app"}
	}
	return plugin.Icon{Path: png}
}

// drawIcon draws the icon of the executable at path into the cache
// directory, named by a hash of the path.
func (p *Plugin) drawIcon(path string) {
	sum := sha256.Sum256([]byte(strings.ToLower(path)))
	file := filepath.Join(p.h.CacheDir(), "icons", hex.EncodeToString(sum[:12])+".png")
	if _, err := os.Stat(file); err != nil {
		data, err := p.b.Icon(path, iconSize)
		if err != nil || len(data) == 0 {
			return // stays the app icon
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return
		}
		if err := os.WriteFile(file, data, 0o644); err != nil {
			return
		}
	}
	p.mu.Lock()
	p.icons[path] = file
	p.mu.Unlock()
	p.h.Invalidate("")
}

// --- Actions ------------------------------------------------------------------

// actions are the actions of a row (x nil) or of one process x of it.
func (p *Plugin) actions(m *model, r *row, x *proc, where itemContext) []plugin.Action {
	l := p.h.I18n()
	subject := r.root
	group := x == nil && len(r.members) > 1
	wins := r.windows()
	hung := r.hung()
	members := len(r.members)
	if x != nil {
		subject, wins, hung, members = x, x.windows, x.hung(), 1
	}
	name := subject.display()
	pl := target{ID: subject.ID, Group: group, Name: name}
	endable := subject.class.Endable() && (x != nil || r.endable())
	var acts []plugin.Action

	var switchAct, showAct, endAct *plugin.Action
	if len(wins) > 0 {
		a := callback("switch", l.T("action.switch"), cbSwitch, target{ID: subject.ID, Name: name, HWND: wins[0].HWND}, "")
		switchAct = &a
	}
	if where != ctxGroup {
		a := plugin.Action{ID: "show", Title: l.T("action.showProcesses"), Shortcut: "Ctrl+D",
			Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewGroup, Payload: plugin.MustPayload(viewPayload{Root: r.root.ID})}}
		showAct = &a
	}
	if endable {
		a := callback("end-task", l.T("action.endTask"), cbEndTask, pl, "Ctrl+X")
		if len(wins) == 0 || hung {
			// Without a window to close, ending the task ends the process
			// at once: it asks first (docs/process-manager.md §14).
			a.Style = plugin.StyleDestructive
			a.ConfirmMessage = p.confirmMessage(m, r, x, members)
		}
		endAct = &a
	}
	// Enter: End task with "kill" and in the Not Responding view; else
	// switch to the window, or show the app's processes.
	switch {
	case where == ctxKill || where == ctxHung:
		if endAct != nil {
			acts = append(acts, *endAct)
			endAct = nil
		}
		if switchAct != nil {
			acts = append(acts, *switchAct)
			switchAct = nil
		}
	case switchAct != nil:
		acts = append(acts, *switchAct)
		switchAct = nil
	}
	if showAct != nil {
		acts = append(acts, *showAct)
	}
	if endAct != nil {
		acts = append(acts, *endAct)
	}
	if switchAct != nil {
		acts = append(acts, *switchAct)
	}
	if endable {
		force := callback("force-end", l.T("action.forceEnd"), cbForce, pl, "Ctrl+Shift+X")
		force.Style = plugin.StyleDestructive
		force.ConfirmMessage = p.confirmMessage(m, r, x, members)
		acts = append(acts, force)
		tree := process.Tree(m.snap.Procs, subject.ID)
		if len(tree) > 1 {
			t := callback("end-tree", l.T("action.endTree"), cbTree, target{ID: subject.ID, Name: name, Tree: true}, "Ctrl+Alt+X")
			t.Style = plugin.StyleDestructive
			t.ConfirmMessage = p.confirmMessage(m, nil, subject, len(tree))
			acts = append(acts, t)
		}
		p.mu.Lock()
		needs := p.needsAdmin[subject.ID]
		p.mu.Unlock()
		if needs {
			acts = append(acts, callback("end-elevated", l.T("action.endElevated"), cbElevate,
				target{ID: subject.ID, Name: name, Tree: group}, "Ctrl+Shift+A"))
		}
	}
	if subject.class == process.ClassShell {
		a := callback("restart-explorer", l.T("action.restartExplorer"), cbRestart, target{}, "Ctrl+X")
		a.Style = plugin.StyleDestructive
		a.ConfirmMessage = l.T("confirm.restartExplorer")
		acts = append(acts, a)
	}
	if path := subject.info.Path; path != "" {
		acts = append(acts, plugin.Action{ID: "reveal", Title: l.T("action.reveal"), Shortcut: "Ctrl+Shift+F",
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: path, Payload: plugin.MustPayload(plugin.OpenOptions{Reveal: true})}})
	}
	acts = append(acts, copyAction("copy-pid", l.T("action.copyPID"), strconv.FormatUint(uint64(subject.PID), 10), "Ctrl+Shift+C"))
	if path := subject.info.Path; path != "" {
		acts = append(acts, copyAction("copy-path", l.T("action.copyPath"), path, "Ctrl+Alt+C"))
	}
	acts = append(acts, copyAction("copy-name", l.T("action.copyName"), subject.Name, ""))
	if subject.class != process.ClassKernel {
		acts = append(acts, callback("copy-command-line", l.T("action.copyCommandLine"), cbCopyCmd, target{ID: subject.ID, Name: name}, ""))
	}
	acts = append(acts, plugin.Action{ID: "search-online", Title: l.T("action.searchOnline"),
		Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: "https://www.google.com/search?q=" + url.QueryEscape(`"`+subject.Name+`"`)}})
	if where != ctxRoot && where != ctxKill {
		acts = append(acts, p.listActions()...)
	}
	return acts
}

// listActions are the actions on the whole list: sorting, grouping, Task
// Manager, refresh.
func (p *Plugin) listActions() []plugin.Action {
	l := p.h.I18n()
	var acts []plugin.Action
	cur := p.sortOrder()
	for _, o := range sortOrders {
		if o != cur {
			a := callback("sort-"+o, l.T("action.sortBy", "order", sortText(l, o)), cbSort, target{Sort: o}, "")
			acts = append(acts, a)
		}
	}
	gt := l.T("action.groupOff")
	if !p.grouping() {
		gt = l.T("action.groupOn")
	}
	acts = append(acts, callback("toggle-group", gt, cbGroup, target{}, ""))
	acts = append(acts,
		plugin.Action{ID: "task-manager", Title: l.T("action.taskManager"), Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: "taskmgr.exe"}},
		plugin.Action{ID: "resource-monitor", Title: l.T("action.resourceMonitor"), Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: "resmon.exe"}},
	)
	refresh := callback("refresh", l.T("action.refresh"), cbRefresh, target{}, "Ctrl+R")
	refresh.ReadOnly = true
	acts = append(acts, refresh)
	return acts
}

// confirmMessage is the confirmation text of ending: how many processes
// end, and what the user should know about them.
func (p *Plugin) confirmMessage(m *model, r *row, x *proc, count int) string {
	l := p.h.I18n()
	lines := []string{l.N("confirm.end", count)}
	var subjects []*proc
	if x != nil {
		subjects = []*proc{x}
	} else if r != nil {
		subjects = r.members
	}
	notes := map[string]bool{}
	for _, s := range subjects {
		switch {
		case s.note != "" && !notes[s.note]:
			notes[s.note] = true
			lines = append(lines, noteText(l, s.note))
		case s.task && !notes["task"]:
			notes["task"] = true
			lines = append(lines, l.T("note.task"))
		}
	}
	return strings.Join(lines, " ")
}

// --- Details ------------------------------------------------------------------

// detail is the detail panel of a row (r set) or a process.
func (p *Plugin) detail(m *model, r *row, x *proc) *plugin.Detail {
	l := p.h.I18n()
	md := []plugin.Metadata{}
	add := func(label, value string) {
		if value != "" {
			md = append(md, plugin.Metadata{Label: label, Value: value})
		}
	}
	name := x.Name
	if x.info.Package != "" {
		name += "\n" + x.info.Package
	}
	add(l.T("meta.name"), name)
	cpu, mem, io := x.usage.CPU, x.PrivateWorkingSet, x.usage.IO
	if r != nil && len(r.members) > 1 {
		cpu, mem, io = r.cpu, r.mem, r.io
		add(l.T("meta.processes"), strconv.Itoa(len(r.members)))
	}
	add(l.T("meta.cpu"), p.percent(cpu))
	add(l.T("meta.memory"), formatBytes(mem))
	add(l.T("meta.workingSet"), formatBytes(x.WorkingSet))
	add(l.T("meta.commit"), formatBytes(x.Commit))
	add(l.T("meta.peak"), formatBytes(x.PeakWorkingSet))
	add(l.T("meta.io"), formatRate(io))
	add(l.T("meta.pid"), strconv.FormatUint(uint64(x.PID), 10))
	if x.Parent != 0 {
		if par, ok := process.ParentOf(m.snap.Procs, x.Proc); ok {
			pname := par.Name
			if pp := m.procs[par.ID]; pp != nil {
				pname = pp.display()
			}
			add(l.T("meta.parent"), l.T("meta.parentValue", "name", pname, "pid", par.PID))
		} else {
			add(l.T("meta.parent"), l.T("meta.parentExited", "pid", x.Parent))
		}
	}
	switch {
	case x.info.Path != "":
		add(l.T("meta.path"), x.info.Path)
	case x.class == process.ClassKernel:
	default:
		add(l.T("meta.path"), l.T("meta.pathDenied"))
	}
	user := x.info.User
	switch {
	case user != "":
	case x.Session == 0:
		user = l.T("meta.userService")
	default:
		user = l.T("meta.userOther")
	}
	switch x.info.Elevated {
	case process.Yes:
		user = l.T("meta.userElevated", "user", user)
	case process.No:
		user = l.T("meta.userNotElevated", "user", user)
	}
	add(l.T("meta.user"), user)
	add(l.T("meta.integrity"), x.info.Integrity)
	if x.Created > 0 && x.class != process.ClassKernel {
		now := p.h.Now()
		started := fromFiletime(x.Created).In(now.Location())
		add(l.T("meta.started"), l.T("meta.startedValue", "time", started.Format("2006-01-02 15:04"), "ago", l.RelativeTime(started, now)))
	}
	add(l.T("meta.handles"), l.Number(float64(x.Handles)))
	add(l.T("meta.threads"), l.Number(float64(x.Threads)))
	wins := x.windows
	if r != nil {
		wins = r.windows()
	}
	if len(wins) > 0 {
		var titles []string
		for i, w := range wins {
			if i == 5 {
				break
			}
			titles = append(titles, w.Title)
		}
		md = append(md, plugin.Metadata{Label: l.T("meta.windows"), Value: strings.Join(titles, "\n"), Sensitive: true})
	}
	add(l.T("meta.ports"), strings.Join(x.ports, ", "))
	if x.class != process.ClassKernel {
		cmd := p.commandLine(x)
		if cmd != "" {
			md = append(md, plugin.Metadata{Label: l.T("meta.commandLine"), Value: cmd, Sensitive: true})
		}
	}
	var note string
	switch {
	case x.class == process.ClassSelf:
		note = l.T("note.self")
	case x.class == process.ClassCritical:
		note = l.T("note.critical")
	case x.class == process.ClassKernel:
		note = l.T("note.kernel")
	case x.class == process.ClassShell:
		note = l.T("note.shell")
	case x.task:
		note = l.T("note.task")
	case x.note != "":
		note = noteText(l, x.note)
	}
	add(l.T("meta.note"), note)
	if cpu >= 0 && p.cpuMode() == process.CPUTotal {
		add(l.T("meta.cpuNote"), l.T("meta.cpuNoteValue"))
	}
	return &plugin.Detail{Metadata: md}
}

// commandLine is the command line of x as shown: read once per process,
// with secrets hidden unless the user turned that off.
func (p *Plugin) commandLine(x *proc) string {
	p.mu.Lock()
	read := x.info.cmdRead
	p.mu.Unlock()
	if !read {
		cmd, err := p.b.CommandLine(x.ID)
		p.mu.Lock()
		x.info.cmd, x.info.cmdErr, x.info.cmdRead = cmd, err, true
		p.mu.Unlock()
	}
	p.mu.Lock()
	cmd, err := x.info.cmd, x.info.cmdErr
	p.mu.Unlock()
	if err != nil {
		return p.h.I18n().T("meta.commandLineDenied")
	}
	if p.h.Preferences().Bool(prefRedact) {
		cmd = process.RedactCommandLine(cmd)
	}
	return cmd
}

// --- Views ------------------------------------------------------------------------

// processesView is the Processes view: the summary, then the apps,
// background processes and Windows processes; a search lists the matches,
// best first.
func (p *Plugin) processesView(v viewState) plugin.View {
	l := p.h.I18n()
	m := p.current()
	out := plugin.View{Kind: plugin.ViewList, Title: l.T("view.title"), Placeholder: l.T("view.placeholder"),
		ShowDetail: true, EmptyText: l.T("view.empty")}
	q := strings.TrimSpace(v.query)
	rows := p.visibleRows(m, q)
	if q != "" {
		s := plugin.Section{}
		for _, r := range rows {
			s.Items = append(s.Items, p.rowItem(m, r, ctxView))
		}
		if len(s.Items) > 0 {
			out.Sections = []plugin.Section{s}
		}
		return out
	}
	sum := p.summaryItem(m)
	list := p.listActions()
	sum.Actions = append([]plugin.Action{list[len(list)-1]}, list[:len(list)-1]...) // Enter refreshes
	out.Sections = append(out.Sections, plugin.Section{Items: []plugin.Item{sum}})
	for sec := secApps; sec <= secWindows; sec++ {
		var s plugin.Section
		for _, r := range rows {
			if r.section == sec {
				s.Items = append(s.Items, p.rowItem(m, r, ctxView))
			}
		}
		if len(s.Items) > 0 {
			switch sec {
			case secApps:
				s.Title = l.T("section.apps", "count", len(s.Items))
			case secBackground:
				s.Title = l.T("section.background", "count", len(s.Items))
			default:
				s.Title = l.T("section.windows", "count", len(s.Items))
			}
			out.Sections = append(out.Sections, s)
		}
	}
	return out
}

// groupView lists the processes of one app as a tree.
func (p *Plugin) groupView(v viewState) plugin.View {
	l := p.h.I18n()
	m := p.current()
	out := plugin.View{Kind: plugin.ViewList, Title: l.T("view.title"), Placeholder: l.T("view.placeholder"),
		ShowDetail: true, EmptyText: l.T("view.groupGone")}
	r := m.byRoot[v.root]
	if r == nil {
		if x := m.procs[v.root]; x != nil {
			r = &row{root: x, members: []*proc{x}, depth: map[process.ID]int{}}
		}
	}
	if r == nil {
		return out
	}
	out.Title = r.title(l)
	q := strings.TrimSpace(v.query)
	var s plugin.Section
	for _, x := range r.members {
		single := &row{root: x, members: []*proc{x}, depth: map[process.ID]int{}}
		if q != "" && matchRow(single, q) == 0 {
			continue
		}
		s.Items = append(s.Items, p.memberItem(m, r, x))
	}
	if len(s.Items) > 0 {
		out.Sections = []plugin.Section{s}
	}
	return out
}

// hungView lists the apps not responding, with "End all".
func (p *Plugin) hungView(v viewState) plugin.View {
	l := p.h.I18n()
	m := p.current()
	out := plugin.View{Kind: plugin.ViewList, Title: l.T("view.hungTitle"), Placeholder: l.T("view.placeholder"),
		ShowDetail: true, EmptyText: l.T("view.hungEmpty")}
	var s plugin.Section
	n := 0
	for _, r := range p.visibleRows(m, v.query) {
		if !r.hung() || !r.endable() {
			continue
		}
		n++
		s.Items = append(s.Items, p.rowItem(m, r, ctxHung))
	}
	if n > 1 {
		all := callback("end-all", l.N("action.endAllHung", n), cbEndAllHung, target{}, "")
		all.Style = plugin.StyleDestructive
		all.ConfirmMessage = l.N("confirm.endAllHung", n)
		s.Items = append([]plugin.Item{{ID: "end-all-hung", Title: l.N("action.endAllHung", n),
			Icon: plugin.Icon{Name: "circle-stop", Tint: plugin.TintDanger}, Actions: []plugin.Action{all}}}, s.Items...)
	}
	if len(s.Items) > 0 {
		out.Sections = []plugin.Section{s}
	}
	return out
}

// sortText names a sort order.
func sortText(l plugin.Localizer, order string) string {
	switch order {
	case sortMemory:
		return l.T("sort.memory")
	case sortIO:
		return l.T("sort.io")
	case sortName:
		return l.T("sort.name")
	}
	return l.T("sort.cpu")
}

// noteText is the warning about ending a Windows component (the notes of
// process.Classify).
func noteText(l plugin.Localizer, note string) string {
	switch note {
	case "svchost":
		return l.T("note.svchost")
	case "dwm":
		return l.T("note.dwm")
	case "ctfmon":
		return l.T("note.ctfmon")
	case "sihost":
		return l.T("note.sihost")
	case "startmenu":
		return l.T("note.startmenu")
	case "shellexperience":
		return l.T("note.shellexperience")
	case "search":
		return l.T("note.search")
	case "textinput":
		return l.T("note.textinput")
	case "audio":
		return l.T("note.audio")
	case "spooler":
		return l.T("note.spooler")
	}
	return ""
}

// --- Formatting ---------------------------------------------------------------

// percent formats a percentage with one decimal ("3.2%"), "—" when unknown.
func (p *Plugin) percent(v float64) string {
	if v < 0 {
		return "—"
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + "%"
}

// formatBytes formats a size as Windows does, in binary units ("1.2 GB",
// "42 MB"); the units are not translated.
func formatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	units := []string{"KB", "MB", "GB", "TB"}
	i := -1
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// formatRate formats bytes per second, "—" when unknown.
func formatRate(v float64) string {
	if v < 0 {
		return "—"
	}
	return formatBytes(uint64(v)) + "/s"
}

// fromFiletime converts a FILETIME to a time.
func fromFiletime(ft int64) time.Time {
	return time.Unix(0, (ft-116444736000000000)*100)
}
