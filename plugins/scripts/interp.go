package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// System abstracts the lookups used to pick an interpreter, so tests can
// simulate missing ones.
type System struct {
	// LookPath finds a program on PATH (exec.LookPath).
	LookPath func(name string) (string, error)
	// Getenv reads an environment variable (os.Getenv).
	Getenv func(name string) string
	// Exists reports whether a file exists.
	Exists func(path string) bool
	// Unix selects the rules of macOS and other Unix systems (unixInterpreter)
	// instead of Windows'.
	Unix bool
}

// RealSystem looks things up on this machine.
func RealSystem() System {
	return System{
		LookPath: exec.LookPath,
		Getenv:   os.Getenv,
		Exists: func(p string) bool {
			st, err := os.Stat(p)
			return err == nil && !st.IsDir()
		},
		Unix: runtime.GOOS != "windows",
	}
}

// Interpreter is how a script is run: Program Args... <script> <arguments>.
type Interpreter struct {
	Program string
	Args    []string
	// Missing names the program that could not be found; Program is empty.
	Missing string
	// Batch scripts go through cmd.exe, whose command line needs its own quoting.
	Batch bool
}

// interpreter picks the interpreter of s.
func (sys System) interpreter(s *Script) Interpreter {
	if sys.Unix {
		return sys.unixInterpreter(s)
	}
	if len(s.Shebang) > 0 {
		cmd := s.Shebang
		if base(cmd[0]) == "env" {
			cmd = cmd[1:]
			// "env -S prog args" splits the rest; the line was split already.
			if len(cmd) > 0 && cmd[0] == "-S" {
				cmd = cmd[1:]
			}
		}
		if len(cmd) > 0 {
			return sys.byName(base(cmd[0]), cmd[1:])
		}
	}
	switch strings.ToLower(filepath.Ext(s.Path)) {
	case ".ps1":
		return sys.byName("powershell", nil)
	case ".py":
		return sys.byName("python", nil)
	case ".js":
		return sys.byName("node", nil)
	case ".bat", ".cmd":
		return sys.byName("cmd", nil)
	case ".sh":
		return sys.byName("bash", nil)
	}
	return Interpreter{Missing: filepath.Ext(s.Path)}
}

// base is the lower-case program name of a shebang path like /usr/bin/python3.
func base(p string) string {
	p = p[strings.LastIndexAny(p, `/\`)+1:]
	return strings.TrimSuffix(strings.ToLower(p), ".exe")
}

// byName resolves a program named in a shebang or implied by an extension.
func (sys System) byName(name string, extra []string) Interpreter {
	find := func(names ...string) string {
		for _, n := range names {
			if p, err := sys.LookPath(n); err == nil {
				return p
			}
		}
		return ""
	}
	var it Interpreter
	switch name {
	case "pwsh", "powershell":
		// PowerShell 7 is preferred when installed.
		it.Program = find("pwsh", "powershell")
		it.Args = []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"}
		extra = nil // shebang flags are for Unix pwsh
	case "python", "python3":
		it.Program = find("python", "python3", "py")
	case "node", "nodejs":
		it.Program = find("node")
	case "cmd":
		it.Program = find("cmd")
		if it.Program == "" {
			if root := sys.Getenv("SystemRoot"); root != "" {
				it.Program = filepath.Join(root, "System32", "cmd.exe")
			}
		}
		it.Args, it.Batch = []string{"/d", "/c"}, true
	case "bash", "sh", "zsh", "dash":
		it.Program = sys.gitBash()
	default:
		it.Program = find(name)
	}
	if it.Program == "" {
		it.Missing = name
		return it
	}
	it.Args = append(it.Args, extra...)
	return it
}

// gitBash finds Git for Windows' bash. The bash.exe in System32 is WSL's,
// which cannot run a script given by a Windows path, so it is skipped.
func (sys System) gitBash() string {
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LocalAppData"} {
		dir := sys.Getenv(env)
		if dir == "" {
			continue
		}
		if env == "LocalAppData" {
			dir = filepath.Join(dir, "Programs")
		}
		if p := filepath.Join(dir, "Git", "bin", "bash.exe"); sys.Exists(p) {
			return p
		}
	}
	if p, err := sys.LookPath("bash"); err == nil {
		if root := sys.Getenv("SystemRoot"); root == "" || !strings.HasPrefix(strings.ToLower(p), strings.ToLower(filepath.Join(root, "System32"))) {
			return p
		}
	}
	return ""
}

// unixExtensions are the interpreters of the script extensions on Unix,
// those of Raycast's Script Commands among them.
var unixExtensions = map[string]string{
	".sh": "bash", ".zsh": "zsh", ".py": "python3", ".js": "node", ".rb": "ruby",
	".swift": "swift", ".applescript": "osascript", ".ps1": "pwsh",
}

// unixDirs are where interpreters are looked for after PATH: a program
// started from Finder or at login has only /usr/bin:/bin:/usr/sbin:/sbin
// on PATH, without Homebrew's or the user's own programs
// (docs/macos-port.md §5.3).
var unixDirs = []string{"/opt/homebrew/bin", "/usr/local/bin", "~/.local/bin", "/usr/bin", "/bin"}

// unixInterpreter picks the interpreter of s on Unix. A shebang names it:
// an absolute path is used as it is when it exists, a program of
// "#!/usr/bin/env prog" (or a path that does not exist) is looked for on
// PATH and in unixDirs. Without a shebang the extension decides; batch
// files cannot run.
func (sys System) unixInterpreter(s *Script) Interpreter {
	if cmd := s.Shebang; len(cmd) > 0 {
		if base(cmd[0]) == "env" {
			cmd = cmd[1:]
			if len(cmd) > 0 && cmd[0] == "-S" {
				cmd = cmd[1:]
			}
		} else if filepath.IsAbs(cmd[0]) && sys.Exists(cmd[0]) {
			return Interpreter{Program: cmd[0], Args: cmd[1:]}
		}
		if len(cmd) > 0 {
			return sys.unixByName(filepath.Base(cmd[0]), cmd[1:])
		}
	}
	if name, ok := unixExtensions[strings.ToLower(filepath.Ext(s.Path))]; ok {
		return sys.unixByName(name, nil)
	}
	return Interpreter{Missing: filepath.Ext(s.Path)}
}

// unixByName finds the program name for unixInterpreter.
func (sys System) unixByName(name string, extra []string) Interpreter {
	var it Interpreter
	if name == "pwsh" {
		it.Args = []string{"-NoProfile", "-NonInteractive", "-File"}
	}
	if p, err := sys.LookPath(name); err == nil {
		it.Program = p
	} else {
		for _, dir := range unixDirs {
			if rest, ok := strings.CutPrefix(dir, "~/"); ok {
				dir = filepath.Join(sys.Getenv("HOME"), rest)
			}
			if p := filepath.Join(dir, name); filepath.IsAbs(p) && sys.Exists(p) {
				it.Program = p
				break
			}
		}
	}
	if it.Program == "" {
		return Interpreter{Missing: name}
	}
	it.Args = append(it.Args, extra...)
	return it
}
