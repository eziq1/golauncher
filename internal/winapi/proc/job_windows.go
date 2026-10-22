//go:build windows

package proc

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job is a Job Object created with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: when
// golauncher exits for any reason (a crash included) and its handle closes,
// Windows ends the processes in it. Plugin processes and the mouse hook's
// host run in one.
type Job struct{ h windows.Handle }

// NewKillOnCloseJob puts the started process pid in a new such job.
func NewKillOnCloseJob(pid int) (Job, error) {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return Job{}, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(h)
		return Job{}, err
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(h)
		return Job{}, err
	}
	defer windows.CloseHandle(ph)
	if err := windows.AssignProcessToJobObject(h, ph); err != nil {
		windows.CloseHandle(h)
		return Job{}, err
	}
	return Job{h}, nil
}

// Terminate ends every process in the job.
func (j Job) Terminate() {
	if j.h != 0 {
		_ = windows.TerminateJobObject(j.h, 1)
	}
}

// Close releases the job, ending processes still in it.
func (j Job) Close() {
	if j.h != 0 {
		_ = windows.CloseHandle(j.h)
	}
}
