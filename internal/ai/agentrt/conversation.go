package agentrt

import (
	"context"
	"fmt"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/proc"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// conversation is a backend.Conversation.
type conversation struct {
	r        *Runtime
	oo       backend.OpenOptions
	closedCh chan struct{}
	busy     atomic.Bool // a turn is running

	mu       sync.Mutex
	closed   bool
	turn     *turn
	proc     *process // the persistent process, nil when none
	codec    Codec    // the persistent process's codec
	prev     *process // a released process that may still be exiting
	oneShot  *process // the per-turn process of the running turn
	started  bool     // the session exists: later processes resume it
	forceNew bool     // the next process starts a new session (it was lost)
	perTurn  bool     // the persistent process failed: one process per turn
	// toolConfig is the MCP configuration file written for the CLI, "" when
	// none.
	toolConfig string
}

// Send implements backend.Conversation.
func (c *conversation) Send(ctx context.Context, text string) (<-chan backend.Event, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, c.r.errorf("conversation closed")
	}
	if c.turn != nil {
		c.mu.Unlock()
		return nil, c.r.errorf("an answer is being written")
	}
	t := newTurn()
	c.turn = t
	c.busy.Store(true)
	c.mu.Unlock()
	c.r.touch(c)
	go t.forward(c.closedCh)
	go func() {
		select {
		case <-ctx.Done():
			t.requestCancel()
		case <-t.finished:
		}
	}()
	go c.run(ctx, t, text)
	return t.ch, nil
}

// Cancel implements backend.Conversation.
func (c *conversation) Cancel() {
	c.mu.Lock()
	t := c.turn
	c.mu.Unlock()
	if t != nil {
		t.requestCancel()
	}
}

// Release implements backend.Conversation: an idle conversation's process
// is ended; the next Send starts a new one that resumes the session.
func (c *conversation) Release() {
	c.mu.Lock()
	p := c.proc
	if c.turn != nil || p == nil {
		c.mu.Unlock()
		return
	}
	c.proc, c.codec, c.prev = nil, nil, p
	c.mu.Unlock()
	c.r.drop(c)
	c.r.log.Debug("releasing process")
	go p.shutdown(c.r.o.GracePeriod)
}

// Close implements backend.Conversation.
func (c *conversation) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	t := c.turn
	procs := []*process{c.proc, c.prev, c.oneShot}
	c.proc, c.codec, c.prev, c.oneShot = nil, nil, nil, nil
	c.mu.Unlock()
	close(c.closedCh)
	if t != nil {
		t.requestCancel()
	}
	for _, p := range procs {
		if p != nil {
			p.stop()
		}
	}
	c.removeToolConfig()
	c.r.forget(c)
	return nil
}

// attemptKind is how an attempt to answer a turn ended.
type attemptKind int

const (
	kDone        attemptKind = iota // answered
	kCancelled                      // stopped
	kError                          // failed with err
	kLostSession                    // the CLI does not know the session to resume
	kInUse                          // the session to start exists already
	kGarbage                        // the persistent process wrote something that is not the protocol
	kSilentExit                     // the process ended without writing anything for the turn
)

// attempt is the outcome of one try to answer a turn.
type attempt struct {
	kind    attemptKind
	usage   *backend.Usage
	err     error
	emitted bool // text or tool steps reached the user
	first   bool // the process's first turn
}

// attemptOf maps a decoder's outcome.
func attemptOf(o Outcome) attempt {
	switch o.Kind {
	case Done:
		return attempt{kind: kDone, usage: o.Usage}
	case Cancelled:
		return attempt{kind: kCancelled}
	case LostSession:
		return attempt{kind: kLostSession}
	case SessionInUse:
		return attempt{kind: kInUse}
	}
	return attempt{kind: kError, err: o.Err}
}

// perTurnMode reports whether the next attempt runs a process of its own.
func (c *conversation) perTurnMode(cli *cliInfo) bool {
	if c.r.info.PerTurnOnly {
		return true
	}
	if c.r.info.NoPerTurn {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.r.o.PerTurn || cli.untested || c.perTurn
}

// run answers a turn, retrying where the CLI's failure allows it.
func (c *conversation) run(ctx context.Context, t *turn, text string) {
	start := time.Now()
	var final backend.Event
	defer func() {
		if r := recover(); r != nil {
			c.r.log.Error("agent turn panicked", "panic", r, "stack", string(debug.Stack()))
			final = backend.Event{Kind: backend.EventError, Err: &backend.Error{Kind: backend.ErrFailed, Message: fmt.Sprint(r)}}
		}
		c.mu.Lock()
		if c.turn == t {
			c.turn = nil
		}
		c.busy.Store(false)
		c.mu.Unlock()
		c.r.log.Info("agent turn ended", "kind", final.Kind, "cancelled", final.Cancelled,
			"error", errString(final.Err), "duration", time.Since(start), "input_len", len(text), "output_len", t.textLength())
		t.end(final)
	}()
	cli, err := c.r.resolve(ctx)
	if err != nil {
		if t.cancelled() {
			final = backend.Event{Kind: backend.EventDone, Cancelled: true}
		} else {
			final = backend.Event{Kind: backend.EventError, Err: err}
		}
		return
	}
	var retriedLost, retriedInUse, retriedFallback, retriedRestart bool
	for {
		if t.cancelled() {
			final = backend.Event{Kind: backend.EventDone, Cancelled: true}
			return
		}
		perTurn := c.perTurnMode(cli)
		var a attempt
		if perTurn {
			a = c.perTurnAttempt(t, cli, text)
		} else {
			a = c.persistentAttempt(t, cli, text)
		}
		switch a.kind {
		case kDone:
			final = backend.Event{Kind: backend.EventDone, Usage: a.usage}
			return
		case kCancelled:
			final = backend.Event{Kind: backend.EventDone, Cancelled: true}
			return
		case kLostSession:
			if !retriedLost && !a.emitted {
				retriedLost = true
				c.r.log.Warn("the CLI lost the session; starting it again")
				c.mu.Lock()
				c.forceNew = true
				c.mu.Unlock()
				continue
			}
		case kInUse:
			if !retriedInUse && !a.emitted {
				retriedInUse = true
				c.markStarted()
				continue
			}
		case kGarbage:
			if c.r.info.NoPerTurn {
				c.r.log.Warn("the CLI wrote output that is not its protocol; restarting it")
				if !retriedRestart && !a.emitted {
					retriedRestart = true
					continue
				}
				break
			}
			c.r.log.Warn("the CLI wrote output that is not its protocol; using one process per turn")
			c.setPerTurn()
			if !retriedFallback && !a.emitted {
				retriedFallback = true
				continue
			}
		case kSilentExit:
			if !perTurn && a.first && !retriedFallback && !c.r.info.NoPerTurn {
				c.r.log.Warn("the CLI's process ended at once; using one process per turn", "error", errString(a.err))
				c.setPerTurn()
				retriedFallback = true
				continue
			}
			if !perTurn && !retriedRestart {
				retriedRestart = true
				continue
			}
		}
		if a.err == nil {
			a.err = &backend.Error{Kind: backend.ErrFailed, Message: c.r.info.Name + " ended without an answer"}
		}
		final = backend.Event{Kind: backend.EventError, Err: a.err}
		return
	}
}

// persistentAttempt sends the message to the conversation's persistent
// process, starting it when needed.
func (c *conversation) persistentAttempt(t *turn, cli *cliInfo, text string) attempt {
	p, codec, err := c.ensureProcess(cli)
	if err != nil {
		return attempt{kind: kError, err: err}
	}
	p.turns++
	first := p.turns == 1
	msg, err := codec.Message(text)
	if err == nil {
		err = p.write(msg)
	}
	if err != nil {
		// The process ended; what it wrote is still read below.
		p.stop()
	}
	a := c.pump(t, p, codec, true)
	a.first = first
	if !p.alive() || a.kind != kDone && a.kind != kCancelled && a.kind != kError {
		// It ended (crashed) or is in no state for another turn: the next
		// Send starts a new one.
		p.stop()
		c.detach(p)
	}
	return a
}

// perTurnAttempt answers the turn with a process of its own.
func (c *conversation) perTurnAttempt(t *turn, cli *cliInfo, text string) attempt {
	dir, err := c.r.dir()
	if err != nil {
		return attempt{kind: kError, err: &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}}
	}
	mcpConfig, err := c.writeToolConfig(dir)
	if err != nil {
		return attempt{kind: kError, err: &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}}
	}
	resume := c.takeResume()
	l := c.launch(cli.path, dir, false, resume, mcpConfig)
	l.Prompt = text
	cmd, err := c.r.a.Command(l)
	if err != nil {
		return attempt{kind: kError, err: &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}}
	}
	codec := c.r.a.NewCodec(l)
	p, err := startProcess(cmd.Argv, dir, c.env(cmd), nil)
	if err != nil {
		return attempt{kind: kError, err: &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}}
	}
	c.r.log.Debug("agent CLI started", "mode", "per-turn", "pid", p.p.Pid(), "resume", resume)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		p.stop()
		return attempt{kind: kCancelled}
	}
	c.oneShot = p
	c.mu.Unlock()
	if in := codec.Prompt(text); in != nil {
		if err := p.write(in); err != nil {
			p.stop()
		}
	}
	p.closeStdin()
	a := c.pump(t, p, codec, false)
	c.mu.Lock()
	if c.oneShot == p {
		c.oneShot = nil
	}
	c.mu.Unlock()
	// The process exits after its result; make sure it does.
	go p.shutdown(c.r.o.GracePeriod)
	return a
}

// pump reads the output of p for one turn until the turn's end or the end
// of the process. A cancel request interrupts the answer (persistent, when
// the protocol can) or kills the process.
func (c *conversation) pump(t *turn, p *process, codec Codec, persistent bool) attempt {
	dec := codec.NewDecoder(TurnIO{Emit: t.emit, Reply: p.write})
	cancel := t.cancelCh
	var killTimer <-chan time.Time
	cancelled := false
	exited := false
	for {
		for {
			raw, ok := p.lines.pop()
			if !ok {
				break
			}
			end, garbage := c.decodeLine(dec, raw)
			pr := dec.Progress()
			if pr.Started {
				c.markStarted()
			}
			if pr.SessionID != "" {
				c.recordSession(pr.SessionID)
			}
			if garbage {
				if persistent {
					return attempt{kind: kGarbage, emitted: pr.Emitted}
				}
				continue
			}
			if end {
				a := attemptOf(dec.Outcome(cancelled))
				a.emitted = dec.Progress().Emitted
				return a
			}
		}
		if exited {
			if cancelled {
				return attempt{kind: kCancelled}
			}
			pr := dec.Progress()
			msg := p.stderrText()
			if msg == "" {
				msg = c.r.info.Name + " exited with code " + strconv.Itoa(p.code)
			}
			o := c.r.a.ExitOutcome(msg)
			if o.Kind == SessionInUse && !pr.Emitted {
				return attempt{kind: kInUse}
			}
			err := o.Err
			if err == nil {
				err = &backend.Error{Kind: backend.ErrFailed, Message: msg}
			}
			if !pr.Output && !pr.Emitted {
				return attempt{kind: kSilentExit, err: err}
			}
			return attempt{kind: kError, err: err, emitted: pr.Emitted}
		}
		select {
		case <-p.lines.sig:
		case <-p.done:
			exited = true // read what is left, then report the end
		case <-cancel:
			cancel, cancelled = nil, true
			var in []byte
			if persistent {
				in = codec.Interrupt()
			}
			if in != nil {
				if err := p.write(in); err != nil {
					p.stop()
				}
				killTimer = time.After(interruptTimeout)
			} else {
				p.stop()
			}
		case <-killTimer:
			c.r.log.Warn("the CLI did not stop the answer; killing it")
			p.stop()
		}
	}
}

// decodeLine decodes a line, containing a panic over unexpected output.
func (c *conversation) decodeLine(dec Decoder, raw []byte) (end, garbage bool) {
	defer func() {
		if r := recover(); r != nil {
			c.r.log.Error("agent CLI output line panicked", "panic", r, "len", len(raw))
			end, garbage = false, false
		}
	}()
	return dec.Line(raw)
}

// ensureProcess returns the conversation's persistent process and its codec,
// starting one when there is none (resuming the session once it exists).
func (c *conversation) ensureProcess(cli *cliInfo) (*process, Codec, error) {
	c.mu.Lock()
	if p := c.proc; p != nil && p.alive() {
		codec := c.codec
		c.mu.Unlock()
		return p, codec, nil
	}
	c.proc, c.codec = nil, nil
	prev := c.prev
	c.prev = nil
	c.mu.Unlock()
	if prev != nil {
		// Let the released process finish writing the session first.
		t := time.NewTimer(c.r.o.GracePeriod + time.Second)
		select {
		case <-prev.done:
		case <-t.C:
		}
		t.Stop()
		prev.stop()
	}
	failed := func(err error) (*process, Codec, error) {
		return nil, nil, &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}
	}
	dir, err := c.r.dir()
	if err != nil {
		return failed(err)
	}
	mcpConfig, err := c.writeToolConfig(dir)
	if err != nil {
		return failed(err)
	}
	resume := c.takeResume()
	l := c.launch(cli.path, dir, true, resume, mcpConfig)
	cmd, err := c.r.a.Command(l)
	if err != nil {
		return failed(err)
	}
	codec := c.r.a.NewCodec(l)
	p, err := startProcess(cmd.Argv, dir, c.env(cmd), func(p *process) { c.detach(p) })
	if err != nil {
		return failed(err)
	}
	c.r.log.Debug("agent CLI started", "mode", "persistent", "pid", p.p.Pid(), "resume", resume)
	if hs, ok := codec.(Handshaker); ok {
		ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
		go func() {
			select {
			case <-c.closedCh: // closed meanwhile
				cancel()
			case <-ctx.Done():
			}
		}()
		id, err := hs.Handshake(ctx, p)
		cancel()
		if err != nil {
			p.stop()
			return failed(err)
		}
		if id != "" {
			c.recordSession(id)
			c.markStarted()
		}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		p.stop()
		return nil, nil, c.r.errorf("conversation closed")
	}
	c.proc, c.codec = p, codec
	c.mu.Unlock()
	for _, v := range c.r.acquire(c) {
		v.Release()
	}
	if !p.alive() {
		c.detach(p)
	}
	return p, codec, nil
}

// launch is what a process of the conversation is started for.
func (c *conversation) launch(path, dir string, persistent, resume bool, mcpConfig string) Launch {
	id := c.oo.SessionID
	if m := c.r.sessionMap(); m != nil {
		id = ""
		if resume {
			id = m.Get(c.oo.SessionID)
		}
		resume = id != ""
	}
	return Launch{
		Path: path, Dir: dir, Persistent: persistent,
		SessionID: id, Resume: resume,
		Model: c.oo.Model, WebSearch: c.oo.WebSearch, SystemPrompt: c.oo.SystemPrompt,
		Tools: c.toolServer(), MCPConfig: mcpConfig,
	}
}

// recordSession keeps the session ID the CLI reported, for a CLI that
// assigns them.
func (c *conversation) recordSession(id string) {
	m := c.r.sessionMap()
	if m == nil {
		return
	}
	if err := m.Set(c.oo.SessionID, id); err != nil {
		c.r.log.Warn("saving the session ID failed", "error", err)
	}
}

// detach forgets p as the conversation's persistent process.
func (c *conversation) detach(p *process) {
	c.mu.Lock()
	mine := c.proc == p
	if mine {
		c.proc, c.codec = nil, nil
	}
	c.mu.Unlock()
	if mine {
		c.r.drop(c)
	}
}

// takeResume reports whether the next process resumes the session.
func (c *conversation) takeResume() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.forceNew {
		c.forceNew = false
		return false
	}
	return c.started || c.oo.Resume
}

func (c *conversation) markStarted() {
	c.mu.Lock()
	c.started = true
	c.mu.Unlock()
}

func (c *conversation) setPerTurn() {
	c.mu.Lock()
	c.perTurn = true
	p := c.proc
	c.proc, c.codec = nil, nil
	c.mu.Unlock()
	if p != nil {
		p.stop()
		c.r.drop(c)
	}
}

// env is the CLI's environment for a process started with cmd.
func (c *conversation) env(cmd Command) []string {
	env := c.r.Env()
	if len(cmd.Env) > 0 {
		env = proc.MergeEnv(env, cmd.Env)
	}
	return env
}

// turn is a running turn: the events go through an unbounded queue to the
// channel, so that the CLI's output is never held up by a slow reader.
type turn struct {
	ch       chan backend.Event
	cancelCh chan struct{}
	finished chan struct{}
	once     sync.Once

	mu      sync.Mutex
	queue   []backend.Event
	sig     chan struct{}
	ended   bool
	textLen int
}

func newTurn() *turn {
	return &turn{
		ch:       make(chan backend.Event, 16),
		cancelCh: make(chan struct{}),
		finished: make(chan struct{}),
		sig:      make(chan struct{}, 1),
	}
}

// emit queues an event; text joins queued text.
func (t *turn) emit(ev backend.Event) {
	t.mu.Lock()
	if ev.Kind == backend.EventText {
		t.textLen += len(ev.Text)
		if n := len(t.queue); n > 0 && t.queue[n-1].Kind == backend.EventText {
			t.queue[n-1].Text += ev.Text
			t.mu.Unlock()
			return
		}
	}
	t.queue = append(t.queue, ev)
	t.mu.Unlock()
	t.signal()
}

// end queues the final event.
func (t *turn) end(ev backend.Event) {
	t.mu.Lock()
	t.queue = append(t.queue, ev)
	t.ended = true
	t.mu.Unlock()
	close(t.finished)
	t.signal()
}

func (t *turn) signal() {
	select {
	case t.sig <- struct{}{}:
	default:
	}
}

func (t *turn) textLength() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.textLen
}

func (t *turn) requestCancel() { t.once.Do(func() { close(t.cancelCh) }) }

func (t *turn) cancelled() bool {
	select {
	case <-t.cancelCh:
		return true
	default:
		return false
	}
}

// forward delivers the queued events and closes the channel after the
// final one. Once the conversation is closed, it gives up on a reader that
// does not read.
func (t *turn) forward(closed <-chan struct{}) {
	for {
		t.mu.Lock()
		if len(t.queue) == 0 {
			ended := t.ended
			t.mu.Unlock()
			if ended {
				close(t.ch)
				return
			}
			<-t.sig
			continue
		}
		ev := t.queue[0]
		t.queue = t.queue[1:]
		t.mu.Unlock()
		select {
		case t.ch <- ev:
			continue
		case <-closed:
		}
		timer := time.NewTimer(time.Second)
		select {
		case t.ch <- ev:
			timer.Stop()
		case <-timer.C:
			// Nobody reads: wait for the end, then close.
			<-t.finished
			close(t.ch)
			return
		}
	}
}
