package shell

import (
	"os"
	"os/exec"
	"syscall"
)

// Process creation flags (windows.h).
const (
	createNewProcessGroup  = 0x00000200
	createBreakawayFromJob = 0x01000000
)

// startBackground starts this program as the launcher, without showing it
// and without the installer, outside the job of the AI app that runs the
// bridge (so that closing the app does not end the launcher).
func startBackground() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	start := func(flags uint32) error {
		cmd := exec.Command(exe, "--background", "--portable")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	if err := start(createNewProcessGroup | createBreakawayFromJob); err != nil {
		// The job may not allow breaking away.
		return start(createNewProcessGroup)
	}
	return nil
}
