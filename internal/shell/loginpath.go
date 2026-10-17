package shell

import (
	"path/filepath"
	"slices"
	"strings"
)

// The PATH of a GUI program on macOS (docs/macos-port.md §5.3): started
// from Finder, the Dock or at login, golauncher gets launchd's PATH,
// /usr/bin:/bin:/usr/sbin:/sbin, without Homebrew, ~/.local/bin and the
// other folders the user's shell adds. Scripts, external plugins and the
// AI's command-line tools are looked up on it, so the shell takes the PATH
// of the user's login shell when it starts (fixPath, path_darwin.go).

// wellKnownBins are added to PATH when the login shell cannot be asked;
// "~" is the home folder.
var wellKnownBins = []string{"/opt/homebrew/bin", "/opt/homebrew/sbin", "/usr/local/bin", "~/.local/bin"}

// needsPath reports whether path looks like launchd's: none of the
// well-known folders is on it.
func needsPath(path, home string) bool {
	dirs := filepath.SplitList(path)
	for _, d := range wellKnownBins {
		if slices.Contains(dirs, expandHome(d, home)) {
			return false
		}
	}
	return true
}

// mergePath returns the folders of login (the login shell's PATH, or the
// well-known folders when it is empty) followed by those of cur not in it,
// without empty entries and duplicates.
func mergePath(cur, login, home string) string {
	first := filepath.SplitList(strings.TrimSpace(login))
	if len(first) == 0 {
		for _, d := range wellKnownBins {
			first = append(first, expandHome(d, home))
		}
	}
	var out []string
	for _, d := range append(first, filepath.SplitList(cur)...) {
		if d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return strings.Join(out, string(filepath.ListSeparator))
}

// expandHome replaces a leading "~/" of dir with home.
func expandHome(dir, home string) string {
	if rest, ok := strings.CutPrefix(dir, "~/"); ok && home != "" {
		return filepath.Join(home, rest)
	}
	return dir
}
