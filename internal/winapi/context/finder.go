package wincontext

import (
	"errors"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Finder on macOS (detect_darwin.go): the folder of Finder's front window,
// or the desktop when Finder has no window, and the selected items, read
// with AppleScript. These helpers are plain Go so that they are tested on
// every platform.

// FinderBundleID is Finder's bundle identifier.
const FinderBundleID = "com.apple.finder"

// finderScript prints what finderResult parses: "window" or "desktop", then
// the folder (empty for a window without one: Recents, AirDrop, a search),
// then a selected item per line, as POSIX paths. Items that are not files
// (a server, a tag) are skipped. Its argument is "desktop" when the desktop
// has the focus although Finder has windows, else "window".
const finderScript = `on run argv
	tell application "Finder"
		set out to ""
		if (count of Finder windows) is 0 or (item 1 of argv) is "desktop" then
			set out to "desktop" & linefeed & (POSIX path of (desktop as alias))
		else
			set f to ""
			try
				set f to POSIX path of ((target of front Finder window) as alias)
			end try
			set out to "window" & linefeed & f
		end if
		try
			repeat with i in (get selection)
				try
					set out to out & linefeed & (POSIX path of (i as alias))
				end try
			end repeat
		end try
		return out
	end tell
end run`

// finderResult parses the output of finderScript into a context kind
// (plugin.ContextExplorer for a window, plugin.ContextDesktop), the folder
// and the selected items, without trailing slashes. ok is false when the
// output names neither, or the front window shows no folder and nothing is
// selected (Recents, AirDrop, an empty search): there is nothing to offer.
func finderResult(out string) (kind, folder string, selected []string, ok bool) {
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\n")
	switch strings.TrimSpace(lines[0]) {
	case "window":
		kind = plugin.ContextExplorer
	case "desktop":
		kind = plugin.ContextDesktop
	default:
		return "", "", nil, false
	}
	if len(lines) > 1 {
		folder = cleanPOSIX(lines[1])
	}
	for _, l := range lines[min(2, len(lines)):] {
		if p := cleanPOSIX(l); p != "" {
			selected = append(selected, p)
		}
	}
	if folder == "" && len(selected) == 0 {
		return "", "", nil, false
	}
	return kind, folder, selected, true
}

// cleanPOSIX returns the absolute path p without a trailing slash (but "/"
// itself), or "" when p is not absolute.
func cleanPOSIX(p string) string {
	p = strings.TrimRight(p, "\r")
	if !strings.HasPrefix(p, "/") {
		return ""
	}
	return path.Clean(p)
}

// fileURLPath returns the path of a file URL ("file:///Users/a/b%20c/"),
// as the Accessibility attribute AXDocument holds it.
func fileURLPath(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.Path == "" {
		return "", false
	}
	p := cleanPOSIX(u.Path)
	return p, p != ""
}

// ErrNotAuthorized means that macOS did not let golauncher control another
// app (System Settings › Privacy & Security › Automation): AppleScript error
// -1743.
var ErrNotAuthorized = errors.New("wincontext: golauncher is not allowed to control Finder (System Settings › Privacy & Security › Automation)")

// errCode matches the error number osascript prints last: "… (-1743)".
var errCode = regexp.MustCompile(`\((-?\d+)\)\s*$`)

// scriptError maps the standard error of a failed osascript to an error:
// ErrNotAuthorized for -1743 (and -10004, a privilege violation), else one
// naming the AppleScript error number.
func scriptError(stderr string) error {
	stderr = strings.TrimSpace(stderr)
	m := errCode.FindStringSubmatch(stderr)
	if m == nil {
		if stderr == "" {
			return errors.New("wincontext: osascript failed")
		}
		return errors.New("wincontext: osascript: " + stderr)
	}
	code, _ := strconv.Atoi(m[1])
	switch code {
	case -1743, -10004:
		return ErrNotAuthorized
	}
	return errors.New("wincontext: AppleScript error " + m[1])
}

var (
	problemMu sync.Mutex
	problemFn func(error)
)

// OnProblem sets fn to receive the permission problems Detect runs into
// (ErrNotAuthorized on macOS), so that the launcher can tell the user what
// to allow; nil removes it. Detect reports nothing then.
func OnProblem(fn func(error)) {
	problemMu.Lock()
	problemFn = fn
	problemMu.Unlock()
}

// problem reports err to the OnProblem function.
func problem(err error) {
	problemMu.Lock()
	fn := problemFn
	problemMu.Unlock()
	if fn != nil {
		fn(err)
	}
}
