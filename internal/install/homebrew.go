package install

import (
	"encoding/json/v2"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Homebrew installs golauncher on macOS as a cask of the tap eziq1/tap
// (docs/homebrew-packaging.md).
const (
	// CaskToken is the cask's name, and the folder of its Caskroom.
	CaskToken = AppName
	// CaskFullName is the cask with its tap, as brew install takes it.
	CaskFullName = "eziq1/tap/" + CaskToken
	// HomebrewUpgrade is the command that updates a copy installed with
	// Homebrew.
	HomebrewUpgrade = "brew upgrade --cask " + CaskToken
)

// HomebrewPrefixes returns where Homebrew may be installed: HOMEBREW_PREFIX
// when it is set (and absolute), then /opt/homebrew (Apple silicon) and
// /usr/local (Intel). An app opened from Finder does not have the shell's
// HOMEBREW_PREFIX; the two defaults are where Homebrew's installer puts it.
func HomebrewPrefixes(getenv func(string) string) []string {
	prefixes := []string{}
	if p := getenv("HOMEBREW_PREFIX"); strings.HasPrefix(p, "/") {
		prefixes = append(prefixes, path.Clean(p))
	}
	for _, p := range []string{"/opt/homebrew", "/usr/local"} {
		if !slices.Contains(prefixes, p) {
			prefixes = append(prefixes, p)
		}
	}
	return prefixes
}

// HomebrewManaged reports whether exe (with links resolved) is the
// golauncher.app that Homebrew installed from the cask, without running
// brew. Homebrew keeps a cask's state in <prefix>/Caskroom/<token>: a
// folder per installed version, and .metadata/config.json with the
// folders it installed into, among them "appdir" (by precedence: the
// --appdir given to brew install, HOMEBREW_CASK_OPTS, the default
// /Applications). The app is moved into appdir, so exe is managed when it
// is <appdir>/golauncher.app/Contents/MacOS/<program> and a Caskroom of
// golauncher, under one of prefixes, has a version folder. Without a
// readable config.json, appdir may be /Applications or ~/Applications (home
// is the user's home folder, "" for none).
func HomebrewManaged(exe string, prefixes []string, home string) bool {
	if exe == "" {
		return false
	}
	app, ok := BundleOf(exe)
	if !ok || !strings.EqualFold(filepath.Base(app), BundleName) {
		return false
	}
	dir := filepath.Dir(app)
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		room := filepath.Join(prefix, "Caskroom", CaskToken)
		if !hasVersionDir(room) {
			continue
		}
		appdirs := caskAppdirs(filepath.Join(room, ".metadata", "config.json"))
		if appdirs == nil {
			appdirs = []string{"/Applications"}
			if home != "" {
				appdirs = append(appdirs, filepath.Join(home, "Applications"))
			}
		}
		for _, d := range appdirs {
			// APFS and HFS+ volumes ignore case by default.
			if strings.EqualFold(filepath.Clean(d), dir) {
				return true
			}
		}
	}
	return false
}

// hasVersionDir reports whether a Caskroom folder holds an installed
// version: a folder other than .metadata.
func hasVersionDir(room string) bool {
	entries, err := os.ReadDir(room)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}

// caskAppdirs returns the appdir Homebrew recorded in a cask's
// config.json ({"default": {...}, "env": {...}, "explicit": {...}}, the
// paths expanded), or nil when there is none.
func caskAppdirs(file string) []string {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var cfg struct {
		Default  map[string]any `json:"default"`
		Env      map[string]any `json:"env"`
		Explicit map[string]any `json:"explicit"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return nil
	}
	for _, m := range []map[string]any{cfg.Explicit, cfg.Env, cfg.Default} {
		if s, ok := m["appdir"].(string); ok && isAbs(s) {
			return []string{s}
		}
	}
	return nil
}

// isAbs reports whether p is an absolute path, a macOS one included where
// tests run elsewhere.
func isAbs(p string) bool { return strings.HasPrefix(p, "/") || filepath.IsAbs(p) }
