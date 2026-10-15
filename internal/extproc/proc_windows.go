package extproc

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sysProcAttr starts plugin processes without a console window.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}

// job is a Windows Job Object holding a plugin process and its children.
// It is created with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, so when the
// launcher exits for any reason (including a crash) and its handle closes,
// Windows ends the plugin processes too.
type job struct{ h windows.Handle }

func newJob(pid int) (job, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return job{}, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return job{}, err
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(h)
		return job{}, err
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(h, ph); err != nil {
		windows.CloseHandle(h)
		return job{}, err
	}
	return job{h}, nil
}

// terminate ends every process in the job.
func (j job) terminate() {
	if j.h != 0 {
		_ = windows.TerminateJobObject(j.h, 1)
	}
}

// close releases the job, ending processes still in it.
func (j job) close() {
	if j.h != 0 {
		_ = windows.CloseHandle(j.h)
	}
}
