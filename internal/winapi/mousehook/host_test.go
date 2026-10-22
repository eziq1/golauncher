package mousehook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/ring"
)

// The hook host and Remote talk through in-memory pipes here, with a fake
// hook (the test calls the Handler as the hook's thread would) and a fake
// clock: no hook is installed.

// fakeClock fires its timers when told to.
type fakeClock struct {
	mu sync.Mutex
	fs []func()
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := len(c.fs)
	c.fs = append(c.fs, f)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		was := c.fs[i] != nil
		c.fs[i] = nil
		return was
	}
}

func (c *fakeClock) fire() {
	c.mu.Lock()
	var due []func()
	for i, f := range c.fs {
		if f != nil {
			due = append(due, f)
			c.fs[i] = nil
		}
	}
	c.mu.Unlock()
	for _, f := range due {
		f()
	}
}

// fakeHost runs runHost on pipes, as spawnHost runs the real one.
type fakeHost struct {
	clk      fakeClock
	startErr error

	mu      sync.Mutex
	h       Handler
	app     App
	calls   []string // replays and the hook's start and stop
	crashed chan struct{}
	inR     *io.PipeReader
	outW    *io.PipeWriter
}

func (f *fakeHost) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeHost) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls
	f.calls = nil
	return c
}

func (f *fakeHost) handler() Handler {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.h
}

func (f *fakeHost) setApp(a App) {
	f.mu.Lock()
	f.app = a
	f.mu.Unlock()
}

func (f *fakeHost) deps() hostDeps {
	return hostDeps{
		start: func(h Handler) (func(), error) {
			if f.startErr != nil {
				return nil, f.startErr
			}
			f.mu.Lock()
			f.h = h
			f.mu.Unlock()
			f.record("start")
			return func() { f.record("stop") }, nil
		},
		replay:    func(down, up bool) { f.record(fmt.Sprintf("replay %v %v", down, up)) },
		replayNow: func(down, up bool) { f.record(fmt.Sprintf("replayNow %v %v", down, up)) },
		appAt: func(ring.Point) App {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.app
		},
		threshold: 8,
		clock:     &f.clk,
	}
}

func (f *fakeHost) spawn() (*hostConn, error) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	crashed := make(chan struct{})
	f.mu.Lock()
	f.inR, f.outW, f.crashed = inR, outW, crashed
	f.mu.Unlock()
	ended := make(chan error, 1)
	go func() {
		err := runHost(inR, outW, f.deps())
		outW.Close()
		ended <- err
	}()
	return &hostConn{
		in:  inW,
		out: outR,
		wait: func() error {
			select {
			case err := <-ended:
				return err
			case <-crashed:
				return errors.New("crashed")
			}
		},
		kill: func() { inR.CloseWithError(errors.New("killed")) },
		done: make(chan struct{}),
	}, nil
}

// crash ends the host as a crash would: its output ends at once.
func (f *fakeHost) crash() {
	f.mu.Lock()
	defer f.mu.Unlock()
	close(f.crashed)
	f.outW.CloseWithError(io.EOF)
	f.inR.CloseWithError(errors.New("crashed"))
}

// events records the Remote's callbacks.
type events struct {
	mu  sync.Mutex
	got []string
}

func (e *events) add(s string) {
	e.mu.Lock()
	e.got = append(e.got, s)
	e.mu.Unlock()
}

func (e *events) take() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	g := e.got
	e.got = nil
	return g
}

func (e *events) callbacks() ring.Callbacks {
	return ring.Callbacks{
		Open:    func(pt ring.Point) { e.add(fmt.Sprintf("open %v %v", pt.X, pt.Y)) },
		Track:   func(pt ring.Point) { e.add(fmt.Sprintf("track %v %v", pt.X, pt.Y)) },
		Release: func(pt ring.Point, moved bool) { e.add(fmt.Sprintf("release %v %v %v", pt.X, pt.Y, moved)) },
		Press:   func(pt ring.Point, middle bool) { e.add(fmt.Sprintf("press %v %v %v", pt.X, pt.Y, middle)) },
	}
}

func newTestRemote(f *fakeHost, e *events, exited func(error)) *Remote {
	r := NewRemote(300*time.Millisecond, e.callbacks(), exited)
	r.spawn = f.spawn
	return r
}

// eventually waits until cond holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 500 {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never: %s", what)
}

// waitEvents waits until the callbacks so far are want.
func waitEvents(t *testing.T, e *events, want ...string) {
	t.Helper()
	var got []string
	eventually(t, fmt.Sprintf("events %q", want), func() bool {
		got = append(got, e.take()...)
		return len(got) >= len(want)
	})
	if !slices.Equal(got, want) {
		t.Fatalf("events %q, want %q", got, want)
	}
}

// TestRemote runs the machine in the host through Remote: the settings
// reach it before the hook starts, its decisions come back as callbacks,
// Open and ButtonDown follow it, closing the ring reaches it, and stopping
// ends it, giving the app a press it held back.
func TestRemote(t *testing.T) {
	f := &fakeHost{}
	e := &events{}
	r := newTestRemote(f, e, func(err error) { t.Errorf("exited: %v", err) })
	r.SetExceptions([]string{"mstsc"})
	stop, err := r.Start()
	if err != nil {
		t.Fatal(err)
	}
	if c := f.take(); !slices.Equal(c, []string{"start"}) {
		t.Fatalf("calls %q", c)
	}
	h := f.handler()

	// An exception: the button is left alone.
	f.setApp(App{Name: "mstsc.exe"})
	if h.Handle(ring.Event{Kind: ring.MiddleDown}) || h.Handle(ring.Event{Kind: ring.MiddleUp}) {
		t.Error("the button was kept from an exception")
	}

	// A click goes back to the app.
	f.setApp(App{Name: "chrome.exe"})
	if !h.Handle(ring.Event{Kind: ring.MiddleDown, Pt: ring.Point{X: 10, Y: 20}}) {
		t.Fatal("the press was not held back")
	}
	eventually(t, "button down", r.ButtonDown)
	h.Handle(ring.Event{Kind: ring.MiddleUp, Pt: ring.Point{X: 10, Y: 20}})
	eventually(t, "button up", func() bool { return !r.ButtonDown() })
	if c := f.take(); !slices.Equal(c, []string{"replay true true"}) {
		t.Errorf("calls %q", c)
	}

	// Held: the ring opens, follows the pointer and is let go after a drag.
	h.Handle(ring.Event{Kind: ring.MiddleDown, Pt: ring.Point{X: 10, Y: 20}})
	f.clk.fire()
	waitEvents(t, e, "open 10 20")
	if !r.Open() || !r.ButtonDown() {
		t.Errorf("open %v, down %v", r.Open(), r.ButtonDown())
	}
	if !h.WantsMoves() {
		t.Fatal("moves not wanted while holding")
	}
	h.Handle(ring.Event{Kind: ring.Move, Pt: ring.Point{X: 50, Y: 20.5}})
	waitEvents(t, e, "track 50 20.5")
	h.Handle(ring.Event{Kind: ring.MiddleUp, Pt: ring.Point{X: 50, Y: 20.5}})
	waitEvents(t, e, "release 50 20.5 true")
	eventually(t, "button up", func() bool { return !r.ButtonDown() })

	// Still open: another button reports a press; closed, nothing.
	h.Handle(ring.Event{Kind: ring.OtherDown, Pt: ring.Point{X: 1, Y: 2}})
	waitEvents(t, e, "press 1 2 false")
	r.SetOpen(false)
	if r.Open() {
		t.Error("open after SetOpen(false)")
	}
	eventually(t, "the host closed the ring", func() bool { return !h.WantsButtons() })

	// The hold reaches the host.
	r.SetHold(500 * time.Millisecond)

	// Stopped with a press held back: it goes to the app as the host ends.
	h.Handle(ring.Event{Kind: ring.MiddleDown})
	stop()
	stop()
	if c := f.take(); !slices.Equal(c, []string{"stop", "replayNow true false"}) {
		t.Errorf("calls %q", c)
	}
	if r.Open() || r.ButtonDown() {
		t.Error("state left after stop")
	}
	// Commands without a host are kept for the next one.
	r.SetExceptions(nil)
	r.Reset()
}

// TestRemoteStaleOpen: an open the host sent before applying a close
// golauncher sent since is dropped; one after it is taken.
func TestRemoteStaleOpen(t *testing.T) {
	e := &events{}
	c := &hostConn{in: nopWriteCloser{}}
	r := &Remote{cb: e.callbacks(), conn: c}
	r.SetOpen(false) // seq 1
	r.dispatch(c, hostMsg{Ev: evOpen, X: 1, Seq: 0})
	if r.Open() || len(e.take()) != 0 {
		t.Error("a stale open was taken")
	}
	r.dispatch(c, hostMsg{Ev: evOpen, X: 2, Seq: 1})
	if !r.Open() || !slices.Equal(e.take(), []string{"open 2 0"}) {
		t.Error("an open was dropped")
	}
	r.Reset() // seq 2
	if r.Open() {
		t.Error("open after Reset")
	}
	r.dispatch(c, hostMsg{Ev: evOpen, Seq: 1})
	if r.Open() {
		t.Error("an open from before the reset was taken")
	}
	// Events of an older host are ignored.
	r.dispatch(&hostConn{}, hostMsg{Ev: evButton, Down: true})
	if r.ButtonDown() {
		t.Error("an older host's state taken")
	}
}

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

// TestHostOpenAfterClose: the host sends an open only while its machine
// is open, with the seq of the last close it applied.
func TestHostOpenAfterClose(t *testing.T) {
	f := &fakeHost{}
	var out bytes.Buffer
	inR, inW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runHost(inR, &out, f.deps()) }()
	fmt.Fprintln(inW, `{"cmd":"start"}`)
	eventually(t, "started", func() bool { return f.handler() != nil })
	h := f.handler()
	fmt.Fprintln(inW, `{"cmd":"open","open":false,"seq":7}`) // a close of an older ring
	// A pipe's write returns once read: the scanner reads a line after it
	// applied the one before.
	fmt.Fprintln(inW, `{"cmd":"hold","ms":300}`)
	h.Handle(ring.Event{Kind: ring.MiddleDown, Pt: ring.Point{X: 3, Y: 4}})
	f.clk.fire()
	inW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var lines []string
	for l := range strings.Lines(out.String()) {
		lines = append(lines, strings.TrimSpace(l))
	}
	want := []string{`{"ev":"ready"}`, `{"ev":"button","down":true}`, `{"ev":"open","x":3,"y":4,"seq":7}`}
	if !slices.Equal(lines, want) {
		t.Errorf("events\n%q\nwant\n%q", lines, want)
	}
}

// TestRemoteStartError: a hook that cannot be installed fails Start with
// its reason, and nothing is reported as exited.
func TestRemoteStartError(t *testing.T) {
	f := &fakeHost{startErr: errors.New("no hook")}
	r := newTestRemote(f, &events{}, func(err error) { t.Errorf("exited: %v", err) })
	if _, err := r.Start(); err == nil || !strings.Contains(err.Error(), "no hook") {
		t.Fatalf("err %v", err)
	}
	f.startErr = nil
	stop, err := r.Start()
	if err != nil {
		t.Fatal(err)
	}
	stop()
}

// TestRemoteCrash: a host that ends by itself is reported once, its state
// is dropped, and a new one can start.
func TestRemoteCrash(t *testing.T) {
	f := &fakeHost{}
	exited := make(chan error, 2)
	r := newTestRemote(f, &events{}, func(err error) { exited <- err })
	stop, err := r.Start()
	if err != nil {
		t.Fatal(err)
	}
	f.handler().Handle(ring.Event{Kind: ring.MiddleDown})
	eventually(t, "button down", r.ButtonDown)
	f.crash()
	select {
	case err := <-exited:
		if err == nil {
			t.Error("no error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("not reported")
	}
	if r.ButtonDown() || r.Open() {
		t.Error("state kept after the crash")
	}
	stop() // after the crash: nothing left to stop
	stop2, err := r.Start()
	if err != nil {
		t.Fatal(err)
	}
	stop2()
	select {
	case err := <-exited:
		t.Errorf("stopping reported as exited: %v", err)
	default:
	}
}

// TestOutbox: the pointer is coalesced in its place among the other
// events, which keep their order.
func TestOutbox(t *testing.T) {
	b := newOutbox()
	b.pushTrack(ring.Point{X: 1})
	b.pushTrack(ring.Point{X: 2})
	b.push(hostMsg{Ev: evRelease, X: 3, Moved: true})
	b.pushTrack(ring.Point{X: 4})
	b.pushTrack(ring.Point{X: 5})
	b.close()
	var out bytes.Buffer
	if err := b.run(&out); err != nil {
		t.Fatal(err)
	}
	want := `{"ev":"track","x":2,"y":0}` + "\n" + `{"ev":"release","x":3,"y":0,"moved":true}` + "\n" + `{"ev":"track","x":5,"y":0}` + "\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
	// Full: more are dropped, not queued.
	b = newOutbox()
	for range 2 * maxQueued {
		b.push(hostMsg{Ev: evPress})
	}
	if len(b.q) != maxQueued || cap(b.q) != maxQueued {
		t.Errorf("queue %d/%d", len(b.q), cap(b.q))
	}
}

// TestAppendEvent: every event is a JSON line golauncher reads back, and
// the pointer is written without allocating.
func TestAppendEvent(t *testing.T) {
	for _, m := range []hostMsg{
		{Ev: evReady},
		{Ev: evError, Msg: `bad "hook"`},
		{Ev: evOpen, X: -1.5, Y: 2, Seq: 9},
		{Ev: evTrack, X: 1e6, Y: -3},
		{Ev: evRelease, X: 1, Y: 2, Moved: true},
		{Ev: evPress, X: 1, Y: 2, Middle: true},
		{Ev: evButton, Down: true},
	} {
		line := appendEvent(nil, &m)
		var got hostMsg
		if err := json.Unmarshal(line, &got); err != nil || line[len(line)-1] != '\n' {
			t.Fatalf("%s: %v", line, err)
		}
		if got.Ev != m.Ev || got.X != m.X || got.Y != m.Y || got.Seq != m.Seq || got.Moved != m.Moved ||
			got.Middle != m.Middle || got.Down != m.Down || got.Msg != m.Msg {
			t.Errorf("%s read as %+v", line, got)
		}
	}
	buf := make([]byte, 0, 256)
	m := hostMsg{Ev: evTrack, X: 1234, Y: 567}
	if n := testing.AllocsPerRun(100, func() { buf = appendEvent(buf[:0], &m) }); n != 0 {
		t.Errorf("%v allocations", n)
	}
}

// TestHostHandlerIdle: while no press is in progress the host's handler
// wants neither moves nor buttons, so that the hook skips them; asking
// allocates nothing.
func TestHostHandlerIdle(t *testing.T) {
	h := &host{box: newOutbox(), appAt: func(ring.Point) App { return App{} }}
	h.m = ring.NewMachine(ring.Options{Hold: time.Hour})
	x := hostHandler{h}
	if x.WantsMoves() || x.WantsButtons() {
		t.Fatal("idle handler wants events")
	}
	if n := testing.AllocsPerRun(100, func() { _ = x.WantsMoves() }); n != 0 {
		t.Errorf("%v allocations", n)
	}
}

// TestHostLockOrder: the machine calls allowed holding its lock while
// commands take h.mu around the machine's methods; allowed must not take
// h.mu, or the hook's thread and the command loop deadlock.
func TestHostLockOrder(t *testing.T) {
	h := &host{box: newOutbox(), appAt: func(ring.Point) App { return App{Name: "app.exe"} }}
	h.m = ring.NewMachine(ring.Options{Hold: time.Hour, Threshold: 8, Callbacks: ring.Callbacks{Allowed: h.allowed}})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 2000 {
			h.m.Handle(ring.Event{Kind: ring.MiddleDown})
			h.m.Handle(ring.Event{Kind: ring.MiddleUp})
		}
	}()
	for i := range 2000 {
		h.apply(hostMsg{Cmd: cmdOpen, Open: false, Seq: uint64(i)})
		h.apply(hostMsg{Cmd: cmdExceptions, Apps: []string{"x.exe"}})
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock between the machine's lock and h.mu")
	}
}
