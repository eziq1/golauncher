package host

import (
	"log/slog"
	"sync"
)

// Bus is a simple publish/subscribe event bus implementing plugin.Events.
type Bus struct {
	// Log receives panics of subscribers, which are contained.
	Log *slog.Logger

	mu   sync.Mutex
	next int
	subs map[string]map[int]func()
}

// NewBus returns an empty Bus.
func NewBus() *Bus { return &Bus{subs: map[string]map[int]func(){}} }

// Subscribe implements plugin.Events.
func (b *Bus) Subscribe(name string, fn func()) (unsubscribe func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	if b.subs[name] == nil {
		b.subs[name] = map[int]func(){}
	}
	b.subs[name][id] = fn
	return func() {
		b.mu.Lock()
		delete(b.subs[name], id)
		b.mu.Unlock()
	}
}

// Publish runs the subscribers of name on a new goroutine each. A panicking
// subscriber is logged and does not affect the others or the launcher.
func (b *Bus) Publish(name string) {
	for _, fn := range b.handlers(name) {
		Go(b.Log, "event "+name, fn)
	}
}

// PublishSync runs the subscribers of name and waits for them. Tests use it
// to make event handling deterministic.
func (b *Bus) PublishSync(name string) {
	for _, fn := range b.handlers(name) {
		fn()
	}
}

func (b *Bus) handlers(name string) []func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]func(), 0, len(b.subs[name]))
	for _, fn := range b.subs[name] {
		out = append(out, fn)
	}
	return out
}
