package tasks

import (
	"fmt"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// ShouldNotify applies a task's notification policy: never after Stop;
// NotifyAlways for every other outcome; NotifyOnFailure (the default) for
// failures only.
func ShouldNotify(i Info) bool {
	switch {
	case i.Notify == plugin.NotifyNever || i.State == plugin.TaskCancelled || !i.State.Finished():
		return false
	case i.Notify == plugin.NotifyAlways:
		return true
	}
	return i.State == plugin.TaskFailed
}

// FinishToast is the notification for a finished task.
func FinishToast(l plugin.Localizer, i Info) plugin.Toast {
	switch i.State {
	case plugin.TaskSucceeded:
		return plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("tasks.notify.succeeded", "title", i.Title), Message: i.LastLine}
	case plugin.TaskCancelled:
		return plugin.Toast{Style: plugin.ToastInfo, Title: l.T("tasks.notify.cancelled", "title", i.Title)}
	}
	msg := i.LastLine
	switch {
	case i.Error != "":
		msg = i.Error
	case msg == "" && i.ExitCode != 0:
		msg = l.T("tasks.notify.exitCode", "code", i.ExitCode)
	}
	return plugin.Toast{Style: plugin.ToastFailure, Title: l.T("tasks.notify.failed", "title", i.Title), Message: msg}
}

// StateText describes the state of a task, e.g. "Failed (exit code 2)".
func StateText(l plugin.Localizer, i Info) string {
	switch i.State {
	case plugin.TaskQueued:
		return l.T("tasks.state.queued")
	case plugin.TaskRunning:
		if i.Status != "" {
			return i.Status
		}
		return l.T("tasks.state.running")
	case plugin.TaskSucceeded:
		return l.T("tasks.state.succeeded")
	case plugin.TaskCancelled:
		return l.T("tasks.state.cancelled")
	}
	switch {
	case i.Interrupted:
		return l.T("tasks.state.interrupted")
	case i.Error != "":
		return l.T("tasks.state.error", "error", i.Error)
	case i.ExitCode != 0:
		return l.T("tasks.state.failedCode", "code", i.ExitCode)
	}
	return l.T("tasks.state.failed")
}

// Accessory is the short text at the right of a task row: the elapsed time
// and progress while running, the outcome when finished.
func Accessory(l plugin.Localizer, i Info, now time.Time) string {
	switch i.State {
	case plugin.TaskQueued:
		return l.T("tasks.accessory.queued")
	case plugin.TaskRunning:
		elapsed := Clock(now.Sub(i.Started))
		if p := i.Progress; p != nil && *p >= 0 {
			return l.T("tasks.accessory.progress", "elapsed", elapsed, "percent", int(*p*100+0.5))
		}
		return elapsed
	case plugin.TaskSucceeded:
		return l.T("tasks.accessory.succeeded", "duration", Duration(l, i.Ended.Sub(i.Started)))
	case plugin.TaskCancelled:
		return l.T("tasks.accessory.cancelled")
	}
	if i.ExitCode > 0 || i.ExitCode < -1 {
		return l.T("tasks.accessory.failedCode", "code", i.ExitCode)
	}
	return l.T("tasks.accessory.failed")
}

// Clock formats an elapsed time as "03:12" or "1:03:12".
func Clock(d time.Duration) string {
	s := int(max(d, 0) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// Duration formats a duration as "45s", "2m 31s" or "1h 5m".
func Duration(l plugin.Localizer, d time.Duration) string {
	s := int(max(d, 0) / time.Second)
	switch {
	case s < 60:
		return l.T("tasks.duration.seconds", "s", s)
	case s < 3600:
		return l.T("tasks.duration.minutes", "m", s/60, "s", s%60)
	}
	return l.T("tasks.duration.hours", "h", s/3600, "m", s/60%60)
}
