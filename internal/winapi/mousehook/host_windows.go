//go:build windows

package mousehook

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/eziq1/golauncher/internal/ring"
	"github.com/eziq1/golauncher/internal/winapi/proc"
)

// HostSupported reports whether the hook runs in the hook host here: on
// Windows.
func HostSupported() bool { return true }

// HostMain runs the hook host (golauncher --mouse-hook-host): the hook on a
// time-critical thread, the machine, commands from in and events to out,
// until in ends.
func HostMain(in io.Reader, out io.Writer) error {
	hostParent = os.Getppid()
	return runHost(in, out, hostDeps{
		start:     func(h Handler) (func(), error) { return start(h, true) },
		replay:    Replay,
		replayNow: sendMiddle,
		appAt:     AppAt,
		threshold: DragThreshold(),
		clock:     ring.RealClock{},
	})
}

// spawnHost starts the hook host: this executable, without a console, in a
// job that ends it with golauncher (the end of its input ends it too).
func spawnHost() (*hostConn, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, HostFlag)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tail{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Without the job the end of its input still ends it, unless golauncher
	// is killed while the host hangs.
	job, _ := proc.NewKillOnCloseJob(cmd.Process.Pid)
	return &hostConn{
		in:  in,
		out: out,
		wait: func() error {
			err := cmd.Wait()
			job.Close()
			if err != nil && len(stderr.b) > 0 {
				err = fmt.Errorf("%w: %s", err, stderr.b)
			}
			return err
		},
		kill: func() { cmd.Process.Kill() },
		done: make(chan struct{}),
	}, nil
}

// tail keeps the end of what the host writes to its standard error (a
// panic), for the log.
type tail struct{ b []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if n := len(t.b); n > 2048 {
		t.b = t.b[n-2048:]
	}
	return len(p), nil
}
