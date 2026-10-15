package cli

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

const fixtures = "../../testdata/fixtures"

// glctl runs the CLI and returns its stdout and exit code.
func glctl(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Main(t.Context(), args, &out, &errOut)
	if code != 0 && errOut.Len() > 0 {
		t.Logf("stderr: %s", errOut.String())
	}
	return out.String(), code
}

var elapsedRe = regexp.MustCompile(`"elapsedMs": \d+`)

// golden compares got with testdata/golden/name.json.
func golden(t *testing.T, name, got string) {
	t.Helper()
	got = elapsedRe.ReplaceAllString(got, `"elapsedMs": 0`)
	path := filepath.Join("testdata", "golden", name+".json")
	if *update {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/cli -update)", err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("%s differs from golden file %s; rerun with -update if the change is intended.\ngot:\n%s", name, path, got)
	}
}

func TestGolden(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"query_empty", []string{"query"}},
		{"query_vsc", []string{"query", "vsc", "--fixtures", fixtures}},
		{"query_pinyin", []string{"query", "wx", "--fixtures", fixtures}},
		{"query_calc", []string{"query", "12*3+4"}},
		{"query_hex", []string{"query", "255 in hex"}},
		{"run_clipboard_history", []string{"run", "builtin.clipboard/history", "--fixtures", fixtures}},
		{"run_clipboard_search", []string{"run", "builtin.clipboard/history", "--text", "世界", "--fixtures", fixtures}},
		{"plugin_list", []string{"plugin", "list"}},
		{"run_file_search", []string{"run", "builtin.everything/search", "--text", "工作", "--fixtures", fixtures}},
		{"run_file_search_report", []string{"run", "builtin.everything/search", "--text", "report q3", "--fixtures", fixtures}},
		{"query_scripts", []string{"query", "greet", "--fixtures", fixtures}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, code := glctl(t, tt.args...)
			if code != 0 {
				t.Fatalf("exit code %d", code)
			}
			golden(t, tt.name, out)
		})
	}
}

func TestSessionPasteFromClipboardHistory(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	if _, code := glctl(t, "session", "new", "-s", s, "--fixtures", fixtures); code != 0 {
		t.Fatal("session new failed")
	}
	steps := [][]string{
		{"type", "clipboard history"},
		{"key", "Enter"},
		{"type", "git"},
		{"key", "Enter"},
	}
	var out string
	for _, st := range steps {
		var code int
		out, code = glctl(t, append([]string{"session", st[0], "-s", s}, st[1:]...)...)
		if code != 0 {
			t.Fatalf("step %v failed", st)
		}
	}
	golden(t, "session_paste", out)
}

func TestSessionConfirmClear(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	glctl(t, "session", "new", "-s", s, "--fixtures", fixtures)
	glctl(t, "session", "type", "-s", s, "clear clipboard")
	out, _ := glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_confirm_ask", out)
	out, _ = glctl(t, "session", "key", "-s", s, "Enter")
	golden(t, "session_confirm_done", out)
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {"run"}, {"session", "type"}, {"run", "noslash"}} {
		if _, code := glctl(t, args...); code != 2 {
			t.Errorf("%v: exit code %d, want 2", args, code)
		}
	}
	if out, code := glctl(t, "help"); code != 0 || !strings.Contains(out, "glctl drives") {
		t.Errorf("help: %d %q", code, out)
	}
}

func TestConfigPaths(t *testing.T) {
	tree := map[string]any{}
	setPath(tree, strings.Split("plugins.builtin.apps.commands.reload.alias", "."), "rr")
	setPath(tree, []string{"theme"}, "dark")
	if v, ok := lookup(tree, "plugins.builtin.apps.commands.reload.alias"); !ok || v != "rr" {
		t.Fatalf("lookup = %v %v; tree = %v", v, ok, tree)
	}
	if v, _ := lookup(tree, "theme"); v != "dark" {
		t.Fatalf("theme = %v", v)
	}
}

func TestI18nCheck(t *testing.T) {
	out, code := glctl(t, "i18n", "check", "--src", "../..")
	if code != 0 {
		t.Fatalf("i18n check failed:\n%s", out)
	}
}
