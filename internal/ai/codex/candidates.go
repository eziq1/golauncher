package codex

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// findEnv is what Candidates looks at: the directories of PATH, the
// %LOCALAPPDATA% and %APPDATA% folders and the processor architecture
// (GOARCH). Tests make one up.
type findEnv struct {
	Path                  []string
	LocalAppData, AppData string
	Arch                  string
}

// candidateEnv returns the environment Candidates looks at (a variable, so
// that tests can make one up).
var candidateEnv = func() findEnv {
	return findEnv{
		Path:         filepath.SplitList(os.Getenv("PATH")),
		LocalAppData: os.Getenv("LOCALAPPDATA"),
		AppData:      os.Getenv("APPDATA"),
		Arch:         runtime.GOARCH,
	}
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
//     directory of PATH with codex.cmd, codex.ps1 or codex, and
//     %APPDATA%\npm), in every layout the launcher (bin/codex.js) looks at.
//
// The npm launcher itself is never returned: it needs Node.js. When it is
// all there is, the error says so (ErrNotInstalled); when nothing is found,
// both are nil.
func candidates(e findEnv) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
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
		out = append(out, p)
	}
	var dirs []string // PATH, cleaned
	for _, d := range e.Path {
		d = strings.Trim(strings.TrimSpace(d), `"`)
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	for _, d := range dirs {
		add(filepath.Join(d, exeName))
	}
	if e.LocalAppData != "" {
		bin := filepath.Join(e.LocalAppData, "OpenAI", "Codex", "bin")
		add(filepath.Join(bin, exeName))
		matches, _ := filepath.Glob(filepath.Join(bin, "*", exeName))
		slices.Sort(matches)
		for _, m := range matches {
			add(m)
		}
	}
	shimDirs := slices.Clone(dirs)
	if e.AppData != "" {
		shimDirs = append(shimDirs, filepath.Join(e.AppData, "npm"))
	}
	shim := ""
	for _, d := range shimDirs {
		s := shimIn(d)
		if s == "" {
			continue
		}
		if shim == "" {
			shim = s
		}
		for _, p := range vendored(d, e) {
			add(p)
		}
	}
	if len(out) == 0 && shim != "" {
		return nil, &backend.Error{Kind: backend.ErrNotInstalled, Message: "Only the Node.js launcher of Codex was found (" + shim +
			"). golauncher runs the native codex.exe: install or update the Codex app, or put codex.exe from a Codex CLI release on PATH."}
	}
	return out, nil
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
// installed under it, or hoisted next to it.
func vendored(dir string, e findEnv) []string {
	nm := filepath.Join(dir, "node_modules", "@openai")
	var out []string
	for _, t := range e.triples() {
		rel := filepath.Join("vendor", t, "codex", exeName)
		pkg := platformPackage(t)
		out = append(out,
			filepath.Join(nm, "codex", rel),
			filepath.Join(nm, "codex", "node_modules", "@openai", pkg, rel),
			filepath.Join(nm, pkg, rel),
		)
	}
	return out
}

// nativePath returns the CLI to run for a configured path: the path itself,
// unless it is the npm launcher, which is replaced by the native binary
// next to it (or by a codex.exe in its directory that does not exist, which
// the runtime reports as not installed).
func nativePath(path string, e findEnv) string {
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
