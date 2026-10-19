package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// The reasoning effort (R109): --effort <level>, with the levels the help
// lists.

func TestHelpEfforts(t *testing.T) {
	real := parseHelp(readHelp(t, helpReal))
	for _, l := range testedEfforts {
		if !real.Has(markEffort + l) {
			t.Errorf("2.1.292: no %s%s", markEffort, l)
		}
	}
	if got := (support{version: "2.1.292", flags: real}).efforts(); !slices.Equal(got, testedEfforts) {
		t.Errorf("2.1.292: %q", got)
	}
	for _, tt := range []struct {
		help string
		want []string
	}{
		{"  --effort <level>    Effort level\n                      (low, medium, high)\n  --other  x (a, b)\n", []string{"low", "medium", "high"}},
		{"  --effort <level>    Effort level (low,medium , high, max)\n", []string{"low", "medium", "high", "max"}},
		// The list belongs to another option.
		{"  --effort <level>    Effort level\n  --mode <m>   Mode (fast, slow)\n", nil},
		{"  --efforts <x>  Not it (low, high)\n", nil},
		{"no options at all (low, high)", nil},
	} {
		if got := helpEfforts(tt.help); !slices.Equal(got, tt.want) {
			t.Errorf("helpEfforts(%q) = %q, want %q", tt.help, got, tt.want)
		}
	}
}

func TestEffortLevels(t *testing.T) {
	full := parseHelp(readHelp(t, helpReal))
	noList := flagsWithout(t)
	for f := range noList {
		if strings.HasPrefix(f, markEffort) {
			delete(noList, f)
		}
	}
	for _, tt := range []struct {
		name    string
		version string
		flags   agentrt.Flags
		want    []string
	}{
		{"probed", "2.1.292", full, testedEfforts},
		{"probed, no list", "2.1.292", noList, []string{"low", "medium", "high"}},
		{"probed, no --effort", "2.1.292", flagsWithout(t, flagEffort), nil},
		{"unknown", "", nil, testedEfforts},
		{"probe failed, 2.1.296", "2.1.296", nil, []string{"low", "medium", "high", "xhigh"}},
		{"probe failed, 2.1.100", "2.1.100", nil, []string{"low", "medium", "high"}},
		{"probe failed, 2.1.50", "2.1.50", nil, nil},
	} {
		if got := (support{version: tt.version, flags: tt.flags}).efforts(); !slices.Equal(got, tt.want) {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestArgvEffortTable pins the command lines with an effort to
// testdata/argv.effort.golden.json: the full set, an older probed help
// (2.1.150), one without --effort, one whose list lacks the level, and
// versions whose probe failed. Run with -update for an intended change.
func TestArgvEffortTable(t *testing.T) {
	dir := filepath.Join("C:\\", "work", "ai")
	b := New(Options{Dir: dir, Tools: &ToolServer{Command: `C:\golauncher.exe`, Args: []string{"mcp"},
		Env: map[string]string{"GOLAUNCHER_AGENT_TOKEN": "tok"}, SessionEnv: "GOLAUNCHER_AGENT_SESSION"}})
	const id = "12345678-1234-4234-8234-123456789abc"
	threeLevels := parseHelp(strings.Replace(readHelp(t, helpReal), "(low, medium, high, xhigh, max)", "(low, medium, high)", 1))
	sets := []struct {
		name    string
		version string
		flags   agentrt.Flags
	}{
		{"unknown", "", nil},
		{"2.1.292 probed", "2.1.292", parseHelp(readHelp(t, helpReal))},
		{"2.1.150 probed", "2.1.150", parseHelp(readHelp(t, helpOld))},
		{"no --effort", "2.1.292", flagsWithout(t, flagEffort)},
		{"three levels", "2.1.120", threeLevels},
		{"probe failed 2.1.200", "2.1.200", nil},
		{"probe failed 2.1.100", "2.1.100", nil},
		{"probe failed 2.1.50", "2.1.50", nil},
	}
	var got []argvCase
	for _, set := range sets {
		for _, path := range []string{`C:\bin\claude.exe`, `C:\npm\claude.cmd`} {
			for _, effort := range []string{"high", "xhigh", "max", "ultra"} {
				oo := backend.OpenOptions{SessionID: id, Model: "opus", Effort: effort, WebSearch: true}
				argv, env, err := commandWith(b, oo, path, set.version, set.flags, true, false)
				if err != nil {
					t.Fatal(err)
				}
				for _, f := range []string{"--bare", "--dangerously-skip-permissions", "bypassPermissions"} {
					if slices.Contains(argv, f) {
						t.Errorf("%s in %q", f, argv)
					}
				}
				got = append(got, argvCase{
					Name: fmt.Sprintf("%s %s effort=%s", set.name, filepath.Ext(path), effort),
					Argv: windowsPaths(argv, dir), ExtraEnv: env,
				})
			}
		}
	}
	out, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, '\n')
	golden := filepath.Join("testdata", "argv.effort.golden.json")
	if *updateArgv {
		if err := os.WriteFile(golden, out, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), out) {
		t.Fatalf("command lines differ from %s; rerun with -update if intended", golden)
	}
}

// TestEffortsRedetect: the levels are those of the CLI the last check
// found; "Re-detect" (Discover with refresh) checks again, so an update is
// noticed.
func TestEffortsRedetect(t *testing.T) {
	f := newFake(t, map[string]any{"version": "2.1.100 (Claude Code)", "helpFile": helpWithout(t, helpReal, flagEffort)})
	if levels, err := f.b.Efforts(context.Background()); err != nil || levels != nil {
		t.Fatalf("before: %q, %v", levels, err)
	}
	f.writeScript(map[string]any{"version": "2.1.292 (Claude Code)", "helpFile": absPath(t, helpReal)})
	if levels, _ := f.b.Efforts(context.Background()); levels != nil {
		t.Fatalf("checked again without a re-detection: %q", levels)
	}
	f.b.Discover(context.Background(), true)
	if levels, err := f.b.Efforts(context.Background()); err != nil || !slices.Equal(levels, testedEfforts) {
		t.Fatalf("after: %q, %v", levels, err)
	}
}

// TestEffortRun: through the runtime and the fake CLI (which rejects every
// option its help does not list): Efforts reports the levels, and a chat
// passes --effort only when the version takes the level.
func TestEffortRun(t *testing.T) {
	threeFile := filepath.Join(t.TempDir(), "help.txt")
	if err := os.WriteFile(threeFile, []byte(strings.Replace(readHelp(t, helpReal), "(low, medium, high, xhigh, max)", "(low, medium, high)", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		script func(t *testing.T) map[string]any
		levels []string
		effort string
		passed bool
	}{
		{"current", func(*testing.T) map[string]any { return nil }, testedEfforts, "max", true},
		{"without --effort", func(t *testing.T) map[string]any {
			return map[string]any{"helpFile": helpWithout(t, helpReal, flagEffort)}
		}, nil, "high", false},
		{"three levels", func(*testing.T) map[string]any { return map[string]any{"helpFile": threeFile} },
			[]string{"low", "medium", "high"}, "max", false},
		{"three levels, high", func(*testing.T) map[string]any { return map[string]any{"helpFile": threeFile} },
			[]string{"low", "medium", "high"}, "high", true},
		{"probe fails, 2.1.100", func(t *testing.T) map[string]any {
			return map[string]any{"version": "2.1.100 (Claude Code)", "helpFile": absPath(t, helpOld), "helpFails": true}
		}, []string{"low", "medium", "high"}, "xhigh", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake(t, tt.script(t))
			levels, err := f.b.Efforts(context.Background())
			if err != nil || !slices.Equal(levels, tt.levels) {
				t.Fatalf("Efforts = %q, %v; want %q", levels, err, tt.levels)
			}
			c := f.open(backend.OpenOptions{WebSearch: true, Effort: tt.effort})
			wantDone(t, send(t, c, "hi"), "echo: hi")
			for _, argv := range f.starts() {
				if hasFlag(argv, flagEffort, tt.effort) != tt.passed || !tt.passed && slices.Contains(argv, flagEffort) {
					t.Errorf("argv %q", argv)
				}
			}
		})
	}
}
