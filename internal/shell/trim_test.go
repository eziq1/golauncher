package shell

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestTrimmer: the trim runs once, after the last schedule's delay, and
// not while the launcher is in use.
func TestTrimmer(t *testing.T) {
	var frees atomic.Int32
	var busy atomic.Bool
	tr := &trimmer{delay: 30 * time.Millisecond, busy: func(done func(bool)) { done(busy.Load()) }, free: func() { frees.Add(1) }}

	tr.schedule()
	time.Sleep(10 * time.Millisecond)
	tr.schedule() // puts it off
	time.Sleep(10 * time.Millisecond)
	if n := frees.Load(); n != 0 {
		t.Fatalf("trimmed %d times before the delay", n)
	}
	waitFrees(t, &frees, 1)

	busy.Store(true)
	tr.schedule()
	time.Sleep(80 * time.Millisecond)
	if n := frees.Load(); n != 1 {
		t.Fatalf("trimmed while busy: %d", n)
	}

	busy.Store(false)
	tr.schedule()
	tr.stop()
	time.Sleep(80 * time.Millisecond)
	if n := frees.Load(); n != 1 {
		t.Fatalf("trimmed after stop: %d", n)
	}
}

func waitFrees(t *testing.T, frees *atomic.Int32, want int32) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); frees.Load() != want; {
		if time.Now().After(deadline) {
			t.Fatalf("trims %d, want %d", frees.Load(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
