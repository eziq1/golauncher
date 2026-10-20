package codex

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// findEnv is what Candidates looks at: the operating system (GOOS), the
// directories of PATH, the processor architecture (GOARCH), the usual
// install folders and Node.js version managers' folders searched after PATH
// (Dirs, agentrt.InstallDirs) and, on Windows, the %LOCALAPPDATA% and
// %APPDATA% folders; elsewhere the CLIs the Codex desktop app may carry
// (Apps). Tests make one up.
type findEnv struct {
	OS                    string
	Path                  []string
	LocalAppData, AppData string
	Arch                  string
	Dirs                  []agentrt.BinDir
	Apps                  []string
}

// candidateEnv returns the environment Candidates looks at (a variable, so
// that tests can make one up).
var candidateEnv = func() findEnv {
	e := findEnv{
		OS:           runtime.GOOS,
		Path:         filepath.SplitList(os.Getenv("PATH")),
		LocalAppData: os.Getenv("LOCALAPPDATA"),
		AppData:      os.Getenv("APPDATA"),
		Arch:         runtime.GOARCH,
		Dirs:         agentrt.InstallDirs(),
	}
	if e.OS != "windows" {
		home, _ := os.UserHomeDir()
		e.Apps = unixApps(home)
	}
	return e
}

// exeName is the native CLI's file name.
const exeName = "codex.exe"

// shimNames are the files of the npm launcher, which needs Node.js.
var shimNames = []string{"codex.cmd", "codex.ps1", "codex"}

// triples are the Rust target triples of the native binaries this computer
// can run, preferred first: an ARM64 Windows also runs the x64 binary.
func (e findEnv) triples() []string {
	if e.Arch == "arm64" {
		return []string{"aarch64-pc-windows-msvc", "x86_64-pc-windows-msvc"}
	}
	return []string{"x86_64-pc-windows-msvc"}
}

// platformPackage is the npm package that carries the native binary of a
// triple in newer releases (an optional dependency of @openai/codex).
func platformPackage(triple string) string {
	if strings.HasPrefix(triple, "aarch64") {
		return "codex-win32-arm64"
	}
	return "codex-win32-x64"
}

// candidates returns the native Codex CLIs found, in order of preference
// (the runtime uses the one with the highest version):
//
//  1. codex.exe in a directory of PATH;
//  2. the Codex desktop app's CLIs, %LOCALAPPDATA%\OpenAI\Codex\bin\codex.exe
//     and the versioned %LOCALAPPDATA%\OpenAI\Codex\bin\*\codex.exe;
//  3. the native binary of an npm installation, next to its launcher (a
//     directory of PATH with codex.cmd, codex.ps1 or codex, %APPDATA%\npm,
//     npm's prefix and the Node.js version managers' folders, e.Dirs), in
//     every layout the launcher (bin/codex.js) looks at.
//
// The npm launcher itself is never returned: it needs Node.js. When it is
// all there is, the error says so (ErrNotInstalled); when nothing is found,
// both are nil. This is Windows (e.OS); elsewhere see unixDiscover. The
// search is discover's; candidates leaves out the launchers.
func candidates(e findEnv) ([]string, error) {
	found := discover(e)
	var out []string
	shim := ""
	for _, f := range found {
		switch {
		case !f.Launcher:
			out = append(out, f.Path)
		case shim == "":
			shim = f.Path
		}
	}
	if len(out) == 0 && shim != "" {
		if e.OS != "windows" {
			return nil, &backend.Error{Kind: backend.ErrNotInstalled, Message: "Only the Node.js launcher of Codex was found (" + shim +
				"). golauncher runs the native codex binary: install Codex with Homebrew (brew install --cask codex), or put codex from a Codex CLI release on PATH."}
		}
		return nil, &backend.Error{Kind: backend.ErrNotInstalled, Message: "Only the Node.js launcher of Codex was found (" + shim +
			"). golauncher runs the native codex.exe: install or update the Codex app, or put codex.exe from a Codex CLI release on PATH."}
	}
	return out, nil
}

// discover is the search of candidates, with how each CLI was found (R84):
// the native CLIs in order of preference, and the npm launchers (Launcher,
// one per folder, after the native binaries of their installation), which
// are never run.
func discover(e findEnv) []agentrt.Found {
	if e.OS != "windows" {
		return unixDiscover(e)
	}
	var out []agentrt.Found
	seen := map[string]bool{}
	add := func(p string, d agentrt.BinDir, src backend.Source, launcher bool) {
		if !isFile(p) {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		k := strings.ToLower(filepath.Clean(p))
		if seen[k] {
			return
		}
		seen[k] = true
		f := agentrt.FoundIn(p, d, src)
		f.Launcher = launcher
		out = append(out, f)
	}
	none := agentrt.BinDir{}
	var dirs []string // PATH, cleaned
	for _, d := range e.Path {
		d = strings.Trim(strings.TrimSpace(d), `"`)
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	for _, d := range dirs {
		add(filepath.Join(d, exeName), none, backend.SourcePath, false)
	}
	if e.LocalAppData != "" {
		bin := filepath.Join(e.LocalAppData, "OpenAI", "Codex", "bin")
		add(filepath.Join(bin, exeName), none, backend.SourceApp, false)
		matches, _ := filepath.Glob(filepath.Join(bin, "*", exeName))
		slices.Sort(matches)
		for _, m := range matches {
			add(m, none, backend.SourceApp, false)
		}
	}
	type shimDir struct {
		dir   string
		src   backend.Source
		after agentrt.BinDir // searched after PATH
	}
	var shimDirs []shimDir
	for _, d := range dirs {
		shimDirs = append(shimDirs, shimDir{dir: d, src: backend.SourcePath})
	}
	if e.AppData != "" {
		npm := filepath.Join(e.AppData, "npm")
		shimDirs = append(shimDirs, shimDir{dir: npm, src: backend.SourceInstallDir, after: agentrt.BinDir{Dir: npm}})
	}
	for _, d := range e.Dirs {
		shimDirs = append(shimDirs, shimDir{dir: d.Dir, src: backend.SourceInstallDir, after: d})
	}
	for _, d := range shimDirs {
		s := shimIn(d.dir)
		if s == "" {
			continue
		}
		for _, p := range vendored(d.dir, e) {
			add(p, d.after, backend.SourceNpmVendor, false)
		}
		add(s, d.after, d.src, true)
	}
	return out
}

// shimIn returns the npm launcher in dir, "" when there is none.
func shimIn(dir string) string {
	for _, n := range shimNames {
		if p := filepath.Join(dir, n); isFile(p) {
			return p
		}
	}
	return ""
}

// vendored returns the paths where an npm installation whose launcher is in
// dir keeps the native binary (whether they exist or not), preferred first:
// inside the @openai/codex package (older releases), in its platform package
// installed under it, or hoisted next to it; each in the layout of 0.150 and
// later (vendor/<triple>/bin/codex.exe), then in the older one
// (vendor/<triple>/codex/codex.exe).
func vendored(dir string, e findEnv) []string {
	nm := filepath.Join(dir, "node_modules", "@openai")
	var out []string
	for _, t := range e.triples() {
		pkg := platformPackage(t)
		for _, base := range []string{
			filepath.Join(nm, "codex"),
			filepath.Join(nm, "codex", "node_modules", "@openai", pkg),
			filepath.Join(nm, pkg),
		} {
			out = append(out,
				filepath.Join(base, "vendor", t, "bin", exeName),
				filepath.Join(base, "vendor", t, "codex", exeName),
			)
		}
	}
	return out
}

// nativePath returns the CLI to run for a configured path: the path itself,
// unless it is the npm launcher, which is replaced by the native binary
// next to it (or by a codex.exe in its directory that does not exist, which
// the runtime reports as not installed).
func nativePath(path string, e findEnv) string {
	if e.OS != "windows" {
		return unixNativePath(path, e)
	}
	ext := strings.ToLower(filepath.Ext(path))
	base := strings.ToLower(filepath.Base(path))
	if ext == ".exe" || base != "codex" && ext != ".cmd" && ext != ".ps1" && ext != ".bat" {
		return path
	}
	dir := filepath.Dir(path)
	for _, p := range vendored(dir, e) {
		if isFile(p) {
			return p
		}
	}
	return filepath.Join(dir, exeName)
}

// isFile reports whether p is an existing file.
func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// The native CLI's file name outside Windows, and the npm launcher's script
// (bin/codex.js of the @openai/codex package) that the `codex` on PATH links
// to when Codex was installed with npm.
const (
	unixExeName  = "codex"
	launcherName = "codex.js"
)

// unixApps are the CLIs the Codex desktop app may carry outside Windows, for
// the home folder home. (The folders searched after PATH, e.Dirs, are
// agentrt.InstallDirs: Homebrew, whose codex formula and cask are native,
// npm's prefixes and the Node.js version managers' folders.)
func unixApps(home string) (apps []string) {
	appRoots := []string{"/Applications"}
	if home != "" {
		appRoots = append(appRoots, filepath.Join(home, "Applications"))
	}
	for _, r := range appRoots {
		res := filepath.Join(r, "Codex.app", "Contents", "Resources")
		apps = append(apps, filepath.Join(res, unixExeName), filepath.Join(res, "bin", unixExeName))
	}
	return apps
}

// unixDiscover is discover outside Windows, in order of preference:
//
//  1. a native codex in a directory of PATH, then of e.Dirs (Homebrew, a
//     release binary, a version manager's folder, …);
//  2. the Codex desktop app's CLI (e.Apps);
//  3. the native binary of an npm installation whose launcher (a codex that
//     is a script, usually a link to bin/codex.js) is in one of those
//     directories, in every layout the launcher looks at.
//
// As on Windows, the launcher itself is never returned: it needs Node.js
// (unixDiscover lists it as a Launcher; candidates leaves it out).
func unixDiscover(e findEnv) []agentrt.Found {
	var out []agentrt.Found
	seen := map[string]bool{}
	add := func(p string, d agentrt.BinDir, src backend.Source, launcher bool) {
		if launcher {
			if !isFile(p) {
				return
			}
		} else if !isExecutable(p) || isScript(p) {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		k := filepath.Clean(p)
		if real, err := filepath.EvalSymlinks(p); err == nil && !launcher {
			k = real
		}
		if seen[k] {
			return
		}
		seen[k] = true
		f := agentrt.FoundIn(p, d, src)
		f.Launcher = launcher
		out = append(out, f)
	}
	type dir struct {
		bin agentrt.BinDir // the zero BinDir on PATH
		src backend.Source
	}
	var dirs []dir
	for _, d := range e.Path {
		if d = strings.TrimSpace(d); d != "" {
			dirs = append(dirs, dir{agentrt.BinDir{Dir: d}, backend.SourcePath})
		}
	}
	for _, d := range e.Dirs {
		if strings.TrimSpace(d.Dir) != "" {
			dirs = append(dirs, dir{d, backend.SourceInstallDir})
		}
	}
	none := agentrt.BinDir{}
	for _, d := range dirs {
		add(filepath.Join(d.bin.Dir, unixExeName), d.bin, d.src, false)
	}
	for _, p := range e.Apps {
		add(p, none, backend.SourceApp, false)
	}
	for _, d := range dirs {
		s := filepath.Join(d.bin.Dir, unixExeName)
		if !isFile(s) || !isScript(s) {
			continue
		}
		for _, p := range unixVendored(s, e) {
			add(p, d.bin, backend.SourceNpmVendor, false)
		}
		add(s, d.bin, d.src, true)
	}
	return out
}

// unixTriples are the Rust target triples of the native binaries this Mac
// (or Linux computer) can run, preferred first: Apple silicon also runs the
// Intel binary (Rosetta).
func (e findEnv) unixTriples() []string {
	if e.OS == "darwin" {
		if e.Arch == "arm64" {
			return []string{"aarch64-apple-darwin", "x86_64-apple-darwin"}
		}
		return []string{"x86_64-apple-darwin"}
	}
	if e.Arch == "arm64" {
		return []string{"aarch64-unknown-linux-musl"}
	}
	return []string{"x86_64-unknown-linux-musl"}
}

// unixPlatformPackage is the npm package with the native binary of a triple
// (@openai/codex's optional dependencies @openai/codex-darwin-arm64, …).
func unixPlatformPackage(triple string) string {
	cpu := "x64"
	if strings.HasPrefix(triple, "aarch64") {
		cpu = "arm64"
	}
	if strings.HasSuffix(triple, "-apple-darwin") {
		return "codex-darwin-" + cpu
	}
	return "codex-linux-" + cpu
}

// unixVendored returns the paths where the npm installation of a launcher
// (the `codex` script in a bin folder, or bin/codex.js itself) keeps the
// native binary, whether they exist or not, preferred first. The package is
// the one the launcher is or links to (…/@openai/codex/bin/codex.js), else
// the one of npm's prefix layout (<prefix>/bin/codex and
// <prefix>/lib/node_modules/@openai/codex). Inside it, in the layouts of
// the launcher: the platform package nested under it or hoisted next to it,
// whose binary is vendor/<triple>/bin/codex (0.150 and later), or the
// package's own vendor folder, vendor/<triple>/bin/codex or
// vendor/<triple>/codex/codex (up to 0.130).
func unixVendored(launcher string, e findEnv) []string {
	var pkgs []string
	real := launcher
	if r, err := filepath.EvalSymlinks(launcher); err == nil {
		real = r
	}
	if filepath.Base(real) == launcherName {
		pkgs = append(pkgs, filepath.Dir(filepath.Dir(real)))
	}
	if filepath.Base(launcher) != launcherName {
		if p := filepath.Join(filepath.Dir(launcher), "..", "lib", "node_modules", "@openai", "codex"); !slices.Contains(pkgs, p) {
			pkgs = append(pkgs, filepath.Clean(p))
		}
	}
	var out []string
	for _, pkg := range pkgs {
		scope := filepath.Dir(pkg) // node_modules/@openai
		for _, t := range e.unixTriples() {
			bin := filepath.Join("vendor", t, "bin", unixExeName)
			plat := unixPlatformPackage(t)
			out = append(out,
				filepath.Join(pkg, "node_modules", "@openai", plat, bin),
				filepath.Join(scope, plat, bin),
				filepath.Join(pkg, bin),
				filepath.Join(pkg, "vendor", t, "codex", unixExeName),
			)
		}
	}
	return out
}

// unixNativePath is nativePath outside Windows: a configured npm launcher
// (a script, or bin/codex.js) is replaced by the native binary of its
// installation, or, when there is none, by where that binary would be
// (which the runtime reports as not installed).
func unixNativePath(path string, e findEnv) string {
	if !isScript(path) && filepath.Base(path) != launcherName {
		return path
	}
	paths := unixVendored(path, e)
	for _, p := range paths {
		if isExecutable(p) {
			return p
		}
	}
	return paths[0]
}

// isExecutable reports whether p is an existing file that may be run.
func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// isScript reports whether the file p is a script ("#!"), like the npm
// launcher (a link to codex.js, which starts with "#!/usr/bin/env node").
func isScript(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 2)
	n, _ := io.ReadFull(f, b)
	return bytes.Equal(b[:n], []byte("#!"))
}
