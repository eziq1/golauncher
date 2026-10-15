//go:build !windows

package terminal

import (
	"context"
	"errors"
)

// readRegistry finds no WSL distribution outside Windows.
func readRegistry() (Registry, error) { return Registry{}, nil }

// runHidden cannot run Windows programs outside Windows.
func runHidden(context.Context, ...string) ([]byte, error) {
	return nil, errors.ErrUnsupported
}
