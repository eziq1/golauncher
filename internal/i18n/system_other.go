//go:build !windows && !darwin

package i18n

import "os"

// systemLanguages reads the POSIX locale environment.
func systemLanguages() []string { return envLanguages(os.Getenv) }
