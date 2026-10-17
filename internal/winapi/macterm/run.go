package macterm

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run starts l: a detached program is started and left running, any other
// is waited for, and its error output becomes the error. When it fails
// and l has a Fallback, the fallback is run instead.
func Run(l Launch) error {
	if len(l.Argv) == 0 {
		return errors.New("macterm: nothing to run")
	}
	cmd := exec.Command(l.Argv[0], l.Argv[1:]...)
	var err error
	if l.Detach {
		if err = cmd.Start(); err == nil {
			go cmd.Wait() // reap it; it outlives the action
			return nil
		}
	} else {
		var out []byte
		if out, err = cmd.CombinedOutput(); err == nil {
			return nil
		}
		if msg := strings.TrimSpace(string(out)); msg != "" {
			err = fmt.Errorf("%s: %s", filepath.Base(l.Argv[0]), msg)
		}
	}
	if l.Fallback != nil {
		return Run(*l.Fallback)
	}
	return err
}

// Problem is a missing permission an error of Run comes from.
type Problem int

const (
	// NoProblem: the error is not about permissions.
	NoProblem Problem = iota
	// NeedAutomation: the user did not allow golauncher to control the
	// app (System Settings › Privacy & Security › Automation), error -1743.
	NeedAutomation
	// NeedAccessibility: System Events may not press keys for golauncher
	// (Privacy & Security › Accessibility), errors -1719 and 1002.
	NeedAccessibility
)

// PermissionProblem tells which permission, if any, made err happen.
func PermissionProblem(err error) Problem {
	if err == nil {
		return NoProblem
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "-1743"), strings.Contains(msg, "Not authorized to send Apple events"):
		return NeedAutomation
	case strings.Contains(msg, "-1719"), strings.Contains(msg, "(1002)"), strings.Contains(msg, "not allowed to send keystrokes"),
		strings.Contains(msg, "not allowed assistive access"):
		return NeedAccessibility
	}
	return NoProblem
}
