//go:build !windows

package process

import (
	"context"
	"errors"
)

// Sampler takes snapshots; it reuses its buffer between them.
type Sampler struct{}

// Snapshot is only supported on Windows.
func (s *Sampler) Snapshot() (*Snapshot, error) { return nil, errors.ErrUnsupported }

// ReadInfo is only supported on Windows.
func ReadInfo(id ID) Info { return Info{} }

// CommandLine is only supported on Windows.
func CommandLine(id ID) (string, error) { return "", errors.ErrUnsupported }

// Windows is only supported on Windows.
func Windows() ([]Window, error) { return nil, errors.ErrUnsupported }

// Identify is only supported on Windows.
func Identify(pid uint32) (ID, error) { return ID{}, errors.ErrUnsupported }

// Kill is only supported on Windows.
func Kill(id ID) error { return errors.ErrUnsupported }

// KillTree is only supported on Windows.
func KillTree(root ID) KillReport {
	var r KillReport
	r.Add(root, errors.ErrUnsupported)
	return r
}

// CloseWindows is only supported on Windows.
func CloseWindows(hwnds []uintptr) error { return errors.ErrUnsupported }

// Wait is only supported on Windows.
func Wait(ctx context.Context, ids []ID) []ID { return nil }

// SwitchTo is only supported on Windows.
func SwitchTo(hwnd uintptr) error { return errors.ErrUnsupported }

// RunElevated is only supported on Windows.
func RunElevated(ctx context.Context, exe string, args []string) (int, error) {
	return 0, errors.ErrUnsupported
}

// IsElevated is only supported on Windows.
func IsElevated() bool { return false }

// StartExplorer is only supported on Windows.
func StartExplorer() error { return errors.ErrUnsupported }

// HelperMain is only supported on Windows.
func HelperMain(args []string) int { return ExitFailed }
