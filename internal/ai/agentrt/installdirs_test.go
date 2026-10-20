package agentrt

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// The folders searched after PATH (R111): made-up layouts of the Node.js
// version managers under a temporary home and root, with an injected GOOS,
// so that both systems' rules run everywhere.

// mkdirs creates the folders under root.
func mkdirs(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// writeFile writes content to rel under root.
func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dirStrings describes BinDirs relative to root: "rel", "rel:manager" or
// "rel:manager:default".
func dirStrings(t *testing.T, root string, dirs []BinDir) []string {
	t.Helper()
	out := make([]string, len(dirs))
	for i, d := range dirs {
		r, err := filepath.Rel(root, d.Dir)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = filepath.ToSlash(r)
		if d.Manager != "" {
			out[i] += ":" + d.Manager
		}
		if d.Default {
			out[i] += ":default"
		}
	}
	return out
}

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// TestDirsUnix: every manager's layout, the nvm default first and the other
// versions from the highest down, the .npmrc prefix, only folders that
// exist, asdf's shims left out.
func TestDirsUnix(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "Users", "someone")
	mkdirs(t, root,
		"opt/homebrew/bin", "usr/local/bin",
		"Users/someone/.local/bin",
		"Users/someone/.npm-packages/bin", "Users/someone/.npm-global/bin",
		"Users/someone/.nvm/versions/node/v18.20.0/bin",
		"Users/someone/.nvm/versions/node/v20.9.0/bin",
		"Users/someone/.nvm/versions/node/v20.20.0/bin",
		"Users/someone/.nvm/versions/node/v22.1.0/bin",
		"Users/someone/.local/share/fnm/node-versions/v21.0.0/installation/bin",
		"Users/someone/Library/Application Support/fnm/node-versions/v19.0.0/installation/bin",
		"Users/someone/.volta/bin",
		"Users/someone/.asdf/installs/nodejs/20.1.0/bin", "Users/someone/.asdf/shims",
		"Users/someone/.local/share/mise/installs/node/22.0.0/bin", "Users/someone/.local/share/mise/shims",
		"Users/someone/.nodenv/versions/18.0.0/bin",
		"usr/local/n/versions/node/16.0.0/bin",
	)
	writeFile(t, root, "Users/someone/.nvm/alias/default", "20\n")
	writeFile(t, root, "Users/someone/.npmrc", "; a comment\ncolor=false\nprefix = ${HOME}/.npm-packages\n")
	e := NodeEnv{GOOS: "darwin", Home: home, Root: root, Getenv: envMap(map[string]string{"HOME": home})}
	want := []string{
		"opt/homebrew/bin", "usr/local/bin", "Users/someone/.local/bin",
		"Users/someone/.npm-packages/bin", "Users/someone/.npm-global/bin",
		"Users/someone/.nvm/versions/node/v20.20.0/bin:nvm:default",
		"Users/someone/.nvm/versions/node/v22.1.0/bin:nvm",
		"Users/someone/.nvm/versions/node/v20.9.0/bin:nvm",
		"Users/someone/.nvm/versions/node/v18.20.0/bin:nvm",
		"Users/someone/.local/share/fnm/node-versions/v21.0.0/installation/bin:fnm",
		"Users/someone/Library/Application Support/fnm/node-versions/v19.0.0/installation/bin:fnm",
		"Users/someone/.volta/bin:volta",
		"Users/someone/.asdf/installs/nodejs/20.1.0/bin:asdf",
		"Users/someone/.local/share/mise/installs/node/22.0.0/bin:mise",
		"Users/someone/.local/share/mise/shims:mise",
		"Users/someone/.nodenv/versions/18.0.0/bin:nodenv",
		"usr/local/n/versions/node/16.0.0/bin:n",
	}
	if got := dirStrings(t, root, e.Dirs()); !slices.Equal(got, want) {
		t.Errorf("dirs\n %q\nwant\n %q", got, want)
	}

	// The variables move the managers' folders.
	alt := t.TempDir()
	mkdirs(t, alt, "nvm/versions/node/v24.0.0/bin", "fnm/node-versions/v23.0.0/installation/bin", "volta/bin",
		"asdf/installs/nodejs/21.0.0/bin", "mise/installs/node/20.0.0/bin", "nodenv/versions/19.0.0/bin",
		"n/bin", "n/n/versions/node/18.0.0/bin", "prefix/bin")
	e = NodeEnv{GOOS: "linux", Home: filepath.Join(alt, "nohome"), Root: filepath.Join(alt, "noroot"), Getenv: envMap(map[string]string{
		"NVM_DIR": filepath.Join(alt, "nvm"), "FNM_DIR": filepath.Join(alt, "fnm"), "VOLTA_HOME": filepath.Join(alt, "volta"),
		"ASDF_DATA_DIR": filepath.Join(alt, "asdf"), "MISE_DATA_DIR": filepath.Join(alt, "mise"), "NODENV_ROOT": filepath.Join(alt, "nodenv"),
		"N_PREFIX": filepath.Join(alt, "n"), "npm_config_prefix": filepath.Join(alt, "prefix"),
	})}
	want = []string{"prefix/bin", "nvm/versions/node/v24.0.0/bin:nvm", "fnm/node-versions/v23.0.0/installation/bin:fnm",
		"volta/bin:volta", "asdf/installs/nodejs/21.0.0/bin:asdf", "mise/installs/node/20.0.0/bin:mise",
		"nodenv/versions/19.0.0/bin:nodenv", "n/bin:n", "n/n/versions/node/18.0.0/bin:n"}
	if got := dirStrings(t, alt, e.Dirs()); !slices.Equal(got, want) {
		t.Errorf("moved dirs\n %q\nwant\n %q", got, want)
	}

	// Nothing installed, no home: nothing.
	if got := (NodeEnv{GOOS: "darwin", Root: t.TempDir()}).Dirs(); len(got) != 0 {
		t.Errorf("empty: %+v", got)
	}
}

// TestDirsFnmDefault: fnm's aliases/default link marks its version.
func TestDirsFnmDefault(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "fnm/node-versions/v20.0.0/installation/bin", "fnm/node-versions/v22.0.0/installation/bin", "fnm/aliases")
	if err := os.Symlink(filepath.Join(root, "fnm", "node-versions", "v20.0.0", "installation"), filepath.Join(root, "fnm", "aliases", "default")); err != nil {
		t.Skip("no symbolic links here: ", err)
	}
	e := NodeEnv{GOOS: "linux", Getenv: envMap(map[string]string{"FNM_DIR": filepath.Join(root, "fnm")})}
	want := []string{"fnm/node-versions/v20.0.0/installation/bin:fnm:default", "fnm/node-versions/v22.0.0/installation/bin:fnm"}
	if got := dirStrings(t, root, e.Dirs()); !slices.Equal(got, want) {
		t.Errorf("dirs %q", got)
	}
}

// TestDirsWindows: nvm-windows (the active version first), fnm, Volta,
// mise, %APPDATA%\npm and the .npmrc prefix.
func TestDirsWindows(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "Users", "someone")
	roaming := filepath.Join(home, "AppData", "Roaming")
	local := filepath.Join(home, "AppData", "Local")
	mkdirs(t, root,
		"Users/someone/AppData/Roaming/npm",
		"Users/someone/npm-prefix",
		"nvm4w/nodejs",
		"Users/someone/AppData/Local/nvm/v18.20.0", "Users/someone/AppData/Local/nvm/v20.1.0", "Users/someone/AppData/Local/nvm/elevate",
		"Users/someone/AppData/Roaming/fnm/node-versions/v22.0.0/installation",
		"Users/someone/AppData/Local/fnm/node-versions/v21.0.0/installation",
		"Users/someone/AppData/Local/Volta/bin",
		"Users/someone/AppData/Local/mise/installs/node/20.0.0", "Users/someone/AppData/Local/mise/shims",
	)
	writeFile(t, root, "Users/someone/.npmrc", "prefix=~/npm-prefix\n")
	e := NodeEnv{GOOS: "windows", Home: home, Getenv: envMap(map[string]string{
		"APPDATA": roaming, "LOCALAPPDATA": local,
		"NVM_HOME": filepath.Join(local, "nvm"), "NVM_SYMLINK": filepath.Join(root, "nvm4w", "nodejs"),
	})}
	want := []string{
		"Users/someone/AppData/Roaming/npm",
		"Users/someone/npm-prefix",
		"nvm4w/nodejs:nvm:default",
		"Users/someone/AppData/Local/nvm/v20.1.0:nvm",
		"Users/someone/AppData/Local/nvm/v18.20.0:nvm",
		"Users/someone/AppData/Roaming/fnm/node-versions/v22.0.0/installation:fnm",
		"Users/someone/AppData/Local/fnm/node-versions/v21.0.0/installation:fnm",
		"Users/someone/AppData/Local/Volta/bin:volta",
		"Users/someone/AppData/Local/mise/installs/node/20.0.0:mise",
		"Users/someone/AppData/Local/mise/shims:mise",
	}
	if got := dirStrings(t, root, e.Dirs()); !slices.Equal(got, want) {
		t.Errorf("dirs\n %q\nwant\n %q", got, want)
	}
}

// TestDirsNvmWindowsLink: the version NVM_SYMLINK links to is listed once,
// as the active one.
func TestDirsNvmWindowsLink(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "nvm/v18.0.0", "nvm/v20.0.0")
	if err := os.Symlink(filepath.Join(root, "nvm", "v20.0.0"), filepath.Join(root, "nodejs")); err != nil {
		t.Skip("no symbolic links here: ", err)
	}
	e := NodeEnv{GOOS: "windows", Getenv: envMap(map[string]string{"NVM_HOME": filepath.Join(root, "nvm"), "NVM_SYMLINK": filepath.Join(root, "nodejs")})}
	if got := dirStrings(t, root, e.Dirs()); !slices.Equal(got, []string{"nodejs:nvm:default", "nvm/v18.0.0:nvm"}) {
		t.Errorf("dirs %q", got)
	}
}

// TestNvmDefault: the aliases nvm resolves, by best effort.
func TestNvmDefault(t *testing.T) {
	versions := []string{"v18.20.0", "v20.9.0", "v20.20.0", "v22.1.0", "v22.10.1"}
	for _, tt := range []struct {
		name    string
		aliases map[string]string // alias file → content
		want    string
	}{
		{"none", nil, ""},
		{"major", map[string]string{"default": "20"}, "v20.20.0"},
		{"exact", map[string]string{"default": "v20.9.0\n"}, "v20.9.0"},
		{"exact without v", map[string]string{"default": "22.1.0"}, "v22.1.0"},
		{"minor prefix", map[string]string{"default": "v22.1"}, "v22.1.0"},
		{"node", map[string]string{"default": "node"}, "v22.10.1"},
		{"stable", map[string]string{"default": "stable"}, "v22.10.1"},
		{"lts name", map[string]string{"default": "lts/iron", "lts/iron": "v20.20.0"}, "v20.20.0"},
		{"user alias", map[string]string{"default": "work", "work": "18"}, "v18.20.0"},
		{"not installed", map[string]string{"default": "16"}, ""},
		{"lts without its file", map[string]string{"default": "lts/jod"}, ""},
		{"system", map[string]string{"default": "system"}, ""},
		{"a loop", map[string]string{"default": "a", "a": "b", "b": "a"}, ""},
		{"outside", map[string]string{"default": "../../x"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.aliases {
				writeFile(t, dir, "alias/"+name, content)
			}
			if got := nvmDefault(dir, versions); got != tt.want {
				t.Errorf("nvmDefault = %q, want %q", got, tt.want)
			}
		})
	}
	if runtime.GOOS != "windows" { // "*" is not a file name there
		dir := t.TempDir()
		writeFile(t, dir, "alias/default", "lts/*")
		writeFile(t, dir, "alias/lts/*", "lts/iron")
		writeFile(t, dir, "alias/lts/iron", "v20.9.0")
		if got := nvmDefault(dir, versions); got != "v20.9.0" {
			t.Errorf("lts/*: %q", got)
		}
	}
}

// TestCompareNodeVersions: numerically, versions above other names.
func TestCompareNodeVersions(t *testing.T) {
	names := []string{"v9.0.0", "elevate", "v20.10.0", "v20.9.0", "node-v21.0.0", "22.0.0-rc.1"}
	slices.SortFunc(names, func(a, b string) int { return -compareNodeVersions(a, b) })
	if want := []string{"22.0.0-rc.1", "node-v21.0.0", "v20.10.0", "v20.9.0", "v9.0.0", "elevate"}; !slices.Equal(names, want) {
		t.Errorf("sorted %q", names)
	}
}

// TestWithDirOnPath: the CLI's folder goes first on PATH unless it is there.
func TestWithDirOnPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	dir := filepath.Join(t.TempDir(), "nvm", "bin")
	cli := filepath.Join(dir, "claude")
	if got := withDirOnPath([]string{"A=1", "PATH=/usr/bin"}, cli, runtime.GOOS); !slices.Equal(got, []string{"A=1", "PATH=" + dir + sep + "/usr/bin"}) {
		t.Errorf("prepended %q", got)
	}
	if got := withDirOnPath([]string{"A=1"}, cli, runtime.GOOS); !slices.Equal(got, []string{"A=1", "PATH=" + dir}) {
		t.Errorf("no PATH %q", got)
	}
	on := []string{"PATH=/usr/bin" + sep + dir + string(filepath.Separator)}
	if got := withDirOnPath(on, cli, runtime.GOOS); !slices.Equal(got, on) {
		t.Errorf("on PATH already %q", got)
	}
	if got := withDirOnPath(on, "claude", runtime.GOOS); !slices.Equal(got, on) {
		t.Errorf("relative %q", got)
	}
	if runtime.GOOS == "windows" {
		env := []string{`Path=C:\Windows;C:\NVM4W\NODEJS`}
		if got := withDirOnPath(env, `C:\nvm4w\nodejs\claude.cmd`, "windows"); !slices.Equal(got, env) {
			t.Errorf("case: %q", got)
		}
		if got := withDirOnPath(env, `C:\Users\someone\AppData\Roaming\npm\claude.cmd`, "windows"); !slices.Equal(got,
			[]string{`PATH=C:\Users\someone\AppData\Roaming\npm;C:\Windows;C:\NVM4W\NODEJS`}) {
			t.Errorf("Path: %q", got)
		}
	}
}
