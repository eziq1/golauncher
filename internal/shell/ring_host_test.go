//go:build windows

package shell

import (
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/internal/winapi/mousehook"
)

// fakeRemote stands for mousehook.Remote: no host process starts.
type fakeRemote struct {
	*ring.Machine
	startErr   error
	starts     int
	stops      int
	exceptions []string
}

func (f *fakeRemote) SetExceptions(apps []string) { f.exceptions = apps }

func (f *fakeRemote) Start() (func(), error) {
	f.starts++
	if f.startErr != nil {
		return nil, f.startErr
	}
	return func() { f.stops++ }, nil
}

// hostShell returns a shell whose ring runs in a fake hook host, and
// counts the hooks started in this process (none is installed).
func hostShell(t *testing.T, remote *fakeRemote) (*Shell, *int) {
	remote.Machine = ring.NewMachine(ring.Options{Hold: time.Hour})
	cfg := config.Default()
	cfg.Ring.Enabled = true
	cfg.Ring.Exceptions = []string{"mstsc"}
	s := &Shell{ctx: t.Context(), log: slog.New(slog.DiscardHandler), env: &app.Env{Config: config.NewMemory(cfg)}}
	s.ring.remote, s.ring.machine = remote, remote
	local := new(int)
	old := startLocalHook
	startLocalHook = func(mousehook.Handler) (func(), error) {
		*local++
		return func() {}, nil
	}
	t.Cleanup(func() { startLocalHook = old })
	return s, local
}

// TestRingHost: the ring's settings go to the hook host, which starts with
// the ring and stops with it; the hook does not run in golauncher.
func TestRingHost(t *testing.T) {
	remote := &fakeRemote{}
	s, local := hostShell(t, remote)
	cfg := s.env.Config.Get()
	s.syncRing(cfg)
	if remote.starts != 1 || *local != 0 || s.ring.stop == nil {
		t.Fatalf("starts %d, local %d", remote.starts, *local)
	}
	if !slices.Equal(remote.exceptions, []string{"mstsc"}) {
		t.Errorf("exceptions %q", remote.exceptions)
	}
	cfg.Ring.Enabled = false
	s.syncRing(cfg)
	if remote.stops != 1 || s.ring.stop != nil {
		t.Errorf("stops %d", remote.stops)
	}
}

// TestRingHostFallback: a host that cannot start leaves the hook to
// golauncher, with the ring's settings.
func TestRingHostFallback(t *testing.T) {
	remote := &fakeRemote{startErr: errors.New("no host")}
	s, local := hostShell(t, remote)
	s.syncRing(s.env.Config.Get())
	if remote.starts != 1 || *local != 1 || s.ring.stop == nil {
		t.Fatalf("starts %d, local %d", remote.starts, *local)
	}
	if s.ring.remote != nil {
		t.Error("still the host's machine")
	}
	if _, ok := s.ring.machine.(*ring.Machine); !ok {
		t.Errorf("machine %T", s.ring.machine)
	}
}

// TestRingHostExited: a host that ends by itself starts again once; the
// second time the hook runs in golauncher, without starting it again.
func TestRingHostExited(t *testing.T) {
	remote := &fakeRemote{}
	s, local := hostShell(t, remote)
	s.syncRing(s.env.Config.Get())
	s.ringHostExited(errors.New("gone"))
	if remote.starts != 2 || remote.stops != 1 || *local != 0 {
		t.Fatalf("starts %d, stops %d, local %d", remote.starts, remote.stops, *local)
	}
	s.ringHostExited(errors.New("gone again"))
	if remote.starts != 2 || remote.stops != 2 || *local != 1 || s.ring.remote != nil {
		t.Fatalf("starts %d, stops %d, local %d", remote.starts, remote.stops, *local)
	}
	// An exit reported after the ring was turned off changes nothing.
	cfg := s.env.Config.Get()
	cfg.Ring.Enabled = false
	s.syncRing(cfg)
	s.ringHostExited(errors.New("late"))
	if *local != 1 {
		t.Errorf("local %d", *local)
	}
}
