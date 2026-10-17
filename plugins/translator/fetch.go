package translator

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/plugins/translator/engines"
)

// Translations are fetched in the background (docs/translator.md §2, §7):
// after the typing paused (debounce), one text at a time (a newer text
// cancels the older request), every engine in parallel, cached; each answer
// pulls the root search (or the view) again.

// cacheSize is how many texts' translations are kept (in memory only).
const cacheSize = 64

var errOffline = errors.New("translator: offline")

// job is one text to translate with the engines of the settings.
type job struct {
	key string
	req engines.Request
	// auto: the target was not given; base is the default target, and a
	// source language detected by an engine may change the target
	// (autoTarget).
	auto     bool
	base     string
	list     []engines.Engine
	fallback engines.Engine
}

// newJob is the job for q with the current settings.
func (p *Plugin) newJob(q query) job {
	s := p.settings()
	j := job{list: s.engines, fallback: s.fallback, req: engines.Request{Text: q.Text, From: q.From, To: q.To}}
	if j.req.To == "" {
		j.auto = true
		j.base = baseTarget(s.target, p.h.I18n().Locale())
		j.req.To = autoTarget(j.base, j.req.From, j.req.Text)
	}
	var ids []string
	for _, e := range j.list {
		ids = append(ids, e.ID())
	}
	if j.fallback != nil {
		ids = append(ids, "+"+j.fallback.ID())
	}
	j.key = strings.Join([]string{strings.Join(ids, ","), j.req.From, j.req.To, j.base, j.req.Text}, "\x00")
	return j
}

// row is one engine's state for a text.
type row struct {
	engine string
	// to is the target asked for (an automatic target may change it).
	to   string
	res  engines.Result
	err  error
	done bool
	// fallback marks the fallback engine's row, after the others failed;
	// failed are their IDs.
	fallback bool
	failed   []string
}

// entry is the translation of one text.
type entry struct {
	rows []row
	done bool
}

// runner fetches translations for one input (the typed text, the selected
// text).
type runner struct {
	p    *Plugin
	name string

	mu      sync.Mutex
	cache   map[string]*entry
	order   []string // cache keys, oldest first
	pending string   // the key being fetched
	cancel  context.CancelFunc
	closed  bool
	wg      sync.WaitGroup
}

func newRunner(p *Plugin, name string) *runner {
	return &runner{p: p, name: name, cache: map[string]*entry{}}
}

// pendingRows are the rows of j before any answer.
func pendingRows(j job) []row {
	rows := make([]row, len(j.list))
	for i, e := range j.list {
		rows[i] = row{engine: e.ID(), to: j.req.To}
	}
	return rows
}

// get returns the rows of j: cached, or pending while they are asked for.
// debounce waits for the typing to pause first; sync waits for the answers
// (at most the timeout).
func (r *runner) get(j job, debounce, sync bool) []row {
	if len(j.list) == 0 && j.fallback == nil {
		return nil
	}
	r.mu.Lock()
	// A finished entry, or (not waiting) the one being fetched.
	if e, ok := r.cache[j.key]; ok && (e.done || !sync && r.pending == j.key) {
		rows := slices.Clone(e.rows)
		r.mu.Unlock()
		return rows
	}
	if r.closed {
		r.mu.Unlock()
		return pendingRows(j)
	}
	r.cancelLocked()
	e := &entry{rows: pendingRows(j)}
	r.put(j.key, e)
	ctx, cancel := context.WithCancel(context.Background())
	r.pending, r.cancel = j.key, cancel
	var wait <-chan time.Time
	if debounce && !sync {
		// The debounce timer starts now, not when the goroutine runs.
		wait = r.p.opts.After(r.p.opts.Debounce)
	}
	done := make(chan struct{})
	r.wg.Add(1)
	r.p.h.Go(func() {
		defer r.wg.Done()
		defer close(done)
		defer cancel()
		r.run(ctx, wait, j, e)
	})
	r.mu.Unlock()
	if sync {
		r.wait(done)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(e.rows)
}

// wait waits for done, at most a little longer than the engines' timeout.
func (r *runner) wait(done <-chan struct{}) {
	t := time.NewTimer(2*r.p.opts.Timeout + time.Second)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

// put adds an entry to the cache, dropping the oldest beyond cacheSize.
// r.mu must be held.
func (r *runner) put(key string, e *entry) {
	if _, ok := r.cache[key]; !ok {
		r.order = append(r.order, key)
	}
	r.cache[key] = e
	for len(r.order) > cacheSize {
		delete(r.cache, r.order[0])
		r.order = r.order[1:]
	}
}

// cancelLocked cancels the request being made and forgets its unfinished
// entry. r.mu must be held.
func (r *runner) cancelLocked() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	if e, ok := r.cache[r.pending]; ok && !e.done {
		delete(r.cache, r.pending)
		r.order = slices.DeleteFunc(r.order, func(k string) bool { return k == r.pending })
	}
	r.pending, r.cancel = "", nil
}

// run waits for the debounce delay, then asks every engine in parallel; when
// all of them failed, the fallback engine.
func (r *runner) run(ctx context.Context, wait <-chan time.Time, j job, e *entry) {
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	var wg sync.WaitGroup
	for i, eng := range j.list {
		wg.Add(1)
		r.p.h.Go(func() {
			defer wg.Done()
			res, to, err := r.translate(ctx, eng, j)
			if ctx.Err() != nil {
				return
			}
			r.mu.Lock()
			e.rows[i] = row{engine: eng.ID(), to: to, res: res, err: err, done: true}
			r.mu.Unlock()
			r.p.h.Invalidate("")
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	r.mu.Lock()
	failed := len(e.rows) == 0 || !slices.ContainsFunc(e.rows, func(x row) bool { return x.err == nil })
	r.mu.Unlock()
	if failed && j.fallback != nil {
		res, to, err := r.translate(ctx, j.fallback, j)
		if ctx.Err() != nil {
			return
		}
		r.mu.Lock()
		var ids []string
		for _, x := range e.rows {
			ids = append(ids, x.engine)
		}
		e.rows = append(e.rows, row{engine: j.fallback.ID(), to: to, res: res, err: err, done: true, fallback: true, failed: ids})
		r.mu.Unlock()
	}
	r.mu.Lock()
	e.done = true
	if r.pending == j.key {
		r.pending, r.cancel = "", nil
	}
	r.mu.Unlock()
	r.p.h.Invalidate("")
}

// translate asks one engine, with the timeout. With an automatic target,
// a source language detected that calls for another target ("bonjour" is
// French, not English) is asked again with that target.
func (r *runner) translate(ctx context.Context, eng engines.Engine, j job) (engines.Result, string, error) {
	start := time.Now()
	res, err := r.ask(ctx, eng, j.req)
	to := j.req.To
	if err == nil && j.auto && res.From != "" {
		if want := autoTarget(j.base, res.From, j.req.Text); want != to {
			again := j.req
			again.To, again.From = want, res.From
			if res2, err2 := r.ask(ctx, eng, again); err2 == nil {
				res, to = res2, want
				if res.From == "" {
					res.From = again.From
				}
			}
		}
	}
	if err == nil && res.From == "" {
		res.From = engines.Guess(j.req.Text)
	}
	// Lengths and timings only: never the text (docs/translator.md §4).
	r.p.h.Logger().Info("translator: request", "input", r.name, "engine", eng.ID(), "chars", utf8.RuneCountInString(j.req.Text),
		"ms", time.Since(start).Milliseconds(), "ok", err == nil, "error", errorKind(err))
	return res, to, err
}

func (r *runner) ask(ctx context.Context, eng engines.Engine, req engines.Request) (engines.Result, error) {
	tctx, cancel := context.WithTimeout(ctx, r.p.opts.Timeout)
	defer cancel()
	res, err := eng.Translate(tctx, req)
	if err != nil && ctx.Err() == nil && tctx.Err() != nil {
		err = context.DeadlineExceeded
	}
	return res, err
}

// stop cancels the request being made (the text no longer asks for one,
// the window hid).
func (r *runner) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelLocked()
}

// clear forgets every translation.
func (r *runner) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelLocked()
	r.cache, r.order = map[string]*entry{}, nil
}

// close stops for good and waits for the requests to end.
func (r *runner) close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.clear()
	r.wg.Wait()
}

// reopen undoes close: the plugin is initialized again after Close.
func (r *runner) reopen() {
	r.mu.Lock()
	r.closed = false
	r.mu.Unlock()
}
