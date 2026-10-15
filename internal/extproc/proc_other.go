//go:build !windows

package extproc

import "syscall"

func sysProcAttr() *syscall.SysProcAttr { return nil }

// job is a no-op outside Windows.
type job struct{}

func newJob(int) (job, error) { return job{}, nil }
func (job) terminate()        {}
func (job) close()            {}
