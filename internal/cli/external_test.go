package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/extproc"
	"github.com/eziq1/golauncher/internal/extproc/extproctest"
)

// goHelloDir is examples/plugins/go-hello built by TestMain.
var goHelloDir string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	// The developer's own plugin path must not leak into golden files.
	os.Unsetenv(extproc.EnvPluginPath)
	tmp, err := os.MkdirTemp("", "glctl-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(tmp)
	if goHelloDir, err = extproctest.BuildExample(tmp, "go-hello"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

func TestExternalPluginGolden(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"query_external", []string{"query", "say hello", "--plugins", goHelloDir}},
		{"run_external_view", []string{"run", "example.go-hello/greetings", "--text", "bon", "--plugins", goHelloDir}},
		{"run_external_command", []string{"run", "example.go-hello/count", "--plugins", goHelloDir}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, code := glctl(t, tt.args...)
			if code != 0 {
				t.Fatalf("exit code %d:\n%s", code, out)
			}
			golden(t, tt.name, out)
		})
	}
}

func TestExternalPluginInfo(t *testing.T) {
	// The parent directory works too: each subdirectory is a plugin.
	out, code := glctl(t, "plugin", "info", "example.go-hello", "--plugins", filepath.Dir(goHelloDir))
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	for _, want := range []string{`"origin": "external"`, `"state": "running"`, `"name": "Hello (Go)"`} {
		if !strings.Contains(out, want) {
			t.Errorf("plugin info lacks %s:\n%s", want, out)
		}
	}
	out, _ = glctl(t, "plugin", "info", "builtin.calc")
	if !strings.Contains(out, `"origin": "builtin"`) || strings.Contains(out, `"state"`) {
		t.Errorf("builtin.calc info:\n%s", out)
	}
}

func TestExternalPluginSession(t *testing.T) {
	s := filepath.Join(t.TempDir(), "s.json")
	if _, code := glctl(t, "session", "new", "-s", s, "--plugins", goHelloDir); code != 0 {
		t.Fatal("session new failed")
	}
	glctl(t, "session", "type", "-s", s, "say hello from go")
	// The secondary action counts; storage persists within the session's
	// environment only, so the replayed session sees one count.
	out, code := glctl(t, "session", "action", "-s", s, "example.go-hello/hello", "count")
	if code != 0 || !strings.Contains(out, `"title": "Said hello 1 time"`) {
		t.Fatalf("action: %d\n%s", code, out)
	}
}

func TestPluginValidate(t *testing.T) {
	if out, code := glctl(t, "plugin", "validate", goHelloDir); code != 0 || !strings.Contains(out, `"ok": true`) {
		t.Fatalf("go-hello: %d\n%s", code, out)
	}
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "plugin.json"), []byte(`{"id":"x","name":"%missing%","version":"1","protocol":1,"run":{"command":"nope.exe"}}`), 0o644)
	out, code := glctl(t, "plugin", "validate", bad)
	if code != 1 || !strings.Contains(out, "nope.exe") || !strings.Contains(out, "en.json is missing") {
		t.Fatalf("bad plugin: %d\n%s", code, out)
	}
	if _, code := glctl(t, "plugin", "validate"); code != 2 {
		t.Fatalf("usage: %d", code)
	}
}
