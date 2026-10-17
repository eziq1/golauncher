//go:build !windows

package extproc

import "syscall"

// sysProcAttr starts plugin processes in a process group of their own, so
// that the plugin and everything it starts can be ended together.
func sysProcAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// job is the process group a plugin process leads, the Unix stand-in for a
// Job Object. Unlike one it does not end the plugin when the launcher
// crashes; plugins exit on their own when stdin reaches EOF (rpcserve does).
type job struct{ pgid int }

func newJob(pid int) (job, error) { return job{pid}, nil }

// terminate ends every process in the group.
func (j job) terminate() {
	if j.pgid > 0 {
		_ = syscall.Kill(-j.pgid, syscall.SIGKILL)
	}
}

// close ends processes still in the group once the plugin process exited,
// as closing a Job Object with KILL_ON_JOB_CLOSE does.
func (j job) close() { j.terminate() }
