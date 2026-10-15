//go:build windows

package scripts

import (
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// prepare sets up cmd to run without a console window. Batch files get a
// cmd.exe command line of their own: cmd does not parse arguments the way
// other programs do.
func prepare(cmd *exec.Cmd, it Interpreter, script string, args []string) {
	attr := &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if it.Batch {
		var b strings.Builder
		b.WriteString(syscall.EscapeArg(it.Program))
		b.WriteString(` /d /s /c "`)
		b.WriteString(cmdQuote(script))
		for _, a := range args {
			b.WriteByte(' ')
			b.WriteString(cmdQuote(a))
		}
		b.WriteByte('"')
		attr.CmdLine = b.String()
	}
	cmd.SysProcAttr = attr
}

// cmdQuote quotes an argument for a batch file. Quotes inside are doubled;
// cmd.exe has no way to pass them through unchanged.
func cmdQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// procGroup is the started process and everything it starts, held in a job
// object so that Stop ends the whole tree.
type procGroup struct {
	job windows.Handle
}

// newProcGroup puts the started process pid in a new job object. A process
// the script starts before this runs escapes the job; Kill still ends the
// script itself.
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

// close releases the job object. Processes still running (e.g. an app the
// script opened) keep running.
func (g *procGroup) close() {
	if g.job != 0 {
		windows.CloseHandle(g.job)
		g.job = 0
	}
}

// decodeLegacy decodes output that is not UTF-8 from the OEM code page,
// which console programs such as cmd.exe and Windows PowerShell write to
// pipes.
func decodeLegacy(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	const cpOEM = 1 // CP_OEMCP
	n, err := windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), nil, 0)
	if err != nil || n == 0 {
		return strings.ToValidUTF8(string(b), "\ufffd")
	}
	w := make([]uint16, n)
	n, err = windows.MultiByteToWideChar(cpOEM, 0, &b[0], int32(len(b)), &w[0], n)
	if err != nil {
		return strings.ToValidUTF8(string(b), "\ufffd")
	}
	return string(utf16.Decode(w[:n]))
}
