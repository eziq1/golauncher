//go:build !windows

package proc

import "errors"

// IsElevated is only supported on Windows.
func IsElevated() bool { return false }

// Elevated is a program started as administrator (Windows only).
type Elevated struct{}

// StartElevated is only supported on Windows.
func StartElevated(argv []string, dir string) (*Elevated, error) { return nil, errors.ErrUnsupported }

// Wait is only supported on Windows.
func (p *Elevated) Wait() (int, error) { return -1, errors.ErrUnsupported }

// Stop is only supported on Windows.
func (p *Elevated) Stop() {}
