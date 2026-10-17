//go:build !windows

package shell

import (
	"os"
	"os/exec"
	"syscall"
)

// startBackground starts this program as the launcher, without showing it
// and without the installer, in a session of its own (so that closing the
// AI app that runs the bridge does not end the launcher).
func startBackground() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "--background", "--portable")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
