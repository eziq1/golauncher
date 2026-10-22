package shell

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/ring"
)

// fakeTrackpad stands for trackpad.Start: no framework is loaded.
type fakeTrackpad struct {
	starts, stops int
	err           error
	onTap         func()
}

// trackpadShell returns a shell whose ring has a machine in this process
// (recording where it opens, over the apps allowed lets it) and its hook
// running, with the trackpad and the pointer faked.
func trackpadShell(t *testing.T, tp *fakeTrackpad, opened *[]ring.Point, allowed *bool) *Shell {
	cfg := config.Default()
	cfg.Ring.Enabled = true
	s := &Shell{ctx: t.Context(), log: slog.New(slog.DiscardHandler), env: &app.Env{Config: config.NewMemory(cfg)}}
	s.ring.machine = ring.NewMachine(ring.Options{Hold: time.Hour, Callbacks: ring.Callbacks{
		Allowed: func(ring.Point) bool { return *allowed },
		Open:    func(pt ring.Point) { *opened = append(*opened, pt) },
	}})
	s.ring.stop = func() {}
	oldStart, oldSupported, oldPointer := startTrackpad, trackpadSupported, ringPointerNow
	startTrackpad = func(onTap func()) (func(), error) {
		tp.starts++
		if tp.err != nil {
			return nil, tp.err
		}
		tp.onTap = onTap
		return func() { tp.stops++ }, nil
	}
	trackpadSupported = func() bool { return true }
	ringPointerNow = func() ring.Point { return ring.Point{X: 40, Y: 50} }
	t.Cleanup(func() { startTrackpad, trackpadSupported, ringPointerNow = oldStart, oldSupported, oldPointer })
	return s
}

// TestTrackpadTap: the tap is watched while the ring's hook runs and the
// setting is on, and stops with either; a tap opens the ring at the
// pointer through the machine, which stays open, and not over an
// exception.
func TestTrackpadTap(t *testing.T) {
	tp := &fakeTrackpad{}
	var opened []ring.Point
	allowed := true
	s := trackpadShell(t, tp, &opened, &allowed)
	cfg := s.env.Config.Get()
	s.syncTrackpad(cfg)
	s.syncTrackpad(cfg)
	if tp.starts != 1 || s.ring.tapStop == nil {
		t.Fatalf("starts %d", tp.starts)
	}

	s.ringTapped() // the tap, posted to the main thread
	if len(opened) != 1 || opened[0] != (ring.Point{X: 40, Y: 50}) || !s.ring.machine.Open() {
		t.Fatalf("opened %v", opened)
	}
	s.ringTapped() // again while it shows
	if len(opened) != 1 {
		t.Errorf("opened again: %v", opened)
	}
	s.ring.machine.SetOpen(false)
	allowed = false
	s.ringTapped()
	if len(opened) != 1 || s.ring.machine.Open() {
		t.Errorf("opened over an exception: %v", opened)
	}
	allowed = true

	cfg.Ring.TrackpadTap = false
	s.syncTrackpad(cfg)
	if tp.stops != 1 || s.ring.tapStop != nil {
		t.Fatalf("stops %d", tp.stops)
	}
	s.ringTapped() // a tap posted before it stopped
	if len(opened) != 1 {
		t.Errorf("opened while off: %v", opened)
	}

	cfg.Ring.TrackpadTap = true
	s.syncTrackpad(cfg)
	s.ring.stop = nil // the hook stopped (the ring off, or no permission)
	s.syncTrackpad(cfg)
	if tp.starts != 2 || tp.stops != 2 {
		t.Errorf("starts %d, stops %d", tp.starts, tp.stops)
	}
	s.ring.stop = func() {}
	cfg.Ring.Enabled = false
	s.syncTrackpad(cfg)
	if tp.starts != 2 {
		t.Errorf("started with the ring off: %d", tp.starts)
	}
	cfg.Ring.Enabled = true
	s.syncTrackpad(cfg)
	s.stopRing()
	if tp.stops != 3 || s.ring.tapStop != nil {
		t.Errorf("quitting: stops %d", tp.stops)
	}
}

// TestTrackpadTapUnavailable: without the framework the tap stays off,
// tried once (and logged once); the ring works without it.
func TestTrackpadTapUnavailable(t *testing.T) {
	tp := &fakeTrackpad{err: errors.New("no MultitouchSupport")}
	var opened []ring.Point
	allowed := true
	s := trackpadShell(t, tp, &opened, &allowed)
	cfg := s.env.Config.Get()
	s.syncTrackpad(cfg)
	s.syncTrackpad(cfg)
	if tp.starts != 1 || s.ring.tapStop != nil || !s.ring.tapFailed {
		t.Errorf("starts %d", tp.starts)
	}
	s.ringTapped()
	if len(opened) != 0 {
		t.Errorf("opened %v", opened)
	}
	tp2 := &fakeTrackpad{}
	s2 := trackpadShell(t, tp2, &opened, &allowed)
	trackpadSupported = func() bool { return false }
	s2.syncTrackpad(cfg)
	if tp2.starts != 0 {
		t.Errorf("started where unsupported: %d", tp2.starts)
	}
}
