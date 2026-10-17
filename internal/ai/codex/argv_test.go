package codex

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

var updateArgv = flag.Bool("update", false, "rewrite testdata/argv.golden.json")

// argvCase is one combination of the options that shape the command line.
type argvCase struct {
	Name string   `json:"name"`
	Argv []string `json:"argv"`
}

// TestArgvTable pins the command line of every combination of new or
// resumed session, launcher tools (off, on, on without a tool server), web
// search, model and system prompt (none or two lines) byte for byte to
// testdata/argv.golden.json, with every isolation flag. Run with -update
// only for an intended change.
func TestArgvTable(t *testing.T) {
	const chat = "12345678-1234-4234-8234-123456789abc"
	const thread = "0190f0e1-2d3c-7b4a-8596-a7b8c9d0e1f2"
	dir := t.TempDir()
	// The thread the chat resumes (Codex assigns thread IDs).
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(`{"`+chat+`":"`+thread+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	withTools := New(Options{Dir: dir, Tools: &ToolServer{Command: `C:\golauncher.exe`, Args: []string{"mcp"},
		Env: map[string]string{"GOLAUNCHER_AGENT_TOKEN": "tok"}, SessionEnv: "GOLAUNCHER_AGENT_SESSION"}})
	noTools := New(Options{Dir: dir})
	const path = `C:\bin\codex.exe`
	var got []argvCase
	for _, resume := range []bool{false, true} {
		for _, tools := range []string{"off", "on", "noServer"} {
			for _, web := range []bool{false, true} {
				for _, model := range []string{"", "gpt-5.5"} {
					for _, sp := range []string{"", "Line one.\nLine two."} {
						b := withTools
						if tools == "noServer" {
							b = noTools
						}
						oo := backend.OpenOptions{SessionID: chat, Resume: resume, Model: model, WebSearch: web,
							LauncherTools: tools != "off", SystemPrompt: sp}
						got = append(got, argvCase{
							Name: fmt.Sprintf("resume=%v tools=%s web=%v model=%q prompt=%v", resume, tools, web, model, sp != ""),
							Argv: commandFor(b, oo, path, resume),
						})
					}
				}
			}
		}
	}
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	golden := filepath.Join("testdata", "argv.golden.json")
	if *updateArgv {
		if err := os.WriteFile(golden, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), b) {
		var w []argvCase
		_ = json.Unmarshal(want, &w)
		for i := range got {
			if i >= len(w) || fmt.Sprint(w[i]) != fmt.Sprint(got[i]) {
				t.Errorf("%s:\n got %q", got[i].Name, got[i].Argv)
				if i < len(w) {
					t.Errorf("want %q", w[i].Argv)
				}
			}
		}
		t.Fatalf("command lines differ from %s (%d cases)", golden, len(got))
	}
}
