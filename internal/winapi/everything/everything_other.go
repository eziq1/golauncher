//go:build !windows

package everything

import (
	"context"
	"errors"
)

// Search runs q in Everything. It is only supported on Windows.
func Search(ctx context.Context, q Query) ([]Result, error) { return nil, errors.ErrUnsupported }

// Installed reports whether Everything is installed; never on this platform.
func Installed() (exePath string, ok bool) { return "", false }

// Running reports whether Everything is running; never on this platform.
func Running() bool { return false }

// Start launches Everything. It is only supported on Windows.
func Start(exePath string) error { return errors.ErrUnsupported }

// UserFolders returns the user's known folders; none on this platform.
func UserFolders() []string { return nil }
