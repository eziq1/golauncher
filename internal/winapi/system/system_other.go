//go:build !windows && !darwin

package system

import "errors"

// Lock locks the workstation. Only supported on Windows.
func Lock() error { return errors.ErrUnsupported }

// Suspend puts the computer to sleep or hibernates it. Only supported on Windows.
func Suspend(hibernate bool) error { return errors.ErrUnsupported }

// ExitWindows signs out, restarts or shuts down. Only supported on Windows.
func ExitWindows(kind ExitKind) error { return errors.ErrUnsupported }

// QueryRecycleBin returns the content of the Recycle Bin. Only supported on Windows.
func QueryRecycleBin() (RecycleBin, error) { return RecycleBin{}, errors.ErrUnsupported }

// EmptyRecycleBin empties the Recycle Bin. Only supported on Windows.
func EmptyRecycleBin() error { return errors.ErrUnsupported }

// DarkMode reports whether apps use the dark theme. Only supported on Windows.
func DarkMode() (bool, error) { return false, errors.ErrUnsupported }

// SetDarkMode switches the theme. Only supported on Windows.
func SetDarkMode(dark bool) error { return errors.ErrUnsupported }

// PressMediaKey sends a volume key. Only supported on Windows.
func PressMediaKey(key MediaKey, times int) error { return errors.ErrUnsupported }

// DisplayOff turns the displays off. Only supported on Windows.
func DisplayOff() error { return errors.ErrUnsupported }

// HibernateAvailable reports whether hibernation is enabled; never here.
func HibernateAvailable() bool { return false }

// OSBuild returns the Windows build number; 0 here.
func OSBuild() int { return 0 }

// EditionID returns the Windows edition; "" here.
func EditionID() string { return "" }
