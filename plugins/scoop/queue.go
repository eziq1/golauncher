package scoop

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Operation kinds (opRequest.Kind).
const (
	opInstall       = "install"
	opUninstall     = "uninstall"
	opUpdate        = "update"
	opHold          = "hold"
	opUnhold        = "unhold"
	opUpdateAll     = "update-all"
	opUpdateBuckets = "update-buckets"
	opBucketAdd     = "bucket-add"
	opBucketRemove  = "bucket-rm"
	opCleanup       = "cleanup"
)

// keepOps is how many finished operations stay viewable.
const keepOps = 10

// opRequest describes a Scoop operation; it is the Payload of the "op"
// callback.
type opRequest struct {
	Kind   string `json:"kind"`
	Name   string `json:"name,omitempty"`
	Bucket string `json:"bucket,omitempty"`
	URL    string `json:"url,omitempty"`
}

var (
	// nameRe is what app and bucket names look like. It keeps names from
	// being taken as options ("-g").
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+@-]*$`)
	urlRe  = regexp.MustCompile(`^(https?://|ssh://|git@)[^\s"'` + "`" + `]+$`)
)

// opStatus is the state of an operation.
type opStatus int

const (
	opQueued opStatus = iota
	opRunning
	opDone
)

// op is one queued Scoop operation: one or more scoop commands run in turn.
type op struct {
	id    string
	req   opRequest
	title string
	steps [][]string
	out   *output
	done  chan struct{}
	dirty atomic.Bool

	mu       sync.Mutex
	status   opStatus
	started  time.Time
	ended    time.Time
	exitCode int
	err      error // a command could not be started or waited for
	stopped  bool
	proc     Process
}

// opState is a snapshot of an operation's status.
type opState struct {
	status   opStatus
	started  time.Time
	ended    time.Time
	exitCode int
	err      error
	stopped  bool
}

func (o *op) state() opState {
	o.mu.Lock()
	defer o.mu.Unlock()
	return opState{o.status, o.started, o.ended, o.exitCode, o.err, o.stopped}
}

// succeeded reports whether the operation ended without error.
func (s opState) succeeded() bool {
	return s.status == opDone && !s.stopped && s.err == nil && s.exitCode == 0
}

// wait waits until the operation ended or ctx is done.
func (o *op) wait(ctx context.Context) {
	select {
	case <-o.done:
	case <-ctx.Done():
	}
}

// plan returns the title and the scoop commands of req, or an error for a
// request that is not valid.
func (p *Plugin) plan(req opRequest) (string, [][]string, error) {
	t := p.h.I18n()
	needName := func() error {
		if !nameRe.MatchString(req.Name) {
			return errors.New(t.T("error.badName", "name", req.Name))
		}
		if a, ok := p.installed(req.Name); ok && a.Global && req.Kind != opInstall {
			return errors.New(t.T("error.global", "name", req.Name))
		}
		return nil
	}
	switch req.Kind {
	case opInstall:
		if err := needName(); err != nil {
			return "", nil, err
		}
		target := req.Name
		if req.Bucket != "" {
			if !nameRe.MatchString(req.Bucket) {
				return "", nil, errors.New(t.T("error.badName", "name", req.Bucket))
			}
			target = req.Bucket + "/" + req.Name
		}
		return t.T("op.install", "name", req.Name), [][]string{{"install", target}}, nil
	case opUninstall:
		if err := needName(); err != nil {
			return "", nil, err
		}
		return t.T("op.uninstall", "name", req.Name), [][]string{{"uninstall", req.Name}}, nil
	case opUpdate:
		if err := needName(); err != nil {
			return "", nil, err
		}
		return t.T("op.update", "name", req.Name), [][]string{{"update", req.Name}}, nil
	case opHold:
		if err := needName(); err != nil {
			return "", nil, err
		}
		return t.T("op.hold", "name", req.Name), [][]string{{"hold", req.Name}}, nil
	case opUnhold:
		if err := needName(); err != nil {
			return "", nil, err
		}
		return t.T("op.unhold", "name", req.Name), [][]string{{"unhold", req.Name}}, nil
	case opUpdateAll:
		var steps [][]string
		if p.h.Preferences().Bool(prefUpdateBucketsFirst) {
			steps = append(steps, []string{"update"})
		}
		return t.T("op.updateAll"), append(steps, []string{"update", "*"}), nil
	case opUpdateBuckets:
		return t.T("op.updateBuckets"), [][]string{{"update"}}, nil
	case opBucketAdd:
		if err := needName(); err != nil {
			return "", nil, err
		}
		args := []string{"bucket", "add", req.Name}
		if req.URL != "" {
			if !urlRe.MatchString(req.URL) {
				return "", nil, errors.New(t.T("error.badURL", "url", req.URL))
			}
			args = append(args, req.URL)
		}
		return t.T("op.bucketAdd", "name", req.Name), [][]string{args}, nil
	case opBucketRemove:
		if err := needName(); err != nil {
			return "", nil, err
		}
		return t.T("op.bucketRemove", "name", req.Name), [][]string{{"bucket", "rm", req.Name}}, nil
	case opCleanup:
		return t.T("op.cleanup"), [][]string{{"cleanup", "*"}, {"cache", "rm", "*"}}, nil
	}
	return "", nil, fmt.Errorf("scoop: unknown operation %q", req.Kind)
}

// installed returns the installed app called name, if any.
func (p *Plugin) installed(name string) (*App, bool) {
	ix := p.Index()
	if ix == nil {
		return nil, false
	}
	return ix.App(name)
}

// enqueue queues the operation req and starts the worker if it is idle.
func (p *Plugin) enqueue(req opRequest) (*op, error) {
	title, steps, err := p.plan(req)
	if err != nil {
		return nil, err
	}
	o := &op{req: req, title: title, steps: steps, out: &output{}, done: make(chan struct{})}
	o.out.onWrite = func() { o.dirty.Store(true) }
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("closed")
	}
	p.opSeq++
	o.id = "op" + strconv.Itoa(p.opSeq)
	p.ops[o.id] = o
	p.queue = append(p.queue, o)
	p.pruneLocked()
	if !p.working {
		p.working = p.spawnLocked(p.work)
	}
	return o, nil
}

// work runs the queued operations one after the other until the queue is
// empty.
func (p *Plugin) work() {
	for {
		p.mu.Lock()
		if len(p.queue) == 0 || p.closed {
			p.working = false
			p.mu.Unlock()
			return
		}
		o := p.queue[0]
		p.queue = p.queue[1:]
		p.mu.Unlock()
		p.run(o)
	}
}

// run runs the commands of o until one fails or o is stopped.
func (p *Plugin) run(o *op) {
	o.mu.Lock()
	if o.status == opDone {
		o.mu.Unlock()
		return
	}
	o.status, o.started = opRunning, p.h.Now()
	o.mu.Unlock()
	p.h.Invalidate("")
	stopRefresh := make(chan struct{})
	p.spawn(func() { p.refreshOutput(o, stopRefresh) })

	code := 0
	var err error
	for _, args := range o.steps {
		o.mu.Lock()
		if o.stopped {
			o.mu.Unlock()
			break
		}
		fmt.Fprintf(o.out, "> %s\n", CommandLine(args))
		var proc Process
		proc, err = p.runner.Start(args, o.out)
		o.proc = proc
		o.mu.Unlock()
		if err != nil {
			code = -1
			break
		}
		code, err = proc.Wait()
		o.mu.Lock()
		o.proc = nil
		stopped := o.stopped
		o.mu.Unlock()
		if err != nil || code != 0 || stopped {
			break
		}
	}
	o.mu.Lock()
	o.status, o.ended, o.exitCode, o.err = opDone, p.h.Now(), code, err
	stopped := o.stopped
	o.mu.Unlock()
	close(stopRefresh)
	close(o.done)
	if (code != 0 || err != nil) && !stopped {
		p.h.Logger().Warn("scoop: operation failed", "op", o.title, "exit", code, "err", err, "output", o.out.LastLine())
	}
	p.h.Invalidate(viewOutput)
	// Installed apps and buckets changed: read them again (this also
	// refreshes every open view).
	p.scheduleRefresh()
}

// refreshOutput asks the host to refresh the output view while o produces
// output, at most every invalidateEvery.
func (p *Plugin) refreshOutput(o *op, stop <-chan struct{}) {
	tick := time.NewTicker(invalidateEvery)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if o.dirty.Swap(false) {
				p.h.Invalidate(viewOutput)
			}
		case <-stop:
			return
		case <-p.ctx.Done():
			return
		}
	}
}

// stopOp stops o: a queued operation is dropped, a running one is ended
// with everything it started.
func (p *Plugin) stopOp(o *op) {
	p.mu.Lock()
	o.mu.Lock()
	if o.status == opDone {
		o.mu.Unlock()
		p.mu.Unlock()
		return
	}
	o.stopped = true
	proc := o.proc
	finish := false
	if i := slices.Index(p.queue, o); i >= 0 && o.status == opQueued {
		p.queue = slices.Delete(p.queue, i, i+1)
		o.status, o.ended, o.exitCode = opDone, p.h.Now(), -1
		finish = true
	}
	o.mu.Unlock()
	p.mu.Unlock()
	if finish {
		close(o.done)
		p.h.Invalidate("")
	}
	if proc != nil {
		proc.Stop()
	}
}

// pruneLocked forgets the oldest finished operations beyond keepOps. p.mu
// is held.
func (p *Plugin) pruneLocked() {
	var finished []*op
	for _, o := range p.ops {
		if o.state().status == opDone {
			finished = append(finished, o)
		}
	}
	if len(finished) <= keepOps {
		return
	}
	slices.SortFunc(finished, func(a, b *op) int {
		ai, _ := strconv.Atoi(a.id[2:])
		bi, _ := strconv.Atoi(b.id[2:])
		return ai - bi
	})
	for _, o := range finished[:len(finished)-keepOps] {
		delete(p.ops, o.id)
	}
}

// activeOps returns the queued and running operations, oldest first.
func (p *Plugin) activeOps() []*op {
	p.mu.Lock()
	var out []*op
	for _, o := range p.ops {
		if o.state().status != opDone {
			out = append(out, o)
		}
	}
	p.mu.Unlock()
	slices.SortFunc(out, func(a, b *op) int {
		ai, _ := strconv.Atoi(a.id[2:])
		bi, _ := strconv.Atoi(b.id[2:])
		return ai - bi
	})
	return out
}
