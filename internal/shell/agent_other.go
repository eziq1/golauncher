//go:build !windows

package shell

import "errors"

// startBackground is not supported here.
func startBackground() error { return errors.ErrUnsupported }
