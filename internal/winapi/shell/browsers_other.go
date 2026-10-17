//go:build !windows && !darwin

package shell

// Browsers returns the registered web browsers. It is only supported on
// Windows.
func Browsers() []Browser { return nil }
