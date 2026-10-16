//go:build !windows

package main

import "os/exec"

// hide does nothing outside Windows.
func hide(*exec.Cmd) {}
