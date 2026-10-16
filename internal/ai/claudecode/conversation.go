package claudecode

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/proc"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// lostSessionText is the CLI's error when the session to resume is unknown.
const lostSessionText = "No conversation found with session ID"

// sessionInUse reports whether text is the CLI's error for a session ID
// that exists already (matched loosely: "… is already in use").
func sessionInUse(text string) bool {
	t := strings.ToLower(text)
	return strings.Contains(t, "already") && (strings.Contains(t, "in use") || strings.Contains(t, "exists"))
}

// interruptSeq numbers interrupt requests.
var interruptSeq atomic.Int64

// conversation is a backend.Conversation.
type conversation struct {
	b        *Backend
	oo       backend.OpenOptions
	closedCh chan struct{}
	busy     atomic.Bool // a turn is running

	mu       sync.Mutex
	closed   bool
	turn     *turn
	proc     *process // the persistent process, nil when none
	prev     *process // a released process that may still be exiting
	oneShot  *process // the per-turn process of the running turn
	started  bool     // the session exists: later processes resume it
	forceNew bool     // the next process starts a new session (it was lost)
	perTurn  bool     // the persistent process failed: one process per turn
}

// Send implements backend.Conversation.
func (c *conversation) Send(ctx context.Context, text string) (<-chan backend.Event, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("claudecode: conversation closed")
	}
	if c.turn != nil {
		c.mu.Unlock()
		return nil, errors.New("claudecode: an answer is being written")
	}
	t := newTurn()
	c.turn = t
	c.busy.Store(true)
	c.mu.Unlock()
	c.b.touch(c)
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
	c.proc, c.prev = nil, p
	c.mu.Unlock()
	c.b.drop(c)
	c.b.log.Debug("releasing process")
	go p.shutdown(c.b.o.GracePeriod)
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
	c.proc, c.prev, c.oneShot = nil, nil, nil
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
	c.b.forget(c)
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
	kGarbage                        // the persistent process wrote something that is not JSON
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

// run answers a turn, retrying where the CLI's failure allows it.
func (c *conversation) run(ctx context.Context, t *turn, text string) {
	start := time.Now()
	var final backend.Event
	defer func() {
		if r := recover(); r != nil {
			c.b.log.Error("claude code turn panicked", "panic", r, "stack", string(debug.Stack()))
			final = backend.Event{Kind: backend.EventError, Err: &backend.Error{Kind: backend.ErrFailed, Message: fmt.Sprint(r)}}
		}
		c.mu.Lock()
		if c.turn == t {
			c.turn = nil
		}
		c.busy.Store(false)
		c.mu.Unlock()
		c.b.log.Info("claude code turn ended", "kind", final.Kind, "cancelled", final.Cancelled,
			"error", errString(final.Err), "duration", time.Since(start), "input_len", len(text), "output_len", t.textLength())
		t.end(final)
	}()
	info, err := c.b.resolve(ctx)
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
		c.mu.Lock()
		perTurn := c.b.o.PerTurn || info.untested || c.perTurn
		c.mu.Unlock()
		var a attempt
		if perTurn {
			a = c.perTurnAttempt(t, info, text)
		} else {
			a = c.persistentAttempt(t, info, text)
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
				c.b.log.Warn("claude code lost the session; starting it again")
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
			c.b.log.Warn("claude code wrote output that is not stream-json; using one process per turn")
			c.setPerTurn()
			if !retriedFallback && !a.emitted {
				retriedFallback = true
				continue
			}
		case kSilentExit:
			if !perTurn && a.first && !retriedFallback {
				c.b.log.Warn("claude code process ended at once; using one process per turn", "error", errString(a.err))
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
			a.err = &backend.Error{Kind: backend.ErrFailed, Message: "Claude Code ended without an answer"}
		}
		final = backend.Event{Kind: backend.EventError, Err: a.err}
		return
	}
}

// persistentAttempt sends the message to the conversation's persistent
// process, starting it when needed.
func (c *conversation) persistentAttempt(t *turn, info *cliInfo, text string) attempt {
	p, err := c.ensureProcess(info)
	if err != nil {
		return attempt{kind: kError, err: err}
	}
	p.turns++
	first := p.turns == 1
	if err := p.writeJSON(userLine(text)); err != nil {
		// The process ended; what it wrote is still read below.
		p.stop()
	}
	a := c.pump(t, p, true)
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
func (c *conversation) perTurnAttempt(t *turn, info *cliInfo, text string) attempt {
	dir, err := c.b.dir()
	if err != nil {
		return attempt{kind: kError, err: &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}}
	}
	resume := c.takeResume()
	argv := c.argv(info.path, false, resume)
	p, err := startProcess(argv, dir, c.b.env(), nil)
	if err != nil {
		return attempt{kind: kError, err: &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}}
	}
	c.b.log.Debug("claude code started", "mode", "per-turn", "pid", p.p.Pid(), "resume", resume)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		p.stop()
		return attempt{kind: kCancelled}
	}
	c.oneShot = p
	c.mu.Unlock()
	if err := p.write([]byte(promptText(text))); err != nil {
		p.stop()
	}
	p.closeStdin()
	a := c.pump(t, p, false)
	c.mu.Lock()
	if c.oneShot == p {
		c.oneShot = nil
	}
	c.mu.Unlock()
	// The process exits after its result; make sure it does.
	go p.shutdown(c.b.o.GracePeriod)
	return a
}

// pump reads the output of p for one turn until the turn's result or the
// end of the process. A cancel request interrupts the answer (persistent)
// or kills the process (per turn).
func (c *conversation) pump(t *turn, p *process, persistent bool) attempt {
	st := newParser(t.emit)
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
			res, garbage := c.handleLine(st, raw)
			if st.sawInit {
				c.markStarted()
			}
			if garbage {
				if persistent {
					return attempt{kind: kGarbage, emitted: st.emitted}
				}
				continue
			}
			if res != nil {
				a := c.interpret(st, res, cancelled)
				a.emitted = st.emitted
				return a
			}
		}
		if exited {
			if cancelled {
				return attempt{kind: kCancelled}
			}
			msg := p.stderrText()
			if sessionInUse(msg) && !st.emitted {
				return attempt{kind: kInUse}
			}
			if msg == "" {
				msg = "Claude Code exited with code " + strconv.Itoa(p.code)
			}
			err := &backend.Error{Kind: classify("", msg, false), Message: msg}
			if !st.sawJSON && !st.emitted {
				return attempt{kind: kSilentExit, err: err}
			}
			return attempt{kind: kError, err: err, emitted: st.emitted}
		}
		select {
		case <-p.lines.sig:
		case <-p.done:
			exited = true // read what is left, then report the end
		case <-cancel:
			cancel, cancelled = nil, true
			if persistent {
				id := "golauncher-interrupt-" + strconv.FormatInt(interruptSeq.Add(1), 10)
				if err := p.writeJSON(interruptLine(id)); err != nil {
					p.stop()
				}
				killTimer = time.After(interruptTimeout)
			} else {
				p.stop()
			}
		case <-killTimer:
			c.b.log.Warn("claude code did not stop the answer; killing it")
			p.stop()
		}
	}
}

// handleLine parses a line, containing a panic over unexpected output.
func (c *conversation) handleLine(st *parser, raw []byte) (res *outLine, garbage bool) {
	defer func() {
		if r := recover(); r != nil {
			c.b.log.Error("claude code output line panicked", "panic", r, "len", len(raw))
			res, garbage = nil, false
		}
	}()
	return st.handle(raw)
}

// interpret maps the result line that ended a turn.
func (c *conversation) interpret(st *parser, res *outLine, cancelled bool) attempt {
	text := resultText(res)
	if res.TerminalReason == "aborted_streaming" || cancelled && res.Subtype == "error_during_execution" {
		return attempt{kind: kCancelled}
	}
	if strings.Contains(text, lostSessionText) {
		return attempt{kind: kLostSession}
	}
	if !res.IsError && st.errCode == "" {
		if !st.emitted && text != "" {
			st.emitted = true
			st.emit(backend.Event{Kind: backend.EventText, Text: text})
		}
		return attempt{kind: kDone, usage: st.usageOf(res)}
	}
	if sessionInUse(text) {
		return attempt{kind: kInUse}
	}
	if text == "" {
		text = st.errText
	}
	if text == "" {
		text = res.Subtype
	}
	return attempt{kind: kError, err: &backend.Error{Kind: classify(st.errCode, text, st.rateLimited), Message: text}}
}

// ensureProcess returns the conversation's persistent process, starting
// one when there is none (resuming the session once it exists).
func (c *conversation) ensureProcess(info *cliInfo) (*process, error) {
	c.mu.Lock()
	if p := c.proc; p != nil && p.alive() {
		c.mu.Unlock()
		return p, nil
	}
	c.proc = nil
	prev := c.prev
	c.prev = nil
	c.mu.Unlock()
	if prev != nil {
		// Let the released process finish writing the session first.
		t := time.NewTimer(c.b.o.GracePeriod + time.Second)
		select {
		case <-prev.done:
		case <-t.C:
		}
		t.Stop()
		prev.stop()
	}
	dir, err := c.b.dir()
	if err != nil {
		return nil, &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}
	}
	resume := c.takeResume()
	var p *process
	p, err = startProcess(c.argv(info.path, true, resume), dir, c.b.env(), func(p *process) { c.detach(p) })
	if err != nil {
		return nil, &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}
	}
	c.b.log.Debug("claude code started", "mode", "persistent", "pid", p.p.Pid(), "resume", resume)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		p.stop()
		return nil, errors.New("claudecode: conversation closed")
	}
	c.proc = p
	c.mu.Unlock()
	for _, v := range c.b.acquire(c) {
		v.Release()
	}
	if !p.alive() {
		c.detach(p)
	}
	return p, nil
}

// detach forgets p as the conversation's persistent process.
func (c *conversation) detach(p *process) {
	c.mu.Lock()
	mine := c.proc == p
	if mine {
		c.proc = nil
	}
	c.mu.Unlock()
	if mine {
		c.b.drop(c)
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
	c.proc = nil
	c.mu.Unlock()
	if p != nil {
		p.stop()
		c.b.drop(c)
	}
}

// argv is the CLI's command line. Every flag keeps the CLI away from the
// user's own setup and from this computer: no settings or hooks, no MCP
// servers, no slash commands, no local tools, no permission prompts.
func (c *conversation) argv(path string, persistent, resume bool) []string {
	a := []string{path, "-p"}
	if persistent {
		a = append(a, "--input-format", "stream-json")
	}
	a = append(a, "--output-format", "stream-json", "--include-partial-messages", "--verbose")
	if resume {
		a = append(a, "--resume", c.oo.SessionID)
	} else {
		a = append(a, "--session-id", c.oo.SessionID)
	}
	a = append(a, "--setting-sources", "", "--safe-mode", "--strict-mcp-config", "--disable-slash-commands")
	if c.oo.WebSearch {
		a = append(a, "--tools", "WebSearch,WebFetch", "--allowedTools", "WebSearch,WebFetch")
	} else {
		a = append(a, "--tools", "")
	}
	a = append(a, "--permission-prompts", "none")
	if sp := c.oo.SystemPrompt; sp != "" {
		if proc.IsBatch(path) {
			// cmd.exe ends the command line at a line break.
			sp = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(sp)
		}
		a = append(a, "--append-system-prompt", sp)
	}
	if c.oo.Model != "" {
		a = append(a, "--model", c.oo.Model)
	}
	return a
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

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
