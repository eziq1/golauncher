package extproc

import (
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/eziq1/golauncher/internal/winapi/proc"
)

// sysProcAttr starts plugin processes without a console window.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}

// job is a Windows Job Object holding a plugin process and its children
// (proc.Job): when the launcher exits for any reason (including a crash)
// and its handle closes, Windows ends the plugin processes too.
type job struct{ j proc.Job }

func newJob(pid int) (job, error) {
	j, err := proc.NewKillOnCloseJob(pid)
	return job{j}, err
}

// terminate ends every process in the job.
func (j job) terminate() { j.j.Terminate() }

// close releases the job, ending processes still in it.
func (j job) close() { j.j.Close() }
