//go:build !windows

package i18n

import (
	"os"
	"strings"
)

// systemLanguages reads the POSIX locale environment.
func systemLanguages() []string {
	for _, v := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if s := os.Getenv(v); s != "" {
			s, _, _ = strings.Cut(s, ".")
			return []string{strings.ReplaceAll(s, "_", "-")}
		}
	}
	return nil
}
