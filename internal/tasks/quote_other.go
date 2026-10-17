//go:build !windows

package tasks

import "strings"

// quoteArg quotes a of a command line shown to the user so that a POSIX
// shell reads it back: in double quotes when nothing inside is special
// there (the same text as on Windows), else in single quotes; a single
// quote inside closes them, is escaped with a backslash and reopens them.
func quoteArg(a string) string {
	if !strings.ContainsAny(a, "$`\\\"!") {
		return `"` + a + `"`
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}

// quoted are the characters that make CommandLine quote an argument: those
// a shell would split at or expand.
const quoted = " \t\n\"'\\$`;&|<>()*?[]{}~#!"
