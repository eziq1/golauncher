//go:build !darwin

package install

// QuarantineAttr is the extended attribute with which macOS marks
// downloaded files for Gatekeeper.
const QuarantineAttr = "com.apple.quarantine"

// ClearQuarantine does nothing: only macOS quarantines files.
func ClearQuarantine(string) error { return nil }
