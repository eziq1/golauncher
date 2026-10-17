//go:build darwin

package shell

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// On macOS an App is an .app bundle: ID is its bundle identifier (or its
// path when it has none), Path the bundle, Name the name Finder shows in the
// user's language and Keywords its names in English, Chinese, Japanese and
// Korean (so that "WeChat" and "微信" both find it). LinkPath, Args and
// Packaged are unused.

// appDirs are the folders apps are installed in, as Spotlight and
// Launchpad find them.
func appDirs() []string {
	dirs := []string{
		"/Applications",
		"/System/Applications",
		"/System/Library/CoreServices/Applications",
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	return dirs
}

// finderApp is Finder, which lives in CoreServices with the system's
// helpers rather than in an applications folder.
const finderApp = "/System/Library/CoreServices/Finder.app"

// bundlePaths returns the .app bundles in dirs and one level down (the
// Utilities folder, an app's own folder), without looking inside bundles,
// followed by extra; each bundle once, also when a symbolic link points to
// it (as /Applications/Safari.app does), at the first path found.
func bundlePaths(ctx context.Context, dirs []string, extra ...string) []string {
	var paths []string
	seen := map[string]bool{}
	add := func(p string) {
		real := p
		if r, err := filepath.EvalSymlinks(p); err == nil {
			real = r
		} else if _, err := os.Lstat(p); err != nil {
			return
		}
		if !seen[real] {
			seen[real] = true
			paths = append(paths, p)
		}
	}
	for _, dir := range dirs {
		if ctx.Err() != nil {
			return paths
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if isBundleName(e.Name()) {
				add(p)
				continue
			}
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if info, err := os.Stat(p); err != nil || !info.IsDir() {
				continue
			}
			sub, _ := os.ReadDir(p)
			for _, s := range sub {
				if isBundleName(s.Name()) {
					add(filepath.Join(p, s.Name()))
				}
			}
		}
	}
	for _, p := range extra {
		add(p)
	}
	return paths
}

func isBundleName(name string) bool {
	return strings.HasSuffix(name, ".app") && !strings.HasPrefix(name, ".")
}

// bundleInfo is what an app bundle says about itself.
type bundleInfo struct {
	// Plist holds the string values of Contents/Info.plist.
	Plist map[string]string
	// Localized maps a localization (en, zh_CN, zh-Hans, …) to the string
	// values of its InfoPlist.strings (or of InfoPlist.loctable).
	Localized map[string]map[string]string
	// DisplayName is the name Finder shows in the user's language; ""
	// when unknown.
	DisplayName string
}

// keywordLangs are the localizations whose names become keywords: English
// (the name most people know an app by) and the CJK languages, whose names
// cannot be typed as a prefix of the English one.
var keywordLangs = []string{"en", "base", "english", "zh", "chinese", "ja", "japanese", "ko", "korean"}

func keywordLang(loc string) bool {
	loc = strings.ToLower(loc)
	lang, _, _ := strings.Cut(strings.ReplaceAll(loc, "-", "_"), "_")
	return slices.Contains(keywordLangs, lang)
}

// appOf builds the App of the bundle at path from what it says about
// itself; ok is false for a bundle that is not an app worth listing (one
// that runs in the background only, or has no name).
func appOf(path string, b bundleInfo) (a App, ok bool) {
	pl := b.Plist
	if pl["LSBackgroundOnly"] == "1" || strings.EqualFold(pl["LSBackgroundOnly"], "true") {
		return App{}, false
	}
	base := strings.TrimSuffix(filepath.Base(path), ".app")
	a = App{
		Name: cmp.Or(strings.TrimSuffix(b.DisplayName, ".app"), pl["CFBundleDisplayName"], pl["CFBundleName"], base),
		ID:   cmp.Or(pl["CFBundleIdentifier"], path),
		Path: path,
	}
	if a.Name == "" {
		return App{}, false
	}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || strings.EqualFold(name, a.Name) || strings.EqualFold(name, base) ||
			utf8.RuneCountInString(name) > maxNameLen ||
			slices.ContainsFunc(a.Keywords, func(k string) bool { return strings.EqualFold(k, name) }) {
			return
		}
		a.Keywords = append(a.Keywords, name)
	}
	add(pl["CFBundleDisplayName"])
	add(pl["CFBundleName"])
	locs := make([]string, 0, len(b.Localized))
	for loc := range b.Localized {
		if keywordLang(loc) {
			locs = append(locs, loc)
		}
	}
	slices.Sort(locs) // deterministic order: en before ja before zh
	for _, loc := range locs {
		add(b.Localized[loc]["CFBundleDisplayName"])
		add(b.Localized[loc]["CFBundleName"])
	}
	return a, true
}

// installedApps lists the apps of the bundles in dirs (and extra), sorted
// by name, each bundle identifier once, reading each with read.
func installedApps(ctx context.Context, read func(path string) (bundleInfo, error), dirs []string, extra ...string) ([]App, error) {
	var apps []App
	seen := map[string]bool{}
	for _, p := range bundlePaths(ctx, dirs, extra...) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := read(p)
		if err != nil {
			continue
		}
		a, ok := appOf(p, info)
		if !ok || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		apps = append(apps, a)
	}
	slices.SortStableFunc(apps, func(x, y App) int {
		return cmp.Or(cmp.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)), cmp.Compare(x.Path, y.Path))
	})
	return apps, nil
}

// InstalledApps lists the .app bundles of /Applications, /System/Applications,
// ~/Applications and CoreServices' Applications (and of their subfolders,
// such as Utilities), plus Finder.
func InstalledApps(ctx context.Context) ([]App, error) {
	return installedApps(ctx, readBundle, appDirs(), finderApp)
}

// readBundle reads the Info.plist and the localized names of the bundle at
// path.
func readBundle(path string) (bundleInfo, error) {
	var b bundleInfo
	if _, err := os.Stat(filepath.Join(path, "Contents")); err != nil {
		return b, err
	}
	b.Plist = plistStrings(filepath.Join(path, "Contents", "Info.plist"))
	b.Localized = map[string]map[string]string{}
	res := filepath.Join(path, "Contents", "Resources")
	// Apple's own apps keep every localization in one table.
	for loc, m := range plistTable(filepath.Join(res, "InfoPlist.loctable")) {
		if keywordLang(loc) {
			b.Localized[loc] = m
		}
	}
	entries, _ := os.ReadDir(res)
	for _, e := range entries {
		loc, ok := strings.CutSuffix(e.Name(), ".lproj")
		if !ok || !keywordLang(loc) {
			continue
		}
		if m := plistStrings(filepath.Join(res, e.Name(), "InfoPlist.strings")); len(m) > 0 {
			b.Localized[loc] = m
		}
	}
	b.DisplayName = displayName(path)
	return b, nil
}

// bundleOf returns the .app bundle path is in (path itself when it is one),
// or "" when it is in none: Finder draws an app's executable with the
// generic icon of programs, but people know it by the bundle's.
func bundleOf(path string) string {
	for p := path; p != "/" && p != "." && p != ""; p = filepath.Dir(p) {
		if strings.HasSuffix(p, ".app") {
			return p
		}
	}
	return ""
}

// IconPNG draws the icon Finder shows for app's bundle (or, for any other
// path, for that file) as a size×size PNG.
func IconPNG(app App, size int) ([]byte, error) {
	if size <= 0 {
		return nil, errors.New("shell: icon size must be positive")
	}
	path := app.Path
	if b := bundleOf(path); b != "" {
		path = b
	}
	if path == "" {
		return nil, errors.New("shell: the app has no path")
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real // the icon of what a link points to, without the arrow
	} else {
		return nil, err
	}
	return iconPNG(path, size)
}

// FileDescription returns the name of the bundle exe is in, or "".
func FileDescription(exe string) string {
	b := bundleOf(exe)
	if b == "" {
		return ""
	}
	pl := plistStrings(filepath.Join(b, "Contents", "Info.plist"))
	return cmp.Or(pl["CFBundleDisplayName"], pl["CFBundleName"])
}

// Open opens target as /usr/bin/open does: a URL with the app that handles
// it, a file or folder with its default app, an app by starting it (or
// bringing it to the front). With Reveal it selects target in Finder.
//
// Args (a command line, quoted as on Windows or in a shell) go to an app as
// the documents or URLs to open when they all are such (a browser and its
// URL, an editor and a folder), else as its command-line arguments; an
// executable file other than an app is run directly with Args in Dir. The
// "runas" verb is not supported: macOS has no equivalent of running an app
// as administrator.
func Open(target string, opts plugin.OpenOptions) error {
	if target == "" {
		return errors.New("shell: empty target")
	}
	if opts.Verb != "" && opts.Verb != "open" {
		return fmt.Errorf("shell: verb %q: %w", opts.Verb, errors.ErrUnsupported)
	}
	if !opts.Reveal && (opts.Args != "" || opts.Dir != "") && isPlainExecutable(target) {
		cmd := exec.Command(target, splitArgs(opts.Args)...)
		cmd.Dir = opts.Dir
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("shell: cannot open %s: %w", target, err)
		}
		go cmd.Wait() // reap it; it outlives the action
		return nil
	}
	argv := openArgv(target, opts, pathExists)
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			return fmt.Errorf("shell: cannot open %s: %s", target, msg)
		}
		return fmt.Errorf("shell: cannot open %s: %w", target, err)
	}
	return nil
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// isPlainExecutable reports whether path is an executable file that is not
// in an app bundle's role (a script, a command-line program).
func isPlainExecutable(path string) bool {
	if bundleOf(path) == path {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

// openArgv returns the /usr/bin/open command line for Open (without the
// direct start of plain executables). exists reports whether a path exists.
func openArgv(target string, opts plugin.OpenOptions, exists func(string) bool) []string {
	if opts.Reveal {
		return []string{"/usr/bin/open", "-R", target}
	}
	args := splitArgs(opts.Args)
	if strings.HasSuffix(strings.TrimSuffix(target, "/"), ".app") && len(args) > 0 {
		docs := true
		for _, a := range args {
			if !(isURL(a) || filepath.IsAbs(a) && exists(a)) {
				docs = false
				break
			}
		}
		argv := []string{"/usr/bin/open", "-a", target}
		if !docs {
			argv = append(argv, "--args")
		}
		return append(argv, args...)
	}
	return []string{"/usr/bin/open", target}
}

// isURL reports whether s starts with a URL scheme ("https:", "mailto:").
func isURL(s string) bool {
	scheme, _, ok := strings.Cut(s, ":")
	if !ok || len(scheme) < 2 {
		return false
	}
	for i, r := range scheme {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return true
}

// splitArgs splits a command line into arguments at unquoted spaces and
// tabs. Double quotes group as on Windows and in a shell (a backslash
// escapes a quote or a backslash in them), single quotes group literally.
func splitArgs(s string) []string {
	var args []string
	var cur strings.Builder
	inArg, quote := false, rune(0)
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\'):
				i++
				cur.WriteRune(rs[i])
			case r == '"':
				quote = 0
			default:
				cur.WriteRune(r)
			}
		case r == ' ' || r == '\t' || r == '\n':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		case r == '"' || r == '\'':
			quote, inArg = r, true
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args
}
