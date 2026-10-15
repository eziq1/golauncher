// Package cmdline builds Windows command lines: arguments quoted the way
// CommandLineToArgvW (and the C runtime) reads them back. It is plain Go and
// builds on every platform, so plugins can use it in tests anywhere.
package cmdline

import "strings"

// Quote quotes s as one argument of a Windows command line: in quotes when
// it is empty or has spaces, tabs or quotes, with backslashes before a quote
// (and at the end of a quoted argument) doubled.
func Quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for i := range len(s) {
		c := s[i]
		switch c {
		case '\\':
			slashes++
		case '"':
			b.WriteString(strings.Repeat(`\`, slashes+1))
			slashes = 0
		default:
			slashes = 0
		}
		b.WriteByte(c)
	}
	b.WriteString(strings.Repeat(`\`, slashes))
	b.WriteByte('"')
	return b.String()
}

// Join quotes each argument with Quote and joins them with spaces.
func Join(args ...string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = Quote(a)
	}
	return strings.Join(q, " ")
}

// WT quotes an argument for wt.exe (Windows Terminal). Windows Terminal
// splits its command line into subcommands at every ";", so a literal
// semicolon in a profile name or a folder is escaped as `\;` first.
func WT(s string) string {
	return Quote(strings.ReplaceAll(s, ";", `\;`))
}

// JoinWT quotes each argument with WT and joins them with spaces.
func JoinWT(args ...string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = WT(a)
	}
	return strings.Join(q, " ")
}
