package scoop

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// Runner starts Scoop commands. The plugin runs one at a time.
type Runner interface {
	// Start starts `scoop args...`, writing its output to out.
	Start(args []string, out io.Writer) (Process, error)
}

// Process is a started Scoop command.
type Process interface {
	// Wait waits for the command to end and returns its exit code.
	Wait() (int, error)
	// Stop ends the command and every process it started. It may be called
	// at any time, also after the command ended.
	Stop()
}

// CommandLine is how a Scoop command is shown: "scoop install main/git".
func CommandLine(args []string) string {
	return strings.Join(append([]string{"scoop"}, args...), " ")
}

// psQuote quotes s as a single-quoted PowerShell string. PowerShell also
// accepts the typographic single quotes as delimiters, so they are doubled
// as well.
func psQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'', '‘', '’', '‚', '‛':
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

// Script is the PowerShell script that runs `scoop args...`: each argument
// is a quoted literal, output is UTF-8 and the exit code is Scoop's.
func Script(args []string) string {
	var b strings.Builder
	b.WriteString("$ProgressPreference = 'SilentlyContinue'\n")
	b.WriteString("try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch {}\n")
	b.WriteString("$global:LASTEXITCODE = 0\n")
	b.WriteString("try { & scoop")
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(psQuote(a))
	}
	b.WriteString(" } catch { Write-Output $_; exit 1 }\n")
	b.WriteString("exit $global:LASTEXITCODE\n")
	return b.String()
}

// encodeCommand encodes a script for powershell -EncodedCommand (base64 of
// UTF-16LE), which needs no command-line quoting at all.
func encodeCommand(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// PowerShell runs Scoop through PowerShell 7 (pwsh) when installed, else
// Windows PowerShell, without a console window. The shims of Root are put
// first on PATH so that scoop is found even if the launcher started before
// Scoop was installed.
type PowerShell struct {
	Root string

	once  sync.Once
	shell string
}

// waitDelay is how long output is read after Scoop exited: a program it
// started may hold the pipe open.
var waitDelay = 2 * time.Second

func (r *PowerShell) shellPath() string {
	r.once.Do(func() {
		r.shell = "powershell.exe"
		if p, err := exec.LookPath("pwsh.exe"); err == nil {
			r.shell = p
		} else if p, err := exec.LookPath("powershell.exe"); err == nil {
			r.shell = p
		}
	})
	return r.shell
}

// environment is the environment of Scoop: the launcher's, with the root's
// shims first on PATH and SCOOP set to the root.
func (r *PowerShell) environment() []string {
	if r.Root == "" {
		return os.Environ()
	}
	shims := filepath.Join(r.Root, "shims")
	pathSet := false
	var env []string
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case strings.EqualFold(k, "PATH"):
			env = append(env, k+"="+shims+string(os.PathListSeparator)+v)
			pathSet = true
		case strings.EqualFold(k, "SCOOP"):
		default:
			env = append(env, kv)
		}
	}
	if !pathSet {
		env = append(env, "PATH="+shims)
	}
	return append(env, "SCOOP="+r.Root)
}

// Start implements Runner.
func (r *PowerShell) Start(args []string, out io.Writer) (Process, error) {
	cmd := exec.Command(r.shellPath(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encodeCommand(Script(args)))
	cmd.Env = r.environment()
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = waitDelay
	prepare(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &psProcess{cmd: cmd, group: newProcGroup(cmd.Process.Pid)}, nil
}

type psProcess struct {
	cmd *exec.Cmd

	mu    sync.Mutex
	group *procGroup
	ended bool
}

func (p *psProcess) Wait() (int, error) {
	err := p.cmd.Wait()
	p.mu.Lock()
	p.ended = true
	p.group.close()
	p.mu.Unlock()
	code := -1
	if p.cmd.ProcessState != nil {
		code = p.cmd.ProcessState.ExitCode()
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok || errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	return code, err
}

func (p *psProcess) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ended {
		return
	}
	p.group.kill()
	_ = p.cmd.Process.Kill()
}

// FakeRunner records the commands it is asked to run instead of running
// them, for tests, glctl's fake environment and glctl --real without --exec.
type FakeRunner struct {
	// Hook, if set, runs in place of each command (in Process.Wait): it may
	// write output and block until stop is closed, and returns the exit code.
	// Without it a command prints one line and succeeds.
	Hook func(args []string, out io.Writer, stop <-chan struct{}) int

	mu       sync.Mutex
	commands [][]string
}

// Commands returns the commands started so far.
func (f *FakeRunner) Commands() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.commands))
	for i, c := range f.commands {
		out[i] = append([]string(nil), c...)
	}
	return out
}

// Start implements Runner.
func (f *FakeRunner) Start(args []string, out io.Writer) (Process, error) {
	f.mu.Lock()
	f.commands = append(f.commands, append([]string(nil), args...))
	hook := f.Hook
	f.mu.Unlock()
	return &fakeProcess{args: args, out: out, hook: hook, stop: make(chan struct{})}, nil
}

type fakeProcess struct {
	args []string
	out  io.Writer
	hook func([]string, io.Writer, <-chan struct{}) int
	once sync.Once
	stop chan struct{}
}

func (p *fakeProcess) Wait() (int, error) {
	if p.hook != nil {
		return p.hook(p.args, p.out, p.stop), nil
	}
	fmt.Fprintf(p.out, "[not run] %s\n", CommandLine(p.args))
	return 0, nil
}

func (p *fakeProcess) Stop() { p.once.Do(func() { close(p.stop) }) }
