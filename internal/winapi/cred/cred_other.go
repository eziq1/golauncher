//go:build !windows

package cred

import "errors"

// Get returns the secret stored under target. It is only supported on Windows.
func Get(target string) (string, bool, error) { return "", false, errors.ErrUnsupported }

// Set stores secret under target. It is only supported on Windows.
func Set(target, secret string) error { return errors.ErrUnsupported }

// Delete removes the secret stored under target. It is only supported on Windows.
func Delete(target string) error { return errors.ErrUnsupported }
