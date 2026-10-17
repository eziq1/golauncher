package claudecode

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

var updateArgv = flag.Bool("update", false, "rewrite testdata/argv.golden.json")

// argvCase is one combination of the options that shape the command line.
type argvCase struct {
	Name     string   `json:"name"`
	Argv     []string `json:"argv"`
	ExtraEnv []string `json:"extraEnv,omitempty"`
}

// TestArgvTable pins the command line of every combination of mode
// (persistent or per turn), new or resumed session, launcher tools (off, on,
// on without a tool server), web search, model, system prompt (none or two
// lines) and CLI kind (claude.exe or the npm claude.cmd) byte for byte to
// testdata/argv.golden.json, which was recorded before the agent runtime
// refactoring (R71). Run with -update only for an intended change.
func TestArgvTable(t *testing.T) {
	dir := filepath.Join("C:\\", "work", "ai")
	withTools := New(Options{Dir: dir, Tools: &ToolServer{Command: `C:\golauncher.exe`, Args: []string{"mcp"},
		Env: map[string]string{"GOLAUNCHER_AGENT_TOKEN": "tok"}, SessionEnv: "GOLAUNCHER_AGENT_SESSION"}})
	noTools := New(Options{Dir: dir})
	const id = "12345678-1234-4234-8234-123456789abc"
	var got []argvCase
	for _, path := range []string{`C:\bin\claude.exe`, `C:\npm\claude.cmd`} {
		for _, persistent := range []bool{true, false} {
			for _, resume := range []bool{false, true} {
				for _, tools := range []string{"off", "on", "noServer"} {
					for _, web := range []bool{false, true} {
						for _, model := range []string{"", "opus"} {
							for _, sp := range []string{"", "Line one.\nLine two."} {
								b := withTools
								if tools == "noServer" {
									b = noTools
								}
								oo := backend.OpenOptions{SessionID: id, Resume: resume, Model: model, WebSearch: web,
									LauncherTools: tools != "off", SystemPrompt: sp}
								argv, env := commandFor(b, oo, path, persistent, resume)
								argv = windowsPaths(argv, dir)
								got = append(got, argvCase{
									Name: fmt.Sprintf("%s persistent=%v resume=%v tools=%s web=%v model=%q prompt=%v",
										filepath.Ext(path), persistent, resume, tools, web, model, sp != ""),
									Argv: argv, ExtraEnv: env,
								})
							}
						}
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
				t.Errorf("%s:\n got %q %q", got[i].Name, got[i].Argv, got[i].ExtraEnv)
				if i < len(w) {
					t.Errorf("want %q %q", w[i].Argv, w[i].ExtraEnv)
				}
			}
		}
		t.Fatalf("command lines differ from %s (%d cases)", golden, len(got))
	}
}

// windowsPaths spells the paths under dir in argv the way Windows joins
// them, so that the golden file recorded on Windows also holds elsewhere
// (filepath.Join(`C:\`, "work") is `C:\/work` on Unix).
func windowsPaths(argv []string, dir string) []string {
	if runtime.GOOS == "windows" {
		return argv
	}
	out := make([]string, len(argv))
	for i, a := range argv {
		if rest, ok := strings.CutPrefix(a, dir); ok {
			a = `C:\work\ai` + strings.ReplaceAll(rest, "/", `\`)
		}
		out[i] = a
	}
	return out
}
