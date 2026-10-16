package proc

import (
	"errors"
	"strings"
	"syscall"
)

// Running a program as administrator (docs/background-tasks.md §11, R68).
// StartElevated shows the UAC prompt through ShellExecuteEx "runas" and
// returns an Elevated; golauncher itself keeps running unelevated. The
// output of an elevated program cannot be captured: its console belongs
// to another integrity level, and ShellExecuteEx has no pipes.

var (
	// ErrElevationCancelled reports that the user declined the UAC prompt
	// (ERROR_CANCELLED).
	ErrElevationCancelled = errors.New("proc: the administrator prompt was cancelled")
	// ErrNotEnded is returned by Elevated.Wait after Stop could not end
	// the elevated program (an unelevated process may not be allowed to);
	// the program may still be running.
	ErrNotEnded = errors.New("proc: the elevated program could not be ended")
	// errElevateInTest keeps tests from ever showing a UAC prompt.
	errElevateInTest = errors.New("proc: refusing to run a program as administrator in a test")
)

// ErrorElevationRequired is ERROR_ELEVATION_REQUIRED (740): CreateProcess
// refuses to start a program whose manifest asks for administrator rights.
// errors.Is(err, ErrorElevationRequired) tests a Start error for it.
const ErrorElevationRequired = syscall.Errno(740)

// elevationMarkers are output fragments of the Windows tools known to fail
// for lack of administrator rights, in English and Chinese (zh-CN):
//
//   - "requires elevation" / "需要提升": the system message of
//     ERROR_ELEVATION_REQUIRED, which ipconfig /flushdns, /release, /renew
//     and netsh print ("The requested operation requires elevation." /
//     "请求的操作需要提升。", netsh adds "(Run as administrator)").
//
// The generic "Access is denied" alone is not a marker: too many programs
// print it for reasons administrator rights do not fix. sc.exe's form is
// matched separately (scDenied).
var elevationMarkers = []string{"requires elevation", "需要提升"}

// NeedsElevation reports whether output of a program that failed says it
// needs administrator rights (elevationMarkers, or sc.exe's "[SC] <call>
// FAILED 5:", ERROR_ACCESS_DENIED from the service manager). It is
// conservative: other wording is not recognized.
func NeedsElevation(output string) bool {
	low := strings.ToLower(output)
	for _, m := range elevationMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return scDenied(output)
}

// scDenied reports whether output has a line "[SC] <Function> FAILED 5:".
func scDenied(output string) bool {
	for line := range strings.Lines(output) {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "[SC] ")
		if !ok {
			continue
		}
		fn, rest, ok := strings.Cut(rest, " ")
		if ok && fn != "" && strings.HasPrefix(rest, "FAILED 5:") {
			return true
		}
	}
	return false
}
