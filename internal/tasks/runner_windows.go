package tasks

import (
	"github.com/eziq1/golauncher/internal/winapi/proc"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Only Windows has a way to run a program as administrator (the UAC
// prompt): elsewhere ProcessRunner is no Elevator, so an Elevate task fails
// with tasks.error.noElevation and no "Retry as Administrator" is offered
// (docs/macos-port.md §10).

var _ Elevator = ProcessRunner{}

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
