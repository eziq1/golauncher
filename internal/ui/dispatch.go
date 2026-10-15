package ui

import (
	"context"
	"log/slog"
	"sync"
)

// Dispatcher runs launcher work on a single goroutine, in the order it was
// queued, so that key presses never block the UI thread and are handled in
// the order they were typed. It is safe for concurrent use.
type Dispatcher struct {
	ctx context.Context
	log *slog.Logger

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []func(context.Context)
	running bool
	closed  bool
}

// NewDispatcher starts a Dispatcher that stops when ctx is done. A panic in
// queued work is logged to log and does not stop the Dispatcher.
func NewDispatcher(ctx context.Context, log *slog.Logger) *Dispatcher {
	d := &Dispatcher{ctx: ctx, log: log}
	d.cond = sync.NewCond(&d.mu)
	context.AfterFunc(ctx, func() {
		d.mu.Lock()
		d.closed = true
		d.cond.Broadcast()
		d.mu.Unlock()
	})
	go d.loop()
	return d
}

// Do queues fn. It never blocks.
func (d *Dispatcher) Do(fn func(ctx context.Context)) {
	d.mu.Lock()
	if !d.closed {
		d.queue = append(d.queue, fn)
		d.cond.Broadcast()
	}
	d.mu.Unlock()
}

// Pending reports whether work is queued or running.
func (d *Dispatcher) Pending() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.queue) > 0 || d.running
}

// Wait blocks until all queued work has run.
func (d *Dispatcher) Wait() {
	d.mu.Lock()
	for (len(d.queue) > 0 || d.running) && !d.closed {
		d.cond.Wait()
	}
	d.mu.Unlock()
}

func (d *Dispatcher) loop() {
	d.mu.Lock()
	for {
		for len(d.queue) == 0 && !d.closed {
			d.cond.Wait()
		}
		if d.closed {
			d.queue = nil
			d.cond.Broadcast()
			d.mu.Unlock()
			return
		}
		fn := d.queue[0]
		d.queue = d.queue[1:]
		d.running = true
		d.mu.Unlock()
		d.run(fn)
		d.mu.Lock()
		d.running = false
		d.cond.Broadcast()
	}
}

func (d *Dispatcher) run(fn func(context.Context)) {
	defer func() {
		if r := recover(); r != nil && d.log != nil {
			d.log.Error("launcher work panicked", "panic", r)
		}
	}()
	fn(d.ctx)
}
