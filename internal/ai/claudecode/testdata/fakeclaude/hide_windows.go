package main

import (
	"os/exec"
	"syscall"
)

// hide keeps cmd from opening a console window (the fake runs without one).
func hide(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
