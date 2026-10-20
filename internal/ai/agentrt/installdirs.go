package agentrt

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// The folders where agent CLIs are installed outside PATH (R111, docs/ai.md
// §22.7): the usual install folders (Homebrew, ~/.local/bin, npm's global
// prefixes) and the bin folders of the Node.js version managers, where npm
// links the launchers of the packages it installs globally. A GUI app on
// macOS gets the login shell's PATH, which lacks what zsh sets up only in
// ~/.zshrc (nvm, fnm, …); on Windows a version manager's folder may not be on
// the PATH golauncher inherited.

// BinDir is a folder searched for an agent CLI after PATH.
type BinDir struct {
	Dir string
	// Manager is the Node.js version manager the folder belongs to ("nvm",
	// "fnm", "volta", "asdf", "mise", "nodenv", "n"); "" for a usual install
	// folder (Homebrew, ~/.local/bin, an npm prefix).
	Manager string
	// Default: the folder of the manager's default Node.js version (nvm's
	// alias/default, fnm's aliases/default, nvm-windows' NVM_SYMLINK).
	Default bool
}

// NodeEnv is what NodeDirs looks at; tests make one up.
type NodeEnv struct {
	// GOOS is the operating system ("windows", "darwin", "linux").
	GOOS string
	// Home is the user's home folder ("" when unknown).
	Home string
	// Getenv reads a variable of the environment (nil: none is set).
	Getenv func(string) string
	// Root is prepended to the fixed system folders (/opt/homebrew/bin,
	// /usr/local/…); "" on a real system.
	Root string
}

// HostNodeEnv is this computer's NodeEnv.
func HostNodeEnv() NodeEnv {
	home, _ := os.UserHomeDir()
	return NodeEnv{GOOS: runtime.GOOS, Home: home, Getenv: os.Getenv}
}

// InstallDirs are the folders of this computer searched after PATH
// (HostNodeEnv().Dirs()).
func InstallDirs() []BinDir { return HostNodeEnv().Dirs() }

func (e NodeEnv) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return strings.TrimSpace(e.Getenv(k))
}

func (e NodeEnv) home(elem ...string) string {
	if e.Home == "" {
		return ""
	}
	return filepath.Join(append([]string{e.Home}, elem...)...)
}

func (e NodeEnv) sys(p string) string {
	if e.Root == "" {
		return filepath.FromSlash(p)
	}
	return filepath.Join(e.Root, filepath.FromSlash(p))
}

// Dirs lists the folders, existing ones only, without duplicates, in this
// order (docs/ai.md §22.7):
//
// macOS and Linux: Homebrew (/opt/homebrew/bin, /usr/local/bin),
// ~/.local/bin, npm's prefix (~/.npmrc prefix=,
// $npm_config_prefix; its bin folder) and ~/.npm-global/bin; then the
// version managers: nvm ($NVM_DIR or ~/.nvm, versions/node/*/bin), fnm
// ($FNM_DIR, $XDG_DATA_HOME/fnm, ~/.local/share/fnm, ~/Library/Application
// Support/fnm, ~/.fnm; node-versions/*/installation/bin), Volta
// ($VOLTA_HOME/bin or ~/.volta/bin), asdf ($ASDF_DATA_DIR or ~/.asdf,
// installs/nodejs/*/bin), mise ($MISE_DATA_DIR, $XDG_DATA_HOME/mise or
// ~/.local/share/mise; installs/node/*/bin, then shims), nodenv
// ($NODENV_ROOT or ~/.nodenv, versions/*/bin), n ($N_PREFIX/bin;
// $N_PREFIX or /usr/local, n/versions/node/*/bin).
//
// Windows: %APPDATA%\npm, npm's prefix (.npmrc, %npm_config_prefix%); nvm-windows
// (%NVM_SYMLINK%, %NVM_HOME%\v*), fnm (%FNM_DIR%, %APPDATA%\fnm,
// %LOCALAPPDATA%\fnm; node-versions\*\installation), Volta
// (%LOCALAPPDATA%\Volta\bin), mise (%MISE_DATA_DIR% or %LOCALAPPDATA%\mise;
// installs\node\*, then shims).
//
// Within a manager the default version comes first, then the others from the
// highest version down. Only fixed patterns are read (one ReadDir each),
// never a tree.
func (e NodeEnv) Dirs() []BinDir {
	var out []BinDir
	seen := map[string]bool{}
	add := func(d BinDir) {
		if d.Dir == "" {
			return
		}
		d.Dir = filepath.Clean(d.Dir)
		k := d.Dir
		if e.GOOS == "windows" {
			k = strings.ToLower(k)
		}
		if seen[k] || !isDir(d.Dir) {
			return
		}
		seen[k] = true
		out = append(out, d)
	}
	addAll := func(ds []BinDir) {
		for _, d := range ds {
			add(d)
		}
	}
	if e.GOOS == "windows" {
		if a := e.getenv("APPDATA"); a != "" {
			add(BinDir{Dir: filepath.Join(a, "npm")})
		}
		for _, p := range e.npmPrefixes() {
			add(BinDir{Dir: p})
		}
		addAll(e.nvmWindows())
		var fnmRoots []string
		if d := e.getenv("FNM_DIR"); d != "" {
			fnmRoots = append(fnmRoots, d)
		}
		if a := e.getenv("APPDATA"); a != "" {
			fnmRoots = append(fnmRoots, filepath.Join(a, "fnm"))
		}
		local := e.getenv("LOCALAPPDATA")
		if local != "" {
			fnmRoots = append(fnmRoots, filepath.Join(local, "fnm"))
		}
		addAll(e.fnm(fnmRoots, "installation"))
		if local != "" {
			add(BinDir{Dir: filepath.Join(local, "Volta", "bin"), Manager: "volta"})
		}
		mise := e.getenv("MISE_DATA_DIR")
		if mise == "" && local != "" {
			mise = filepath.Join(local, "mise")
		}
		if mise != "" {
			addAll(versionDirs(filepath.Join(mise, "installs", "node"), "", "mise", ""))
			add(BinDir{Dir: filepath.Join(mise, "shims"), Manager: "mise"})
		}
		return out
	}

	add(BinDir{Dir: e.sys("/opt/homebrew/bin")})
	add(BinDir{Dir: e.sys("/usr/local/bin")})
	add(BinDir{Dir: e.home(".local", "bin")})
	for _, p := range e.npmPrefixes() {
		add(BinDir{Dir: filepath.Join(p, "bin")})
	}
	add(BinDir{Dir: e.home(".npm-global", "bin")})

	nvm := e.getenv("NVM_DIR")
	if nvm == "" {
		nvm = e.home(".nvm")
	}
	if nvm != "" {
		root := filepath.Join(nvm, "versions", "node")
		addAll(versionDirs(root, "bin", "nvm", nvmDefault(nvm, versionNames(root))))
	}

	var fnmRoots []string
	if d := e.getenv("FNM_DIR"); d != "" {
		fnmRoots = append(fnmRoots, d)
	}
	if x := e.getenv("XDG_DATA_HOME"); x != "" {
		fnmRoots = append(fnmRoots, filepath.Join(x, "fnm"))
	}
	if e.Home != "" {
		fnmRoots = append(fnmRoots, e.home(".local", "share", "fnm"), e.home("Library", "Application Support", "fnm"), e.home(".fnm"))
	}
	addAll(e.fnm(fnmRoots, filepath.Join("installation", "bin")))

	volta := e.getenv("VOLTA_HOME")
	if volta == "" {
		volta = e.home(".volta")
	}
	if volta != "" {
		add(BinDir{Dir: filepath.Join(volta, "bin"), Manager: "volta"})
	}

	// asdf: its shims run `asdf exec`, which needs asdf itself on PATH (0.16
	// and later), so the installations' own bin folders are searched instead.
	asdf := e.getenv("ASDF_DATA_DIR")
	if asdf == "" {
		asdf = e.home(".asdf")
	}
	if asdf != "" {
		addAll(versionDirs(filepath.Join(asdf, "installs", "nodejs"), "bin", "asdf", ""))
	}

	mise := e.getenv("MISE_DATA_DIR")
	if mise == "" {
		if x := e.getenv("XDG_DATA_HOME"); x != "" {
			mise = filepath.Join(x, "mise")
		} else {
			mise = e.home(".local", "share", "mise")
		}
	}
	if mise != "" {
		addAll(versionDirs(filepath.Join(mise, "installs", "node"), "bin", "mise", ""))
		// mise's shims are links to mise itself (an absolute path): they run
		// without mise on PATH.
		add(BinDir{Dir: filepath.Join(mise, "shims"), Manager: "mise"})
	}

	nodenv := e.getenv("NODENV_ROOT")
	if nodenv == "" {
		nodenv = e.home(".nodenv")
	}
	if nodenv != "" {
		addAll(versionDirs(filepath.Join(nodenv, "versions"), "bin", "nodenv", ""))
	}

	nPrefix := e.getenv("N_PREFIX")
	if nPrefix != "" {
		add(BinDir{Dir: filepath.Join(nPrefix, "bin"), Manager: "n"})
	} else {
		nPrefix = e.sys("/usr/local")
	}
	addAll(versionDirs(filepath.Join(nPrefix, "n", "versions", "node"), "bin", "n", ""))
	return out
}

// nvmWindows are nvm-windows' folders: the active version (%NVM_SYMLINK%, a
// link to one of %NVM_HOME%\v*, which is then left out), then the
// installed versions.
func (e NodeEnv) nvmWindows() []BinDir {
	var out []BinDir
	active := ""
	if s := e.getenv("NVM_SYMLINK"); s != "" {
		out = append(out, BinDir{Dir: s, Manager: "nvm", Default: true})
		if r, err := filepath.EvalSymlinks(s); err == nil {
			active = r
		}
	}
	if h := e.getenv("NVM_HOME"); h != "" {
		for _, d := range versionDirs(h, "", "nvm", "") {
			if !strings.HasPrefix(strings.ToLower(filepath.Base(d.Dir)), "v") {
				continue
			}
			if active != "" {
				if r, err := filepath.EvalSymlinks(d.Dir); err == nil && strings.EqualFold(r, active) {
					continue
				}
			}
			out = append(out, d)
		}
	}
	return out
}

// fnm lists node-versions/*/<sub> of each fnm folder, the version its
// aliases/default links to first.
func (e NodeEnv) fnm(roots []string, sub string) []BinDir {
	var out []BinDir
	for _, r := range roots {
		vers := filepath.Join(r, "node-versions")
		def := ""
		if t, err := filepath.EvalSymlinks(filepath.Join(r, "aliases", "default")); err == nil {
			// The link points at node-versions/<v>/installation.
			def = filepath.Base(filepath.Dir(t))
			if filepath.Base(t) != "installation" {
				def = filepath.Base(t)
			}
		}
		out = append(out, versionDirs(vers, sub, "fnm", def)...)
	}
	return out
}

// npmPrefixes are npm's global prefixes the user configured: $npm_config_prefix
// and the prefix= line of ~/.npmrc (with ~ and ${VAR} expanded).
func (e NodeEnv) npmPrefixes() []string {
	var out []string
	if p := e.getenv("npm_config_prefix"); p != "" {
		out = append(out, e.expand(p))
	} else if p := e.getenv("NPM_CONFIG_PREFIX"); p != "" {
		out = append(out, e.expand(p))
	}
	if e.Home == "" {
		return out
	}
	b, err := os.ReadFile(e.home(".npmrc"))
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "prefix" {
			continue
		}
		v = strings.TrimSpace(v)
		if uq, err := strconv.Unquote(v); err == nil {
			v = uq
		} else {
			v = strings.Trim(v, `'"`)
		}
		if v != "" {
			out = append(out, e.expand(v))
		}
	}
	return out
}

// expand replaces a leading ~ with the home folder and ${VAR} with the
// variable (HOME, USERPROFILE: the home folder when not set), as npm does.
func (e NodeEnv) expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		p = e.Home + p[1:]
	}
	for {
		i := strings.Index(p, "${")
		if i < 0 {
			break
		}
		j := strings.Index(p[i:], "}")
		if j < 0 {
			break
		}
		name := p[i+2 : i+j]
		v := e.getenv(name)
		if v == "" && (name == "HOME" || name == "USERPROFILE") {
			v = e.Home
		}
		p = p[:i] + v + p[i+j+1:]
	}
	return filepath.Clean(p)
}

// versionNames are the names of the folders in root.
func versionNames(root string) []string {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, en := range ents {
		if en.IsDir() || en.Type()&os.ModeSymlink != 0 {
			out = append(out, en.Name())
		}
	}
	return out
}

// versionDirs lists root/<version>/sub for the version folders in root: def
// first (Default), then the others from the highest version down.
func versionDirs(root, sub, manager, def string) []BinDir {
	names := versionNames(root)
	slices.SortStableFunc(names, func(a, b string) int { return -compareNodeVersions(a, b) })
	var out []BinDir
	if def != "" && slices.Contains(names, def) {
		out = append(out, BinDir{Dir: filepath.Join(root, def, sub), Manager: manager, Default: true})
	}
	for _, n := range names {
		if n != def {
			out = append(out, BinDir{Dir: filepath.Join(root, n, sub), Manager: manager})
		}
	}
	return out
}

// nodeVersion parses a version folder's name ("v20.10.0", "20.10.0",
// "node-v20.1.0"); ok is false for anything else.
func nodeVersion(name string) (v [3]int, ok bool) {
	s := strings.TrimPrefix(strings.ToLower(name), "node-")
	s = strings.TrimPrefix(s, "v")
	parts := strings.SplitN(s, ".", 3)
	if len(parts) == 0 || parts[0] == "" {
		return v, false
	}
	for i, p := range parts {
		if j := strings.IndexFunc(p, func(r rune) bool { return r < '0' || r > '9' }); j >= 0 {
			p = p[:j]
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// compareNodeVersions compares version folder names; names that are not
// versions sort below versions, by name.
func compareNodeVersions(a, b string) int {
	va, oka := nodeVersion(a)
	vb, okb := nodeVersion(b)
	switch {
	case oka && !okb:
		return 1
	case !oka && okb:
		return -1
	case !oka && !okb:
		return -strings.Compare(a, b)
	}
	for i := range va {
		if va[i] != vb[i] {
			if va[i] < vb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// nvmDefault is the installed version (a folder name of versions) that nvm's
// default alias names, "" when there is none or it cannot be resolved. It
// follows aliases as nvm does, by best effort: alias/default, then other
// alias files it names (alias/lts/*, alias/lts/iron, a user alias), up to a
// few levels; "node" and "stable" are the highest installed version; a
// version or its prefix ("20", "v20.1", "v20.1.0") the highest installed
// version that matches. "system", "iojs" and an LTS name without its alias
// file resolve to nothing.
func nvmDefault(nvmDir string, versions []string) string {
	b, err := os.ReadFile(filepath.Join(nvmDir, "alias", "default"))
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(b))
	for range 8 {
		if name == "" || strings.Contains(name, "..") {
			return ""
		}
		next, err := os.ReadFile(filepath.Join(nvmDir, "alias", filepath.FromSlash(name)))
		if err != nil {
			break
		}
		name = strings.TrimSpace(string(next))
	}
	best := ""
	consider := func(v string) {
		if best == "" || compareNodeVersions(v, best) > 0 {
			best = v
		}
	}
	switch name {
	case "node", "stable":
		for _, v := range versions {
			if _, ok := nodeVersion(v); ok {
				consider(v)
			}
		}
		return best
	case "system", "iojs":
		return ""
	}
	want := strings.TrimPrefix(name, "v")
	if _, ok := nodeVersion(want); !ok {
		return ""
	}
	for _, v := range versions {
		s := strings.TrimPrefix(v, "v")
		if s == want || strings.HasPrefix(s, want+".") {
			consider(v)
		}
	}
	return best
}

// isDir reports whether p is an existing folder.
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
