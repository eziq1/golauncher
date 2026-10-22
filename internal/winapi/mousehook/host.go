package mousehook

import (
	"bufio"
	"encoding/json"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eziq1/golauncher/internal/ring"
)

// The hook host (R122, docs/action-ring.md §9): on Windows the low-level
// mouse hook runs in a process of its own, "golauncher --mouse-hook-host",
// which does nothing else, so that its callback is never kept waiting by
// the launcher's work (a busy Go process delays the callback, and Windows'
// input thread waits for it with the keyboard behind). The ring.Machine
// runs there too, since whether the button is kept from the apps is decided
// in the callback, with Allowed (AppAt, the full-screen check, the
// exceptions) and the replays. Golauncher talks to it through Remote.
//
// The protocol is one JSON object per line; unknown lines and fields are
// ignored, so that it can grow without a version number.
//
// Commands, golauncher → host (its standard input):
//
//	{"cmd":"hold","ms":300}                the hold before the ring opens
//	{"cmd":"exceptions","apps":["mstsc"]}  the apps the ring leaves alone
//	{"cmd":"open","open":false,"seq":3}    the ring closed (Machine.SetOpen)
//	{"cmd":"reset","seq":4}                forget the press (Machine.Reset)
//	{"cmd":"start"}                        install the hook
//
// Golauncher sends the hold and the exceptions before start, so that the
// hook never runs without them. The end of its input (golauncher quit or
// crashed) unhooks and ends the host.
//
// Events, host → golauncher (its standard output):
//
//	{"ev":"ready"}                          the hook is installed
//	{"ev":"error","msg":"…"}                it could not be: the host ends
//	{"ev":"open","x":1,"y":2,"seq":3}       ring.Callbacks.Open
//	{"ev":"track","x":1,"y":2}              Track: only the newest is sent
//	{"ev":"release","x":1,"y":2,"moved":true}
//	{"ev":"press","x":1,"y":2,"middle":true}
//	{"ev":"button","down":true}             Machine.ButtonDown changed
//
// The machine opens the ring by itself (the hold ran out) and golauncher
// closes it; seq tells an open from before a close golauncher sent: each
// close carries a new seq, and an open carries the seq of the last close
// the host had applied, so that golauncher drops an open its close undoes
// (as the launcher's openRing found Machine.Open false in one process).

// HostFlag is the command line of the hook host.
const HostFlag = "--mouse-hook-host"

// Commands and events.
const (
	cmdHold       = "hold"
	cmdExceptions = "exceptions"
	cmdOpen       = "open"
	cmdReset      = "reset"
	cmdStart      = "start"

	evReady   = "ready"
	evError   = "error"
	evOpen    = "open"
	evTrack   = "track"
	evRelease = "release"
	evPress   = "press"
	evButton  = "button"
)

// hostMsg is a line of the protocol, either way.
type hostMsg struct {
	Cmd    string   `json:"cmd,omitempty"`
	Ev     string   `json:"ev,omitempty"`
	X      float64  `json:"x,omitempty"`
	Y      float64  `json:"y,omitempty"`
	MS     int      `json:"ms,omitempty"`
	Apps   []string `json:"apps,omitempty"`
	Open   bool     `json:"open,omitempty"`
	Moved  bool     `json:"moved,omitempty"`
	Middle bool     `json:"middle,omitempty"`
	Down   bool     `json:"down,omitempty"`
	Seq    uint64   `json:"seq,omitempty"`
	Msg    string   `json:"msg,omitempty"`
}

// appendEvent appends event m as a line, without allocating (the host
// keeps its heap small: the hook's thread is never held by a long GC).
func appendEvent(b []byte, m *hostMsg) []byte {
	b = append(b, `{"ev":"`...)
	b = append(b, m.Ev...)
	b = append(b, '"')
	switch m.Ev {
	case evOpen, evTrack, evRelease, evPress:
		b = append(b, `,"x":`...)
		b = strconv.AppendFloat(b, m.X, 'f', -1, 64)
		b = append(b, `,"y":`...)
		b = strconv.AppendFloat(b, m.Y, 'f', -1, 64)
	}
	switch m.Ev {
	case evOpen:
		b = append(b, `,"seq":`...)
		b = strconv.AppendUint(b, m.Seq, 10)
	case evRelease:
		b = append(b, `,"moved":`...)
		b = strconv.AppendBool(b, m.Moved)
	case evPress:
		b = append(b, `,"middle":`...)
		b = strconv.AppendBool(b, m.Middle)
	case evButton:
		b = append(b, `,"down":`...)
		b = strconv.AppendBool(b, m.Down)
	case evError:
		msg, _ := json.Marshal(m.Msg) // once, as the host ends
		b = append(b, `,"msg":`...)
		b = append(b, msg...)
	}
	return append(b, "}\n"...)
}

// outbox holds the host's events until its writer sends them: the hook's
// thread only queues, never waits for the pipe. The pointer is coalesced:
// only the newest pending one is sent, in its place among the others.
type outbox struct {
	mu       sync.Mutex
	q        []hostMsg
	track    ring.Point
	hasTrack bool
	closed   bool
	wake     chan struct{}
}

// maxQueued bounds the events waiting: more than a hand makes, and the
// queue never grows (the writer is stuck if it is full).
const maxQueued = 64

func newOutbox() *outbox {
	return &outbox{q: make([]hostMsg, 0, maxQueued), wake: make(chan struct{}, 1)}
}

func (b *outbox) push(m hostMsg) {
	b.mu.Lock()
	if !b.closed {
		b.flushTrack()
		if len(b.q) < maxQueued {
			b.q = append(b.q, m)
		}
	}
	b.mu.Unlock()
	b.signal()
}

func (b *outbox) pushTrack(pt ring.Point) {
	b.mu.Lock()
	b.track, b.hasTrack = pt, !b.closed
	b.mu.Unlock()
	b.signal()
}

// flushTrack queues the pending pointer before a later event. b.mu is held.
func (b *outbox) flushTrack() {
	if b.hasTrack && len(b.q) < maxQueued {
		b.q = append(b.q, hostMsg{Ev: evTrack, X: b.track.X, Y: b.track.Y})
	}
	b.hasTrack = false
}

// close lets the writer send what is queued and return.
func (b *outbox) close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	b.signal()
}

func (b *outbox) signal() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// run writes the events to w until the outbox is closed or w fails.
func (b *outbox) run(w io.Writer) error {
	batch := make([]hostMsg, 0, maxQueued+1)
	var line []byte
	for range b.wake {
		b.mu.Lock()
		b.flushTrack()
		batch = append(batch[:0], b.q...)
		clear(b.q) // no strings kept alive
		b.q = b.q[:0]
		closed := b.closed
		b.mu.Unlock()
		line = line[:0]
		for i := range batch {
			line = appendEvent(line, &batch[i])
		}
		if len(line) > 0 {
			if _, err := w.Write(line); err != nil {
				return err
			}
		}
		if closed {
			return nil
		}
	}
	return nil
}

// hostDeps is what the host takes from the platform; tests replace it.
type hostDeps struct {
	start func(Handler) (stop func(), err error)
	// replay gives the apps the button from another goroutine; replayNow
	// before returning, as the host ends.
	replay, replayNow func(down, up bool)
	appAt             func(ring.Point) App
	threshold         float64
	clock             ring.Clock
}

// defaultHold is the hold until golauncher sends its own.
const defaultHold = 300 * time.Millisecond

// host is the hook host's state.
type host struct {
	m      *ring.Machine
	box    *outbox
	ending atomic.Bool // the host ends: replays go out at once

	mu         sync.Mutex
	seq        uint64 // the last close applied
	down       bool   // the ButtonDown last sent
	exceptions []string
	appAt      func(ring.Point) App
}

// runHost runs the hook host: commands from in, events to out, until in
// ends.
func runHost(in io.Reader, out io.Writer, d hostDeps) error {
	h := &host{box: newOutbox(), appAt: d.appAt}
	replay := func(down, up bool) {
		if h.ending.Load() {
			d.replayNow(down, up) // the process ends next: no goroutine left to send it
			return
		}
		d.replay(down, up)
	}
	h.m = ring.NewMachine(ring.Options{
		Hold:      defaultHold,
		Threshold: d.threshold,
		Clock:     d.clock,
		Callbacks: ring.Callbacks{
			Allowed: h.allowed,
			Replay:  replay,
			Open:    h.opened,
			Track:   h.box.pushTrack,
			Release: func(pt ring.Point, moved bool) {
				h.box.push(hostMsg{Ev: evRelease, X: pt.X, Y: pt.Y, Moved: moved})
			},
			Press: func(pt ring.Point, middle bool) {
				h.box.push(hostMsg{Ev: evPress, X: pt.X, Y: pt.Y, Middle: middle})
			},
		},
	})
	written := make(chan error, 1)
	go func() { written <- h.box.run(out) }()
	var stop func()
	var err error
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 4096), 1<<20) // a long exception list
	for sc.Scan() {
		var c hostMsg
		if json.Unmarshal(sc.Bytes(), &c) != nil {
			continue
		}
		if c.Cmd == cmdStart {
			if stop != nil {
				continue
			}
			if stop, err = d.start(hostHandler{h}); err != nil {
				h.box.push(hostMsg{Ev: evError, Msg: err.Error()})
				break
			}
			h.box.push(hostMsg{Ev: evReady})
			continue
		}
		h.apply(c)
	}
	if err == nil {
		err = sc.Err()
	}
	// Golauncher is gone: the hook stops, and a press it held back goes to
	// the app.
	h.ending.Store(true)
	if stop != nil {
		stop()
	}
	h.m.Reset()
	h.box.close()
	<-written
	return err
}

// apply carries out command c.
func (h *host) apply(c hostMsg) {
	switch c.Cmd {
	case cmdHold:
		h.m.SetHold(time.Duration(c.MS) * time.Millisecond)
	case cmdExceptions:
		h.mu.Lock()
		h.exceptions = c.Apps
		h.mu.Unlock()
	case cmdOpen:
		h.mu.Lock()
		h.m.SetOpen(c.Open)
		if !c.Open {
			h.seq = c.Seq
		}
		h.mu.Unlock()
	case cmdReset:
		h.mu.Lock()
		h.m.Reset()
		h.seq = c.Seq
		h.mu.Unlock()
		h.syncButton()
	}
}

// allowed is Callbacks.Allowed, on the hook's thread as the button goes
// down: as the launcher's own (shell.ringAllowed).
func (h *host) allowed(pt ring.Point) bool {
	a := h.appAt(pt)
	if a.Blocked || a.FullScreen {
		return false
	}
	h.mu.Lock()
	ex := h.exceptions
	h.mu.Unlock()
	return !ring.Excluded(ex, a.Name, a.Bundle, a.Title)
}

// opened is Callbacks.Open: sent with the seq of the last close applied,
// unless a close came between (h.mu orders the two).
func (h *host) opened(pt ring.Point) {
	h.mu.Lock()
	seq, open := h.seq, h.m.Open()
	h.mu.Unlock()
	if open {
		h.box.push(hostMsg{Ev: evOpen, X: pt.X, Y: pt.Y, Seq: seq})
	}
}

// syncButton tells golauncher when Machine.ButtonDown changed.
func (h *host) syncButton() {
	h.mu.Lock()
	if d := h.m.ButtonDown(); d != h.down {
		h.down = d
		h.box.push(hostMsg{Ev: evButton, Down: d})
	}
	h.mu.Unlock()
}

// hostHandler is the machine as the hook sees it in the host: after each
// event the machine takes, the button's state may have changed. The moves
// and buttons it does not want are skipped by the hook before Handle,
// reading an atomic: nothing is locked nor allocated for them.
type hostHandler struct{ h *host }

func (x hostHandler) Handle(ev ring.Event) bool {
	swallow := x.h.m.Handle(ev)
	if !ev.Own && !ev.Injected {
		x.h.syncButton()
	}
	return swallow
}

func (x hostHandler) WantsMoves() bool   { return x.h.m.WantsMoves() }
func (x hostHandler) WantsButtons() bool { return x.h.m.WantsButtons() }
