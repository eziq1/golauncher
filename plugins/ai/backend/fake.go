package backend

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

// Fake is a Backend for tests and glctl's fake environment. By default it
// answers "You asked: <text>" in a few pieces at once; Reply, Delay and the
// error fields change that. It is safe for concurrent use; set the fields
// before handing it out.
type Fake struct {
	// Reply returns the pieces of the answer to text (nil: the default).
	Reply func(text string) []string
	// Delay is waited before each piece, for tests of streaming and Stop.
	Delay time.Duration
	// Gate, when set, is received from before each piece instead of
	// waiting Delay, so a test can release the answer piece by piece.
	Gate chan struct{}
	// Go runs the answers with Delay or Gate in the background (e.g.
	// plugin.Host.Go); without it Send waits for the whole answer.
	Go func(fn func())
	// CheckErr is returned by Check; SendErr ends every turn with an
	// EventError.
	CheckErr error
	SendErr  error

	mu    sync.Mutex
	opens []OpenOptions
	sends []string
	convs []*fakeConv
}

var _ Backend = (*Fake)(nil)

// Name implements Backend.
func (f *Fake) Name() string { return "Claude Code" }

// Check implements Backend.
func (f *Fake) Check(context.Context) (Status, error) {
	if f.CheckErr != nil {
		return Status{}, f.CheckErr
	}
	return Status{Version: "2.1.0"}, nil
}

// Open implements Backend.
func (f *Fake) Open(_ context.Context, o OpenOptions) (Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opens = append(f.opens, o)
	c := &fakeConv{f: f, opts: o}
	f.convs = append(f.convs, c)
	return c, nil
}

// Opens returns the options of the conversations opened so far.
func (f *Fake) Opens() []OpenOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.opens)
}

// Sends returns the messages sent so far, in order.
func (f *Fake) Sends() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sends)
}

// Released reports how many conversations have been released or closed
// and not used since.
func (f *Fake) Released() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.convs {
		c.mu.Lock()
		if c.released {
			n++
		}
		c.mu.Unlock()
	}
	return n
}

func (f *Fake) reply(text string) []string {
	if f.Reply != nil {
		return f.Reply(text)
	}
	return []string{"You ", "asked: ", text}
}

type fakeConv struct {
	f    *Fake
	opts OpenOptions

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	released bool
}

func (c *fakeConv) Send(ctx context.Context, text string) (<-chan Event, error) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil, errors.New("backend: an answer is being written")
	}
	c.running, c.released = true, false
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.mu.Unlock()
	c.f.mu.Lock()
	c.f.sends = append(c.f.sends, text)
	c.f.mu.Unlock()

	out := make(chan Event, 256)
	finish := func(e Event) {
		cancel()
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
		out <- e
		close(out)
	}
	run := func() {
		if c.f.SendErr != nil {
			finish(Event{Kind: EventError, Err: c.f.SendErr})
			return
		}
		var all strings.Builder
		for _, piece := range c.f.reply(text) {
			if err := c.f.wait(ctx); err != nil {
				finish(Event{Kind: EventDone, Cancelled: true})
				return
			}
			all.WriteString(piece)
			out <- Event{Kind: EventText, Text: piece}
		}
		model := c.opts.Model
		if model == "" {
			model = "fake-model"
		}
		finish(Event{Kind: EventDone, Usage: &Usage{InputTokens: len(text), OutputTokens: all.Len(), Model: model}})
	}
	if c.f.Go == nil || c.f.Delay == 0 && c.f.Gate == nil {
		run() // at once: the channel holds the whole answer
	} else {
		c.f.Go(run)
	}
	return out, nil
}

// wait waits before the next piece of an answer.
func (f *Fake) wait(ctx context.Context) error {
	switch {
	case f.Gate != nil:
		select {
		case <-f.Gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	case f.Delay > 0:
		t := time.NewTimer(f.Delay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func (c *fakeConv) Cancel() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
}

func (c *fakeConv) Release() {
	c.mu.Lock()
	c.released = true
	c.mu.Unlock()
}

func (c *fakeConv) Close() error {
	c.Cancel()
	c.Release()
	return nil
}
