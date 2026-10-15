package scripts

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

var (
	// compactTimeout bounds compact runs, which the action waits for (the
	// host gives an action 30 seconds).
	compactTimeout = 25 * time.Second
	// invalidateEvery throttles refreshes of the output view.
	invalidateEvery = 100 * time.Millisecond
	// waitDelay is how long output is read after the script exited: a
	// program it started and left running may hold the pipe open.
	waitDelay = 2 * time.Second
)

const (
	// keepRuns is how many finished runs stay viewable.
	keepRuns = 10
	// viewTail is how much of the output the view shows.
	viewTail = 32 << 10
)

// run is one execution of a script.
type run struct {
	id      string
	script  Script
	out     *output
	cmd     *exec.Cmd
	group   *procGroup
	started time.Time
	done    chan struct{}
	dirty   atomic.Bool

	mu       sync.Mutex
	finished bool
	ended    time.Time
	exitCode int
	err      error // the process could not be waited for
	stopped  bool
}

// stop ends the script and everything it started.
func (r *run) stop() {
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return
	}
	r.stopped = true
	r.mu.Unlock()
	r.group.kill()
	if r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
}

// wait blocks until the script exited.
func (r *run) wait() { <-r.done }

// state returns a snapshot of the run's status.
func (r *run) state() (finished, stopped bool, code int, err error, ended time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finished, r.stopped, r.exitCode, r.err, r.ended
}

// argValues returns the script arguments from the action's values, in
// order. Missing optional values are passed as empty strings.
func argValues(s *Script, values map[string]jsontext.Value) []string {
	args := make([]string, 0, len(s.Arguments))
	for _, a := range s.Arguments {
		var v string
		if raw, ok := values[argName(a.Index)]; ok {
			if err := json.Unmarshal(raw, &v); err != nil {
				v = strings.TrimSpace(string(raw)) // a number or bool
			}
		}
		if a.PercentEncoded {
			v = strings.ReplaceAll(url.QueryEscape(v), "+", "%20")
		}
		args = append(args, v)
	}
	return args
}

// findScript returns the scanned script at path, or parses it.
func (p *Plugin) findScript(path string) (Script, error) {
	p.mu.Lock()
	i := slices.IndexFunc(p.scripts, func(s Script) bool { return s.Path == path })
	var s Script
	if i >= 0 {
		s = p.scripts[i]
	}
	p.mu.Unlock()
	if i >= 0 {
		return s, nil
	}
	s, ok, err := parseFile(path)
	if err == nil && !ok {
		err = errors.New("not a script command")
	}
	return s, err
}

// runScript runs the script at path in its mode.
func (p *Plugin) runScript(ctx context.Context, path string, values map[string]jsontext.Value) (plugin.Result, error) {
	t := p.h.I18n()
	s, err := p.findScript(path)
	if err != nil {
		return failure(t.T("toast.runFailed", "name", filepath.Base(path)), err.Error()), nil
	}
	it := p.opts.System.interpreter(&s)
	if it.Missing != "" {
		return failure(t.T("toast.runFailed", "name", s.Title), t.T("error.noInterpreter", "program", it.Missing)), nil
	}
	r, err := p.start(&s, it, argValues(&s, values))
	if err != nil {
		return failure(t.T("toast.runFailed", "name", s.Title), err.Error()), nil
	}
	switch s.Mode {
	case modeSilent:
		return plugin.Result{Hide: true}, nil
	case modeFullOutput:
		return plugin.Result{Push: &plugin.ViewRequest{ID: viewOutput, Payload: plugin.MustPayload(stopPayload{Run: r.id})}}, nil
	}
	// compact (and inline): wait for the result and show the last line.
	timer := time.NewTimer(compactTimeout)
	defer timer.Stop()
	timedOut := false
	select {
	case <-r.done:
	case <-timer.C:
		timedOut = true
		r.stop()
	case <-ctx.Done():
		r.stop()
	}
	r.wait()
	_, stopped, code, werr, _ := r.state()
	last := r.out.LastLine()
	switch {
	case timedOut:
		return failure(t.T("toast.timedOut", "name", s.Title), last), nil
	case stopped:
		return failure(t.T("toast.stopped", "name", s.Title), last), nil
	case werr != nil:
		return failure(t.T("toast.runFailed", "name", s.Title), werr.Error()), nil
	case code != 0:
		return failure(cmp.Or(last, t.T("toast.runFailed", "name", s.Title)), t.T("status.exited", "code", code)), nil
	}
	return plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: cmp.Or(last, t.T("toast.finished", "name", s.Title))}}, nil
}

// environment is the environment of a script.
func environment() []string {
	env := append(os.Environ(), "GOLAUNCHER=1", "PYTHONUNBUFFERED=1")
	if os.Getenv("PYTHONIOENCODING") == "" {
		env = append(env, "PYTHONIOENCODING=utf-8")
	}
	return env
}

// start starts s and returns its run. The run is tracked until it is
// pruned; a background goroutine waits for the process.
func (p *Plugin) start(s *Script, it Interpreter, args []string) (*run, error) {
	path, err := filepath.Abs(s.Path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if s.CurrentDirectory != "" {
		d := p.expand(s.CurrentDirectory)
		if strings.HasPrefix(d, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				d = filepath.Join(home, d[1:])
			}
		}
		if !filepath.IsAbs(d) {
			d = filepath.Join(dir, d)
		}
		dir = d
	}
	cmd := exec.Command(it.Program, append(append(slices.Clone(it.Args), path), args...)...)
	cmd.Dir = dir
	cmd.Env = environment()
	cmd.WaitDelay = waitDelay
	prepare(cmd, it, path, args)
	r := &run{script: *s, out: &output{}, cmd: cmd, started: p.h.Now(), done: make(chan struct{})}
	r.out.onWrite = func() { r.dirty.Store(true) }
	cmd.Stdout, cmd.Stderr = r.out, r.out

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("closed")
	}
	if err := cmd.Start(); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	r.group = newProcGroup(cmd.Process.Pid)
	p.runSeq++
	r.id = "r" + strconv.Itoa(p.runSeq)
	p.runs[r.id] = r
	p.pruneLocked()
	p.wg.Add(2)
	p.mu.Unlock()

	p.h.Go(func() {
		defer p.wg.Done()
		p.waitRun(r)
	})
	p.h.Go(func() {
		defer p.wg.Done()
		p.refreshRun(r)
	})
	return r, nil
}

// waitRun waits for the process of r to exit and records the result.
func (p *Plugin) waitRun(r *run) {
	err := r.cmd.Wait()
	r.group.close()
	code := -1
	if r.cmd.ProcessState != nil {
		code = r.cmd.ProcessState.ExitCode()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) || errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	r.mu.Lock()
	r.finished, r.ended, r.exitCode, r.err = true, p.h.Now(), code, err
	stopped := r.stopped
	r.mu.Unlock()
	close(r.done)
	if (code != 0 || err != nil) && !stopped {
		p.h.Logger().Warn("scripts: script failed", "path", r.script.Path, "mode", r.script.Mode,
			"exit", code, "err", err, "output", r.out.LastLine())
	}
}

// refreshRun asks the host to refresh the output view while r produces
// output, at most every invalidateEvery, and once more when it ends.
func (p *Plugin) refreshRun(r *run) {
	if r.script.Mode != modeFullOutput {
		return
	}
	tick := time.NewTicker(invalidateEvery)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if r.dirty.Swap(false) {
				p.h.Invalidate(viewOutput)
			}
		case <-r.done:
			p.h.Invalidate(viewOutput)
			return
		case <-p.ctx.Done():
			return
		}
	}
}

// pruneLocked forgets the oldest finished runs beyond keepRuns. p.mu is held.
func (p *Plugin) pruneLocked() {
	var finished []*run
	for _, r := range p.runs {
		if done, _, _, _, _ := r.state(); done {
			finished = append(finished, r)
		}
	}
	if len(finished) <= keepRuns {
		return
	}
	slices.SortFunc(finished, func(a, b *run) int {
		ai, _ := strconv.Atoi(a.id[1:])
		bi, _ := strconv.Atoi(b.id[1:])
		return ai - bi
	})
	for _, r := range finished[:len(finished)-keepRuns] {
		delete(p.runs, r.id)
	}
}

// View implements plugin.ViewProvider for the "output" view of a
// fullOutput script: its output so far and its status.
func (p *Plugin) View(_ context.Context, req plugin.ViewRequest) (plugin.View, error) {
	if req.ID != viewOutput {
		return plugin.View{}, fmt.Errorf("scripts: unknown view %q", req.ID)
	}
	t := p.h.I18n()
	var sp stopPayload
	_ = json.Unmarshal(req.Payload, &sp)
	p.mu.Lock()
	r := p.runs[sp.Run]
	p.mu.Unlock()
	if r == nil {
		return plugin.View{Kind: plugin.ViewList, Title: t.T("view.title"), EmptyText: t.T("view.gone")}, nil
	}
	finished, stopped, code, werr, ended := r.state()
	if !finished {
		ended = p.h.Now()
	}
	var status string
	switch {
	case !finished:
		status = t.T("status.running")
	case stopped:
		status = t.T("status.stopped")
	case werr != nil:
		status = t.T("status.error", "error", werr.Error())
	default:
		status = t.T("status.exited", "code", code)
	}
	duration := ended.Sub(r.started).Round(100 * time.Millisecond).String()

	text := r.out.String()
	if len(text) > viewTail {
		text = text[len(text)-viewTail:]
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
		text = t.T("view.truncated") + "\n" + text
	} else if r.out.Truncated() {
		text = t.T("view.truncated") + "\n" + text
	}
	if text == "" {
		text = t.T("view.noOutput")
	}
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	md := fence + "text\n" + text + "\n" + fence

	var actions []plugin.Action
	if !finished {
		actions = append(actions, plugin.Action{
			ID: "stop", Title: t.T("action.stop"), Shortcut: "Ctrl+X",
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: callbackStop, Payload: plugin.MustPayload(stopPayload{Run: r.id})},
		})
	}
	actions = append(actions,
		plugin.Action{
			ID: "copy-output", Title: t.T("action.copyOutput"), Shortcut: "Ctrl+Shift+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: r.out.String()},
		},
		plugin.Action{
			ID: "reveal", Title: t.T("action.reveal"), Shortcut: "Ctrl+Shift+F",
			Do: plugin.ActionDo{Kind: plugin.ActionOpen, Target: r.script.Path, Payload: plugin.MustPayload(plugin.OpenOptions{Reveal: true})},
		},
	)
	item := plugin.Item{
		ID:        "output",
		Title:     status,
		Subtitle:  r.script.Title,
		Icon:      plugin.Icon{Name: "command"},
		Accessory: duration,
		Actions:   actions,
		Detail: &plugin.Detail{
			Markdown: md,
			Metadata: []plugin.Metadata{
				{Label: t.T("meta.status"), Value: status},
				{Label: t.T("meta.duration"), Value: duration},
				{Label: t.T("meta.script"), Value: r.script.Path},
			},
		},
	}
	return plugin.View{
		Kind:       plugin.ViewList,
		Title:      r.script.Title,
		ShowDetail: true,
		Sections:   []plugin.Section{{Items: []plugin.Item{item}}},
	}, nil
}
