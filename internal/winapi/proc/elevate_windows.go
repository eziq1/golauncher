//go:build windows

package proc

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32             = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = shell32.NewProc("ShellExecuteExW")
)

const (
	errorCancelled = windows.Errno(1223) // ERROR_CANCELLED
	swHide         = 0
	seeMaskNoClose = 0x40 // SEE_MASK_NOCLOSEPROCESS
)

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize        uint32
	mask          uint32
	hwnd          uintptr
	verb          *uint16
	file          *uint16
	parameters    *uint16
	directory     *uint16
	show          int32
	instApp       uintptr
	idList        uintptr
	class         *uint16
	keyClass      uintptr
	hotKey        uint32
	iconOrMonitor uintptr
	process       windows.Handle
}

// IsElevated reports whether this process runs as administrator.
func IsElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// ElevatedCommand returns the file and the parameters ShellExecuteEx gets
// to run argv: argv[0] and the other arguments quoted the way os/exec
// quotes them (syscall.EscapeArg), or, for a batch file, comspec with the
// cmd.exe line BatchCommandLine builds.
func ElevatedCommand(argv []string, comspec string) (file, params string) {
	if IsBatch(argv[0]) {
		line := BatchCommandLine(comspec, argv[0], argv[1:])
		return comspec, strings.TrimPrefix(line, syscall.EscapeArg(comspec)+" ")
	}
	parts := make([]string, len(argv)-1)
	for i, a := range argv[1:] {
		parts[i] = syscall.EscapeArg(a)
	}
	return argv[0], strings.Join(parts, " ")
}

// Elevated is a program started as administrator by StartElevated.
type Elevated struct {
	h    windows.Handle
	stop chan struct{}

	mu      sync.Mutex
	stopped bool
	closed  bool
	stopErr error
}

// StartElevated starts argv as administrator in folder dir through the
// UAC prompt (ShellExecuteEx "runas", hidden window), and returns once the
// user answered it: ErrElevationCancelled when they declined. The program
// gets its own environment, not golauncher's, and its output is lost. It
// never runs in a test binary.
func StartElevated(argv []string, dir string) (*Elevated, error) {
	if len(argv) == 0 || argv[0] == "" {
		return nil, errors.New("proc: no program")
	}
	if testing.Testing() {
		return nil, errElevateInTest
	}
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	file, params := ElevatedCommand(argv, comspec)
	info := shellExecuteInfo{mask: seeMaskNoClose, show: swHide}
	info.cbSize = uint32(unsafe.Sizeof(info))
	var err error
	if info.verb, err = windows.UTF16PtrFromString("runas"); err != nil {
		return nil, err
	}
	if info.file, err = windows.UTF16PtrFromString(file); err != nil {
		return nil, err
	}
	if info.parameters, err = windows.UTF16PtrFromString(params); err != nil {
		return nil, err
	}
	if dir != "" {
		if info.directory, err = windows.UTF16PtrFromString(dir); err != nil {
			return nil, err
		}
	}
	err = func() error {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		// S_OK or S_FALSE (already initialized) both need CoUninitialize.
		if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil || err == windows.Errno(1) {
			defer windows.CoUninitialize()
		}
		if r, _, e := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
			if errors.Is(e, errorCancelled) {
				return ErrElevationCancelled
			}
			return fmt.Errorf("proc: ShellExecuteEx: %w", e)
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}
	if info.process == 0 {
		return nil, errors.New("proc: ShellExecuteEx returned no process")
	}
	return &Elevated{h: info.process, stop: make(chan struct{})}, nil
}

// Wait waits for the program to end and returns its exit code. After a
// Stop that could not end the program it returns at once with ErrNotEnded.
func (p *Elevated) Wait() (int, error) {
	defer p.close()
	for {
		ev, err := windows.WaitForSingleObject(p.h, 100)
		if err != nil {
			return -1, fmt.Errorf("proc: waiting for the elevated program: %w", err)
		}
		if ev == windows.WAIT_OBJECT_0 {
			break
		}
		select {
		case <-p.stop:
			p.mu.Lock()
			err := p.stopErr
			p.mu.Unlock()
			if err != nil {
				return -1, fmt.Errorf("%w: %v", ErrNotEnded, err)
			}
		default:
		}
	}
	var code uint32
	if err := windows.GetExitCodeProcess(p.h, &code); err != nil {
		return -1, fmt.Errorf("proc: GetExitCodeProcess: %w", err)
	}
	return int(code), nil
}

// Stop ends the program with TerminateProcess on the handle ShellExecuteEx
// returned. It ends the program itself, not what it started (an elevated
// program cannot be put in golauncher's job object). When that fails,
// Wait stops waiting and the program keeps running.
func (p *Elevated) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.closed {
		return
	}
	p.stopped = true
	if err := windows.TerminateProcess(p.h, 1); err != nil {
		p.stopErr = err
	}
	close(p.stop)
}

func (p *Elevated) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		windows.CloseHandle(p.h)
	}
}
