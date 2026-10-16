//go:build !windows

package pipe

import (
	"context"
	"errors"
	"fmt"
	"net"
)

var errUnsupported = fmt.Errorf("pipe: named pipes require Windows: %w", errors.ErrUnsupported)

// UserSID is not supported on this platform.
func UserSID() (string, error) { return "", errUnsupported }

// AgentName is not supported on this platform.
func AgentName() (string, error) { return "", errUnsupported }

// Listen is not supported on this platform.
func Listen(name string) (net.Listener, error) { return nil, errUnsupported }

// Dial is not supported on this platform.
func Dial(ctx context.Context, name string) (net.Conn, error) { return nil, errUnsupported }
