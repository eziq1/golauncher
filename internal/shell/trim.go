package shell

import (
	"runtime/debug"
	"sync"
	"time"
)

// trimDelay is how long golauncher stays idle before the memory the Go
// runtime freed goes back to the system.
const trimDelay = 30 * time.Second

// trimmer gives the system back the memory the Go runtime has freed, once
// golauncher has been idle for a while (R125): after startup, whose
// background refreshes leave tens of megabytes of garbage, and after each
// hide of the launcher. The runtime would keep it for minutes. Each
// schedule puts the trim off again; busy skips it (the launcher shows: the
// next hide schedules it again).
type trimmer struct {
	delay time.Duration
	busy  func(done func(busy bool)) // asks whether the user is at it
	free  func()

	mu    sync.Mutex
	timer *time.Timer
}

func newTrimmer(busy func(done func(bool))) *trimmer {
	return &trimmer{delay: trimDelay, busy: busy, free: debug.FreeOSMemory}
}

// schedule (re)starts the wait before the trim.
func (t *trimmer) schedule() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Reset(t.delay)
		return
	}
	t.timer = time.AfterFunc(t.delay, func() {
		t.busy(func(busy bool) {
			if !busy {
				go t.free() // a full collection: off the main thread
			}
		})
	})
}

// stop cancels a pending trim.
func (t *trimmer) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
	}
}
