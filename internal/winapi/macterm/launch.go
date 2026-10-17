package macterm

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Open and osascript are the system's tools.
const (
	openTool      = "/usr/bin/open"
	osascriptTool = "/usr/bin/osascript"
	qlmanageTool  = "/usr/bin/qlmanage"
)

// Launch is a program to start for an action.
type Launch struct {
	Argv []string
	// Detach starts the program without waiting for it: it is the app
	// itself, or keeps running while its window is open (qlmanage).
	// Otherwise Run waits for it and reports its error output.
	Detach bool
	// Fallback is started when the program fails (WezTerm's new tab when
	// WezTerm is not running).
	Fallback *Launch
}

// Request is what to open in a terminal.
type Request struct {
	// Dir is the folder to start in. It must be set (the home folder for
	// none in particular).
	Dir string
	// Command is a shell command line to run, "" for an interactive shell.
	Command string
	// Tab opens a tab of the frontmost window instead of a new window.
	Tab bool
	// Shell is the user's login shell (from $SHELL), used by terminals
	// that start a program rather than type into a shell; default
	// /bin/zsh.
	Shell string
}

// ErrNoDir is returned for a Request without a folder.
var ErrNoDir = errors.New("macterm: no folder to open")

// Launch returns how to open r in t.
func (t Installed) Launch(r Request) (Launch, error) {
	if r.Dir == "" {
		return Launch{}, ErrNoDir
	}
	if r.Tab && !t.Tabs {
		return Launch{}, fmt.Errorf("macterm: %s cannot open tabs: %w", t.Name, errors.ErrUnsupported)
	}
	if r.Command != "" && !t.Commands {
		return Launch{}, fmt.Errorf("macterm: %s cannot run commands: %w", t.Name, errors.ErrUnsupported)
	}
	if r.Shell == "" {
		r.Shell = "/bin/zsh"
	}
	switch t.Driver {
	case DriverTerminal:
		if r.Command == "" && !r.Tab {
			// A new window in the folder without Apple Events, so without
			// the Automation permission.
			return Launch{Argv: []string{openTool, "-a", t.Path, r.Dir}}, nil
		}
		return Launch{Argv: Osascript(TerminalScript(t.BundleID, ShellLine(r.Dir, r.Command), r.Tab))}, nil
	case DriverITerm:
		return Launch{Argv: Osascript(ITermScript(t.BundleID, ShellLine(r.Dir, r.Command), r.Tab))}, nil
	case DriverGhostty:
		argv := []string{openTool, "-na", t.Path, "--args", "--working-directory=" + r.Dir}
		if r.Command != "" {
			argv = append(append(argv, "-e"), shellArgv(r.Shell, r.Command)...)
		}
		return Launch{Argv: argv}, nil
	case DriverAlacritty:
		argv := []string{openTool, "-na", t.Path, "--args", "--working-directory", r.Dir}
		if r.Command != "" {
			argv = append(append(argv, "-e"), shellArgv(r.Shell, r.Command)...)
		}
		return Launch{Argv: argv}, nil
	case DriverKitty:
		argv := []string{openTool, "-na", t.Path, "--args", "--directory", r.Dir}
		if r.Command != "" {
			argv = append(argv, shellArgv(r.Shell, r.Command)...)
		}
		return Launch{Argv: argv}, nil
	case DriverWezTerm:
		cli := filepath.Join(t.Path, "Contents", "MacOS", "wezterm")
		var prog []string
		if r.Command != "" {
			prog = append([]string{"--"}, shellArgv(r.Shell, r.Command)...)
		}
		start := Launch{Argv: append([]string{cli, "start", "--cwd", r.Dir}, prog...), Detach: true}
		if !r.Tab {
			return start, nil
		}
		// cli spawn needs a running WezTerm; without one, start opens the
		// first window.
		return Launch{Argv: append([]string{cli, "cli", "spawn", "--cwd", r.Dir}, prog...), Fallback: &start}, nil
	case DriverWarp:
		action := "new_window"
		if r.Tab {
			action = "new_tab"
		}
		return Launch{Argv: []string{openTool, "-b", t.BundleID, "warp://action/" + action + "?path=" + queryEscape(r.Dir)}}, nil
	}
	return Launch{}, fmt.Errorf("macterm: %s: %w", t.Name, errors.ErrUnsupported)
}

// shellArgv runs command in the login shell, then stays in an interactive
// shell, as typing the command into a terminal would.
func shellArgv(shell, command string) []string {
	return []string{shell, "-l", "-c", command + "; exec " + ShellQuote(shell) + " -l"}
}

// ShellLine is what is typed into a new shell to go to dir and run command
// ("" clears the screen instead, so the window starts clean).
func ShellLine(dir, command string) string {
	line := "cd " + ShellQuote(dir)
	if command == "" {
		return line + " && clear"
	}
	return line + " && " + command
}

// ShellQuote quotes s as one word of a POSIX shell: as is when it only has
// characters that are never special, else in single quotes (a single quote
// in s becomes '\”).
func ShellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !shellSafe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("/._-+,:@%=", r)
}

// AppleScriptString returns s as an AppleScript string literal: in double
// quotes, with backslashes, quotes and line breaks escaped, so that the
// script stays one line.
func AppleScriptString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Osascript is the osascript command line running the script lines, each
// as its own -e.
func Osascript(lines []string) []string {
	argv := []string{osascriptTool}
	for _, l := range lines {
		argv = append(argv, "-e", l)
	}
	return argv
}

// waitForWindow is the start of a script for an app that was not running:
// it starts it and waits (at most 5 s) for the window it opens at launch,
// which the script then uses instead of opening a second one.
func waitForWindow() []string {
	return []string{
		"\tactivate",
		"\tif not wasRunning then",
		"\t\trepeat 50 times",
		"\t\t\tif (count of windows) > 0 then exit repeat",
		"\t\t\tdelay 0.1",
		"\t\tend repeat",
		"\tend if",
	}
}

// TerminalScript is the AppleScript that types line into a new shell of
// Terminal.app (bundle id): in a new window, or in a new tab of the front
// window when tab. Terminal has no AppleScript command for tabs, so a tab
// is opened by pressing ⌘T through System Events, which needs the
// Accessibility permission besides Automation.
func TerminalScript(bundleID, line string, tab bool) []string {
	app := "application id " + AppleScriptString(bundleID)
	cmd := AppleScriptString(line)
	s := []string{
		"set wasRunning to " + app + " is running",
		"tell " + app,
	}
	s = append(s, waitForWindow()...)
	if tab {
		s = append(s,
			"\tif (count of windows) = 0 then",
			"\t\tdo script "+cmd,
			"\telse",
			"\t\tif wasRunning then",
			"\t\t\ttell application \"System Events\" to keystroke \"t\" using command down",
			"\t\t\tdelay 0.2",
			"\t\tend if",
			"\t\tdo script "+cmd+" in front window",
			"\tend if",
		)
	} else {
		s = append(s,
			"\tif not wasRunning and (count of windows) > 0 then",
			"\t\tdo script "+cmd+" in front window",
			"\telse",
			"\t\tdo script "+cmd,
			"\tend if",
		)
	}
	return append(s, "end tell")
}

// ITermScript is the AppleScript that types line into a new session of
// iTerm2 (bundle id): in a new window, or in a new tab of the current
// window when tab.
func ITermScript(bundleID, line string, tab bool) []string {
	app := "application id " + AppleScriptString(bundleID)
	s := []string{
		"set wasRunning to " + app + " is running",
		"tell " + app,
	}
	s = append(s, waitForWindow()...)
	if tab {
		s = append(s,
			"\tif (count of windows) = 0 then",
			"\t\tset w to (create window with default profile)",
			"\telse",
			"\t\tset w to current window",
			"\t\tif wasRunning then tell w to create tab with default profile",
			"\tend if",
		)
	} else {
		s = append(s,
			"\tif not wasRunning and (count of windows) > 0 then",
			"\t\tset w to current window",
			"\telse",
			"\t\tset w to (create window with default profile)",
			"\tend if",
		)
	}
	return append(s,
		"\ttell current session of w to write text "+AppleScriptString(line),
		"end tell")
}

// queryEscape escapes s for a URL query value: everything but unreserved
// characters and "/" is percent-encoded (spaces as %20, not +).
func queryEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte("-._~/", c) >= 0:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

// OpenWith opens paths (folders or files) with the app of bundleID.
func OpenWith(bundleID string, paths ...string) Launch {
	return Launch{Argv: append([]string{openTool, "-b", bundleID}, paths...)}
}

// QuickLook previews paths with Quick Look. qlmanage keeps running while
// the preview is open.
func QuickLook(paths ...string) Launch {
	return Launch{Argv: append([]string{qlmanageTool, "-p"}, paths...), Detach: true}
}
