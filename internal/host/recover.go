package host

import (
	"log/slog"
	"runtime/debug"
)

// Contain logs a recovered panic. It must be deferred directly:
//
//	defer host.Contain(log, "what was running")
//
// (recover only works when called by the deferred function itself).
func Contain(log *slog.Logger, what string) {
	if r := recover(); r != nil {
		if log != nil {
			log.Error("panic contained", "in", what, "panic", r, "stack", string(debug.Stack()))
		}
	}
}

// Go runs fn on a new goroutine, containing panics.
func Go(log *slog.Logger, what string, fn func()) {
	go func() {
		defer Contain(log, what)
		fn()
	}()
}
