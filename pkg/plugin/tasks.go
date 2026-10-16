package plugin

import (
	"context"
	"io"
)

// EventTaskFinished is published when a background task ended (any
// outcome). Like every event it carries no payload: to learn which task
// ended and how, set TaskSpec.OnDone.
const EventTaskFinished = "task-finished"

// NotifyMode controls the notification the host shows when a task ends.
type NotifyMode string

const (
	// NotifyOnFailure (the default) notifies when the task failed.
	NotifyOnFailure NotifyMode = ""
	// NotifyAlways notifies whatever the outcome, except after Stop.
	NotifyAlways NotifyMode = "always"
	// NotifyNever never notifies.
	NotifyNever NotifyMode = "never"
)

// TaskSpec describes a background task (docs/background-tasks.md).
type TaskSpec struct {
	Title    string `json:"title"`              // e.g. "Update All Apps"
	Subtitle string `json:"subtitle,omitempty"` // e.g. "scoop update *"
	Icon     Icon   `json:"icon,omitzero"`
	// Queue serializes tasks with the same non-empty key, also across
	// plugins (e.g. "scoop": Scoop must not run two operations at once).
	// Tasks in different queues run in parallel, up to the host's limit.
	Queue string `json:"queue,omitempty"`
	// Notify controls the notification when the task ends.
	Notify NotifyMode `json:"notify,omitempty"`
	// OnDone, if set, is called once on a background goroutine after the
	// task ended, with its result. It is how a plugin learns which of its
	// tasks finished (e.g. to reload its data). It is not serialized: an
	// external plugin gets the result in the taskFinished notification
	// instead (rpcserve calls OnDone for it). A rerun from the task view
	// calls the same OnDone again.
	OnDone func(TaskResult) `json:"-"`
}

// ProcessSpec is a command line the host runs as a task: without a window,
// in a Job Object (Stop or quitting golauncher ends the whole process
// tree), with stdout and stderr captured as the task's console output.
// Output that is not UTF-8 is decoded from the OEM code page; ANSI escapes
// and text overwritten with \r are removed. A batch file (.bat, .cmd) as
// Argv[0] is run through cmd.exe with its own quoting rules. A task that
// fails because the program needs administrator rights gets a "Retry as
// Administrator" action in the task view (see Elevate).
type ProcessSpec struct {
	Argv []string `json:"argv"`
	Dir  string   `json:"dir,omitempty"`
	// Env holds NAME=value entries added to the host's environment; they
	// replace variables of the same name.
	Env []string `json:"env,omitempty"`
	// CommandLine is how the command is shown to the user (default: Argv
	// joined with spaces). Set it when Argv is an implementation detail,
	// e.g. an encoded PowerShell script.
	CommandLine string `json:"commandLine,omitempty"`
	// Elevate runs the program as administrator: the host shows the UAC
	// prompt (ShellExecuteEx "runas", hidden window) when the task starts.
	// Its output cannot be captured (the task log says so), only its exit
	// code; Env is not applied; Stop ends the program itself if Windows
	// lets it, not the programs it started. Declining the prompt ends the
	// task as cancelled. When golauncher already runs as administrator the
	// program runs as usual, with its output. Keep elevated commands short
	// and tell the user before (e.g. "Requires administrator" in the
	// subtitle). For a chain of commands, run cmd.exe /d /c "a && b".
	Elevate bool `json:"elevate,omitzero"`
}

// Task is handed to a Go task function (Host.RunTask) to report progress
// and console output. It is safe for concurrent use.
type Task interface {
	// Write appends console output; it is split into lines.
	io.Writer
	// SetStatus sets a one-line status, e.g. "Downloading 3/5".
	SetStatus(status string)
	// SetProgress sets the progress in [0, 1]; a negative value means
	// indeterminate.
	SetProgress(fraction float64)
}

// TaskFunc is the body of a Go task. It must return soon after ctx is
// cancelled (Stop or quit).
type TaskFunc func(ctx context.Context, t Task) error

// TaskState is the state of a task.
type TaskState string

const (
	TaskQueued    TaskState = "queued"
	TaskRunning   TaskState = "running"
	TaskSucceeded TaskState = "succeeded"
	TaskFailed    TaskState = "failed"
	TaskCancelled TaskState = "cancelled"
)

// Finished reports whether s is a final state.
func (s TaskState) Finished() bool {
	return s == TaskSucceeded || s == TaskFailed || s == TaskCancelled
}

// TaskResult is how a task ended, passed to TaskSpec.OnDone.
type TaskResult struct {
	ID    string    `json:"id"`
	State TaskState `json:"state"`
	// ExitCode is the exit code of a process task (-1 if it did not start
	// or was stopped).
	ExitCode int `json:"exitCode"`
	// Error describes a failure other than a non-zero exit code.
	Error string `json:"error,omitempty"`
	// LastLine is the last non-empty line of output.
	LastLine string `json:"lastLine,omitempty"`
}

// RunProcessPayload is the Payload of ActionRunProcess.
type RunProcessPayload struct {
	Spec TaskSpec    `json:"spec"`
	Cmd  ProcessSpec `json:"cmd"`
}
