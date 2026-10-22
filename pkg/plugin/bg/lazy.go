package bg

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// IdleRelease is how long a Lazy value stays loaded without being used.
const IdleRelease = 10 * time.Minute

// ErrClosed is returned by Lazy.Get once the group is closed.
var ErrClosed = errors.New("bg: closed")

// Lazy is a value that is loaded the first time it is needed and dropped
// when it has not been used for Idle, so that the garbage collector can free
// it; the next use loads it again. It suits large indexes that are cheap to
// read again from a cache.
//
// Loads and the idle timer run on Group (through Host.Go); closing the group
// drops the value and ends the timer. Set the fields before the first use;
// the rest of the zero Lazy is ready.
type Lazy[T any] struct {
	Host  plugin.Host
	Group *Group
	// Load reads the value. Its result, error included, is kept until it is
	// dropped.
	Load func() (T, error)
	// OnLate, if set, runs after a load that a caller did not wait for (TryGet,
	// or Get whose context ended), so that what was shown without the value
	// can be refreshed.
	OnLate func()
	// Idle is how long the value stays unused before it is dropped (default
	// IdleRelease), measured with Host.Now.
	Idle time.Duration
	// After waits for a duration (default time.After; tests deliver ticks).
	After func(time.Duration) <-chan time.Time

	mu      sync.Mutex
	val     T
	err     error
	loaded  bool
	used    time.Time     // Host.Now of the last use
	loading chan struct{} // closed when the running load ends
	late    bool          // a caller did not wait for the running load
	timing  bool          // the idle timer runs
}

// Get returns the value, loading it first if needed; concurrent callers
// share one load. If ctx ends first, Get returns ctx's error and the load
// goes on. Get counts as a use.
func (l *Lazy[T]) Get(ctx context.Context) (T, error) {
	l.mu.Lock()
	l.used = l.Host.Now()
	if l.loaded {
		defer l.mu.Unlock()
		return l.val, l.err
	}
	ch := l.startLocked()
	l.mu.Unlock()
	var zero T
	if ch == nil {
		return zero, ErrClosed
	}
	select {
	case <-ch:
	case <-ctx.Done():
		l.mu.Lock()
		if l.loading == ch {
			l.late = true
		}
		l.mu.Unlock()
		return zero, ctx.Err()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.loaded {
		return zero, ErrClosed // the group closed during the load
	}
	return l.val, l.err
}

// TryGet returns the value if it is loaded, counting as a use. Otherwise it
// starts loading it in the background (OnLate runs when it is loaded) and
// reports false.
func (l *Lazy[T]) TryGet() (T, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.used = l.Host.Now()
	if l.loaded {
		return l.val, true
	}
	if l.startLocked() != nil {
		l.late = true
	}
	var zero T
	return zero, false
}

// Peek returns the value if it is loaded, without loading it and without
// counting as a use (for background refreshes).
func (l *Lazy[T]) Peek() (T, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.val, l.loaded
}

// Loaded reports whether the value is loaded.
func (l *Lazy[T]) Loaded() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loaded
}

// Set replaces the value with a refreshed one if it is loaded, and reports
// whether it did; it does not count as a use. A value that is not loaded
// stays so: the next use loads the refreshed one.
func (l *Lazy[T]) Set(v T, err error) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.loaded {
		return false
	}
	l.val, l.err = v, err
	return true
}

// Drop drops the value now.
func (l *Lazy[T]) Drop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropLocked()
}

func (l *Lazy[T]) dropLocked() {
	var zero T
	l.val, l.err, l.loaded = zero, nil, false
}

// startLocked starts a load unless one runs and returns the channel closed
// when it ends; nil if the group is closed.
func (l *Lazy[T]) startLocked() chan struct{} {
	if l.loading != nil {
		return l.loading
	}
	ch := make(chan struct{})
	started := l.Group.Go(l.Host, func() {
		v, err := l.load()
		l.mu.Lock()
		l.loading = nil
		late := l.late
		l.late = false
		stored := !l.Group.Closed()
		if stored {
			l.val, l.err, l.loaded = v, err, true
			l.startTimerLocked()
		}
		l.mu.Unlock()
		close(ch)
		if stored && late && l.OnLate != nil {
			l.OnLate()
		}
	})
	if !started {
		return nil
	}
	l.loading = ch
	return ch
}

// load runs Load, turning a panic into an error: the load must end, or
// every later Get would wait for it.
func (l *Lazy[T]) load() (v T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("bg: load panicked: %v", r)
		}
	}()
	return l.Load()
}

// startTimerLocked starts the idle timer unless it runs.
func (l *Lazy[T]) startTimerLocked() {
	if !l.timing {
		l.timing = l.Group.Go(l.Host, l.idleLoop)
	}
}

// idleLoop drops the value once it has not been used for Idle, or when the
// group closes.
func (l *Lazy[T]) idleLoop() {
	after := l.After
	if after == nil {
		after = time.After
	}
	idle := l.Idle
	if idle <= 0 {
		idle = IdleRelease
	}
	wait := idle
	for {
		select {
		case <-l.Group.Done():
			l.mu.Lock()
			l.dropLocked()
			l.timing = false
			l.mu.Unlock()
			return
		case <-after(wait):
		}
		l.mu.Lock()
		wait = idle - l.Host.Now().Sub(l.used)
		if !l.loaded || wait <= 0 {
			l.dropLocked()
			l.timing = false
			l.mu.Unlock()
			return
		}
		l.mu.Unlock()
	}
}
