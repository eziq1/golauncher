//go:build windows

package proc

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// command returns the command for argv, set up to run without a console
// window. A batch file gets a cmd.exe command line of its own: cmd does not
// parse arguments the way other programs do.
func command(argv []string) *exec.Cmd {
	attr := &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	var cmd *exec.Cmd
	if IsBatch(argv[0]) {
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			comspec = "cmd.exe"
		}
		cmd = exec.Command(comspec)
		attr.CmdLine = BatchCommandLine(comspec, argv[0], argv[1:])
	} else {
		cmd = exec.Command(argv[0], argv[1:]...)
	}
	cmd.SysProcAttr = attr
	return cmd
}

// BatchCommandLine is the cmd.exe command line that runs the batch file
// script with args: comspec /d /s /c ""script" "arg"...".
func BatchCommandLine(comspec, script string, args []string) string {
	var b strings.Builder
	b.WriteString(syscall.EscapeArg(comspec))
	b.WriteString(` /d /s /c "`)
	b.WriteString(cmdQuote(script))
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(cmdQuote(a))
	}
	b.WriteByte('"')
	return b.String()
}

// cmdQuote quotes an argument for a batch file. Quotes inside are doubled;
// cmd.exe has no way to pass them through unchanged.
func cmdQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// envKey is how environment variable names compare: case-insensitively.
func envKey(name string) string { return strings.ToUpper(name) }

// group is the started process and everything it starts, held in a job
// object so that Stop ends the whole tree.
type group struct {
	job windows.Handle
}

// newGroup puts the started process pid in a new job object. A process
// started before this runs escapes the job; Kill still ends the process
// itself.
func newGroup(pid int) *group {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return &group{}
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		windows.CloseHandle(job)
		return &group{}
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		windows.CloseHandle(job)
		return &group{}
	}
	return &group{job: job}
}

// kill ends every process of the group.
func (g *group) kill() {
	if g.job != 0 {
		_ = windows.TerminateJobObject(g.job, 1)
	}
}

// close releases the job object. Processes still running (e.g. an app the
// program opened) keep running.
func (g *group) close() {
	if g.job != 0 {
		windows.CloseHandle(g.job)
		g.job = 0
	}
}

// DecodeLegacy decodes output that is not UTF-8 from the OEM code page.
func DecodeLegacy(b []byte) string {
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
