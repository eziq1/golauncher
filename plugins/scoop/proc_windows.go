//go:build windows

package scoop

// Copied from plugins/scripts (plugins may not import each other).

import (
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// prepare sets up cmd to run without a console window.
func prepare(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}

// procGroup is the started process and everything it starts, held in a job
// object so that Stop ends the whole tree.
type procGroup struct {
	job windows.Handle
}

// newProcGroup puts the started process pid in a new job object. A process
// started before this runs escapes the job; Kill still ends the process
// itself.
func newProcGroup(pid int) *procGroup {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return &procGroup{}
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(job)
		return &procGroup{}
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return &procGroup{}
	}
	return &procGroup{job: job}
}

// kill ends every process of the group.
func (g *procGroup) kill() {
	if g.job != 0 {
		_ = windows.TerminateJobObject(g.job, 1)
	}
}

// close releases the job object.
func (g *procGroup) close() {
	if g.job != 0 {
		windows.CloseHandle(g.job)
		g.job = 0
	}
}

// decodeLegacy decodes output that is not UTF-8 from the OEM code page.
func decodeLegacy(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	const cpOEM = 1 // CP_OEMCP
	n, err := windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n == 0 {
		return strings.ToValidUTF8(string(b), "�")
	}
	w := make([]uint16, n)
	n, err = windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), &w[0], n)
	if err != nil {
		return strings.ToValidUTF8(string(b), "�")
	}
	return string(utf16.Decode(w[:n]))
}
