package scripts

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/proc"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// compactTimeout bounds compact runs, which the action waits for (the host
// gives an action 30 seconds).
var compactTimeout = 25 * time.Second

// run is one execution of a compact or silent script (fullOutput scripts
// are background tasks of the host).
type run struct {
	id   string
	proc *proc.Process
	out  *output
	done chan struct{}

	mu       sync.Mutex
	finished bool
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
	r.proc.Stop()
}

// wait blocks until the script exited.
func (r *run) wait() { <-r.done }

// state returns a snapshot of the run's status.
func (r *run) state() (stopped bool, code int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped, r.exitCode, r.err
}

// output keeps the last non-empty line of a run's output.
type output struct {
	lw   proc.LineWriter
	mu   sync.Mutex
	last string
}

func newOutput() *output {
	o := &output{}
	o.lw.OnLine = func(line string) {
		if line = strings.TrimSpace(line); line != "" {
			o.mu.Lock()
			o.last = line
			o.mu.Unlock()
		}
	}
	return o
}

func (o *output) Write(p []byte) (int, error) { return o.lw.Write(p) }

// LastLine returns the last non-empty line.
func (o *output) LastLine() string {
	o.lw.Flush()
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.last
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
	cmd, err := p.command(&s, it, argValues(&s, values))
	if err != nil {
		return failure(t.T("toast.runFailed", "name", s.Title), err.Error()), nil
	}
	if s.Mode == modeFullOutput {
		// A background task of the host: its output shows live in Background
		// Tasks, which opens at once.
		id := p.h.RunProcess(plugin.TaskSpec{Title: s.Title, Subtitle: s.PackageName, Icon: s.icon()}, cmd)
		p.mu.Lock()
		p.lastTask[s.Path] = id
		p.mu.Unlock()
		return plugin.Result{Do: &plugin.ActionDo{Kind: plugin.ActionShowTask, Target: id}}, nil
	}
	r, err := p.start(cmd)
	if err != nil {
		return failure(t.T("toast.runFailed", "name", s.Title), err.Error()), nil
	}
	if s.Mode == modeSilent {
		return plugin.Result{Hide: true}, nil
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
	stopped, code, werr := r.state()
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

// environment is what a script gets in addition to the launcher's
// environment.
func environment() []string {
	env := []string{"GOLAUNCHER=1", "PYTHONUNBUFFERED=1"}
	if os.Getenv("PYTHONIOENCODING") == "" {
		env = append(env, "PYTHONIOENCODING=utf-8")
	}
	return env
}

// command is the command line that runs s with its interpreter: a batch
// file runs itself (through cmd.exe, see proc.Start), other scripts are an
// argument of their interpreter.
func (p *Plugin) command(s *Script, it Interpreter, args []string) (plugin.ProcessSpec, error) {
	path, err := filepath.Abs(s.Path)
	if err != nil {
		return plugin.ProcessSpec{}, err
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
	argv := append([]string{path}, args...)
	if !it.Batch {
		argv = append(append([]string{it.Program}, it.Args...), argv...)
	}
	return plugin.ProcessSpec{Argv: argv, Dir: dir, Env: environment()}, nil
}

// start starts a compact or silent script. The run is tracked until it
// ends, so that Close can stop it; a background goroutine waits for it.
func (p *Plugin) start(cmd plugin.ProcessSpec) (*run, error) {
	r := &run{out: newOutput(), done: make(chan struct{})}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("closed")
	}
	pr, err := proc.Start(proc.Options{Argv: cmd.Argv, Dir: cmd.Dir, Env: proc.Environ(cmd.Env...), Output: r.out})
	if err != nil {
		return nil, err
	}
	r.proc = pr
	p.runSeq++
	r.id = "r" + strconv.Itoa(p.runSeq)
	p.runs[r.id] = r
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		p.waitRun(r)
	})
	return r, nil
}

// waitRun waits for the process of r to exit and records the result.
func (p *Plugin) waitRun(r *run) {
	code, err := r.proc.Wait()
	r.mu.Lock()
	r.finished, r.exitCode, r.err = true, code, err
	stopped := r.stopped
	r.mu.Unlock()
	p.mu.Lock()
	delete(p.runs, r.id)
	p.mu.Unlock()
	close(r.done)
	if (code != 0 || err != nil) && !stopped {
		p.h.Logger().Warn("scripts: script failed", "run", r.id, "exit", code, "err", err, "output", r.out.LastLine())
	}
}
