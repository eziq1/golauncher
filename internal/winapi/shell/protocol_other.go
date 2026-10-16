//go:build !windows

package shell

// ProtocolRegistered reports whether an app handles links of the URL scheme.
// It is only supported on Windows.
func ProtocolRegistered(scheme string) bool { return false }
