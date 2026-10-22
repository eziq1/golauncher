// Package ring is the platform-independent part of the Action Ring
// (docs/action-ring.md): the state machine that tells a held middle button
// from a click or a drag, the geometry that picks a slot by direction, and
// the choice of the actions the ring shows. The mouse hooks (package
// mousehook), the window and the actions themselves are elsewhere.
package ring

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Point is a position on the screen, in the units of the mouse hook:
// physical pixels on Windows, points on macOS.
type Point struct{ X, Y float64 }

// Dist returns the distance between p and q.
func (p Point) Dist(q Point) float64 { return math.Hypot(p.X-q.X, p.Y-q.Y) }

// EventKind is a kind of mouse event the hook reports.
type EventKind uint8

// Event kinds.
const (
	MiddleDown EventKind = iota + 1
	MiddleUp
	Move
	// OtherDown is another button going down (left, right, …).
	OtherDown
)

// Event is a mouse event seen by the hook.
type Event struct {
	Kind EventKind
	Pt   Point
	// Own marks an event this program injected (a replay of the middle
	// button): it is never handled again.
	Own bool
	// Injected marks an event another program injected (automation tools,
	// remote control): it is left alone, so that a tool's middle click is
	// never held back nor turns into the ring.
	Injected bool
}

// Clock starts timers; tests use a fake one.
type Clock interface {
	// AfterFunc calls f after d, on its own goroutine, unless stop is
	// called first.
	AfterFunc(d time.Duration, f func()) (stop func() bool)
}

// RealClock is the system clock.
type RealClock struct{}

// AfterFunc implements Clock.
func (RealClock) AfterFunc(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }

// Callbacks are how the machine acts. Allowed runs on the hook's thread
// while it waits, and must return fast; the others run after the machine
// decided about an event (outside its lock) and must not block: post the
// work elsewhere.
type Callbacks struct {
	// Allowed reports whether the ring may open over the app at pt (not
	// one of the exceptions, not a full-screen game). Nil allows all.
	Allowed func(pt Point) bool
	// Replay gives the app the middle button the machine held back: down,
	// or down and up. Its events come back as Own.
	Replay func(down, up bool)
	// Open opens the ring around pt: the button was held long enough.
	Open func(pt Point)
	// Track reports the pointer at pt while the button holds the ring open.
	Track func(pt Point)
	// Release reports the button let go at pt while it held the ring open;
	// moved tells whether the pointer went farther than the threshold from
	// where it went down (a drag toward a slot) or not (the ring stays).
	Release func(pt Point, moved bool)
	// Press reports a button going down at pt while the ring stays open
	// (after a Release that did not move): middle tells the middle button,
	// which the machine keeps from the app, from the others, which go on.
	Press func(pt Point, middle bool)
}

// Options configure a Machine.
type Options struct {
	// Hold is how long the button is held before the ring opens.
	Hold time.Duration
	// Threshold is how far the pointer may move, in hook units, before a
	// press counts as a drag (autoscroll, panning): it then goes to the app
	// at once.
	Threshold float64
	// Clock defaults to RealClock.
	Clock Clock
	Callbacks
}

type state uint8

const (
	idle      state = iota
	pending         // the down is held back, the timer runs
	passing         // the app has the button: everything passes until it goes up
	holding         // the ring is open, the button still down
	swallowUp       // the down was kept from the app: keep its up too
)

// Machine tells a held middle button from a click or a drag. Feed it the
// hook's events with Handle; it is safe for concurrent use.
//
//   - A press released before Hold is given back to the app as a click
//     (down and up, through Replay).
//   - A press that moves farther than Threshold, or that another button
//     joins, is given back at once (the down) and left alone until it goes
//     up: autoscroll and panning keep working.
//   - A press held for Hold opens the ring (Open); moving reports the
//     pointer (Track) and letting go reports where (Release).
//   - Over the apps Allowed refuses, the button is left alone.
//   - Events injected by others, and this program's replays (Own), pass.
type Machine struct {
	mu    sync.Mutex
	o     Options
	st    state
	down  Point // where the pending or holding press went down
	moved bool  // the holding press moved farther than the threshold
	open  bool  // the ring shows
	gen   int   // tells a timer of an older press
	stop  func() bool

	// interest tells the hook which events it must report (wantMoves,
	// wantButtons): it skips the others without taking the lock.
	interest atomic.Uint32
}

const (
	wantMoves uint32 = 1 << iota
	wantButtons
)

// NewMachine returns a machine with options o.
func NewMachine(o Options) *Machine {
	if o.Clock == nil {
		o.Clock = RealClock{}
	}
	return &Machine{o: o}
}

// SetHold changes how long the button is held before the ring opens.
func (m *Machine) SetHold(d time.Duration) {
	m.mu.Lock()
	m.o.Hold = d
	m.mu.Unlock()
}

// SetAllowed replaces Callbacks.Allowed.
func (m *Machine) SetAllowed(fn func(Point) bool) {
	m.mu.Lock()
	m.o.Allowed = fn
	m.mu.Unlock()
}

// WantsMoves reports whether pointer moves matter now (a press is pending
// or holds the ring open). Cheap: the hook asks on every move.
func (m *Machine) WantsMoves() bool { return m.interest.Load()&wantMoves != 0 }

// WantsButtons reports whether the other buttons matter now (a press is
// pending, or the ring shows).
func (m *Machine) WantsButtons() bool { return m.interest.Load()&wantButtons != 0 }

// Open reports whether the ring shows, as far as the machine knows.
func (m *Machine) Open() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.open
}

// ButtonDown reports whether the middle button is down in a press the
// machine handles (held back, holding the ring, or kept from the app until
// it goes up): no copy is simulated meanwhile.
func (m *Machine) ButtonDown() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st == pending || m.st == holding || m.st == swallowUp
}

// SetOpen tells the machine that the ring closed (false) or shows (true).
// Closed while the button still holds it, the button's up is kept from the
// app all the same (its down was).
func (m *Machine) SetOpen(open bool) {
	m.mu.Lock()
	m.open = open
	if !open && m.st == holding {
		m.st = swallowUp
	}
	m.updateInterest()
	m.mu.Unlock()
}

// Reset forgets the press in progress, as when the hook stops: a down held
// back is given back.
func (m *Machine) Reset() {
	m.mu.Lock()
	var calls []func()
	if m.st == pending {
		m.stopTimer()
		calls = append(calls, m.replay(true, false))
	}
	m.st, m.open = idle, false
	m.updateInterest()
	m.mu.Unlock()
	run(calls)
}

// Handle decides about one event of the hook: swallow reports whether the
// event is kept from the apps.
func (m *Machine) Handle(ev Event) (swallow bool) {
	if ev.Own || ev.Injected {
		return false
	}
	m.mu.Lock()
	var calls []func()
	switch ev.Kind {
	case MiddleDown:
		switch {
		case m.st != idle:
			// A second down without an up: a device of its own; leave it.
		case m.open:
			// The ring stays open: a middle click chooses or closes.
			m.st = swallowUp
			swallow = true
			calls = append(calls, m.press(ev.Pt, true))
		case m.o.Allowed == nil || m.o.Allowed(ev.Pt):
			m.st, m.down, m.moved = pending, ev.Pt, false
			m.gen++
			gen := m.gen
			m.stop = m.o.Clock.AfterFunc(m.o.Hold, func() { m.timeout(gen) })
			swallow = true
		default:
			m.st = passing
		}
	case MiddleUp:
		switch m.st {
		case pending:
			m.stopTimer()
			m.st = idle
			swallow = true
			calls = append(calls, m.replay(true, true))
		case holding:
			m.st = idle
			swallow = true
			if f := m.o.Release; f != nil {
				pt, moved := ev.Pt, m.moved || ev.Pt.Dist(m.down) > m.o.Threshold
				calls = append(calls, func() { f(pt, moved) })
			}
		case swallowUp:
			m.st = idle
			swallow = true
		case passing:
			m.st = idle
		}
	case Move:
		switch m.st {
		case pending:
			if ev.Pt.Dist(m.down) > m.o.Threshold {
				m.stopTimer()
				m.st = passing
				calls = append(calls, m.replay(true, false))
			}
		case holding:
			if ev.Pt.Dist(m.down) > m.o.Threshold {
				m.moved = true
			}
			if f := m.o.Track; f != nil {
				pt := ev.Pt
				calls = append(calls, func() { f(pt) })
			}
		}
	case OtherDown:
		switch {
		case m.st == pending:
			// A chord (middle and left, …): the app's.
			m.stopTimer()
			m.st = passing
			calls = append(calls, m.replay(true, false))
		case m.st == idle && m.open:
			calls = append(calls, m.press(ev.Pt, false))
		}
	}
	m.updateInterest()
	m.mu.Unlock()
	run(calls)
	return swallow
}

// timeout opens the ring if the press gen is still pending.
func (m *Machine) timeout(gen int) {
	m.mu.Lock()
	if m.st != pending || m.gen != gen {
		m.mu.Unlock()
		return
	}
	m.st, m.open, m.stop = holding, true, nil
	m.updateInterest()
	pt, f := m.down, m.o.Open
	m.mu.Unlock()
	if f != nil {
		f(pt)
	}
}

// stopTimer stops the timer of the pending press. m.mu is held.
func (m *Machine) stopTimer() {
	if m.stop != nil {
		m.stop()
		m.stop = nil
	}
	m.gen++ // a timer already firing finds an older generation
}

// replay returns the call giving the app the button. m.mu is held.
func (m *Machine) replay(down, up bool) func() {
	f := m.o.Replay
	return func() {
		if f != nil {
			f(down, up)
		}
	}
}

// press returns the call reporting a press while the ring shows.
func (m *Machine) press(pt Point, middle bool) func() {
	f := m.o.Press
	return func() {
		if f != nil {
			f(pt, middle)
		}
	}
}

// updateInterest publishes which events matter now. m.mu is held.
func (m *Machine) updateInterest() {
	var v uint32
	if m.st == pending || m.st == holding {
		v |= wantMoves
	}
	if m.st == pending || m.open {
		v |= wantButtons
	}
	m.interest.Store(v)
}

func run(calls []func()) {
	for _, f := range calls {
		f()
	}
}
