package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// contextDir makes a folder to inject as the context and returns it with a
// function that replaces it in glctl output by <DIR>, so goldens do not
// depend on the temporary directory.
func contextDir(t *testing.T) (string, func(string) string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Project 项目")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, func(s string) string {
		s = strings.ReplaceAll(s, strings.ReplaceAll(dir, `\`, `\\`), "<DIR>")
		return strings.ReplaceAll(s, dir, "<DIR>")
	}
}

// TestGoldenContext: an injected context puts the "In <folder>" section on
// top of the empty root, and its items take part in the search.
func TestGoldenContext(t *testing.T) {
	dir, norm := contextDir(t)
	out, code := glctl(t, "query", "", "--context-folder", dir, "--context-select", filepath.Join(dir, "notes.txt"))
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	golden(t, "query_context", norm(out))
	out, code = glctl(t, "query", "terminal", "--context-folder", dir, "--format", "text")
	if code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if first := strings.SplitN(out, "\n", 3); len(first) < 2 || first[0] != "[In Project 项目]" || !strings.Contains(first[1], "builtin.files/terminal") {
		t.Errorf("search with context:\n%s", out)
	}
	// Without the flags there is no context section.
	out, _ = glctl(t, "query", "", "--format", "text")
	if strings.Contains(out, "[In ") {
		t.Errorf("a context section without a context:\n%s", out)
	}
}

// TestSessionContextNewFolder creates folders from the context items: one
// named with the argument, then one with the default name.
func TestSessionContextNewFolder(t *testing.T) {
	dir, norm := contextDir(t)
	s := filepath.Join(t.TempDir(), "s.json")
	if _, code := glctl(t, "session", "new", "-s", s, "--context-folder", dir); code != 0 {
		t.Fatal("session new failed")
	}
	glctl(t, "session", "type", "-s", s, "new folder")
	glctl(t, "session", "arg", "-s", s, "name", "Drafts")
	out, code := glctl(t, "session", "key", "-s", s, "Enter")
	if code != 0 {
		t.Fatalf("Enter failed:\n%s", out)
	}
	golden(t, "session_context_new_folder", norm(out))
	if st, err := os.Stat(filepath.Join(dir, "Drafts")); err != nil || !st.IsDir() {
		t.Fatalf("Drafts was not created: %v", err)
	}

	// A new session (each step replays the session, so a session that
	// created a folder would create it again on replay).
	s2 := filepath.Join(t.TempDir(), "s2.json")
	glctl(t, "session", "new", "-s", s2, "--context-folder", dir)
	glctl(t, "session", "type", "-s", s2, "new folder")
	glctl(t, "session", "key", "-s", s2, "Enter")
	for _, name := range []string{"New folder"} {
		if st, err := os.Stat(filepath.Join(dir, name)); err != nil || !st.IsDir() {
			t.Errorf("%s was not created: %v", name, err)
		}
	}
}
