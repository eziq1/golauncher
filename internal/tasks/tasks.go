// Package tasks is the host's background task manager
// (docs/background-tasks.md): it queues, runs, stops and remembers the
// tasks plugins start with Host.RunProcess and Host.RunTask, keeps their
// console output, and provides the builtin.tasks plugin that shows them.
package tasks

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/internal/winapi/proc"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Defaults of Options.
const (
	DefaultMaxParallel  = 4
	DefaultKeepFinished = 50
	DefaultMaxOutput    = 2 << 20
)

// Kind is how a task runs.
type Kind string

const (
	KindProcess Kind = "process" // Host.RunProcess
	KindGo      Kind = "go"      // Host.RunTask
)

// Info describes a task. It is also what the log directory keeps of a
// task (<id>.json next to <id>.log).
type Info struct {
	ID       string            `json:"id"`
	Plugin   string            `json:"plugin"`
	Title    string            `json:"title"`
	Subtitle string            `json:"subtitle,omitempty"`
	Icon     plugin.Icon       `json:"icon,omitzero"`
	Queue    string            `json:"queue,omitempty"`
	Notify   plugin.NotifyMode `json:"notify,omitempty"`
	Kind     Kind              `json:"kind"`
	State    plugin.TaskState  `json:"state"`
	// Status is the last status line reported by a Go task.
	Status string `json:"status,omitempty"`
	// Progress is the last progress reported by a Go task (nil: none;
	// negative: indeterminate).
	Progress *float64 `json:"progress,omitempty"`
	ExitCode int      `json:"exitCode"`
	Error    string   `json:"error,omitempty"`
	// Interrupted is set for a task of a previous run that had not ended
	// when golauncher quit.
	Interrupted bool                `json:"interrupted,omitzero"`
	Cmd         *plugin.ProcessSpec `json:"cmd,omitempty"`
	Created     time.Time           `json:"created"`
	Started     time.Time           `json:"started,omitzero"`
	Ended       time.Time           `json:"ended,omitzero"`
	LogPath     string              `json:"logPath,omitempty"`
	// History is set for tasks of a previous run, read from the log
	// directory; they cannot run again.
	History bool `json:"history,omitzero"`
	// Seen is set for a failed task once the task view was opened.
	Seen     bool   `json:"seen,omitzero"`
	LastLine string `json:"lastLine,omitempty"`
}

// Result is the plugin.TaskResult of a finished task.
func (i Info) Result() plugin.TaskResult {
	return plugin.TaskResult{ID: i.ID, State: i.State, ExitCode: i.ExitCode, Error: i.Error, LastLine: i.LastLine}
}

// Rerunnable reports whether the task can run again: a process task of this
// run that ended.
func (i Info) Rerunnable() bool {
	return i.Kind == KindProcess && i.Cmd != nil && !i.History && i.State.Finished()
}

// Options configure a Manager.
type Options struct {
	// Dir keeps the log and the description of every task and is where the
	// history is read from at start ("" keeps nothing).
	Dir string
	// ReadOnly reads the history from Dir but writes nothing there (glctl
	// --real without --exec).
	ReadOnly bool
	// Runner starts programs (default ProcessRunner).
	Runner Runner
	// MaxParallel is how many tasks run at once (default 4).
	MaxParallel int
	// KeepFinished is how many finished tasks are kept, with their logs
	// (default 50).
	KeepFinished int
	// MaxOutput is how many bytes of output are kept in memory per task
	// (default 2 MiB); the log file has all of it.
	MaxOutput int
	// Env is the base environment of programs (default os.Environ()).
	Env []string
	Now func() time.Time
	Log *slog.Logger
	Loc plugin.Localizer // for the host's own error messages (may be nil)
	// Allowed reports whether a plugin may run programs (PermExec); nil
	// allows every plugin.
	Allowed func(pluginID string) bool
	// OnFinished is called after a task ended and its OnDone returned, on a
	// background goroutine (the engine publishes plugin.EventTaskFinished
	// and notifies the user).
	OnFinished func(Info)
}

// Manager runs background tasks. It is safe for concurrent use.
type Manager struct {
	o   Options
	log *slog.Logger

	mu       sync.Mutex
	idle     *sync.Cond // signalled when inflight drops
	tasks    []*task    // in creation order
	seq      int
	running  int
	busy     map[string]bool // queues with a running task
	inflight int             // tasks not finished, or whose callbacks run
	closed   bool
	watchers []func(state bool)
}

// task is one task. Its fields are guarded by Manager.mu, except out.
type task struct {
	info    Info
	onDone  func(plugin.TaskResult)
	fn      plugin.TaskFunc
	out     *output
	proc    Process
	cancel  context.CancelFunc
	stopped bool
}

// New returns a Manager and reads the history in o.Dir.
func New(o Options) *Manager {
	o.MaxParallel = cmp.Or(o.MaxParallel, DefaultMaxParallel)
	o.KeepFinished = cmp.Or(o.KeepFinished, DefaultKeepFinished)
	o.MaxOutput = cmp.Or(o.MaxOutput, DefaultMaxOutput)
	if o.Runner == nil {
		o.Runner = ProcessRunner{}
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	m := &Manager{o: o, log: o.Log, busy: map[string]bool{}}
	m.idle = sync.NewCond(&m.mu)
	if o.Dir != "" {
		if !o.ReadOnly {
			if err := os.MkdirAll(o.Dir, 0o755); err != nil {
				m.log.Warn("tasks: creating the log directory", "dir", o.Dir, "err", err)
			}
		}
		m.loadHistory()
	}
	return m
}

// loc is the localizer of the host's messages (one returning the message
// IDs without Options.Loc).
func (m *Manager) loc() interface{ T(string, ...any) string } {
	if m.o.Loc == nil {
		return keys{}
	}
	return m.o.Loc
}

type keys struct{}

func (keys) T(id string, _ ...any) string { return id }

// OnChange registers fn, called after a change: with state true when a
// task was added, removed, started or ended (the indicator may change),
// with state false when output or a status changed. fn runs on the
// goroutine making the change and must not block.
func (m *Manager) OnChange(fn func(state bool)) {
	m.mu.Lock()
	m.watchers = append(m.watchers, fn)
	m.mu.Unlock()
}

func (m *Manager) changed(state bool) {
	m.mu.Lock()
	ws := slices.Clone(m.watchers)
	m.mu.Unlock()
	for _, fn := range ws {
		fn(state)
	}
}

// newIDLocked returns a task ID that is unique also among the history:
// "t<yymmdd-hhmmss>-<n>". m.mu is held.
func (m *Manager) newIDLocked() string {
	stamp := m.o.Now().Format("060102-150405")
	for {
		m.seq++
		id := "t" + stamp + "-" + strconv.Itoa(m.seq)
		if m.findLocked(id) == nil {
			return id
		}
	}
}

func (m *Manager) findLocked(id string) *task {
	for _, t := range m.tasks {
		if t.info.ID == id {
			return t
		}
	}
	return nil
}

// RunProcess queues cmd as a task of plugin pluginID and returns its ID.
func (m *Manager) RunProcess(pluginID string, spec plugin.TaskSpec, cmd plugin.ProcessSpec) string {
	cmd.Argv = slices.Clone(cmd.Argv)
	cmd.Env = slices.Clone(cmd.Env)
	t := m.newTask(pluginID, spec, KindProcess)
	t.info.Cmd = &cmd
	var err error
	switch {
	case m.o.Allowed != nil && !m.o.Allowed(pluginID):
		err = errors.New(m.loc().T("tasks.error.permission", "plugin", pluginID))
	case len(cmd.Argv) == 0 || cmd.Argv[0] == "":
		err = errors.New(m.loc().T("tasks.error.noCommand"))
	}
	return m.add(t, err)
}

// RunTask queues fn as a task of plugin pluginID and returns its ID.
func (m *Manager) RunTask(pluginID string, spec plugin.TaskSpec, fn plugin.TaskFunc) string {
	t := m.newTask(pluginID, spec, KindGo)
	t.fn = fn
	var err error
	if fn == nil {
		err = errors.New(m.loc().T("tasks.error.noCommand"))
	}
	return m.add(t, err)
}

func (m *Manager) newTask(pluginID string, spec plugin.TaskSpec, kind Kind) *task {
	return &task{
		info: Info{
			Plugin: pluginID, Title: spec.Title, Subtitle: spec.Subtitle, Icon: spec.Icon,
			Queue: spec.Queue, Notify: spec.Notify, Kind: kind, State: plugin.TaskQueued,
			Created: m.o.Now(),
		},
		onDone: spec.OnDone,
	}
}

// add registers t; a task that cannot run (err) ends as failed at once.
func (m *Manager) add(t *task, err error) string {
	m.mu.Lock()
	t.info.ID = m.newIDLocked()
	if t.info.Title == "" {
		t.info.Title = t.info.ID
	}
	if m.o.Dir != "" && !m.o.ReadOnly {
		t.info.LogPath = filepath.Join(m.o.Dir, t.info.ID+".log")
	}
	if m.closed && err == nil {
		err = errors.New(m.loc().T("tasks.error.closed"))
	}
	m.tasks = append(m.tasks, t)
	m.inflight++
	id := t.info.ID
	if err != nil {
		out := m.newOutput(t)
		t.out = out
		now := m.o.Now()
		t.info.State, t.info.Error, t.info.ExitCode = plugin.TaskFailed, err.Error(), -1
		t.info.Started, t.info.Ended = now, now
		t.info.LastLine = err.Error()
		m.mu.Unlock()
		out.writeLine(err.Error())
		out.finish()
		m.changed(true)
		host.Go(m.log, "task "+id, func() { m.finished(t) })
		return id
	}
	m.scheduleLocked()
	m.mu.Unlock()
	m.changed(true)
	return id
}

// newOutput creates the output of t and its log file.
func (m *Manager) newOutput(t *task) *output {
	var log io.WriteCloser
	if t.info.LogPath != "" {
		f, err := os.Create(t.info.LogPath)
		if err != nil {
			m.log.Warn("tasks: creating a log file", "path", t.info.LogPath, "err", err)
			t.info.LogPath = ""
		} else {
			log = f
		}
	}
	return newOutput(m.o.MaxOutput, log, func() { m.changed(false) })
}

// scheduleLocked starts the queued tasks that may run: in creation order,
// one per queue, at most MaxParallel. m.mu is held.
func (m *Manager) scheduleLocked() {
	if m.closed {
		return
	}
	for _, t := range m.tasks {
		if m.running >= m.o.MaxParallel {
			return
		}
		if t.info.State != plugin.TaskQueued || (t.info.Queue != "" && m.busy[t.info.Queue]) {
			continue
		}
		t.info.State, t.info.Started = plugin.TaskRunning, m.o.Now()
		m.running++
		if t.info.Queue != "" {
			m.busy[t.info.Queue] = true
		}
		t.out = m.newOutput(t)
		m.saveLocked(t)
		host.Go(m.log, "task "+t.info.ID, func() { m.run(t) })
	}
}

// run runs t and records how it ended.
func (m *Manager) run(t *task) {
	var (
		code = 0
		err  error
	)
	switch t.info.Kind {
	case KindProcess:
		code, err = m.runProcess(t)
	case KindGo:
		code, err = m.runGo(t)
	}
	t.out.finish()
	m.mu.Lock()
	now := m.o.Now()
	t.info.Ended, t.info.ExitCode = now, code
	t.info.LastLine = t.out.lastLine()
	switch {
	case t.stopped:
		t.info.State = plugin.TaskCancelled
	case err != nil:
		t.info.State, t.info.Error = plugin.TaskFailed, err.Error()
	case code != 0:
		t.info.State = plugin.TaskFailed
	default:
		t.info.State = plugin.TaskSucceeded
	}
	t.proc, t.cancel = nil, nil
	m.running--
	if t.info.Queue != "" {
		m.busy[t.info.Queue] = false
	}
	m.scheduleLocked()
	m.mu.Unlock()
	m.changed(true)
	m.finished(t)
}

func (m *Manager) runProcess(t *task) (int, error) {
	m.mu.Lock()
	cmd := *t.info.Cmd
	m.mu.Unlock()
	p, err := m.o.Runner.Start(cmd, proc.MergeEnv(m.o.Env, cmd.Env), t.out)
	if err != nil {
		t.out.writeLine(err.Error())
		return -1, err
	}
	m.mu.Lock()
	t.proc = p
	stopped := t.stopped
	m.mu.Unlock()
	if stopped {
		p.Stop()
	}
	code, err := p.Wait()
	if err != nil {
		t.out.writeLine(err.Error())
	}
	return code, err
}

func (m *Manager) runGo(t *task) (code int, err error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.mu.Lock()
	t.cancel = cancel
	if t.stopped {
		cancel()
	}
	fn := t.fn
	m.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("tasks: a task panicked", "task", t.info.ID, "plugin", t.info.Plugin, "panic", r)
			err = fmt.Errorf("panic: %v", r)
			t.out.writeLine(err.Error())
			code = -1
		}
	}()
	if err := fn(ctx, &handle{m: m, t: t}); err != nil {
		if !errors.Is(err, context.Canceled) {
			t.out.writeLine(err.Error())
		}
		return -1, err
	}
	return 0, nil
}

// finished saves t, prunes the history and runs the callbacks.
func (m *Manager) finished(t *task) {
	m.mu.Lock()
	m.saveLocked(t)
	removed := m.pruneLocked()
	info := t.info
	onDone := t.onDone
	m.mu.Unlock()
	m.deleteFiles(removed)
	if onDone != nil {
		func() {
			defer host.Contain(m.log, "task OnDone "+info.ID)
			onDone(info.Result())
		}()
	}
	if fn := m.o.OnFinished; fn != nil {
		func() {
			defer host.Contain(m.log, "task finished "+info.ID)
			fn(info)
		}()
	}
	m.mu.Lock()
	m.inflight--
	m.idle.Broadcast()
	m.mu.Unlock()
}

// handle is the plugin.Task given to a Go task.
type handle struct {
	m *Manager
	t *task
}

func (h *handle) Write(p []byte) (int, error) { return h.t.out.Write(p) }

func (h *handle) SetStatus(status string) {
	h.m.mu.Lock()
	h.t.info.Status = status
	h.m.mu.Unlock()
	h.m.changed(false)
}

func (h *handle) SetProgress(fraction float64) {
	fraction = min(fraction, 1)
	h.m.mu.Lock()
	h.t.info.Progress = &fraction
	h.m.mu.Unlock()
	h.m.changed(false)
}

// ErrNotFound is returned for an unknown task ID.
var ErrNotFound = errors.New("tasks: no such task")

// Stop stops a task: a queued one is dropped, a running one is ended with
// every process it started (or its context is cancelled). Stopping a
// finished task does nothing.
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	t := m.findLocked(id)
	if t == nil {
		m.mu.Unlock()
		return ErrNotFound
	}
	switch t.info.State {
	case plugin.TaskQueued:
		t.stopped = true
		now := m.o.Now()
		t.info.State, t.info.ExitCode, t.info.Ended = plugin.TaskCancelled, -1, now
		t.out = newOutput(m.o.MaxOutput, nil, nil)
		m.mu.Unlock()
		m.changed(true)
		m.finished(t)
		return nil
	case plugin.TaskRunning:
		t.stopped = true
		p, cancel := t.proc, t.cancel
		m.mu.Unlock()
		if p != nil {
			p.Stop()
		}
		if cancel != nil {
			cancel()
		}
		return nil
	}
	m.mu.Unlock()
	return nil
}

// Rerun queues a finished process task of this run again, as a new task
// with the same description (and OnDone), and returns the new ID.
func (m *Manager) Rerun(id string) (string, error) {
	m.mu.Lock()
	t := m.findLocked(id)
	if t == nil {
		m.mu.Unlock()
		return "", ErrNotFound
	}
	if !t.info.Rerunnable() {
		m.mu.Unlock()
		return "", fmt.Errorf("tasks: task %s cannot run again", id)
	}
	i := t.info
	spec := plugin.TaskSpec{Title: i.Title, Subtitle: i.Subtitle, Icon: i.Icon, Queue: i.Queue, Notify: i.Notify, OnDone: t.onDone}
	cmd := *i.Cmd
	m.mu.Unlock()
	return m.RunProcess(i.Plugin, spec, cmd), nil
}

// Remove forgets a finished task and deletes its log.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	i := slices.IndexFunc(m.tasks, func(t *task) bool { return t.info.ID == id })
	if i < 0 {
		m.mu.Unlock()
		return ErrNotFound
	}
	t := m.tasks[i]
	if !t.info.State.Finished() {
		m.mu.Unlock()
		return fmt.Errorf("tasks: task %s is still running", id)
	}
	m.tasks = slices.Delete(m.tasks, i, i+1)
	m.mu.Unlock()
	m.deleteFiles([]Info{t.info})
	m.changed(true)
	return nil
}

// ClearFinished forgets every finished task and returns how many.
func (m *Manager) ClearFinished() int {
	m.mu.Lock()
	var removed []Info
	m.tasks = slices.DeleteFunc(m.tasks, func(t *task) bool {
		if t.info.State.Finished() {
			removed = append(removed, t.info)
			return true
		}
		return false
	})
	m.mu.Unlock()
	m.deleteFiles(removed)
	if len(removed) > 0 {
		m.changed(true)
	}
	return len(removed)
}

// rank orders tasks for display: running, queued, then finished (newest
// first).
func rank(i Info) int {
	switch i.State {
	case plugin.TaskRunning:
		return 0
	case plugin.TaskQueued:
		return 1
	}
	return 2
}

// List returns all tasks: running, then queued (both oldest first), then
// finished ones, newest first.
func (m *Manager) List() []Info {
	m.mu.Lock()
	out := make([]Info, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, m.infoLocked(t))
	}
	m.mu.Unlock()
	order := map[string]int{}
	for i, t := range out {
		order[t.ID] = i
	}
	slices.SortStableFunc(out, func(a, b Info) int {
		if c := cmp.Compare(rank(a), rank(b)); c != 0 {
			return c
		}
		if rank(a) == 2 {
			if c := b.Ended.Compare(a.Ended); c != 0 {
				return c
			}
			return cmp.Compare(order[b.ID], order[a.ID])
		}
		return cmp.Compare(order[a.ID], order[b.ID])
	})
	return out
}

// infoLocked returns t's Info with its current last line. m.mu is held.
func (m *Manager) infoLocked(t *task) Info {
	i := t.info
	if i.Cmd != nil {
		c := *i.Cmd
		i.Cmd = &c
	}
	if t.out != nil && !i.State.Finished() {
		i.LastLine = t.out.lastLine()
	}
	return i
}

// Get returns the task with id.
func (m *Manager) Get(id string) (Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.findLocked(id)
	if t == nil {
		return Info{}, false
	}
	return m.infoLocked(t), true
}

// Output returns the last n lines of a task's output (all that is kept for
// n <= 0) and whether earlier output exists that is not returned.
func (m *Manager) Output(id string, n int) (string, bool) {
	m.mu.Lock()
	t := m.findLocked(id)
	var (
		out     *output
		logPath string
	)
	if t != nil {
		out, logPath = t.out, t.info.LogPath
	}
	m.mu.Unlock()
	switch {
	case out != nil:
		return out.tail(n)
	case logPath != "":
		return readTail(logPath, m.o.MaxOutput, n)
	}
	return "", false
}

// Indicator returns the number of queued and running tasks and of failed
// tasks not seen yet in the task view.
func (m *Manager) Indicator() (active, failed int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		switch {
		case !t.info.State.Finished():
			active++
		case t.info.State == plugin.TaskFailed && !t.info.Seen && !t.info.History:
			failed++
		}
	}
	return active, failed
}

// Active returns the number of queued and running tasks.
func (m *Manager) Active() int {
	a, _ := m.Indicator()
	return a
}

// MarkSeen marks every failed task as seen (the task view was opened).
func (m *Manager) MarkSeen() {
	m.mu.Lock()
	n := 0
	for _, t := range m.tasks {
		if t.info.State == plugin.TaskFailed && !t.info.Seen {
			t.info.Seen = true
			n++
		}
	}
	m.mu.Unlock()
	if n > 0 {
		m.changed(true)
	}
}

// Wait blocks until no task is queued or running and their callbacks have
// returned, or ctx is done.
func (m *Manager) Wait(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() {
		m.mu.Lock()
		m.idle.Broadcast()
		m.mu.Unlock()
	})
	defer stop()
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.inflight > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.idle.Wait()
	}
	return nil
}

// Close stops every task, waits for them (at most a few seconds) and
// keeps no new ones from starting.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	var ids []string
	for _, t := range m.tasks {
		if !t.info.State.Finished() {
			ids = append(ids, t.info.ID)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Stop(id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.Wait(ctx)
}
