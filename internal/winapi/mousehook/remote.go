package mousehook

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/ring"
)

// Remote is the machine of the hook host (host.go) as golauncher sees it:
// Start starts the host, which installs the hook; the settings and the
// ring closing are sent to it, its decisions come back as the Callbacks
// (Open, Track, Release, Press; Allowed and Replay run in the host). Open
// and ButtonDown answer from a copy of the host's state, kept from its
// events. It is safe for concurrent use.
type Remote struct {
	cb     ring.Callbacks
	exited func(error)
	spawn  func() (*hostConn, error) // replaced in tests

	mu         sync.Mutex
	hold       time.Duration
	exceptions []string
	open, down bool
	seq        uint64 // the last close sent
	conn       *hostConn
}

// hostConn is a running host.
type hostConn struct {
	in   io.WriteCloser // its standard input: the commands
	out  io.Reader      // its standard output: the events
	wait func() error   // waits for it to end, once out reached its end
	kill func()         // ends it at once
	done chan struct{}  // closed once it ended and its events were read
}

// hostStartTimeout bounds waiting for the host to install the hook;
// hostStopTimeout for it to end once its input is closed.
var (
	hostStartTimeout = 5 * time.Second
	hostStopTimeout  = 2 * time.Second
)

// NewRemote returns the host's machine with hold and the callbacks cb, of
// which Open, Track, Release and Press are used: they run on a goroutine
// reading the host and must not block. exited is called there if the host
// ends while it runs (not after the stop Start returned): its machine is
// then gone, and the ring with it.
func NewRemote(hold time.Duration, cb ring.Callbacks, exited func(error)) *Remote {
	return &Remote{cb: cb, exited: exited, hold: hold, spawn: spawnHost}
}

// Start starts the host and waits until its hook is installed. stop ends
// it, giving the apps a press it held back.
func (r *Remote) Start() (stop func(), err error) {
	r.mu.Lock()
	if r.conn != nil {
		r.mu.Unlock()
		return nil, errors.New("mousehook: the host already runs")
	}
	c, err := r.spawn()
	if err != nil {
		r.mu.Unlock()
		return nil, fmt.Errorf("mousehook: starting the host: %w", err)
	}
	r.conn, r.open, r.down, r.seq = c, false, false, 0
	r.send(hostMsg{Cmd: cmdHold, MS: int(r.hold / time.Millisecond)})
	r.send(hostMsg{Cmd: cmdExceptions, Apps: r.exceptions})
	r.send(hostMsg{Cmd: cmdStart})
	r.mu.Unlock()

	ready := make(chan error, 1)
	go r.read(c, ready)
	select {
	case err = <-ready:
	case <-time.After(hostStartTimeout):
		err = errors.New("mousehook: the host did not start")
	}
	if err != nil {
		r.end(c)
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { r.end(c) }) }, nil
}

// end stops host c: the end of its input ends it, or it is killed.
func (r *Remote) end(c *hostConn) {
	r.mu.Lock()
	if r.conn == c {
		r.conn, r.open, r.down = nil, false, false
	}
	r.mu.Unlock()
	c.in.Close()
	select {
	case <-c.done:
	case <-time.After(hostStopTimeout):
		c.kill()
		<-c.done
	}
}

// read handles the events of host c until it ends; ready gets nil once its
// hook is installed, or why not.
func (r *Remote) read(c *hostConn, ready chan<- error) {
	sc := bufio.NewScanner(c.out)
	// started: ready got its answer; hooked: the hook was installed.
	started, hooked := false, false
	for sc.Scan() {
		var m hostMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch m.Ev {
		case evReady:
			if !started {
				started, hooked = true, true
				ready <- nil
			}
		case evError:
			if !started {
				started = true
				ready <- errors.New("mousehook host: " + m.Msg)
			}
		default:
			r.dispatch(c, m)
		}
	}
	err := c.wait()
	r.mu.Lock()
	unexpected := r.conn == c
	if unexpected {
		r.conn, r.open, r.down = nil, false, false
	}
	r.mu.Unlock()
	if err == nil {
		err = errors.New("mousehook: the host ended")
	}
	if !started {
		ready <- err
	}
	close(c.done)
	if unexpected && hooked && r.exited != nil {
		r.exited(err)
	}
}

// dispatch handles event m of host c.
func (r *Remote) dispatch(c *hostConn, m hostMsg) {
	pt := ring.Point{X: m.X, Y: m.Y}
	switch m.Ev {
	case evOpen:
		r.mu.Lock()
		ok := r.conn == c && m.Seq == r.seq // else a close sent since undoes it
		if ok {
			r.open = true
		}
		r.mu.Unlock()
		if ok && r.cb.Open != nil {
			r.cb.Open(pt)
		}
	case evTrack:
		if r.cb.Track != nil {
			r.cb.Track(pt)
		}
	case evRelease:
		if r.cb.Release != nil {
			r.cb.Release(pt, m.Moved)
		}
	case evPress:
		if r.cb.Press != nil {
			r.cb.Press(pt, m.Middle)
		}
	case evButton:
		r.mu.Lock()
		if r.conn == c {
			r.down = m.Down
		}
		r.mu.Unlock()
	}
}

// send writes command m to the running host, if any. r.mu is held. A host
// that cannot take it has ended: read finds out.
func (r *Remote) send(m hostMsg) {
	if r.conn == nil {
		return
	}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	r.conn.in.Write(append(b, '\n'))
}

// Open reports whether the ring shows, as far as the machine knows.
func (r *Remote) Open() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.open
}

// ButtonDown reports whether the middle button is down in a press the
// machine handles (ring.Machine.ButtonDown).
func (r *Remote) ButtonDown() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.down
}

// SetOpen tells the machine that the ring closed (false) or shows (true).
func (r *Remote) SetOpen(open bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.open = open
	if !open {
		r.seq++
	}
	r.send(hostMsg{Cmd: cmdOpen, Open: open, Seq: r.seq})
}

// SetHold changes how long the button is held before the ring opens.
func (r *Remote) SetHold(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d == r.hold {
		return
	}
	r.hold = d
	r.send(hostMsg{Cmd: cmdHold, MS: int(d / time.Millisecond)})
}

// SetExceptions changes the apps the ring leaves alone (ring.Excluded).
func (r *Remote) SetExceptions(apps []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if slices.Equal(apps, r.exceptions) {
		return
	}
	r.exceptions = slices.Clone(apps)
	r.send(hostMsg{Cmd: cmdExceptions, Apps: r.exceptions})
}

// Reset forgets the press in progress (ring.Machine.Reset).
func (r *Remote) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.open = false
	r.seq++
	r.send(hostMsg{Cmd: cmdReset, Seq: r.seq})
}
