package gemini

import (
	"bytes"
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/ai/acp"
	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

var update = flag.Bool("update", false, "rewrite this system's testdata/argv*.golden.json")

// fakeHost is a computer with the files in files and the variables in env.
func fakeHost(env map[string]string, files map[string]string) host {
	return host{
		getenv: func(k string) string { return env[k] },
		exists: func(p string) bool { _, ok := files[p]; return ok },
		readFile: func(p string) ([]byte, error) {
			if s, ok := files[p]; ok {
				return []byte(s), nil
			}
			return nil, os.ErrNotExist
		},
		executable: func(p string) bool { _, ok := files[p]; return ok },
	}
}

// goldenFile is the golden file of this system's command lines: Windows
// (gemini.cmd, gemini.exe) and macOS (gemini) differ in the CLI and in the
// system defaults path.
func goldenFile(t *testing.T) (name string, paths []string) {
	switch runtime.GOOS {
	case "windows":
		return "argv.golden.json", []string{`C:\npm\gemini.cmd`, `C:\bin\gemini.exe`}
	case "darwin":
		return "argv.darwin.golden.json", []string{"/opt/homebrew/bin/gemini"}
	}
	t.Skip("no golden file for " + runtime.GOOS)
	return "", nil
}

// argvGolden is testdata/argv.golden.json.
type argvGolden struct {
	Cases []argvCase `json:"cases"`
	// Settings are the system settings files written, by name.
	Settings map[string]jsontext.Value `json:"settings"`
}

type argvCase struct {
	Name  string   `json:"name"`
	Argv  []string `json:"argv"`
	Env   []string `json:"env"`
	Files []string `json:"files"`
}

// TestArgvTable pins the command line, environment and system settings of
// every combination of new or resumed session, launcher tools, web search,
// model and system prompt for gemini.cmd and gemini.exe on Windows, gemini
// on macOS (on a computer without system settings). Run with -update only
// for an intended change.
func TestArgvTable(t *testing.T) {
	goldenName, paths := goldenFile(t)
	dir := t.TempDir()
	h := fakeHost(nil, nil)
	ts := &ToolServer{Command: `C:\golauncher.exe`, Args: []string{"mcp"}, Env: map[string]string{"GOLAUNCHER_AGENT_TOKEN": "tok"}, SessionEnv: "GOLAUNCHER_AGENT_SESSION"}
	b := agentrt.New(acp.NewAdapter(config(h)), Options{Dir: dir, Tools: ts})
	const id = "12345678-1234-4234-8234-123456789abc"
	g := argvGolden{Settings: map[string]jsontext.Value{}}
	for _, path := range paths {
		for _, resume := range []bool{false, true} {
			for _, tools := range []bool{false, true} {
				for _, web := range []bool{false, true} {
					for _, model := range []string{"", "gemini-2.5-flash"} {
						for _, sp := range []string{"", "Line one.\nLine two."} {
							oo := backend.OpenOptions{SessionID: id, Resume: resume, Model: model, WebSearch: web, LauncherTools: tools, SystemPrompt: sp}
							cmd, err := b.CommandAt(oo, path, "0.34.0", true, resume)
							if err != nil {
								t.Fatal(err)
							}
							c := argvCase{
								Name: fmt.Sprintf("%s resume=%v tools=%v web=%v model=%q prompt=%v", cmp.Or(filepath.Ext(path), filepath.Base(path)), resume, tools, web, model, sp != ""),
								Argv: cmd.Argv, Env: slices.Clone(cmd.Env),
							}
							for i := range c.Env {
								c.Env[i] = strings.ReplaceAll(c.Env[i], dir, `<dir>`)
							}
							for name, content := range cmd.Files {
								c.Files = append(c.Files, name)
								g.Settings[name] = jsontext.Value(content)
							}
							g.Cases = append(g.Cases, c)
						}
					}
				}
			}
		}
	}
	got, err := json.Marshal(g, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	golden := filepath.Join("testdata", goldenName)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("command lines differ from %s; got:\n%s", golden, got)
	}
}

// TestSettings: the settings without web search register no built-in tool;
// with it only the web tools; an administrator's system settings are kept
// under golauncher's, and its system defaults are still read.
func TestSettings(t *testing.T) {
	var s map[string]any
	json.Unmarshal(systemSettings(false, fakeHost(nil, nil)).content, &s)
	tools := s["tools"].(map[string]any)
	if core := tools["core"].([]any); len(core) != 0 {
		t.Errorf("core %v", core)
	}
	if ex := tools["exclude"].([]any); !slices.Contains(ex, any("run_shell_command")) || !slices.Contains(ex, any("web_fetch")) {
		t.Errorf("exclude %v", ex)
	}
	json.Unmarshal(systemSettings(true, fakeHost(nil, nil)).content, &s)
	tools = s["tools"].(map[string]any)
	if core := tools["core"].([]any); !slices.Equal(core, []any{"google_web_search", "web_fetch"}) || slices.Contains(tools["exclude"].([]any), any("web_fetch")) {
		t.Errorf("tools %v", tools)
	}

	admin := `{
	  // the administrator's settings
	  "tools": {"core": ["run_shell_command"], "sandbox": "docker"},
	  "security": {"auth": {"enforcedType": "oauth-personal"}},
	  "telemetry": {"enabled": false}, /* kept */
	  "url": "https://example.com/a//b"
	}`
	h := fakeHost(nil, map[string]string{defaultSystemSettings: admin})
	st := systemSettings(false, h)
	if err := json.Unmarshal(st.content, &s); err != nil {
		t.Fatal(err)
	}
	tools = s["tools"].(map[string]any)
	if len(tools["core"].([]any)) != 0 || tools["sandbox"] != "docker" || s["url"] != "https://example.com/a//b" ||
		s["security"].(map[string]any)["auth"].(map[string]any)["enforcedType"] != "oauth-personal" || s["security"].(map[string]any)["disableYoloMode"] != true ||
		s["telemetry"].(map[string]any)["enabled"] != false {
		t.Errorf("merged %s", st.content)
	}
	// This system's paths (Gemini CLI's settings.ts).
	defaults, moved, movedDefaults, otherDefaults := `C:\ProgramData\gemini-cli\system-defaults.json`, `D:\policy\gemini.json`, `D:\policy\system-defaults.json`, `E:\d.json`
	switch runtime.GOOS {
	case "darwin":
		defaults, moved, movedDefaults, otherDefaults = "/Library/Application Support/GeminiCli/system-defaults.json", "/etc/policy/gemini.json", "/etc/policy/system-defaults.json", "/tmp/d.json"
	case "windows":
	default:
		defaults, moved, movedDefaults, otherDefaults = "/etc/gemini-cli/system-defaults.json", "/etc/policy/gemini.json", "/etc/policy/system-defaults.json", "/tmp/d.json"
	}
	if st.defaultsPath != defaults {
		t.Errorf("defaults %q", st.defaultsPath)
	}
	// Moved by the environment; an unparsable file is ignored, like Gemini CLI does.
	h = fakeHost(map[string]string{envSystemSettings: moved}, map[string]string{moved: "{nonsense"})
	if st := systemSettings(false, h); st.defaultsPath != movedDefaults || !bytes.Equal(st.content, systemSettings(false, fakeHost(nil, nil)).content) {
		t.Errorf("moved: %q %s", st.defaultsPath, st.content)
	}
	h = fakeHost(map[string]string{envSystemDefaults: otherDefaults}, nil)
	if st := systemSettings(false, h); st.defaultsPath != otherDefaults {
		t.Errorf("defaults %q", st.defaultsPath)
	}
	for goos, want := range map[string]string{
		"windows": `C:\ProgramData\gemini-cli\settings.json`,
		"darwin":  "/Library/Application Support/GeminiCli/settings.json",
		"linux":   "/etc/gemini-cli/settings.json",
	} {
		if got := systemSettingsPath(goos); got != want {
			t.Errorf("systemSettingsPath(%s) = %q", goos, got)
		}
	}
}

func TestModel(t *testing.T) {
	h := fakeHost(nil, nil)
	for _, m := range []string{"gemini 2.5", "-x", "a&b", "a\nb", `a"b`} {
		if _, err := args(agentrt.Launch{Dir: `C:\w`, Model: m}, h); err == nil {
			t.Errorf("model %q accepted", m)
		}
	}
	if cmd, err := args(agentrt.Launch{Dir: `C:\w`, Model: "gemini-2.5-pro"}, h); err != nil || !slices.Contains(cmd.Argv, "gemini-2.5-pro") {
		t.Errorf("%q %v", cmd.Argv, err)
	}
}

// TestCandidates: the Windows lookup (gemini.exe, gemini.cmd, never the
// other launchers).
func TestCandidates(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows paths")
	}
	env := map[string]string{"PATH": `C:\a;"C:\b";;C:\c`, "APPDATA": `C:\Users\someone\AppData\Roaming`}
	files := map[string]string{`C:\b\gemini.cmd`: "", `C:\c\gemini.exe`: "", `C:\Users\someone\AppData\Roaming\npm\gemini.cmd`: "", `C:\a\gemini.ps1`: ""}
	got, err := candidates(fakeHost(env, files))
	if err != nil || !slices.Equal(got, []string{`C:\c\gemini.exe`, `C:\b\gemini.cmd`, `C:\Users\someone\AppData\Roaming\npm\gemini.cmd`}) {
		t.Errorf("candidates %q %v", got, err)
	}
	// The discovery (R84) also lists the launcher it skips, and how each was
	// found.
	if got := foundStrings(discover(fakeHost(env, files))); !slices.Equal(got, []string{`C:\c\gemini.exe:path`, `C:\b\gemini.cmd:path`,
		`C:\Users\someone\AppData\Roaming\npm\gemini.cmd:installDir`, `C:\a\gemini.ps1:path:launcher`}) {
		t.Errorf("discover %q", got)
	}
	// Only launchers golauncher does not run.
	got, err = candidates(fakeHost(env, map[string]string{`C:\a\gemini.ps1`: "", `C:\a\gemini`: ""}))
	if len(got) != 0 || err == nil || !strings.Contains(err.Error(), `C:\a\gemini.ps1`) {
		t.Errorf("candidates %q %v", got, err)
	}
	if got, err := candidates(fakeHost(env, nil)); len(got) != 0 || err != nil {
		t.Errorf("candidates %q %v", got, err)
	}
	// Not installed: Check says so.
	b := agentrt.New(acp.NewAdapter(config(fakeHost(env, nil))), Options{Dir: t.TempDir()})
	if _, err := b.Check(t.Context()); backend.KindOf(err) != backend.ErrNotInstalled {
		t.Errorf("Check %v", err)
	}
}

// TestCandidatesUnix: outside Windows, an executable gemini on PATH, then in
// the usual folders (Homebrew, npm's prefixes); and a node next to it is put
// on its PATH.
func TestCandidatesUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix paths")
	}
	env := map[string]string{"PATH": "/usr/bin: /Users/someone/bin::/opt/homebrew/bin"}
	files := map[string]string{"/opt/homebrew/bin/gemini": "", "/Users/someone/bin/gemini": "", "/Users/someone/.nvm/versions/node/v22.1.0/bin/gemini": ""}
	h := fakeHost(env, files)
	h.dirs = []string{"/opt/homebrew/bin", "/usr/local/bin", "/Users/someone/.nvm/versions/node/v22.1.0/bin"}
	got, err := candidates(h)
	if err != nil || !slices.Equal(got, []string{"/Users/someone/bin/gemini", "/opt/homebrew/bin/gemini", "/Users/someone/.nvm/versions/node/v22.1.0/bin/gemini"}) {
		t.Errorf("candidates %q %v", got, err)
	}
	if got := foundStrings(discover(h)); !slices.Equal(got, []string{"/Users/someone/bin/gemini:path", "/opt/homebrew/bin/gemini:path",
		"/Users/someone/.nvm/versions/node/v22.1.0/bin/gemini:installDir"}) {
		t.Errorf("discover %q", got)
	}
	// A gemini that may not be run, or none.
	h = fakeHost(env, map[string]string{"/usr/bin/gemini": ""})
	h.executable = func(string) bool { return false }
	if got, err := candidates(h); len(got) != 0 || err != nil {
		t.Errorf("candidates %q %v", got, err)
	}
	b := agentrt.New(acp.NewAdapter(config(fakeHost(env, nil))), Options{Dir: t.TempDir()})
	if _, err := b.Check(t.Context()); backend.KindOf(err) != backend.ErrNotInstalled {
		t.Errorf("Check %v", err)
	}

	// node next to gemini (nvm): its folder goes first on PATH, unless it is
	// on PATH already.
	nvm := "/Users/someone/.nvm/versions/node/v22.1.0/bin"
	h = fakeHost(env, map[string]string{nvm + "/gemini": "", nvm + "/node": "", "/opt/homebrew/bin/gemini": "", "/opt/homebrew/bin/node": ""})
	info := config(h).Info
	if got := info.PathEnv(nvm + "/gemini"); !slices.Equal(got, []string{"PATH=" + nvm + ":" + env["PATH"]}) {
		t.Errorf("PathEnv %q", got)
	}
	if got := info.PathEnv("/opt/homebrew/bin/gemini"); got != nil {
		t.Errorf("PathEnv on PATH %q", got)
	}
	if got := info.PathEnv("/Users/someone/bin/gemini"); got != nil {
		t.Errorf("PathEnv without node %q", got)
	}
}

func TestTool(t *testing.T) {
	failed := func(tool, resp string) string {
		return "MCP tool '" + tool + "' reported tool error for function call: {\"name\":\"" + tool + "\",\"args\":{}} with response: " + resp
	}
	for _, tc := range []struct {
		c            acp.ToolCall
		tool, result string
	}{
		{acp.ToolCall{Title: `{"query":"code"}`}, "mcp__golauncher__search", ""},
		{acp.ToolCall{Title: `{}`}, "mcp__golauncher__list_plugins", ""},
		{acp.ToolCall{Title: `{"plugin":"builtin.winget"}`}, "mcp__golauncher__list_plugins", ""},
		{acp.ToolCall{Title: `{"plugin":"builtin.winget","command":"search"}`}, "mcp__golauncher__run_command", ""},
		{acp.ToolCall{Title: `{"handle":"i1","action":"open"}`}, "mcp__golauncher__run_action", ""},
		{acp.ToolCall{Title: `{"handle":"i1"}`}, "mcp", ""},
		{acp.ToolCall{Title: `{"handle":"i1"}`, Done: true, Result: `{"title":"x","text":"y"}`}, "mcp__golauncher__get_detail", `{"title":"x","text":"y"}`},
		{acp.ToolCall{Title: `{"handle":"i1"}`, Done: true, Result: `{"status":"done","action":"Open"}`}, "mcp__golauncher__run_action", `{"status":"done","action":"Open"}`},
		{acp.ToolCall{Title: `{"handle":"i1"}`, Done: true, Failed: true,
			Result: failed("run_action", `[{"functionResponse":{"name":"run_action","response":{"content":[{"type":"text","text":"{\"status\":\"declined\"}"}],"isError":true}}}]`)},
			"mcp__golauncher__run_action", `{"status":"declined"}`},
		{acp.ToolCall{Title: `Searching the web for: "x"`, Kind: "search"}, "WebSearch", ""},
	} {
		if tool, _ := tool(tc.c); tool != tc.tool {
			t.Errorf("tool(%+v) = %q, want %q", tc.c, tool, tc.tool)
		}
		if tc.c.Done {
			if r := toolResult(tc.c); r != tc.result {
				t.Errorf("toolResult(%+v) = %q, want %q", tc.c, r, tc.result)
			}
		}
	}
}

func TestStripComments(t *testing.T) {
	in := "{\"a\": \"// no\", // yes\n \"b\": /* x */ 1, \"c\": \"\\\"/*\"}"
	var v map[string]any
	if err := json.Unmarshal(stripComments([]byte(in)), &v); err != nil || v["a"] != "// no" || v["c"] != `"/*` {
		t.Errorf("%s %v %v", stripComments([]byte(in)), v, err)
	}
}

// foundStrings describes what discover found: path, source and "launcher".
func foundStrings(found []agentrt.Found) []string {
	out := make([]string, len(found))
	for i, f := range found {
		out[i] = f.Path + ":" + string(f.Source)
		if f.Launcher {
			out[i] += ":launcher"
		}
	}
	return out
}
