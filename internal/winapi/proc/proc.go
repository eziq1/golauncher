// Package proc runs console programs in the background the way golauncher
// needs it: without a console window, in a Job Object so that Stop ends the
// whole process tree, with batch files quoted for cmd.exe, and with their
// output decoded into clean lines (OEM code page, ANSI escapes, \r progress
// bars). The background task manager and the Script Commands plugin use it.
package proc

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultWaitDelay is how long output is read after the program exited: a
// program it started and left running may hold the pipe open.
const DefaultWaitDelay = 2 * time.Second

// Options describe a program to start.
type Options struct {
	// Argv is the program and its arguments. A batch file (.bat, .cmd) is
	// run through cmd.exe.
	Argv []string
	Dir  string
	// Env is the complete environment (nil: the launcher's).
	Env []string
	// Output receives stdout and stderr (nil discards them).
	Output io.Writer
	// Stderr receives stderr instead of Output when set.
	Stderr io.Writer
	// StdinPipe connects the program's standard input to a pipe the caller
	// writes through Process.Stdin; otherwise it reads nothing.
	StdinPipe bool
	// WaitDelay overrides DefaultWaitDelay.
	WaitDelay time.Duration
}

// Process is a started program.
type Process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu    sync.Mutex
	group *group
	ended bool
}

// IsBatch reports whether path is a batch file, which cmd.exe must run.
func IsBatch(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".bat", ".cmd":
		return true
	}
	return false
}

// Start starts the program described by o.
func Start(o Options) (*Process, error) {
	if len(o.Argv) == 0 || o.Argv[0] == "" {
		return nil, errors.New("proc: no program")
	}
	cmd := command(o.Argv)
	cmd.Dir = o.Dir
	cmd.Env = o.Env
	out := o.Output
	if out == nil {
		out = io.Discard
	}
	cmd.Stdout, cmd.Stderr = out, out
	if o.Stderr != nil {
		cmd.Stderr = o.Stderr
	}
	cmd.WaitDelay = o.WaitDelay
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = DefaultWaitDelay
	}
	var stdin io.WriteCloser
	if o.StdinPipe {
		var err error
		if stdin, err = cmd.StdinPipe(); err != nil {
			return nil, err
		}
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Process{cmd: cmd, stdin: stdin, group: newGroup(cmd.Process.Pid)}, nil
}

// Pid returns the process ID.
func (p *Process) Pid() int { return p.cmd.Process.Pid }

// Stdin returns the pipe to the program's standard input, nil unless
// Options.StdinPipe was set. Closing it signals end of input; Wait closes it
// after the program ended.
func (p *Process) Stdin() io.WriteCloser { return p.stdin }

// Wait waits for the program to end and returns its exit code. A non-zero
// exit code is not an error; err reports that waiting failed.
func (p *Process) Wait() (int, error) {
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

// Stop ends the program and every process it started. It may be called at
// any time, also after the program ended.
func (p *Process) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ended {
		return
	}
	p.group.kill()
	_ = p.cmd.Process.Kill()
}

// MergeEnv returns base with the NAME=value entries of extra added; an
// entry replaces a variable of the same name (case-insensitively, as on
// Windows).
func MergeEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	names := map[string]bool{}
	for _, kv := range extra {
		k, _, _ := strings.Cut(kv, "=")
		names[strings.ToUpper(k)] = true
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if k != "" && names[strings.ToUpper(k)] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, extra...)
}

// Environ returns the launcher's environment with extra merged in.
func Environ(extra ...string) []string { return MergeEnv(os.Environ(), extra) }

// Getenv looks name up in env (NAME=value entries), case-insensitively.
func Getenv(env []string, name string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}
