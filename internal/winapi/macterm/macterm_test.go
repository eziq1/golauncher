package macterm

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func installed(ids ...string) []Installed {
	var out []Installed
	for _, t := range Terminals {
		if slices.Contains(ids, t.ID) {
			out = append(out, Installed{Terminal: t, Path: "/Applications/" + t.Name + ".app"})
		}
	}
	return out
}

func term(t *testing.T, id string) Installed {
	t.Helper()
	in := installed(id)
	if len(in) != 1 {
		t.Fatalf("no terminal %q", id)
	}
	return in[0]
}

func TestDetect(t *testing.T) {
	paths := map[string]string{"com.apple.Terminal": "/System/Applications/Utilities/Terminal.app", "net.kovidgoyal.kitty": "/Applications/kitty.app",
		"dev.zed.Zed": "/Applications/Zed.app"}
	lookup := func(id string) string { return paths[id] }
	got := Detect(lookup)
	if len(got) != 2 || got[0].ID != "terminal" || got[1].ID != "kitty" || got[1].Path != "/Applications/kitty.app" {
		t.Errorf("Detect = %+v", got)
	}
	if eds := InstalledEditors(lookup); len(eds) != 1 || eds[0].ID != "zed" {
		t.Errorf("InstalledEditors = %+v", eds)
	}
	if e, ok := EditorByID("cursor"); !ok || e.BundleID != "com.todesktop.230313mzl4w4u92" {
		t.Errorf("EditorByID(cursor) = %+v %v", e, ok)
	}
	if _, ok := EditorByID("emacs"); ok {
		t.Error("unknown editor found")
	}
}

func TestPick(t *testing.T) {
	for _, tt := range []struct {
		installed []string
		pref      string
		want      string
	}{
		{[]string{"terminal"}, "", "terminal"},
		{[]string{"terminal", "iterm2", "warp"}, "", "iterm2"},
		{[]string{"terminal", "warp", "kitty"}, "", "kitty"},
		{[]string{"terminal", "iterm2"}, "terminal", "terminal"},
		{[]string{"terminal", "iterm2"}, "ghostty", "iterm2"}, // chosen, then uninstalled
		{nil, "terminal", ""},
	} {
		got, ok := Pick(installed(tt.installed...), tt.pref)
		if got.ID != tt.want || ok != (tt.want != "") {
			t.Errorf("Pick(%v, %q) = %q %v, want %q", tt.installed, tt.pref, got.ID, ok, tt.want)
		}
	}
}

func TestPreferred(t *testing.T) {
	old := Preferred()
	defer SetPreferred(old)
	SetPreferred("wezterm")
	if got := Preferred(); got != "wezterm" {
		t.Errorf("Preferred = %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/me/src":         "/Users/me/src",
		"":                      "''",
		"/Users/me/My Project":  "'/Users/me/My Project'",
		"/tmp/it's":             `'/tmp/it'\''s'`,
		"/tmp/$HOME `x` \"q\"":  `'/tmp/$HOME ` + "`x`" + ` "q"'`,
		"/Users/me/项目":          "'/Users/me/项目'",
		"/a;rm -rf ~":           "'/a;rm -rf ~'",
		"~/x":                   "'~/x'",
		"a\\b":                  `'a\b'`,
		"/Volumes/Data/a-b_c.d": "/Volumes/Data/a-b_c.d",
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestShellQuoteRoundTrip has sh read the quoted words back.
func TestShellQuoteRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	words := []string{"plain", "with space", "it's", `back\slash`, `$HOME`, "`date`", `"dq"`, "项目 报告", "a'b'c", "!bang", "*glob?", "new\nline"}
	var line []string
	for _, w := range words {
		line = append(line, ShellQuote(w))
	}
	out, err := exec.Command("/bin/sh", "-c", `for a in `+strings.Join(line, " ")+`; do printf '%s\0' "$a"; done`).Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	if !slices.Equal(got, words) {
		t.Errorf("sh read %q, want %q", got, words)
	}
}

func TestAppleScriptString(t *testing.T) {
	for in, want := range map[string]string{
		"cd /tmp":            `"cd /tmp"`,
		`say "hi"`:           `"say \"hi\""`,
		`C:\x`:               `"C:\\x"`,
		"a\nb\tc\r":          `"a\nb\tc\r"`,
		"cd '/tmp/it'\\''s'": `"cd '/tmp/it'\\''s'"`,
		"项目":                 `"项目"`,
	} {
		if got := AppleScriptString(in); got != want {
			t.Errorf("AppleScriptString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestShellLine(t *testing.T) {
	if got := ShellLine("/Users/me/My Project", ""); got != "cd '/Users/me/My Project' && clear" {
		t.Errorf("no command: %s", got)
	}
	if got := ShellLine("/tmp", "git status | head"); got != "cd /tmp && git status | head" {
		t.Errorf("command: %s", got)
	}
}

func TestLaunchTerminalApp(t *testing.T) {
	tm := term(t, "terminal")
	tm.Path = "/System/Applications/Utilities/Terminal.app"
	l, err := tm.Launch(Request{Dir: "/Users/me/My Project"})
	if err != nil || !slices.Equal(l.Argv, []string{"/usr/bin/open", "-a", tm.Path, "/Users/me/My Project"}) || l.Detach {
		t.Errorf("window: %q %v", l.Argv, err)
	}

	l, err = tm.Launch(Request{Dir: `/tmp/a "b"`, Command: `echo "it's"`})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/osascript",
		"-e", `set wasRunning to application id "com.apple.Terminal" is running`,
		"-e", `tell application id "com.apple.Terminal"`,
		"-e", "\tactivate",
		"-e", "\tif not wasRunning then",
		"-e", "\t\trepeat 50 times",
		"-e", "\t\t\tif (count of windows) > 0 then exit repeat",
		"-e", "\t\t\tdelay 0.1",
		"-e", "\t\tend repeat",
		"-e", "\tend if",
		"-e", "\tif not wasRunning and (count of windows) > 0 then",
		"-e", "\t\tdo script \"cd '/tmp/a \\\"b\\\"' && echo \\\"it's\\\"\" in front window",
		"-e", "\telse",
		"-e", "\t\tdo script \"cd '/tmp/a \\\"b\\\"' && echo \\\"it's\\\"\"",
		"-e", "\tend if",
		"-e", "end tell",
	}
	if !slices.Equal(l.Argv, want) {
		t.Errorf("command:\n%s\nwant\n%s", strings.Join(l.Argv, "\n"), strings.Join(want, "\n"))
	}

	l, _ = tm.Launch(Request{Dir: "/tmp", Tab: true})
	script := strings.Join(l.Argv, "\n")
	for _, s := range []string{`tell application "System Events" to keystroke "t" using command down`, `do script "cd /tmp && clear" in front window`} {
		if !strings.Contains(script, s) {
			t.Errorf("tab script lacks %q:\n%s", s, script)
		}
	}
}

func TestLaunchITerm(t *testing.T) {
	it := term(t, "iterm2")
	l, err := it.Launch(Request{Dir: "/Users/me/src", Command: "make test"})
	if err != nil || l.Argv[0] != "/usr/bin/osascript" {
		t.Fatalf("%q %v", l.Argv, err)
	}
	script := strings.Join(l.Argv, "\n")
	for _, s := range []string{
		`tell application id "com.googlecode.iterm2"`,
		"set w to (create window with default profile)",
		`tell current session of w to write text "cd /Users/me/src && make test"`,
	} {
		if !strings.Contains(script, s) {
			t.Errorf("window script lacks %q:\n%s", s, script)
		}
	}
	l, _ = it.Launch(Request{Dir: "/Users/me/src", Tab: true})
	if script := strings.Join(l.Argv, "\n"); !strings.Contains(script, "if wasRunning then tell w to create tab with default profile") ||
		!strings.Contains(script, `write text "cd /Users/me/src && clear"`) {
		t.Errorf("tab script:\n%s", script)
	}
}

func TestLaunchCLITerminals(t *testing.T) {
	dir := "/Users/me/My Project"
	cmd := "npm run dev"
	keep := []string{"/bin/bash", "-l", "-c", "npm run dev; exec /bin/bash -l"}
	for _, tt := range []struct {
		id   string
		r    Request
		want []string
	}{
		{"ghostty", Request{Dir: dir}, []string{"/usr/bin/open", "-na", "/Applications/Ghostty.app", "--args", "--working-directory=" + dir}},
		{"ghostty", Request{Dir: dir, Command: cmd, Shell: "/bin/bash"},
			append([]string{"/usr/bin/open", "-na", "/Applications/Ghostty.app", "--args", "--working-directory=" + dir, "-e"}, keep...)},
		{"alacritty", Request{Dir: dir, Command: cmd, Shell: "/bin/bash"},
			append([]string{"/usr/bin/open", "-na", "/Applications/Alacritty.app", "--args", "--working-directory", dir, "-e"}, keep...)},
		{"kitty", Request{Dir: dir}, []string{"/usr/bin/open", "-na", "/Applications/kitty.app", "--args", "--directory", dir}},
		{"kitty", Request{Dir: dir, Command: cmd}, []string{"/usr/bin/open", "-na", "/Applications/kitty.app", "--args", "--directory", dir,
			"/bin/zsh", "-l", "-c", "npm run dev; exec /bin/zsh -l"}},
		{"wezterm", Request{Dir: dir}, []string{"/Applications/WezTerm.app/Contents/MacOS/wezterm", "start", "--cwd", dir}},
		{"wezterm", Request{Dir: dir, Command: cmd, Shell: "/bin/bash"},
			append([]string{"/Applications/WezTerm.app/Contents/MacOS/wezterm", "start", "--cwd", dir, "--"}, keep...)},
		{"warp", Request{Dir: dir}, []string{"/usr/bin/open", "-b", "dev.warp.Warp-Stable", "warp://action/new_window?path=/Users/me/My%20Project"}},
		{"warp", Request{Dir: "/tmp/a&b=c+d#?", Tab: true}, []string{"/usr/bin/open", "-b", "dev.warp.Warp-Stable", "warp://action/new_tab?path=/tmp/a%26b%3Dc%2Bd%23%3F"}},
	} {
		l, err := term(t, tt.id).Launch(tt.r)
		if err != nil || !slices.Equal(l.Argv, tt.want) {
			t.Errorf("%s %+v:\n got %q %v\nwant %q", tt.id, tt.r, l.Argv, err, tt.want)
		}
	}

	// WezTerm: a tab of the running WezTerm, else its first window.
	l, err := term(t, "wezterm").Launch(Request{Dir: dir, Tab: true})
	if err != nil || !slices.Equal(l.Argv, []string{"/Applications/WezTerm.app/Contents/MacOS/wezterm", "cli", "spawn", "--cwd", dir}) ||
		l.Detach || l.Fallback == nil || !l.Fallback.Detach || l.Fallback.Argv[1] != "start" {
		t.Errorf("wezterm tab: %+v %v", l, err)
	}
}

func TestLaunchUnsupported(t *testing.T) {
	for _, tt := range []struct {
		id string
		r  Request
	}{
		{"ghostty", Request{Dir: "/tmp", Tab: true}},
		{"alacritty", Request{Dir: "/tmp", Tab: true}},
		{"kitty", Request{Dir: "/tmp", Tab: true}},
		{"warp", Request{Dir: "/tmp", Command: "ls"}},
	} {
		if _, err := term(t, tt.id).Launch(tt.r); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("%s %+v: %v, want ErrUnsupported", tt.id, tt.r, err)
		}
	}
	if _, err := term(t, "terminal").Launch(Request{}); !errors.Is(err, ErrNoDir) {
		t.Errorf("no folder: %v", err)
	}
}

func TestOpenWithAndQuickLook(t *testing.T) {
	if l := OpenWith("com.microsoft.VSCode", "/a b", "/c"); !slices.Equal(l.Argv, []string{"/usr/bin/open", "-b", "com.microsoft.VSCode", "/a b", "/c"}) || l.Detach {
		t.Errorf("OpenWith: %+v", l)
	}
	if l := QuickLook("/x.png"); !slices.Equal(l.Argv, []string{"/usr/bin/qlmanage", "-p", "/x.png"}) || !l.Detach {
		t.Errorf("QuickLook: %+v", l)
	}
}

func TestPermissionProblem(t *testing.T) {
	for msg, want := range map[string]Problem{
		"osascript: 31:62: execution error: Not authorized to send Apple events to Terminal. (-1743)":                 NeedAutomation,
		"osascript: execution error: System Events got an error: osascript is not allowed to send keystrokes. (1002)": NeedAccessibility,
		"osascript: execution error: System Events got an error: osascript is not allowed assistive access. (-1719)":  NeedAccessibility,
		"exit status 1": NoProblem,
	} {
		if got := PermissionProblem(errors.New(msg)); got != want {
			t.Errorf("PermissionProblem(%q) = %v, want %v", msg, got, want)
		}
	}
	if PermissionProblem(nil) != NoProblem {
		t.Error("nil error has a problem")
	}
}

// TestRun runs harmless programs (never a terminal): the error output,
// the fallback and detaching.
func TestRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	err := Run(Launch{Argv: []string{"/bin/sh", "-c", "echo 'went wrong' >&2; exit 3"}})
	if err == nil || err.Error() != "sh: went wrong" {
		t.Errorf("failure: %v", err)
	}
	mark := filepath.Join(t.TempDir(), "fallback")
	err = Run(Launch{Argv: []string{"/bin/sh", "-c", "exit 1"}, Fallback: &Launch{Argv: []string{"/usr/bin/touch", mark}}})
	if _, statErr := os.Stat(mark); err != nil || statErr != nil {
		t.Errorf("fallback: %v %v", err, statErr)
	}
	if err := Run(Launch{Argv: []string{"/bin/sleep", "0"}, Detach: true}); err != nil {
		t.Errorf("detached: %v", err)
	}
	if err := Run(Launch{}); err == nil {
		t.Error("empty launch ran")
	}
}

// TestCompileScripts compiles the AppleScripts of the installed
// terminals with osacompile, which reads the apps' dictionaries but
// sends them nothing. Opt in with GOLAUNCHER_TERMINAL_TESTS=1.
func TestCompileScripts(t *testing.T) {
	if os.Getenv("GOLAUNCHER_TERMINAL_TESTS") != "1" || runtime.GOOS != "darwin" {
		t.Skip("set GOLAUNCHER_TERMINAL_TESTS=1 on a Mac to compile the scripts for the installed terminals")
	}
	for _, it := range Detect(AppPath) {
		t.Logf("%s: %s", it.Name, it.Path)
		if it.Driver != DriverTerminal && it.Driver != DriverITerm {
			continue
		}
		for _, r := range []Request{{Dir: "/tmp/a b", Command: `echo "it's"`}, {Dir: "/tmp", Tab: true}} {
			l, err := it.Launch(r)
			if err != nil {
				t.Fatal(err)
			}
			args := append([]string{"-o", filepath.Join(t.TempDir(), "s.scpt")}, l.Argv[1:]...)
			if out, err := exec.Command("/usr/bin/osacompile", args...).CombinedOutput(); err != nil {
				t.Errorf("%s %+v: %v\n%s", it.Name, r, err, out)
			}
		}
	}
	for _, e := range InstalledEditors(AppPath) {
		t.Logf("editor %s", e.Name)
	}
}
