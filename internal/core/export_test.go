package core

import "time"

// SetTimeouts changes the plugin call time limits for tests and returns a
// function restoring them.
func SetTimeouts(view, action time.Duration) (restore func()) {
	v, a := viewTimeout, actionTimeout
	viewTimeout, actionTimeout = view, action
	return func() { viewTimeout, actionTimeout = v, a }
}
