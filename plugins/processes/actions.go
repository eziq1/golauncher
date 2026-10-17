package processes

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/process"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// explorerWait is how long Restart Windows Explorer waits for Windows to
// start a new taskbar before starting explorer.exe itself.
const explorerWait = 2 * time.Second

// members returns the processes an action on t ends: the processes of the
// app whose root is t.ID (t.Group), else t.ID alone; and their windows.
func (p *Plugin) members(t target) ([]process.ID, []process.Window, bool) {
	m := p.current()
	if t.Group {
		if r := m.byRoot[t.ID]; r != nil {
			return r.ids(), r.windows(), r.hung()
		}
	}
	if x := m.procs[t.ID]; x != nil {
		return []process.ID{x.ID}, x.windows, x.hung()
	}
	return []process.ID{t.ID}, nil, false
}

func failure(title, message string) plugin.Result {
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: title, Message: message}}
}

// endTask asks the app to close (WM_CLOSE to its windows) and waits up to
// closeTimeout for it to exit; it offers Force end when the app is still
// running (it may be asking to save). An app not responding, or a process
// without windows, is ended at once (docs/process-manager.md §5.1).
func (p *Plugin) endTask(t target) plugin.Result {
	l := p.h.I18n()
	ids, wins, hung := p.members(t)
	if len(wins) == 0 || hung {
		return p.forceEnd(t)
	}
	hwnds := make([]uintptr, len(wins))
	for i, w := range wins {
		hwnds[i] = w.HWND
	}
	if err := p.b.CloseWindows(hwnds); err != nil {
		return failure(l.T("toast.failed", "name", t.Name), err.Error())
	}
	wait := func() plugin.Toast {
		ctx, cancel := context.WithTimeout(p.bg, p.closeTimeout())
		defer cancel()
		exited := p.b.Wait(ctx, ids)
		p.sample()
		p.h.Invalidate("")
		if len(exited) >= len(ids) {
			return plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.ended", "name", t.Name)}
		}
		return plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.stillRunning", "name", t.Name), Message: l.T("toast.stillRunningHint")}
	}
	if p.opts.Sync {
		toast := wait()
		return plugin.Result{Refresh: true, Toast: &toast}
	}
	p.goBackground(func() { p.h.Toast(wait()) })
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.closing", "name", t.Name)}}
}

// forceEnd ends the processes of t at once.
func (p *Plugin) forceEnd(t target) plugin.Result {
	ids, _, _ := p.members(t)
	var rep process.KillReport
	for _, id := range ids {
		rep.Add(id, p.b.Kill(id))
	}
	return p.report(t, rep)
}

// endTree ends t.ID and all its descendants.
func (p *Plugin) endTree(t target) plugin.Result {
	if x := p.current().procs[t.ID]; x != nil && !x.class.Endable() {
		// Never the descendants of a process that must not be ended
		// (golauncher's own background tasks under golauncher, say).
		var rep process.KillReport
		rep.Add(t.ID, process.ErrProtected)
		return p.report(t, rep)
	}
	return p.report(t, p.b.KillTree(t.ID))
}

// report turns the outcome of ending processes into a toast; when rights
// were missing, "End as administrator" is offered from then on.
func (p *Plugin) report(t target, rep process.KillReport) plugin.Result {
	l := p.h.I18n()
	total := len(rep.Ended) + len(rep.Gone) + len(rep.NeedsAdmin) + len(rep.Protected) + len(rep.Failed)
	if len(rep.NeedsAdmin) > 0 {
		p.mu.Lock()
		p.needsAdmin[t.ID] = true
		p.mu.Unlock()
	}
	p.sample()
	res := plugin.Result{Refresh: true}
	switch {
	case len(rep.Ended) == total:
		if total == 1 {
			res.Toast = &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.ended", "name", t.Name)}
		} else {
			res.Toast = &plugin.Toast{Style: plugin.ToastSuccess, Title: l.N("toast.endedCount", total, "name", t.Name)}
		}
	case len(rep.Gone) == total:
		res.Toast = &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.gone", "name", t.Name)}
	case len(rep.Ended) == 0 && len(rep.NeedsAdmin) > 0:
		res.Toast = &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("toast.needsAdmin", "name", t.Name), Message: l.T("toast.needsAdminHint")}
	case len(rep.Ended) == 0 && len(rep.Protected) > 0:
		res.Toast = &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("toast.protected", "name", t.Name)}
	case len(rep.Ended) == 0:
		res.Toast = &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("toast.failed", "name", t.Name)}
	default:
		msg := l.N("toast.partialMessage", len(rep.NeedsAdmin)+len(rep.Protected)+len(rep.Failed))
		if len(rep.NeedsAdmin) > 0 {
			msg += " " + l.T("toast.needsAdminHint")
		}
		res.Toast = &plugin.Toast{Style: plugin.ToastInfo, Title: l.N("toast.partial", len(rep.Ended), "name", t.Name), Message: msg}
	}
	return res
}

// endElevated ends t with administrator rights: golauncher's hidden kill
// helper runs through a UAC prompt (docs/process-manager.md §5.5).
func (p *Plugin) endElevated(t target) plugin.Result {
	l := p.h.I18n()
	run := func() plugin.Toast {
		ctx := process.WithPrompt(p.bg, l.T("elevate.prompt", "name", t.Name))
		err := p.b.Elevate(ctx, process.HelperRequest{ID: t.ID, Tree: t.Tree})
		p.mu.Lock()
		delete(p.needsAdmin, t.ID)
		p.mu.Unlock()
		p.sample()
		p.h.Invalidate("")
		switch {
		case err == nil:
			return plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.ended", "name", t.Name)}
		case errors.Is(err, process.ErrCancelled):
			return plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.cancelled")}
		case errors.Is(err, process.ErrGone):
			return plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.gone", "name", t.Name)}
		case errors.Is(err, process.ErrProtected):
			return plugin.Toast{Style: plugin.ToastFailure, Title: l.T("toast.protectedAdmin", "name", t.Name)}
		}
		return plugin.Toast{Style: plugin.ToastFailure, Title: l.T("toast.failed", "name", t.Name), Message: err.Error()}
	}
	if p.opts.Sync {
		toast := run()
		return plugin.Result{Refresh: true, Toast: &toast}
	}
	p.goBackground(func() { p.h.Toast(run()) })
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.elevating", "name", t.Name)}}
}

// switchTo brings the window to the front and hides the launcher.
func (p *Plugin) switchTo(t target) plugin.Result {
	if err := p.b.SwitchTo(t.HWND); err != nil {
		return failure(p.h.I18n().T("toast.switchFailed", "name", t.Name), err.Error())
	}
	return plugin.Result{Hide: true}
}

// copyCommandLine copies the whole command line, read only now, as a
// secret: it may hold passwords, so clipboard history does not record it.
func (p *Plugin) copyCommandLine(t target) (plugin.Result, error) {
	l := p.h.I18n()
	cmd, err := p.b.CommandLine(t.ID)
	switch {
	case errors.Is(err, process.ErrGone):
		return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("toast.gone", "name", t.Name)}}, nil
	case err != nil:
		return failure(l.T("toast.commandLineFailed", "name", t.Name), l.T("meta.commandLineDenied")), nil
	}
	if err := p.h.Clipboard().WriteSecret(cmd); err != nil {
		return plugin.Result{}, err
	}
	return plugin.Result{Hide: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.copiedCommandLine")}}, nil
}

// endAllHung ends every app not responding.
func (p *Plugin) endAllHung() plugin.Result {
	m := p.current()
	var rep process.KillReport
	n := 0
	for _, r := range m.rows {
		if !r.hung() || !r.endable() {
			continue
		}
		n++
		for _, id := range r.ids() {
			rep.Add(id, p.b.Kill(id))
		}
	}
	return p.report(target{Name: p.h.I18n().N("toast.hungApps", n)}, rep)
}

// restartExplorer ends the explorer.exe owning the taskbar and, when
// Windows does not start a new one within explorerWait, starts it.
func (p *Plugin) restartExplorer() plugin.Result {
	l := p.h.I18n()
	p.sample()
	m := p.current()
	var shell *proc
	for _, x := range m.procs {
		if x.class == process.ClassShell {
			shell = x
			break
		}
	}
	if shell == nil {
		if err := p.b.StartExplorer(); err != nil {
			return failure(l.T("toast.explorerFailed"), err.Error())
		}
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.explorerStarted")}}
	}
	if err := p.b.Kill(shell.ID); err != nil && !errors.Is(err, process.ErrGone) {
		return failure(l.T("toast.explorerFailed"), err.Error())
	}
	restart := func() {
		if !p.opts.Sync {
			select {
			case <-p.bg.Done():
				return
			case <-p.opts.After(explorerWait):
			}
		}
		wins, _ := p.b.Windows()
		for _, w := range wins {
			if w.Shell {
				return // Windows started it again
			}
		}
		if err := p.b.StartExplorer(); err != nil {
			p.h.Logger().Warn("processes: start explorer", "err", err)
		}
	}
	if p.opts.Sync {
		restart()
	} else {
		p.goBackground(restart)
	}
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.explorerRestarted")}}
}

// --- The foreground app -----------------------------------------------------

// fgCache holds the items about the foreground app of the current show.
type fgCache struct {
	pid   int
	name  string
	quit  []plugin.Item
	force []plugin.Item
}

// ContextItems implements plugin.ContextProvider: "Force quit <app> (not
// responding)" when the window that was in front is not responding.
func (p *Plugin) ContextItems(_ context.Context, c plugin.Context) ([]plugin.Item, error) {
	f := p.foreground(&c)
	if f == nil {
		return nil, nil
	}
	return f.force, nil
}

// foregroundItems is "Quit <app>" for the app that was in front, matched
// by the host against "quit", "close app", "退出", "结束"….
func (p *Plugin) foregroundItems(c *plugin.Context, _ bool) []plugin.Item {
	f := p.foreground(c)
	if f == nil {
		return nil
	}
	return f.quit
}

// foreground builds (once per show) the items about the foreground app.
func (p *Plugin) foreground(c *plugin.Context) *fgCache {
	if c == nil || !p.h.Preferences().Bool(prefForegroundQuit) {
		return nil
	}
	key := c.App.PID
	p.mu.Lock()
	if f := p.fg; f != nil && f.pid == key && f.name == c.App.Name {
		p.mu.Unlock()
		return f
	}
	p.mu.Unlock()
	f := p.buildForeground(c)
	p.mu.Lock()
	p.fg = f
	p.mu.Unlock()
	return f
}

func (p *Plugin) buildForeground(c *plugin.Context) *fgCache {
	f := &fgCache{pid: c.App.PID, name: c.App.Name}
	name := strings.ToLower(c.App.Name)
	if name == "" || name == "explorer.exe" || c.Kind == plugin.ContextDesktop {
		return f // the taskbar and desktop: Restart Windows Explorer instead
	}
	if p.b.Mac() && name == "finder" {
		return f // the desktop of macOS, which starts it again at once
	}
	pid := uint32(c.App.PID)
	if pid == 0 { // an injected context (glctl --context-app) names the app only
		m := p.current()
		for _, x := range m.procs {
			if strings.EqualFold(x.Name, c.App.Name) && len(x.windows) > 0 && (pid == 0 || x.PID < pid) {
				pid = x.PID
			}
		}
	}
	if pid == 0 || pid == p.b.Self() {
		return f
	}
	id, err := p.b.Identify(pid)
	if err != nil {
		return f
	}
	info := p.b.Info(id)
	exe := c.App.Name
	class, _ := process.Classify(process.Facts{ID: id, Name: exe, Path: info.Path, Critical: info.Critical,
		Self: p.b.Self(), SystemRoot: p.b.SystemRoot()})
	if !class.Endable() {
		return f
	}
	display := strings.TrimSpace(info.Description)
	if display == "" {
		display = strings.TrimSuffix(exe, ".exe")
	}
	wins, _ := p.b.Windows()
	var own []process.Window
	hung := false
	for _, w := range wins {
		if w.PID == pid && !w.Shell {
			own = append(own, w)
			hung = hung || w.Hung
		}
	}
	l := p.h.I18n()
	t := target{ID: id, Name: display}
	force := callback("force-end", l.T("action.forceEnd"), cbForce, t, "Ctrl+Shift+X")
	force.Style = plugin.StyleDestructive
	force.ConfirmMessage = l.N("confirm.end", 1)
	end := callback("end-task", l.T("action.endTask"), cbEndTask, t, "")
	if len(own) == 0 || hung {
		end.Style, end.ConfirmMessage = plugin.StyleDestructive, force.ConfirmMessage
	}
	keywords := []string{"quit", "close app", "kill", "end task", "退出", "关闭", "结束", "tc", "js", l.T("fg.keywords")}
	f.quit = []plugin.Item{{ID: "quit:" + itemID(id), Title: l.T("fg.quit", "name", display), Subtitle: l.T("fg.quitSubtitle"),
		Icon: plugin.Icon{Name: "circle-stop"}, Keywords: keywords, Transient: true, Actions: []plugin.Action{end, force}}}
	if hung {
		f.force = []plugin.Item{{ID: "force-quit:" + itemID(id), Title: l.T("fg.forceQuit", "name", display),
			Subtitle: l.T("fg.forceQuitSubtitle"), Icon: plugin.Icon{Name: "alert", Tint: plugin.TintDanger},
			Keywords: keywords, Transient: true, Actions: []plugin.Action{force}}}
	}
	return f
}
