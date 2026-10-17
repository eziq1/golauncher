package scripts

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func parse(t *testing.T, name, src string) (Script, bool) {
	t.Helper()
	s, ok, err := ParseScript(filepath.Join(testDir(`C:\scripts\tools`, "/scripts/tools"), name), strings.NewReader(src))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s, ok
}

// testDir returns the Windows path win on Windows, else the Unix path unix.
func testDir(win, unix string) string {
	if runtime.GOOS == "windows" {
		return win
	}
	return unix
}

func TestParseCommentStyles(t *testing.T) {
	tests := []struct{ name, prefix string }{
		{"a.ps1", "#"},
		{"a.sh", "#"},
		{"a.py", "# "},
		{"a.js", "//"},
		{"a.lua.js", "--"},
		{"a.bat", "REM"},
		{"a.cmd", "rem"},
		{"a.cmd", "@REM"},
		{"a.bat", "::"},
	}
	for _, tt := range tests {
		src := strings.Join([]string{
			tt.prefix + " @raycast.schemaVersion 1",
			tt.prefix + " @raycast.title  Flush DNS ",
			tt.prefix + "\t@raycast.mode fullOutput",
			tt.prefix + " @raycast.packageName Network",
			tt.prefix + " @raycast.icon 🌐",
			tt.prefix + " @raycast.needsConfirmation true",
			tt.prefix + " @raycast.description Clears the DNS cache",
			tt.prefix + " @raycast.author Alice",
			tt.prefix + " @raycast.authorURL https://example.com",
			tt.prefix + " @raycast.currentDirectoryPath ~",
			"echo hi",
		}, "\n")
		s, ok := parse(t, tt.name, src)
		if !ok {
			t.Fatalf("%s %q: not a script command", tt.name, tt.prefix)
		}
		want := Script{
			Path: s.Path, SchemaVersion: 1, Title: "Flush DNS", Mode: modeFullOutput, PackageName: "Network",
			Icon: "🌐", NeedsConfirmation: true, Description: "Clears the DNS cache", Author: "Alice",
			AuthorURL: "https://example.com", CurrentDirectory: "~",
		}
		if s.Title != want.Title || s.Mode != want.Mode || s.PackageName != want.PackageName || s.Icon != want.Icon ||
			!s.NeedsConfirmation || s.Description != want.Description || s.Author != want.Author ||
			s.AuthorURL != want.AuthorURL || s.CurrentDirectory != "~" || s.SchemaVersion != 1 {
			t.Errorf("%s %q: got %+v", tt.name, tt.prefix, s)
		}
	}
}

func TestParseArguments(t *testing.T) {
	src := `#!/usr/bin/env python3
# @raycast.title Search
# @raycast.mode silent
# @raycast.argument2 { "type": "dropdown", "placeholder": "Engine", "optional": true, "data": [{"title": "Bing", "value": "bing"}, {"title": "DDG", "value": "ddg"}] }
# @raycast.argument1 { "type": "text", "placeholder": "query", "percentEncoded": true }
# @raycast.argument3 { "placeholder": "token", "secure": true }
# @raycast.argument4 { "type": "text", "placeholder": "ignored" }
`
	s, ok := parse(t, "search.py", src)
	if !ok {
		t.Fatal("not a script command")
	}
	if !slices.Equal(s.Shebang, []string{"/usr/bin/env", "python3"}) {
		t.Errorf("shebang = %q", s.Shebang)
	}
	if s.PackageName != "tools" {
		t.Errorf("packageName defaults to the directory name, got %q", s.PackageName)
	}
	if len(s.Arguments) != 3 {
		t.Fatalf("arguments = %+v", s.Arguments)
	}
	a1, a2, a3 := s.Arguments[0], s.Arguments[1], s.Arguments[2]
	if a1.Index != 1 || a1.Type != "text" || !a1.PercentEncoded || a1.Optional || a1.Placeholder != "query" {
		t.Errorf("argument1 = %+v", a1)
	}
	if a2.Index != 2 || a2.Type != "dropdown" || !a2.Optional || len(a2.Data) != 2 || a2.Data[1].Value != "ddg" {
		t.Errorf("argument2 = %+v", a2)
	}
	if a3.Type != "password" {
		t.Errorf("secure argument3 = %+v", a3)
	}
	args := s.pluginArguments()
	want := []plugin.Argument{
		{Name: "argument1", Type: plugin.ArgText, Placeholder: "query", Required: true},
		{Name: "argument2", Type: plugin.ArgDropdown, Placeholder: "Engine", Options: []plugin.Option{{Value: "bing", Title: "Bing"}, {Value: "ddg", Title: "DDG"}}},
		{Name: "argument3", Type: plugin.ArgPassword, Placeholder: "token", Required: true},
	}
	if len(args) != len(want) {
		t.Fatalf("plugin arguments = %+v", args)
	}
	for i := range want {
		if args[i].Name != want[i].Name || args[i].Type != want[i].Type || args[i].Placeholder != want[i].Placeholder ||
			args[i].Required != want[i].Required || !slices.Equal(args[i].Options, want[i].Options) {
			t.Errorf("argument %d = %+v, want %+v", i, args[i], want[i])
		}
	}
}

func TestParseEdgeCases(t *testing.T) {
	// No title: not a script command.
	if _, ok := parse(t, "plain.ps1", "# just a script\nWrite-Output 1\n"); ok {
		t.Error("a script without @raycast.title was accepted")
	}
	// BOM, inline and unknown modes fall back to compact.
	for _, mode := range []string{"inline", "bogus", ""} {
		s, ok := parse(t, "a.ps1", "\ufeff# @raycast.title X\n# @raycast.mode "+mode+"\n# @raycast.refreshTime 1h\n")
		if !ok || s.Mode != modeCompact {
			t.Errorf("mode %q: ok=%v mode=%q", mode, ok, s.Mode)
		}
	}
	// Metadata is read from the first 40 lines only.
	src := strings.Repeat("echo\n", 40) + "# @raycast.title Late\n"
	if _, ok := parse(t, "late.ps1", src); ok {
		t.Error("metadata after line 40 was read")
	}
	// "remark" is not a REM comment, and a shebang is not read in batch files.
	s, ok := parse(t, "a.bat", "#!/bin/bash\nremark @raycast.mode silent\n:: @raycast.title T\n")
	if !ok || s.Mode != modeCompact || s.Shebang != nil {
		t.Errorf("batch: %+v", s)
	}
	// Broken argument JSON is an error.
	if _, _, err := ParseScript("a.ps1", strings.NewReader("# @raycast.title X\n# @raycast.argument1 {type: text}\n")); err == nil {
		t.Error("bad argument JSON was accepted")
	}
}

func TestIcon(t *testing.T) {
	dir := testDir(`C:\s`, "/s")
	abs := testDir(`D:\icons\x.SVG`, "/icons/x.SVG")
	tests := []struct {
		icon string
		want plugin.Icon
	}{
		{"🌐", plugin.Icon{Name: "command"}},
		{"", plugin.Icon{Name: "command"}},
		{"https://example.com/a.png", plugin.Icon{Name: "command"}},
		{"images/logo.png", plugin.Icon{Path: filepath.Join(dir, "images", "logo.png")}},
		{abs, plugin.Icon{Path: abs}},
		{"logo.jpg", plugin.Icon{Name: "command"}},
	}
	for _, tt := range tests {
		s := Script{Path: filepath.Join(dir, "a.ps1"), Icon: tt.icon}
		if got := s.icon(); got != tt.want {
			t.Errorf("icon %q = %+v, want %+v", tt.icon, got, tt.want)
		}
	}
}

func TestFixtureScripts(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "fixtures", "scripts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]Script{}
	for _, e := range entries {
		if !candidate(e.Name()) {
			continue
		}
		s, ok, err := parseFile(filepath.Join(dir, e.Name()))
		if err != nil || !ok {
			t.Fatalf("%s: ok=%v err=%v", e.Name(), ok, err)
		}
		titles[s.Title] = s
	}
	if len(titles) != 3 || titles["Greet"].Mode != modeCompact || titles["Show Environment"].Mode != modeFullOutput ||
		!titles["Show Environment"].NeedsConfirmation || len(titles["Search the Web"].Arguments) != 2 {
		t.Errorf("fixtures = %+v", titles)
	}
}

// fakeSystem has the given programs on PATH and files on disk.
func fakeSystem(programs map[string]string, files ...string) System {
	return System{
		LookPath: func(name string) (string, error) {
			if p, ok := programs[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Getenv: func(name string) string {
			return map[string]string{"ProgramFiles": `C:\Program Files`, "SystemRoot": `C:\Windows`}[name]
		},
		Exists: func(p string) bool { return slices.Contains(files, p) },
	}
}

// TestInterpreter checks the Windows rules (fakeSystem is not Unix); the
// expected paths are joined the Windows way, so it runs on Windows only.
func TestInterpreter(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows paths; see TestInterpreterUnix")
	}
	gitBash := `C:\Program Files\Git\bin\bash.exe`
	all := map[string]string{
		"pwsh": `C:\pwsh.exe`, "powershell": `C:\powershell.exe`, "python": `C:\python.exe`,
		"py": `C:\py.exe`, "node": `C:\node.exe`, "cmd": `C:\Windows\System32\cmd.exe`, "ruby": `C:\ruby.exe`,
	}
	without := func(names ...string) map[string]string {
		m := map[string]string{}
		for k, v := range all {
			if !slices.Contains(names, k) {
				m[k] = v
			}
		}
		return m
	}
	tests := []struct {
		name     string
		file     string
		shebang  []string
		sys      System
		program  string
		args     []string
		missing  string
		batchOut bool
	}{
		{name: "pwsh preferred", file: "a.ps1", sys: fakeSystem(all), program: `C:\pwsh.exe`,
			args: []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"}},
		{name: "windows powershell", file: "a.ps1", sys: fakeSystem(without("pwsh")), program: `C:\powershell.exe`,
			args: []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"}},
		{name: "python", file: "a.py", sys: fakeSystem(all), program: `C:\python.exe`},
		{name: "py launcher", file: "a.py", sys: fakeSystem(without("python")), program: `C:\py.exe`},
		{name: "no python", file: "a.py", sys: fakeSystem(without("python", "py")), missing: "python"},
		{name: "node", file: "a.js", sys: fakeSystem(all), program: `C:\node.exe`},
		{name: "no node", file: "a.js", sys: fakeSystem(without("node")), missing: "node"},
		{name: "batch", file: "a.BAT", sys: fakeSystem(all), program: `C:\Windows\System32\cmd.exe`, args: []string{"/d", "/c"}, batchOut: true},
		{name: "cmd fallback", file: "a.cmd", sys: fakeSystem(without("cmd")), program: `C:\Windows\System32\cmd.exe`, args: []string{"/d", "/c"}, batchOut: true},
		{name: "git bash", file: "a.sh", sys: fakeSystem(all, gitBash), program: gitBash},
		{name: "bash on PATH", file: "a.sh", sys: fakeSystem(map[string]string{"bash": `D:\msys\bash.exe`}), program: `D:\msys\bash.exe`},
		{name: "WSL bash skipped", file: "a.sh", sys: fakeSystem(map[string]string{"bash": `C:\Windows\System32\bash.exe`}), missing: "bash"},
		{name: "shebang env", file: "a.py", shebang: []string{"/usr/bin/env", "node"}, sys: fakeSystem(all), program: `C:\node.exe`},
		{name: "shebang flags", file: "a.sh", shebang: []string{"/bin/bash", "-l"}, sys: fakeSystem(all, gitBash), program: gitBash, args: []string{"-l"}},
		{name: "shebang python3", file: "a.js", shebang: []string{"/usr/bin/python3"}, sys: fakeSystem(all), program: `C:\python.exe`},
		{name: "shebang pwsh", file: "a.sh", shebang: []string{"/usr/bin/env", "pwsh", "-NoLogo"}, sys: fakeSystem(all), program: `C:\pwsh.exe`,
			args: []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"}},
		{name: "shebang other", file: "a.sh", shebang: []string{"/usr/bin/env", "ruby"}, sys: fakeSystem(all), program: `C:\ruby.exe`},
		{name: "shebang missing", file: "a.ps1", shebang: []string{"/usr/bin/env", "perl"}, sys: fakeSystem(all), missing: "perl"},
	}
	for _, tt := range tests {
		s := Script{Path: `C:\s\` + tt.file, Shebang: tt.shebang}
		it := tt.sys.interpreter(&s)
		if it.Program != tt.program || it.Missing != tt.missing || it.Batch != tt.batchOut || !slices.Equal(it.Args, tt.args) {
			t.Errorf("%s: got %+v", tt.name, it)
		}
	}
}

func TestOutputLastLine(t *testing.T) {
	o := newOutput()
	o.Write([]byte("\x1b[32mgreen\x1b[0m line\r\n"))
	o.Write([]byte("progress 10%\rprogress 100%\n\n"))
	if got := o.LastLine(); got != "progress 100%" {
		t.Errorf("LastLine = %q", got)
	}
	o.Write([]byte("part"))
	o.Write([]byte("ial"))
	if got := o.LastLine(); got != "partial" {
		t.Errorf("LastLine = %q", got)
	}
	o.Write([]byte("\n  \n"))
	if got := o.LastLine(); got != "partial" {
		t.Errorf("LastLine after blank lines = %q", got)
	}
}

func TestInterpreterUnix(t *testing.T) {
	on := func(programs map[string]string, files ...string) System {
		sys := fakeSystem(programs, files...)
		sys.Unix = true
		sys.Getenv = func(name string) string { return map[string]string{"HOME": "/Users/a"}[name] }
		return sys
	}
	path := map[string]string{"bash": "/bin/bash", "python3": "/usr/bin/python3", "osascript": "/usr/bin/osascript", "zsh": "/bin/zsh"}
	tests := []struct {
		name    string
		file    string
		shebang []string
		sys     System
		program string
		args    []string
		missing string
	}{
		{name: "sh", file: "a.sh", sys: on(path), program: "/bin/bash"},
		{name: "python", file: "a.py", sys: on(path), program: "/usr/bin/python3"},
		{name: "applescript", file: "a.applescript", sys: on(path), program: "/usr/bin/osascript"},
		{name: "node from Homebrew, not on PATH", file: "a.js", sys: on(path, "/opt/homebrew/bin/node"), program: "/opt/homebrew/bin/node"},
		{name: "user's bin", file: "a.rb", sys: on(path, "/Users/a/.local/bin/ruby"), program: "/Users/a/.local/bin/ruby"},
		{name: "no node", file: "a.js", sys: on(path), missing: "node"},
		{name: "batch", file: "a.bat", sys: on(path), missing: ".bat"},
		{name: "shebang path", file: "a.py", shebang: []string{"/bin/zsh", "-l"}, sys: on(path, "/bin/zsh"), program: "/bin/zsh", args: []string{"-l"}},
		{name: "shebang path missing", file: "a.sh", shebang: []string{"/usr/local/bin/python3"}, sys: on(path), program: "/usr/bin/python3"},
		{name: "shebang env", file: "a.sh", shebang: []string{"/usr/bin/env", "-S", "osascript", "-l", "JavaScript"}, sys: on(path),
			program: "/usr/bin/osascript", args: []string{"-l", "JavaScript"}},
		{name: "shebang pwsh", file: "a.sh", shebang: []string{"/usr/bin/env", "pwsh"}, sys: on(map[string]string{"pwsh": "/usr/local/bin/pwsh"}),
			program: "/usr/local/bin/pwsh", args: []string{"-NoProfile", "-NonInteractive", "-File"}},
		{name: "shebang missing", file: "a.sh", shebang: []string{"/usr/bin/env", "perl"}, sys: on(path), missing: "perl"},
	}
	for _, tt := range tests {
		s := Script{Path: "/s/" + tt.file, Shebang: tt.shebang}
		it := tt.sys.interpreter(&s)
		if it.Program != tt.program || it.Missing != tt.missing || it.Batch || !slices.Equal(it.Args, tt.args) {
			t.Errorf("%s: got %+v", tt.name, it)
		}
	}
}
