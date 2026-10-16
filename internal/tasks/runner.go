package tasks

import (
	"fmt"
	"io"
	"os"
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

// Elevator is implemented by a Runner that can run programs as
// administrator (ProcessSpec.Elevate). Without it, a task asking for
// elevation fails.
type Elevator interface {
	// Elevated reports whether golauncher already runs as administrator:
	// then an Elevate task is started with Start, with its output.
	Elevated() bool
	// StartElevated starts cmd as administrator through the UAC prompt
	// and returns once the user answered it: ErrElevationCancelled when
	// they declined. The program's output is not available, cmd.Env is not
	// applied, and Stop ends only the program itself, if it can (see
	// proc.Elevated.Stop): Wait then returns an error wrapping ErrNotEnded.
	StartElevated(cmd plugin.ProcessSpec) (Process, error)
}

var (
	// ErrElevationCancelled reports that the user declined the UAC prompt.
	ErrElevationCancelled = proc.ErrElevationCancelled
	// ErrNotEnded reports that Stop could not end an elevated program.
	ErrNotEnded = proc.ErrNotEnded
)

// ProcessRunner runs programs for real: without a window, in a Job Object
// (internal/winapi/proc), or as administrator through a UAC prompt.
type ProcessRunner struct{}

var _ Elevator = ProcessRunner{}

// Start implements Runner.
func (ProcessRunner) Start(cmd plugin.ProcessSpec, env []string, out io.Writer) (Process, error) {
	return proc.Start(proc.Options{Argv: cmd.Argv, Dir: cmd.Dir, Env: env, Output: out})
}

// Elevated implements Elevator.
func (ProcessRunner) Elevated() bool { return proc.IsElevated() }

// StartElevated implements Elevator with proc.StartElevated.
func (ProcessRunner) StartElevated(cmd plugin.ProcessSpec) (Process, error) {
	p, err := proc.StartElevated(cmd.Argv, cmd.Dir)
	if err != nil {
		return nil, err
	}
	return p, nil
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
	// ElevationRequired makes an unelevated start fail as CreateProcess does
	// for a program that requires administrator rights (error 740).
	ElevationRequired bool `json:"elevationRequired,omitzero"`
}

// FakeRunner records the commands it is asked to run instead of running
// them, for tests and glctl's fake environment. A command ends at once: by
// default it prints "[not run] <command line>" and succeeds.
//
// It is also a fake Elevator: StartElevated never shows a UAC prompt; it
// records the command (with Elevate set) and runs it like Start, with its
// output discarded, as a real elevated program's is.
type FakeRunner struct {
	// Results configures the output and exit code by command line prefix;
	// the first match wins.
	Results []FakeResult
	// Hook, if set, runs in place of each command (in Process.Wait): it may
	// write output and block until stop is closed, and returns the exit
	// code. It takes precedence over Results.
	Hook func(cmd plugin.ProcessSpec, out io.Writer, stop <-chan struct{}) int
	// Admin pretends golauncher runs as administrator (Elevator.Elevated).
	Admin bool
	// DeclineUAC pretends the user declines every UAC prompt.
	DeclineUAC bool
	// StopFails pretends Stop cannot end an elevated program: its Wait
	// returns ErrNotEnded.
	StopFails bool

	mu       sync.Mutex
	commands []plugin.ProcessSpec
	gate     chan struct{} // while held: closed by Hold(false)
}

var _ Elevator = (*FakeRunner)(nil)

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
	return f.start(cmd, out, false)
}

// Elevated implements Elevator.
func (f *FakeRunner) Elevated() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Admin
}

// StartElevated implements Elevator without any UAC prompt.
func (f *FakeRunner) StartElevated(cmd plugin.ProcessSpec) (Process, error) {
	cmd.Elevate = true
	return f.start(cmd, io.Discard, true)
}

func (f *FakeRunner) start(cmd plugin.ProcessSpec, out io.Writer, elevated bool) (Process, error) {
	f.mu.Lock()
	cmd.Argv = slices.Clone(cmd.Argv)
	f.commands = append(f.commands, cmd)
	hook := f.Hook
	results := f.Results
	gate := f.gate
	decline, stopFails, admin := f.DeclineUAC, f.StopFails, f.Admin
	f.mu.Unlock()
	if elevated && decline {
		return nil, ErrElevationCancelled
	}
	if !elevated && !admin {
		if r, ok := match(results, CommandLine(cmd)); ok && r.ElevationRequired {
			return nil, &os.PathError{Op: "exec", Path: cmd.Argv[0], Err: proc.ErrorElevationRequired}
		}
	}
	return &fakeProcess{cmd: cmd, out: out, hook: hook, results: results, gate: gate, stop: make(chan struct{}),
		stopFails: elevated && stopFails}, nil
}

// match returns the first result whose Match prefixes line.
func match(results []FakeResult, line string) (FakeResult, bool) {
	for _, r := range results {
		if strings.HasPrefix(line, r.Match) {
			return r, true
		}
	}
	return FakeResult{}, false
}

type fakeProcess struct {
	cmd     plugin.ProcessSpec
	out     io.Writer
	hook    func(plugin.ProcessSpec, io.Writer, <-chan struct{}) int
	results []FakeResult
	gate    chan struct{}
	once    sync.Once
	stop    chan struct{}
	// stopFails: Stop cannot end the (elevated) program; Wait returns
	// ErrNotEnded once stopped.
	stopFails bool
}

func (p *fakeProcess) Wait() (int, error) {
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-p.stop:
			return p.stoppedResult()
		}
	}
	if p.hook != nil {
		code := p.hook(p.cmd, p.out, p.stop)
		if p.stopFails {
			select {
			case <-p.stop:
				return p.stoppedResult()
			default:
			}
		}
		return code, nil
	}
	line := CommandLine(p.cmd)
	if r, ok := match(p.results, line); ok {
		io.WriteString(p.out, r.Output)
		if r.Output != "" && !strings.HasSuffix(r.Output, "\n") {
			io.WriteString(p.out, "\n")
		}
		return r.ExitCode, nil
	}
	fmt.Fprintf(p.out, "[not run] %s\n", line)
	return 0, nil
}

// stoppedResult is what Wait returns after Stop.
func (p *fakeProcess) stoppedResult() (int, error) {
	if p.stopFails {
		return -1, fmt.Errorf("%w: access denied (fake)", ErrNotEnded)
	}
	return -1, nil
}

func (p *fakeProcess) Stop() { p.once.Do(func() { close(p.stop) }) }
