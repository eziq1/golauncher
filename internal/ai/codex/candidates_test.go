package codex

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// touch creates the files (and their directories) under root.
func touch(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// rels returns paths relative to root, with forward slashes.
func rels(t *testing.T, root string, paths []string) []string {
	t.Helper()
	out := make([]string, len(paths))
	for i, p := range paths {
		r, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = filepath.ToSlash(r)
	}
	return out
}

// TestCandidates: the Windows lookup (codex.exe, the desktop app under
// %LOCALAPPDATA%, npm under %APPDATA%); it runs on every system, with a
// made-up Windows environment.
func TestCandidates(t *testing.T) {
	const (
		x64Old   = "node_modules/@openai/codex/vendor/x86_64-pc-windows-msvc/codex/codex.exe"
		armOld   = "node_modules/@openai/codex/vendor/aarch64-pc-windows-msvc/codex/codex.exe"
		x64Nest  = "node_modules/@openai/codex/node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/codex/codex.exe"
		x64Hoist = "node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/codex/codex.exe"
		armHoist = "node_modules/@openai/codex-win32-arm64/vendor/aarch64-pc-windows-msvc/codex/codex.exe"
		// The layout of 0.150 and later.
		x64NestBin  = "node_modules/@openai/codex/node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/bin/codex.exe"
		x64HoistBin = "node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/bin/codex.exe"
	)
	for _, tt := range []struct {
		name  string
		files []string
		path  []string // PATH directories under root
		arch  string
		want  []string
		shim  bool // only the npm launcher: an error
	}{
		{name: "nothing", path: []string{"bin"}, arch: "amd64"},
		{
			name:  "PATH first, then the desktop app, then npm",
			files: []string{"bin/codex.exe", "local/OpenAI/Codex/bin/codex.exe", "local/OpenAI/Codex/bin/bbb/codex.exe", "local/OpenAI/Codex/bin/aaa/codex.exe", "node/codex.cmd", "node/" + x64Old},
			path:  []string{"bin", "node"}, arch: "amd64",
			want: []string{"bin/codex.exe", "local/OpenAI/Codex/bin/codex.exe", "local/OpenAI/Codex/bin/aaa/codex.exe", "local/OpenAI/Codex/bin/bbb/codex.exe", "node/" + x64Old},
		},
		{
			name:  "deduplicated",
			files: []string{"bin/codex.exe"},
			path:  []string{"bin", "bin", `"bin"`}, arch: "amd64",
			want: []string{"bin/codex.exe"},
		},
		{
			name:  "npm layouts on x64",
			files: []string{"node/codex.ps1", "node/" + x64Nest, "node/" + x64Hoist, "node/" + armOld, "node/" + armHoist},
			path:  []string{"node"}, arch: "amd64",
			want: []string{"node/" + x64Nest, "node/" + x64Hoist},
		},
		{
			name:  "arm64 prefers the ARM binary",
			files: []string{"node/codex", "node/" + x64Old, "node/" + armOld, "node/" + armHoist},
			path:  []string{"node"}, arch: "arm64",
			want: []string{"node/" + armOld, "node/" + armHoist, "node/" + x64Old},
		},
		{
			name:  "npm 0.150 and later: vendor/<triple>/bin",
			files: []string{"node/codex.cmd", "node/" + x64NestBin, "node/" + x64HoistBin, "node/" + x64Hoist},
			path:  []string{"node"}, arch: "amd64",
			want: []string{"node/" + x64NestBin, "node/" + x64HoistBin, "node/" + x64Hoist},
		},
		{
			name:  "%APPDATA%\\npm",
			files: []string{"roaming/npm/codex.cmd", "roaming/npm/" + x64Hoist},
			arch:  "amd64",
			want:  []string{"roaming/npm/" + x64Hoist},
		},
		{
			name:  "no launcher, no npm binary",
			files: []string{"node/" + x64Old},
			path:  []string{"node"}, arch: "amd64",
		},
		{
			name:  "only the launcher",
			files: []string{"node/codex.cmd", "node/codex.ps1", "node/codex", "node/node_modules/@openai/codex/bin/codex.js"},
			path:  []string{"node"}, arch: "amd64", shim: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			touch(t, root, tt.files...)
			e := findEnv{OS: "windows", LocalAppData: filepath.Join(root, "local"), AppData: filepath.Join(root, "roaming"), Arch: tt.arch}
			for _, d := range tt.path {
				q := strings.HasPrefix(d, `"`)
				d = filepath.Join(root, strings.Trim(d, `"`))
				if q {
					d = `"` + d + `"`
				}
				e.Path = append(e.Path, d)
			}
			got, err := candidates(e)
			if tt.shim {
				if e, ok := err.(*backend.Error); !ok || e.Kind != backend.ErrNotInstalled || !strings.Contains(e.Message, "codex.cmd") || len(got) != 0 {
					t.Fatalf("candidates = %q, %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if g := rels(t, root, got); !slices.Equal(g, tt.want) {
				t.Errorf("candidates =\n %q\nwant\n %q", g, tt.want)
			}
			for _, p := range got {
				if !strings.EqualFold(filepath.Ext(p), ".exe") {
					t.Errorf("%s is not an executable", p)
				}
			}
		})
	}
}

func TestNativePath(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "node/codex.cmd", "node/node_modules/@openai/codex/vendor/x86_64-pc-windows-msvc/codex/codex.exe", "shim-only/codex.cmd")
	e := findEnv{OS: "windows", Arch: "amd64"}
	for in, want := range map[string]string{
		"node/codex.cmd":      "node/node_modules/@openai/codex/vendor/x86_64-pc-windows-msvc/codex/codex.exe",
		"node/codex.ps1":      "node/node_modules/@openai/codex/vendor/x86_64-pc-windows-msvc/codex/codex.exe",
		"node/codex":          "node/node_modules/@openai/codex/vendor/x86_64-pc-windows-msvc/codex/codex.exe",
		"shim-only/codex.cmd": "shim-only/codex.exe",
		"bin/codex.exe":       "bin/codex.exe",
		"bin/my-codex.exe":    "bin/my-codex.exe",
	} {
		got := nativePath(filepath.Join(root, in), e)
		if r := rels(t, root, []string{got})[0]; r != want {
			t.Errorf("nativePath(%s) = %s, want %s", in, r, want)
		}
	}
}

// The contents of the made-up files of the macOS tests: a native binary
// (the start of a Mach-O file) and the npm launcher (bin/codex.js).
const (
	machO    = "\xcf\xfa\xed\xfe"
	launcher = "#!/usr/bin/env node\n"
)

// put creates the file rel under root with content, executable.
func put(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// link creates the symbolic link rel under root, pointing at target.
func link(t *testing.T, root, rel, target string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

// realTempDir is t.TempDir() without links (/var is /private/var on macOS):
// the npm binaries are found through the launcher's link.
func realTempDir(t *testing.T) string {
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestCandidatesDarwin: the macOS lookup: a native codex on PATH and in the
// usual folders, the Codex app's, and the native binary inside an npm
// installation whose launcher (a link to bin/codex.js) is on PATH, in the
// layouts of @openai/codex (verified with the packages of 0.98, 0.130 and
// 0.163). It runs on every system but Windows (symbolic links, exec bits).
func TestCandidatesDarwin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not on Windows")
	}
	const (
		pkg      = "npm/lib/node_modules/@openai/codex"
		armNest  = pkg + "/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex"
		armHoist = "npm/lib/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex"
		x64Hoist = "npm/lib/node_modules/@openai/codex-darwin-x64/vendor/x86_64-apple-darwin/bin/codex"
		armOwn   = pkg + "/vendor/aarch64-apple-darwin/bin/codex"
		armOld   = pkg + "/vendor/aarch64-apple-darwin/codex/codex"
		x64Old   = pkg + "/vendor/x86_64-apple-darwin/codex/codex"
		app      = "Applications/Codex.app/Contents/Resources/codex"
	)
	npmLauncher := func(t *testing.T, root string) {
		put(t, root, pkg+"/bin/codex.js", launcher)
		link(t, root, "npm/bin/codex", "../lib/node_modules/@openai/codex/bin/codex.js")
	}
	for _, tt := range []struct {
		name     string
		natives  []string // native binaries
		links    [][2]string
		launcher bool // the npm launcher in npm/bin, a link to codex.js
		copied   bool // the npm launcher in npm/bin, a copy of codex.js
		path     []string
		dirs     []string
		arch     string
		want     []string
		shim     bool // only the npm launcher: an error
	}{
		{name: "nothing", path: []string{"bin"}, dirs: []string{"brew"}, arch: "arm64"},
		{
			name:    "PATH first, then the usual folders, the app and npm",
			natives: []string{"bin/codex", "brew/codex", app, armNest}, launcher: true,
			path: []string{"bin", "npm/bin"}, dirs: []string{"brew"}, arch: "arm64",
			want: []string{"bin/codex", "brew/codex", app, armNest},
		},
		{
			name:    "deduplicated through links (Homebrew's cask)",
			natives: []string{"Caskroom/codex/0.163.0/codex-aarch64-apple-darwin"},
			links:   [][2]string{{"brew/codex", "../Caskroom/codex/0.163.0/codex-aarch64-apple-darwin"}},
			path:    []string{"brew", "brew"}, dirs: []string{"brew"}, arch: "arm64",
			want: []string{"brew/codex"},
		},
		{
			name:    "npm layouts on Apple silicon, the ARM binary first",
			natives: []string{armHoist, armOwn, armOld, x64Old}, launcher: true,
			path: []string{"npm/bin"}, arch: "arm64",
			want: []string{armHoist, armOwn, armOld, x64Old},
		},
		{
			name:    "npm on Intel",
			natives: []string{armNest, armHoist, x64Hoist}, launcher: true,
			dirs: []string{"npm/bin"}, arch: "amd64",
			want: []string{x64Hoist},
		},
		{
			name:    "a launcher that is a copy, in npm's prefix layout",
			natives: []string{armOld}, copied: true,
			path: []string{"npm/bin"}, arch: "arm64",
			want: []string{armOld},
		},
		{
			name:    "no launcher, no npm binary",
			natives: []string{armOld},
			path:    []string{"bin"}, arch: "arm64",
		},
		{
			name: "only the launcher", launcher: true,
			path: []string{"npm/bin"}, arch: "arm64", shim: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := realTempDir(t)
			for _, n := range tt.natives {
				put(t, root, n, machO)
			}
			for _, l := range tt.links {
				link(t, root, l[0], l[1])
			}
			if tt.launcher {
				npmLauncher(t, root)
			}
			if tt.copied {
				put(t, root, "npm/bin/codex", launcher)
			}
			abs := func(ds []string) []string {
				out := make([]string, len(ds))
				for i, d := range ds {
					out[i] = filepath.Join(root, d)
				}
				return out
			}
			e := findEnv{OS: "darwin", Arch: tt.arch, Path: abs(tt.path), Dirs: abs(tt.dirs),
				Apps: abs([]string{app, "Applications/Codex.app/Contents/Resources/bin/codex"})}
			got, err := candidates(e)
			if tt.shim {
				if e, ok := err.(*backend.Error); !ok || e.Kind != backend.ErrNotInstalled || !strings.Contains(e.Message, filepath.Join(root, "npm/bin/codex")) ||
					strings.Contains(e.Message, ".exe") || len(got) != 0 {
					t.Fatalf("candidates = %q, %v", got, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if g := rels(t, root, got); !slices.Equal(g, tt.want) {
				t.Errorf("candidates =\n %q\nwant\n %q", g, tt.want)
			}
		})
	}

	// A codex that may not be run is not one.
	root := t.TempDir()
	touch(t, root, "bin/codex")
	if got, err := candidates(findEnv{OS: "darwin", Arch: "arm64", Path: []string{filepath.Join(root, "bin")}}); len(got) != 0 || err != nil {
		t.Errorf("not executable: %q, %v", got, err)
	}
}

// TestNativePathDarwin: a configured npm launcher (the link on PATH, or
// codex.js) is replaced by its native binary; a native codex is kept.
func TestNativePathDarwin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not on Windows")
	}
	const native = "npm/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex"
	root := realTempDir(t)
	put(t, root, "npm/lib/node_modules/@openai/codex/bin/codex.js", launcher)
	link(t, root, "npm/bin/codex", "../lib/node_modules/@openai/codex/bin/codex.js")
	put(t, root, native, machO)
	put(t, root, "brew/codex", machO)
	put(t, root, "lone/bin/codex", launcher)
	e := findEnv{OS: "darwin", Arch: "arm64"}
	for in, want := range map[string]string{
		"npm/bin/codex": native,
		"npm/lib/node_modules/@openai/codex/bin/codex.js": native,
		"brew/codex":     "brew/codex",
		"bin/my-codex":   "bin/my-codex",
		"lone/bin/codex": "lone/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/bin/codex",
	} {
		got := nativePath(filepath.Join(root, in), e)
		if r := rels(t, root, []string{got})[0]; r != want {
			t.Errorf("nativePath(%s) = %s, want %s", in, r, want)
		}
	}
}

// TestUnixDirs: the folders searched after PATH, and the app's CLIs.
func TestUnixDirs(t *testing.T) {
	home := t.TempDir()
	touch(t, home, ".nvm/versions/node/v22.1.0/bin/node")
	dirs, apps := unixDirs(home)
	want := []string{"/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".local", "bin"), filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".nvm", "versions", "node", "v22.1.0", "bin")}
	if !slices.Equal(dirs, want) {
		t.Errorf("dirs %q", dirs)
	}
	if len(apps) != 4 || apps[0] != filepath.Join("/Applications", "Codex.app", "Contents", "Resources", "codex") {
		t.Errorf("apps %q", apps)
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]string{
		"codex-cli 0.162.0-alpha.17.2": "0.162.0",
		"codex-cli 0.130.0-alpha.5\n":  "0.130.0",
		"codex-cli 0.46.0":             "0.46.0",
		"v1.2.3":                       "1.2.3",
		"codex-cli 0.1":                "",
		"nonsense":                     "",
	} {
		if got := parseVersion(in); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
