package core

import "time"

// SetTimeouts changes the plugin call time limits for tests and returns a
// function restoring them.
func SetTimeouts(view, action time.Duration) (restore func()) {
	v, a := viewTimeout, actionTimeout
	viewTimeout, actionTimeout = view, action
	return func() { viewTimeout, actionTimeout = v, a }
}

// SetPastedGrace changes how long a pasted image's temporary file outlives
// the root search's files, and returns a function restoring it.
func SetPastedGrace(d time.Duration) (restore func()) {
	g := pastedGrace
	pastedGrace = d
	return func() { pastedGrace = g }
}
