package tasks

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/eziq1/golauncher/internal/winapi/proc"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Runner starts the programs of process tasks.
type Runner interface {
	// Start starts cmd with the complete environment env, writing its
	// output to out.
	Start(cmd plugin.ProcessSpec, env []string, out io.Writer) (Process, error)
}

// Process is a started program.
type Process interface {
	// Wait waits for the program to end and returns its exit code.
	Wait() (int, error)
	// Stop ends the program and every process it started. It may be called
	// at any time, also after the program ended.
	Stop()
}

// ProcessRunner runs programs for real: without a window, in a Job Object
// (internal/winapi/proc).
type ProcessRunner struct{}

// Start implements Runner.
func (ProcessRunner) Start(cmd plugin.ProcessSpec, env []string, out io.Writer) (Process, error) {
	return proc.Start(proc.Options{Argv: cmd.Argv, Dir: cmd.Dir, Env: env, Output: out})
}

// CommandLine is how cmd is shown to the user: its CommandLine, or Argv
// joined with arguments quoted where needed.
func CommandLine(cmd plugin.ProcessSpec) string {
	if cmd.CommandLine != "" {
		return cmd.CommandLine
	}
	parts := make([]string, len(cmd.Argv))
	for i, a := range cmd.Argv {
		if a == "" || strings.ContainsAny(a, " \t\"") {
			a = syscall.EscapeArg(a)
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

// FakeResult is the outcome a FakeRunner gives the commands whose command
// line (CommandLine) starts with Match.
type FakeResult struct {
	Match    string `json:"match"`
	Output   string `json:"output,omitempty"`
	ExitCode int    `json:"exitCode,omitzero"`
}

// FakeRunner records the commands it is asked to run instead of running
// them, for tests and glctl's fake environment. A command ends at once: by
// default it prints "[not run] <command line>" and succeeds.
type FakeRunner struct {
	// Results configures the output and exit code by command line prefix;
	// the first match wins.
	Results []FakeResult
	// Hook, if set, runs in place of each command (in Process.Wait): it may
	// write output and block until stop is closed, and returns the exit
	// code. It takes precedence over Results.
	Hook func(cmd plugin.ProcessSpec, out io.Writer, stop <-chan struct{}) int

	mu       sync.Mutex
	commands []plugin.ProcessSpec
	gate     chan struct{} // while held: closed by Hold(false)
}

// Hold makes the commands started from now on wait until Hold(false), which
// also releases the waiting ones. glctl holds its fake tasks between steps,
// so that every snapshot sees them running until a wait-tasks step.
func (f *FakeRunner) Hold(hold bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case hold && f.gate == nil:
		f.gate = make(chan struct{})
	case !hold && f.gate != nil:
		close(f.gate)
		f.gate = nil
	}
}

// Commands returns the commands started so far.
func (f *FakeRunner) Commands() []plugin.ProcessSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.commands)
}

// Start implements Runner.
func (f *FakeRunner) Start(cmd plugin.ProcessSpec, _ []string, out io.Writer) (Process, error) {
	f.mu.Lock()
	cmd.Argv = slices.Clone(cmd.Argv)
	f.commands = append(f.commands, cmd)
	hook := f.Hook
	results := f.Results
	gate := f.gate
	f.mu.Unlock()
	return &fakeProcess{cmd: cmd, out: out, hook: hook, results: results, gate: gate, stop: make(chan struct{})}, nil
}

type fakeProcess struct {
	cmd     plugin.ProcessSpec
	out     io.Writer
	hook    func(plugin.ProcessSpec, io.Writer, <-chan struct{}) int
	results []FakeResult
	gate    chan struct{}
	once    sync.Once
	stop    chan struct{}
}

func (p *fakeProcess) Wait() (int, error) {
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-p.stop:
			return -1, nil
		}
	}
	if p.hook != nil {
		return p.hook(p.cmd, p.out, p.stop), nil
	}
	line := CommandLine(p.cmd)
	for _, r := range p.results {
		if strings.HasPrefix(line, r.Match) {
			io.WriteString(p.out, r.Output)
			if r.Output != "" && !strings.HasSuffix(r.Output, "\n") {
				io.WriteString(p.out, "\n")
			}
			return r.ExitCode, nil
		}
	}
	fmt.Fprintf(p.out, "[not run] %s\n", line)
	return 0, nil
}

func (p *fakeProcess) Stop() { p.once.Do(func() { close(p.stop) }) }
