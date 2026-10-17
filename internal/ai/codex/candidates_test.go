package codex

import (
	"os"
	"path/filepath"
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

func TestCandidates(t *testing.T) {
	const (
		x64Old   = "node_modules/@openai/codex/vendor/x86_64-pc-windows-msvc/codex/codex.exe"
		armOld   = "node_modules/@openai/codex/vendor/aarch64-pc-windows-msvc/codex/codex.exe"
		x64Nest  = "node_modules/@openai/codex/node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/codex/codex.exe"
		x64Hoist = "node_modules/@openai/codex-win32-x64/vendor/x86_64-pc-windows-msvc/codex/codex.exe"
		armHoist = "node_modules/@openai/codex-win32-arm64/vendor/aarch64-pc-windows-msvc/codex/codex.exe"
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
			e := findEnv{LocalAppData: filepath.Join(root, "local"), AppData: filepath.Join(root, "roaming"), Arch: tt.arch}
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
	e := findEnv{Arch: "amd64"}
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
