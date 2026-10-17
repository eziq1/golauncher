//go:build !darwin

package shell

import "log/slog"

// fixPath does nothing: only macOS starts GUI programs with a PATH of its
// own (loginpath.go).
func fixPath(*slog.Logger) {}
