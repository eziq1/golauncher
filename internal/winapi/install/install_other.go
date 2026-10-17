//go:build !windows && !darwin

package install

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/eziq1/golauncher/internal/install"
	"github.com/eziq1/golauncher/internal/update"
)

// System is install.System where self-installation is not supported: every
// method fails with errors.ErrUnsupported (Linux and other systems; macOS
// has its own, install_darwin.go).
type System struct{}

var _ install.System = (*System)(nil)

// New returns the System.
func New() *System { return &System{} }

// Paths fails: there are no install locations, so the shell skips
// self-installation and just runs.
func Paths() (install.Paths, error) { return install.Paths{}, errors.ErrUnsupported }

// ReadEntry implements install.System.
func (*System) ReadEntry() (install.Entry, bool, error) {
	return install.Entry{}, false, errors.ErrUnsupported
}

// WriteEntry implements install.System.
func (*System) WriteEntry(install.Entry) error { return errors.ErrUnsupported }

// DeleteEntry implements install.System.
func (*System) DeleteEntry() error { return errors.ErrUnsupported }

// FileVersion implements install.System.
func (*System) FileVersion(string) (string, error) { return "", errors.ErrUnsupported }

// CreateShortcut implements install.System.
func (*System) CreateShortcut(string, install.Shortcut) error { return errors.ErrUnsupported }

// ShortcutTarget implements install.System.
func (*System) ShortcutTarget(string) (string, error) { return "", errors.ErrUnsupported }

// StopInstances implements install.System.
func (*System) StopInstances(string, time.Duration) error { return errors.ErrUnsupported }

// Start implements install.System.
func (*System) Start(string, []string) error { return errors.ErrUnsupported }

// ScheduleRemoval implements install.System.
func (*System) ScheduleRemoval(string, []string) error { return errors.ErrUnsupported }

// HelperSystem is update.HelperSystem where the update helper is not
// supported.
type HelperSystem struct{}

var _ update.HelperSystem = HelperSystem{}

// WaitExit implements update.HelperSystem.
func (HelperSystem) WaitExit(int, time.Duration) error { return errors.ErrUnsupported }

// Start implements update.HelperSystem.
func (HelperSystem) Start(string, []string, []string) (update.Process, error) {
	return nil, errors.ErrUnsupported
}

// StartDetached is not supported here.
func StartDetached(string, []string) error { return errors.ErrUnsupported }

// SystemProxy returns the proxy function for updates: the environment's.
func SystemProxy() func(*http.Request) (*url.URL, error) { return http.ProxyFromEnvironment }
