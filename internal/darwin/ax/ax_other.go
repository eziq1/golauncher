//go:build !darwin

package ax

// Trusted reports false: there is no Accessibility API here.
func Trusted() bool { return false }

// RequestTrust does nothing and reports false.
func RequestTrust() bool { return false }
