package tasks

import "syscall"

// quoteArg quotes a of a command line shown to the user, the way Windows
// reads it back.
func quoteArg(a string) string { return syscall.EscapeArg(a) }

// quoted are the characters that make CommandLine quote an argument.
const quoted = " \t\""
