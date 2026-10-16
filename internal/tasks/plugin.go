package tasks

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// PluginID is the ID of the host-provided plugin that shows the tasks.
const PluginID = "builtin.tasks"

// Views of the plugin.
const (
	ViewList = "tasks" // all tasks; also the command name
	ViewTask = "task"  // one task; Payload: taskPayload
)

const (
	callbackStop   = "stop"
	callbackRerun  = "rerun"
	callbackRemove = "remove"
	callbackClear  = "clear"
	callbackCopy   = "copy"
	// callbackRetryAdmin reruns a task that needs administrator rights with
	// ProcessSpec.Elevate (Manager.RetryElevated).
	callbackRetryAdmin = "retry-admin"

	// tailLines is how much output the detail pane shows.
	tailLines = 300
)

// renderEvery is how often open views are pushed again while tasks change;
// the elapsed time of running tasks is updated every second.
var renderEvery = 250 * time.Millisecond

// taskPayload identifies a task in actions and in ViewTask.
type taskPayload struct {
	ID string `json:"id"`
}

// Plugin is builtin.tasks: the Background Tasks command. Its views follow
// the tasks live through Host.Render.
type Plugin struct {
	m *Manager

	h      plugin.Host
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	dirty  atomic.Bool

	mu         sync.Mutex
	views      map[string]plugin.ViewRequest // open view instances
	lastRender time.Time
}

var (
	_ plugin.Plugin        = (*Plugin)(nil)
	_ plugin.ViewProvider  = (*Plugin)(nil)
	_ plugin.ActionHandler = (*Plugin)(nil)
)

// NewPlugin returns the plugin showing the tasks of m.
func NewPlugin(m *Manager) *Plugin {
	return &Plugin{m: m, views: map[string]plugin.ViewRequest{}}
}

// View returns the view showing task id, or the list of all tasks for "";
// with RunProcess and Indicator it implements core.TaskService.
func (m *Manager) View(id string) (string, plugin.ViewRequest) {
	if id == "" {
		return PluginID, plugin.ViewRequest{ID: ViewList}
	}
	return PluginID, plugin.ViewRequest{ID: ViewTask, Payload: plugin.MustPayload(taskPayload{ID: id})}
}

// Manifest implements plugin.Plugin. Its strings are in the host's catalog.
func (p *Plugin) Manifest() plugin.Manifest {
	icon := plugin.Icon{Name: "activity"}
	return plugin.Manifest{
		ID:          PluginID,
		Name:        "%tasks.plugin.name%",
		Description: "%tasks.plugin.description%",
		Version:     "1.0.0",
		Icon:        icon,
		Commands: []plugin.Command{{
			Name: ViewList, Title: "%tasks.command.title%", Mode: plugin.ModeView, Icon: icon,
			Keywords: []string{"tasks", "%tasks.command.keywords%"},
		}},
	}
}

// Init implements plugin.Plugin.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.m.OnChange(func(bool) { p.dirty.Store(true) })
	p.wg.Add(1)
	h.Go(func() {
		defer p.wg.Done()
		p.renderLoop()
	})
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close() error {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	return nil
}

// ViewClosed is called by the engine when a view instance closes.
func (p *Plugin) ViewClosed(instance string) {
	p.mu.Lock()
	delete(p.views, instance)
	p.mu.Unlock()
}

// renderLoop pushes the open views again when tasks changed, and every
// second while one runs (its elapsed time).
func (p *Plugin) renderLoop() {
	tick := time.NewTicker(renderEvery)
	defer tick.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-tick.C:
		}
		p.mu.Lock()
		views := make(map[string]plugin.ViewRequest, len(p.views))
		for k, v := range p.views {
			views[k] = v
		}
		clock := time.Since(p.lastRender) >= time.Second && p.m.Active() > 0
		p.mu.Unlock()
		if len(views) == 0 || !(p.dirty.Swap(false) || clock) {
			continue
		}
		p.mu.Lock()
		p.lastRender = time.Now()
		p.mu.Unlock()
		for instance, req := range views {
			p.h.Render(instance, p.view(req))
		}
	}
}

// View implements plugin.ViewProvider. Opening a task view marks failed
// tasks as seen, which clears the indicator's red dot.
func (p *Plugin) View(_ context.Context, req plugin.ViewRequest) (plugin.View, error) {
	if req.ID != ViewList && req.ID != ViewTask {
		return plugin.View{}, fmt.Errorf("tasks: unknown view %q", req.ID)
	}
	if req.Instance != "" {
		p.mu.Lock()
		p.views[req.Instance] = req
		p.mu.Unlock()
	}
	p.m.MarkSeen()
	return p.view(req), nil
}

func (p *Plugin) view(req plugin.ViewRequest) plugin.View {
	l := p.h.I18n()
	now := p.h.Now()
	if req.ID == ViewTask {
		var tp taskPayload
		_ = json.Unmarshal(req.Payload, &tp)
		i, ok := p.m.Get(tp.ID)
		v := plugin.View{Kind: plugin.ViewList, Title: l.T("tasks.view.title"), ShowDetail: true, EmptyText: l.T("tasks.view.gone")}
		if ok {
			it := p.item(l, i, now)
			it.Actions = append(it.Actions, plugin.Action{ID: "show-all", Title: l.T("tasks.action.showAll"), Shortcut: "Ctrl+J",
				Do: plugin.ActionDo{Kind: plugin.ActionShowTask}})
			v.Title = i.Title
			v.Sections = []plugin.Section{{Items: []plugin.Item{it}}}
		}
		return v
	}
	var running, queued, finished []plugin.Item
	for _, i := range p.m.List() {
		it := p.item(l, i, now)
		switch i.State {
		case plugin.TaskRunning:
			running = append(running, it)
		case plugin.TaskQueued:
			queued = append(queued, it)
		default:
			finished = append(finished, it)
		}
	}
	v := plugin.View{
		Kind: plugin.ViewList, Title: l.T("tasks.view.title"), Placeholder: l.T("tasks.view.placeholder"),
		ShowDetail: true, FilterLocally: true, EmptyText: l.T("tasks.view.empty"),
	}
	for _, s := range []plugin.Section{
		{Title: l.T("tasks.section.running"), Items: running},
		{Title: l.T("tasks.section.queued"), Items: queued},
		{Title: l.T("tasks.section.finished"), Items: finished},
	} {
		if len(s.Items) > 0 {
			v.Sections = append(v.Sections, s)
		}
	}
	return v
}

// icon is the task's icon, tinted by its state.
func icon(i Info) plugin.Icon {
	ic := i.Icon
	if ic.Path != "" {
		return ic
	}
	if ic.Name == "" {
		ic.Name = "activity"
	}
	switch i.State {
	case plugin.TaskRunning:
		ic.Tint = plugin.TintAccent
	case plugin.TaskQueued, plugin.TaskCancelled:
		ic.Tint = plugin.TintMuted
	case plugin.TaskSucceeded:
		ic.Tint = plugin.TintSuccess
	case plugin.TaskFailed:
		ic.Tint = plugin.TintDanger
	}
	return ic
}

// item is the row of a task.
func (p *Plugin) item(l plugin.Localizer, i Info, now time.Time) plugin.Item {
	state := StateText(l, i)
	subtitle := i.Subtitle
	if !i.State.Finished() || subtitle == "" {
		subtitle = state
	}
	payload := plugin.MustPayload(taskPayload{ID: i.ID})
	cb := func(target string) plugin.ActionDo {
		return plugin.ActionDo{Kind: plugin.ActionCallback, Target: target, Payload: payload}
	}
	var actions []plugin.Action
	if i.RetryableElevated() {
		// First, so that Enter retries: the UAC prompt confirms it.
		actions = append(actions, plugin.Action{ID: "retry-admin", Title: l.T("tasks.action.retryAdmin"), Do: cb(callbackRetryAdmin)})
	}
	if i.LogPath != "" {
		actions = append(actions, plugin.Action{ID: "open-log", Title: l.T("tasks.action.openLog"),
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: i.LogPath}})
	}
	actions = append(actions, plugin.Action{ID: "copy-output", Title: l.T("tasks.action.copyOutput"), Shortcut: "Ctrl+Shift+C", Do: cb(callbackCopy)})
	if !i.State.Finished() {
		actions = append(actions, plugin.Action{ID: "stop", Title: l.T("tasks.action.stop"), Shortcut: "Ctrl+X",
			Style: plugin.StyleDestructive, Do: cb(callbackStop)})
	}
	if i.Rerunnable() {
		actions = append(actions, plugin.Action{ID: "rerun", Title: l.T("tasks.action.rerun"), Shortcut: "Ctrl+R", Do: cb(callbackRerun)})
	}
	if i.State.Finished() {
		actions = append(actions,
			plugin.Action{ID: "remove", Title: l.T("tasks.action.remove"), Do: cb(callbackRemove)},
			plugin.Action{ID: "clear-finished", Title: l.T("tasks.action.clearFinished"), Do: cb(callbackClear)},
		)
	}
	keywords := []string{i.Plugin}
	if i.Queue != "" {
		keywords = append(keywords, i.Queue)
	}
	return plugin.Item{
		ID:        i.ID,
		Title:     i.Title,
		Subtitle:  subtitle,
		Icon:      icon(i),
		Keywords:  keywords,
		Accessory: Accessory(l, i, now),
		Actions:   actions,
		Detail:    p.detail(l, i, now, state),
	}
}

// fenced puts text in a Markdown code block.
func fenced(text string) string {
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + "text\n" + text + "\n" + fence
}

// detail is the detail pane of a task: the end of its output and what it
// runs.
func (p *Plugin) detail(l plugin.Localizer, i Info, now time.Time, state string) *plugin.Detail {
	text, more := p.m.Output(i.ID, tailLines)
	if text == "" {
		text = l.T("tasks.output.empty")
	}
	md := fenced(text)
	if more {
		md = l.T("tasks.output.truncated") + "\n\n" + md
	}
	var meta []plugin.Metadata
	add := func(label, value string) {
		if value != "" {
			meta = append(meta, plugin.Metadata{Label: label, Value: value})
		}
	}
	add(l.T("tasks.meta.status"), state)
	if i.Cmd != nil {
		add(l.T("tasks.meta.command"), CommandLine(*i.Cmd))
		add(l.T("tasks.meta.folder"), i.Cmd.Dir)
		if i.Cmd.Elevate {
			add(l.T("tasks.meta.rights"), l.T("tasks.meta.rightsAdmin"))
		}
	}
	if !i.Started.IsZero() {
		add(l.T("tasks.meta.started"), i.Started.Format(l.T("tasks.time.layout")))
		end := i.Ended
		if !i.State.Finished() {
			end = now
		}
		add(l.T("tasks.meta.duration"), Duration(l, end.Sub(i.Started)))
	}
	if i.State.Finished() && i.Kind == KindProcess && i.ExitCode >= 0 {
		add(l.T("tasks.meta.exitCode"), strconv.Itoa(i.ExitCode))
	}
	add(l.T("tasks.meta.queue"), i.Queue)
	add(l.T("tasks.meta.plugin"), i.Plugin)
	add(l.T("tasks.meta.log"), i.LogPath)
	return &plugin.Detail{Markdown: md, Metadata: meta}
}

// HandleAction implements plugin.ActionHandler.
func (p *Plugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	if a.Kind != plugin.ActionCallback {
		return plugin.Result{}, fmt.Errorf("tasks: unknown action %s %q", a.Kind, a.Target)
	}
	var tp taskPayload
	if err := json.Unmarshal(a.Payload, &tp); err != nil {
		return plugin.Result{}, fmt.Errorf("tasks: bad payload %q", a.Payload)
	}
	l := p.h.I18n()
	switch a.Target {
	case callbackStop:
		return plugin.Result{Refresh: true}, p.m.Stop(tp.ID)
	case callbackRerun, callbackRetryAdmin:
		rerun := p.m.Rerun
		if a.Target == callbackRetryAdmin {
			rerun = p.m.RetryElevated
		}
		id, err := rerun(tp.ID)
		if err != nil {
			return plugin.Result{}, err
		}
		i, _ := p.m.Get(id)
		return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastInfo, Title: l.T("tasks.toast.started", "title", i.Title)},
			Do: &plugin.ActionDo{Kind: plugin.ActionShowTask, Target: id}}, nil
	case callbackRemove:
		if err := p.m.Remove(tp.ID); err != nil {
			return plugin.Result{}, err
		}
		return plugin.Result{Refresh: true}, nil
	case callbackClear:
		n := p.m.ClearFinished()
		return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.N("tasks.toast.cleared", n)}}, nil
	case callbackCopy:
		text, _ := p.m.Output(tp.ID, 0)
		return plugin.Result{Do: &plugin.ActionDo{Kind: plugin.ActionCopyText, Target: text}}, nil
	}
	return plugin.Result{}, fmt.Errorf("tasks: unknown action %q", a.Target)
}
