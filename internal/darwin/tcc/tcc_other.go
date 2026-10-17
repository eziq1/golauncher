//go:build !darwin

package tcc

import "errors"

// Supported reports false: there are no TCC permissions here.
func Supported() bool { return false }

// Check reports Unknown.
func Check(ID) Status { return Unknown }

// RequestAutomation reports Unknown.
func RequestAutomation() Status { return Unknown }

// RequestAccessibility reports Unknown.
func RequestAccessibility() Status { return Unknown }

// OpenSettings is only supported on macOS.
func OpenSettings(id ID) error {
	if SettingsURL(id) == "" {
		return errUnknownID
	}
	return errors.ErrUnsupported
}
