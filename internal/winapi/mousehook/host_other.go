//go:build !windows

package mousehook

import (
	"errors"
	"io"
)

// HostSupported reports whether the hook runs in the hook host here: no,
// the hook runs in golauncher (macOS turns a slow event tap off rather than
// slowing the input down).
func HostSupported() bool { return false }

// HostMain is not supported here.
func HostMain(io.Reader, io.Writer) error { return errors.ErrUnsupported }

func spawnHost() (*hostConn, error) { return nil, errors.ErrUnsupported }
