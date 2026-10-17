package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// The help texts of the tests: the real `claude --help` of 2.1.292 (this
// machine, docs/ai.md §22) and a synthetic older one (2.1.150: without
// --safe-mode, --permission-prompts and other later flags).
const (
	helpReal = "testdata/help-2.1.292.txt"
	helpOld  = "testdata/help-2.1.150.txt"
)

// usedFlags are the long options the full command line uses.
var usedFlags = []string{flagPrint, flagOutputFormat, flagVerbose, flagSessionID, flagResume, flagInputFormat, flagPartial,
	flagSettingSources, flagSafeMode, flagStrictMCP, flagMCPConfig, flagNoSlashCommands, flagTools, flagAllowedTools,
	flagPermissionPrompts, flagAppendSystem, flagModel}

// absPath is path made absolute, for the fake (which runs elsewhere).
func absPath(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func readHelp(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// helpWithout writes the help text of path without the options in drop (an
// option line and its description lines) and returns the file.
func helpWithout(t *testing.T, path string, drop ...string) string {
	t.Helper()
	var out []string
	skip := false
	for line := range strings.Lines(readHelp(t, path)) {
		if strings.HasPrefix(line, "  -") {
			spec, _, _ := strings.Cut(strings.TrimSpace(line), "  ")
			skip = slices.ContainsFunc(drop, func(d string) bool {
				return slices.ContainsFunc(strings.FieldsFunc(spec, func(r rune) bool { return r == ' ' || r == ',' }), func(tok string) bool { return tok == d })
			})
		} else if !strings.HasPrefix(line, "      ") {
			skip = false
		}
		if !skip {
			out = append(out, line)
		}
	}
	file := filepath.Join(t.TempDir(), "help.txt")
	if err := os.WriteFile(file, []byte(strings.Join(out, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

// flagsWithout are the flags of the real help without drop.
func flagsWithout(t *testing.T, drop ...string) agentrt.Flags {
	t.Helper()
	f := parseHelp(readHelp(t, helpReal))
	for _, d := range drop {
		delete(f, d)
	}
	return f
}

func TestParseHelp(t *testing.T) {
	real := parseHelp(readHelp(t, helpReal))
	for _, f := range append(slices.Clone(usedFlags), markDontAsk, "--allowed-tools", "--disallowedTools", "--permission-mode", "--help", "--version") {
		if !real.Has(f) {
			t.Errorf("2.1.292: no %s", f)
		}
	}
	// Only mentioned in descriptions (--bare's), not options of their own.
	for _, f := range []string{"--append-system-prompt-file", "--system-prompt-file", "--settings-file", "--exec-path"} {
		if real.Has(f) {
			t.Errorf("2.1.292: %s found", f)
		}
	}
	if n := len(real); n < 60 {
		t.Errorf("2.1.292: only %d flags: %v", n, real.List())
	}
	old := parseHelp(readHelp(t, helpOld))
	for _, f := range []string{flagSafeMode, flagPermissionPrompts, "--restricted"} {
		if old.Has(f) {
			t.Errorf("2.1.150: %s found", f)
		}
	}
	for _, f := range []string{flagPrint, flagTools, flagStrictMCP, markDontAsk} {
		if !old.Has(f) {
			t.Errorf("2.1.150: no %s", f)
		}
	}
	for _, text := range []string{"", "Usage: something else\n\nOptions:\n  -x, --extra  Something\n", "not a help text at all"} {
		if f := parseHelp(text); f != nil {
			t.Errorf("parseHelp(%q) = %v, want nil", text, f.List())
		}
	}
}

func TestDecide(t *testing.T) {
	full := parseHelp(readHelp(t, helpReal))
	for _, tt := range []struct {
		name     string
		version  string
		flags    agentrt.Flags
		perTurn  bool
		reduced  []string
		dropped  []string // flags named by the log entries, in order
		refused  string   // part of the error message
		argvHas  []string
		argvLack []string
	}{
		{name: "2.1.292 probed", version: "2.1.292", flags: full},
		{name: "probe failed, version unknown", flags: nil},
		{name: "probe failed, 2.1.296", version: "2.1.296"},
		{name: "2.1.150 probed", version: "2.1.150", flags: parseHelp(readHelp(t, helpOld)),
			dropped: []string{flagSafeMode, flagPermissionPrompts}, argvHas: []string{flagPermissionMode}, argvLack: []string{flagSafeMode, flagPermissionPrompts}},
		{name: "probe failed, 2.1.200", version: "2.1.200", dropped: []string{flagPermissionPrompts},
			argvHas: []string{flagMCPConfig}, argvLack: []string{flagPermissionPrompts, flagPermissionMode}},
		{name: "probe failed, 2.1.100", version: "2.1.100", dropped: []string{flagSafeMode, flagPermissionPrompts},
			argvLack: []string{flagSafeMode, flagPermissionPrompts, flagPermissionMode}},
		{name: "no dontAsk either", version: "2.1.150", flags: flagsWithout(t, flagSafeMode, flagPermissionPrompts, markDontAsk),
			dropped: []string{flagSafeMode, flagPermissionPrompts}, argvLack: []string{flagPermissionMode}},
		{name: "no --include-partial-messages", flags: flagsWithout(t, flagPartial), reduced: []string{backend.FeatureStreaming},
			dropped: []string{flagPartial}, argvLack: []string{flagPartial}},
		{name: "no --input-format", flags: flagsWithout(t, flagInputFormat), perTurn: true, dropped: []string{flagInputFormat},
			argvLack: []string{flagInputFormat}},
		{name: "no --append-system-prompt", flags: flagsWithout(t, flagAppendSystem), dropped: []string{flagAppendSystem},
			argvLack: []string{flagAppendSystem}},
		{name: "no --model", flags: flagsWithout(t, flagModel), reduced: []string{backend.FeatureModel}, dropped: []string{flagModel},
			argvLack: []string{flagModel}},
		{name: "no --disable-slash-commands", flags: flagsWithout(t, flagNoSlashCommands), dropped: []string{flagNoSlashCommands},
			argvLack: []string{flagNoSlashCommands}},
		{name: "no --allowedTools", flags: flagsWithout(t, flagAllowedTools),
			reduced: []string{backend.FeatureWebSearch, backend.FeatureLauncherTools}, dropped: []string{flagAllowedTools, flagMCPConfig},
			argvHas: []string{flagSafeMode}, argvLack: []string{flagAllowedTools, flagMCPConfig}},
		{name: "no --mcp-config", flags: flagsWithout(t, flagMCPConfig), reduced: []string{backend.FeatureLauncherTools},
			dropped: []string{flagMCPConfig}, argvHas: []string{flagSafeMode, flagAllowedTools}, argvLack: []string{flagMCPConfig}},
		{name: "no --strict-mcp-config", flags: flagsWithout(t, flagStrictMCP), reduced: []string{backend.FeatureLauncherTools},
			dropped: []string{flagMCPConfig}, argvHas: []string{flagSafeMode}, argvLack: []string{flagStrictMCP, flagMCPConfig}},
		{name: "no --strict-mcp-config nor --safe-mode", flags: flagsWithout(t, flagStrictMCP, flagSafeMode), refused: flagStrictMCP},
		{name: "no --tools", version: "2.1.5", flags: flagsWithout(t, flagTools), refused: "Claude Code 2.1.5 lacks --tools"},
		{name: "no --setting-sources", flags: flagsWithout(t, flagSettingSources), refused: flagSettingSources},
		{name: "no --verbose", flags: flagsWithout(t, flagVerbose), refused: flagVerbose},
		{name: "no --session-id nor --resume", flags: flagsWithout(t, flagSessionID, flagResume), refused: "--session-id, --resume"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fit, err := fit(tt.version, tt.flags)
			if tt.refused != "" {
				e, ok := errors.AsType[*backend.Error](err)
				if !ok || e.Kind != backend.ErrVersion || !strings.Contains(e.Message, tt.refused) {
					t.Fatalf("fit = %+v, %v; want ErrVersion with %q", fit, err, tt.refused)
				}
				if _, err := (adapter{}).Command(agentrt.Launch{Path: "claude", SessionID: "x", Version: tt.version, Flags: tt.flags}); err == nil {
					t.Error("Command did not refuse")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fit.PerTurn != tt.perTurn || !slices.Equal(fit.Reduced, tt.reduced) {
				t.Errorf("fit = %+v, want perTurn %v, reduced %q", fit, tt.perTurn, tt.reduced)
			}
			var named []string
			for _, d := range fit.Dropped {
				named = append(named, regexp.MustCompile(`^(--[A-Za-z-]+|golauncher's MCP server)`).FindString(d))
			}
			for i, n := range named {
				if n == "golauncher's MCP server" {
					named[i] = flagMCPConfig
				}
			}
			if !slices.Equal(named, tt.dropped) {
				t.Errorf("dropped %q, want %q", fit.Dropped, tt.dropped)
			}
			l := agentrt.Launch{Path: "claude", Dir: "work", Persistent: true, SessionID: "id", Version: tt.version, Flags: tt.flags,
				Model: "opus", WebSearch: true, SystemPrompt: "Be brief.", Tools: &agentrt.MCPServer{Name: "golauncher"}, MCPConfig: "mcp.json"}
			cmd, err := (adapter{}).Command(l)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range tt.argvHas {
				if !slices.Contains(cmd.Argv, f) {
					t.Errorf("no %s in %q", f, cmd.Argv)
				}
			}
			for _, f := range tt.argvLack {
				if slices.Contains(cmd.Argv, f) {
					t.Errorf("%s in %q", f, cmd.Argv)
				}
			}
			// Every flag passed is one the CLI has (when it was probed).
			if tt.flags != nil {
				for _, a := range cmd.Argv {
					if strings.HasPrefix(a, "--") && !tt.flags.Has(a) {
						t.Errorf("%s passed, which the CLI lacks", a)
					}
				}
			}
			// Isolation is never reduced.
			for _, f := range []string{"--bare", "--continue", "--dangerously-skip-permissions", "bypassPermissions"} {
				if slices.Contains(cmd.Argv, f) {
					t.Errorf("%s in %q", f, cmd.Argv)
				}
			}
			if !hasFlag(cmd.Argv, flagSettingSources, "") || !slices.Contains(cmd.Argv, flagTools) {
				t.Errorf("isolation flags missing: %q", cmd.Argv)
			}
			if !slices.Contains(cmd.Argv, flagSafeMode) && !slices.Contains(cmd.Env, noAutoMemory) {
				t.Errorf("neither --safe-mode nor %s: %q %q", noAutoMemory, cmd.Argv, cmd.Env)
			}
			if slices.Contains(cmd.Argv, flagSafeMode) && slices.Contains(cmd.Argv, flagMCPConfig) {
				t.Errorf("--safe-mode with --mcp-config: %q", cmd.Argv)
			}
			if !slices.Contains(cmd.Argv, flagStrictMCP) && !slices.Contains(cmd.Argv, flagSafeMode) {
				t.Errorf("neither --strict-mcp-config nor --safe-mode: %q", cmd.Argv)
			}
		})
	}
}

// TestArgvFullSetUnchanged checks that a CLI with every flag (probed, or
// unknown) gets the command line of argv.golden.json.
func TestArgvFullSetUnchanged(t *testing.T) {
	b := New(Options{Dir: filepath.Join("C:\\", "work", "ai"), Tools: &ToolServer{Command: `C:\golauncher.exe`, Args: []string{"mcp"},
		Env: map[string]string{"GOLAUNCHER_AGENT_TOKEN": "tok"}, SessionEnv: "GOLAUNCHER_AGENT_SESSION"}})
	full := parseHelp(readHelp(t, helpReal))
	oo := backend.OpenOptions{SessionID: "12345678-1234-4234-8234-123456789abc", WebSearch: true, LauncherTools: true, Model: "opus", SystemPrompt: "Hi."}
	for _, persistent := range []bool{true, false} {
		want, wantEnv := commandFor(b, oo, `C:\bin\claude.exe`, persistent, false)
		for _, v := range []struct {
			version string
			flags   agentrt.Flags
		}{{"", nil}, {"2.1.292", full}, {"2.1.296", nil}} {
			got, env, err := commandWith(b, oo, `C:\bin\claude.exe`, v.version, v.flags, persistent, false)
			if err != nil || !slices.Equal(got, want) || !slices.Equal(env, wantEnv) {
				t.Errorf("%s probed=%v: %q %q %v\nwant %q %q", v.version, v.flags != nil, got, env, err, want, wantEnv)
			}
		}
	}
}

// TestArgvReducedTable pins the command lines of older CLIs to
// testdata/argv.reduced.golden.json (run with -update for an intended
// change).
func TestArgvReducedTable(t *testing.T) {
	dir := filepath.Join("C:\\", "work", "ai")
	b := New(Options{Dir: dir, Tools: &ToolServer{Command: `C:\golauncher.exe`, Args: []string{"mcp"},
		Env: map[string]string{"GOLAUNCHER_AGENT_TOKEN": "tok"}, SessionEnv: "GOLAUNCHER_AGENT_SESSION"}})
	const id = "12345678-1234-4234-8234-123456789abc"
	sets := []struct {
		name    string
		version string
		flags   agentrt.Flags
	}{
		{"2.1.150 probed", "2.1.150", parseHelp(readHelp(t, helpOld))},
		{"probe failed 2.1.200", "2.1.200", nil},
		{"probe failed 2.1.100", "2.1.100", nil},
		{"no allowedTools", "2.1.292", flagsWithout(t, flagAllowedTools)},
		{"no strict-mcp-config", "2.1.292", flagsWithout(t, flagStrictMCP)},
		{"no partial input-format append-system-prompt model slash-commands", "2.1.292",
			flagsWithout(t, flagPartial, flagInputFormat, flagAppendSystem, flagModel, flagNoSlashCommands)},
	}
	var got []argvCase
	for _, set := range sets {
		for _, persistent := range []bool{true, false} {
			for _, tools := range []bool{false, true} {
				for _, web := range []bool{false, true} {
					oo := backend.OpenOptions{SessionID: id, Model: "opus", WebSearch: web, LauncherTools: tools, SystemPrompt: "Line one.\nLine two."}
					argv, env, err := commandWith(b, oo, `C:\bin\claude.exe`, set.version, set.flags, persistent, false)
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, argvCase{
						Name: fmt.Sprintf("%s persistent=%v tools=%v web=%v", set.name, persistent, tools, web),
						Argv: windowsPaths(argv, dir), ExtraEnv: env,
					})
				}
			}
		}
	}
	b2, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b2 = append(b2, '\n')
	golden := filepath.Join("testdata", "argv.reduced.golden.json")
	if *updateArgv {
		if err := os.WriteFile(golden, b2, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), b2) {
		t.Fatalf("command lines differ from %s; rerun with -update if intended", golden)
	}
}

func TestFoldSystemPrompt(t *testing.T) {
	flags := flagsWithout(t, flagAppendSystem)
	c := (adapter{}).NewCodec(agentrt.Launch{SystemPrompt: "Be brief.", Flags: flags})
	first, _ := c.Message("/hi")
	second, _ := c.Message("again")
	var m struct {
		Message struct{ Content string } `json:"message"`
	}
	_ = json.Unmarshal(first, &m)
	if m.Message.Content != "<golauncher-instructions>\nBe brief.\n</golauncher-instructions>\n\n/hi" {
		t.Errorf("first %q", m.Message.Content)
	}
	if !strings.Contains(string(second), `"content":"again"`) {
		t.Errorf("second %s", second)
	}
	if p := string((adapter{}).NewCodec(agentrt.Launch{SystemPrompt: "Be brief.", Flags: flags}).Prompt("q")); !strings.HasPrefix(p, "<golauncher-instructions>") {
		t.Errorf("per-turn prompt %q", p)
	}
	// Resumed: the session has it already; a CLI with the flag: on the
	// command line.
	for _, l := range []agentrt.Launch{{SystemPrompt: "Be brief.", Flags: flags, Resume: true}, {SystemPrompt: "Be brief."}} {
		if p := string((adapter{}).NewCodec(l).Prompt("q")); p != "q" {
			t.Errorf("%+v: prompt %q", l, p)
		}
	}
}

func TestUnknownOptionExit(t *testing.T) {
	o := (adapter{}).ExitOutcome("error: unknown option '--permission-prompts'")
	if e, ok := errors.AsType[*backend.Error](o.Err); o.Kind != agentrt.Failed || !ok || e.Kind != backend.ErrVersion {
		t.Fatalf("outcome %+v", o)
	}
	if f := unknownOption("error: unknown option '--x'\n(Did you mean --y?)"); f != "--x" {
		t.Errorf("unknownOption = %q", f)
	}
	if o := (adapter{}).ExitOutcome("fake crash: something broke"); backend.KindOf(o.Err) != backend.ErrFailed {
		t.Errorf("crash: %+v", o)
	}
}

// helpRuns counts the fake's --help runs.
func (f *fake) helpRuns() int {
	n := 0
	for _, e := range f.entries() {
		if e.Help {
			n++
		}
	}
	return n
}

// writeScript replaces the fake's script.
func (f *fake) writeScript(s map[string]any) {
	b, _ := json.Marshal(s)
	if err := os.WriteFile(f.script, b, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// TestOlderVersions runs turns with the fake imitating older versions: it
// rejects every option its help does not list, as commander does.
func TestOlderVersions(t *testing.T) {
	type check func(t *testing.T, f *fake, a answer)
	argvLacks := func(flags ...string) check {
		return func(t *testing.T, f *fake, _ answer) {
			for _, argv := range f.starts() {
				for _, fl := range flags {
					if slices.Contains(argv, fl) {
						t.Errorf("%s in %q", fl, argv)
					}
				}
			}
		}
	}
	argvHas := func(flag, value string) check {
		return func(t *testing.T, f *fake, _ answer) {
			for _, argv := range f.starts() {
				if !hasFlag(argv, flag, value) {
					t.Errorf("no %s %q in %q", flag, value, argv)
				}
			}
		}
	}
	for _, tt := range []struct {
		name    string
		script  func(t *testing.T) map[string]any
		reduced []string
		text    string // the answer to "hi" ("" for the default)
		checks  []check
	}{
		{name: "current", script: func(*testing.T) map[string]any { return nil },
			checks: []check{argvHas(flagPermissionPrompts, "none"), argvHas(flagTools, "WebSearch,WebFetch")}},
		{name: "2.1.150", script: func(t *testing.T) map[string]any {
			return map[string]any{"version": "2.1.150 (Claude Code)", "helpFile": absPath(t, helpOld)}
		}, checks: []check{argvLacks(flagSafeMode, flagPermissionPrompts), argvHas(flagPermissionMode, "dontAsk"), argvHas(flagTools, "WebSearch,WebFetch")}},
		{name: "probe fails, 2.1.100", script: func(t *testing.T) map[string]any {
			return map[string]any{"version": "2.1.100 (Claude Code)", "helpFile": absPath(t, helpOld), "helpFails": true}
		}, checks: []check{argvLacks(flagSafeMode, flagPermissionPrompts, flagPermissionMode)}},
		{name: "no streaming", script: func(t *testing.T) map[string]any {
			return map[string]any{"helpFile": helpWithout(t, helpReal, flagPartial)}
		}, reduced: []string{backend.FeatureStreaming}, checks: []check{argvLacks(flagPartial)}},
		{name: "no stream-json input", script: func(t *testing.T) map[string]any {
			return map[string]any{"helpFile": helpWithout(t, helpReal, flagInputFormat)}
		}, checks: []check{argvLacks(flagInputFormat), func(t *testing.T, f *fake, _ answer) {
			if n := len(f.starts()); n != 2 {
				t.Errorf("%d processes for two turns, want one per turn", n)
			}
		}}},
		{name: "no --append-system-prompt", script: func(t *testing.T) map[string]any {
			return map[string]any{"helpFile": helpWithout(t, helpReal, flagAppendSystem)}
		}, text: "echo: <golauncher-instructions>\nBe brief.\n</golauncher-instructions>\n\nhi", checks: []check{argvLacks(flagAppendSystem)}},
		{name: "no --allowedTools", script: func(t *testing.T) map[string]any {
			return map[string]any{"helpFile": helpWithout(t, helpReal, flagAllowedTools)}
		}, reduced: []string{backend.FeatureWebSearch, backend.FeatureLauncherTools}, checks: []check{argvHas(flagTools, ""), argvLacks(flagAllowedTools)}},
		{name: "no --model", script: func(t *testing.T) map[string]any {
			return map[string]any{"helpFile": helpWithout(t, helpReal, flagModel)}
		}, reduced: []string{backend.FeatureModel}, checks: []check{argvLacks(flagModel)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t, tt.script(t))
			st, err := f.b.Check(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(st.Reduced, tt.reduced) {
				t.Errorf("reduced %q, want %q", st.Reduced, tt.reduced)
			}
			c := f.open(backend.OpenOptions{WebSearch: true, Model: "opus", SystemPrompt: "Be brief."})
			want := tt.text
			if want == "" {
				want = "echo: hi"
			}
			a := send(t, c, "hi")
			wantDone(t, a, want)
			wantDone(t, send(t, c, "again"), "echo: again")
			for _, ch := range tt.checks {
				ch(t, f, a)
			}
		})
	}
}

func TestOlderVersionRefused(t *testing.T) {
	f := newFake(t, map[string]any{"version": "2.1.5 (Claude Code)", "helpFile": helpWithout(t, helpReal, flagTools)})
	_, err := f.b.Check(context.Background())
	e, ok := errors.AsType[*backend.Error](err)
	if !ok || e.Kind != backend.ErrVersion || e.Version != "2.1.5" || !strings.Contains(e.Message, "--tools") {
		t.Fatalf("Check = %v", err)
	}
	a := send(t, f.open(backend.OpenOptions{}), "hi")
	if backend.KindOf(a.final.Err) != backend.ErrVersion {
		t.Fatalf("final %+v", a.final)
	}
	if n := len(f.starts()); n != 0 {
		t.Errorf("%d processes started", n)
	}
}

// TestUnknownOptionAtRun: a flag the probe found but the CLI rejects is
// reported as a version too old, with the CLI's text.
func TestUnknownOptionAtRun(t *testing.T) {
	f := newFake(t, map[string]any{"rejects": []string{flagNoSlashCommands}})
	a := send(t, f.open(backend.OpenOptions{}), "hi")
	e, ok := errors.AsType[*backend.Error](a.final.Err)
	if !ok || e.Kind != backend.ErrVersion || !strings.Contains(e.Message, "unknown option '--disable-slash-commands'") {
		t.Fatalf("final %+v", a.final)
	}
}

// TestPermissionRequestDenied: a permission request of a CLI without
// --permission-prompts none is denied.
func TestPermissionRequestDenied(t *testing.T) {
	f := newFake(t, map[string]any{"version": "2.1.150 (Claude Code)", "helpFile": absPath(t, helpOld), "askPermission": true})
	wantDone(t, send(t, f.open(backend.OpenOptions{}), "perm"), "echo: perm: deny")
	var answers []string
	for _, e := range f.entries() {
		if e.Permission != "" {
			answers = append(answers, e.Permission)
		}
	}
	if !slices.Equal(answers, []string{"deny"}) {
		t.Errorf("answers %q", answers)
	}
}

func TestProbeCache(t *testing.T) {
	f := newFake(t, nil)
	for range 3 {
		if _, err := f.b.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	wantDone(t, send(t, f.open(backend.OpenOptions{}), "hi"), "echo: hi")
	if n := f.helpRuns(); n != 1 {
		t.Errorf("%d --help runs, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(f.o.Dir, "capabilities.json")); err != nil {
		t.Errorf("no cache file: %v", err)
	}
	// Another backend (the launcher started again) reads the file.
	b2 := New(f.o)
	defer b2.Close()
	if _, err := b2.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.helpRuns(); n != 1 {
		t.Errorf("%d --help runs after a restart, want 1", n)
	}
	// An update (another version) is probed again.
	f.writeScript(map[string]any{"version": "2.1.150 (Claude Code)", "helpFile": absPath(t, helpOld)})
	if _, err := b2.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.helpRuns(); n != 2 {
		t.Errorf("%d --help runs after an update, want 2", n)
	}
	// A failed probe is not cached.
	f.writeScript(map[string]any{"version": "2.1.200 (Claude Code)", "helpFails": true})
	for range 2 {
		if _, err := b2.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.helpRuns(); n != 4 {
		t.Errorf("%d --help runs, want 4", n)
	}
}
