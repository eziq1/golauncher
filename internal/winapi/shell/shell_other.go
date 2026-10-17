//go:build !windows && !darwin

package shell

import (
	"context"
	"errors"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// InstalledApps lists the Start menu's "All apps". It is only supported on Windows.
func InstalledApps(ctx context.Context) ([]App, error) { return nil, errors.ErrUnsupported }

// IconPNG draws the icon of app. It is only supported on Windows.
func IconPNG(app App, size int) ([]byte, error) { return nil, errors.ErrUnsupported }

// FileDescription reads the version information of exe. It is only
// supported on Windows.
func FileDescription(exe string) string { return "" }

// Open opens target through the shell. It is only supported on Windows.
func Open(target string, opts plugin.OpenOptions) error { return errors.ErrUnsupported }
