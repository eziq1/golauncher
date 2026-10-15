package winget

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/eziq1/golauncher/internal/winapi/proc"
)

// Runner runs a read-only winget command (list, show, source export) and
// returns what it printed. Changes never go through a Runner: they are
// background tasks of the host.
type Runner func(ctx context.Context, args []string) ([]byte, error)

// ExitError reports a winget command that ended with a non-zero exit code.
type ExitError struct {
	Code   int
	Output string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("winget exited with code %#x: %s", uint32(e.Code), lastLine(e.Output))
}

// lastLine returns the last non-empty line of s.
func lastLine(s string) string {
	lines := outputLines([]byte(s))
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes of stdout and
// stderr.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// ExecRunner runs winget at exe without a window. Cancelling ctx ends it.
func ExecRunner(exe string) Runner {
	return func(ctx context.Context, args []string) ([]byte, error) {
		var out syncBuffer
		p, err := proc.Start(proc.Options{Argv: append([]string{exe}, args...), Output: &out})
		if err != nil {
			return nil, err
		}
		stop := context.AfterFunc(ctx, p.Stop)
		defer stop()
		code, err := p.Wait()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			return nil, err
		}
		b := out.b.Bytes()
		if code != 0 {
			return b, &ExitError{Code: code, Output: string(b)}
		}
		return b, nil
	}
}

// FindWinget returns the path of winget.exe: on PATH, else the App
// Installer alias in %LOCALAPPDATA%\Microsoft\WindowsApps (a launcher
// started before PATH was set up may not see it), else "".
func FindWinget(getenv func(string) string) string {
	if p, err := exec.LookPath("winget.exe"); err == nil {
		return p
	}
	if dir := getenv("LOCALAPPDATA"); dir != "" {
		p := filepath.Join(dir, "Microsoft", "WindowsApps", "winget.exe")
		if _, err := os.Lstat(p); err == nil {
			return p
		}
	}
	return ""
}

// appInstallerState is the folder where winget keeps its local state.
func appInstallerState(getenv func(string) string) string {
	dir := getenv("LOCALAPPDATA")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "Packages", "Microsoft.DesktopAppInstaller_8wekyb3d8bbwe", "LocalState")
}

// realIndexStamp identifies the installed version of a source's index
// package: its full package name.
func realIndexStamp(src Source) (string, error) { return packageFullName(src.Data) }

// realOpenIndex opens the index of a pre-indexed source read-only.
func realOpenIndex(src Source) (*sql.DB, error) {
	full, err := packageFullName(src.Data)
	if err != nil {
		return nil, err
	}
	dir, err := packagePath(full)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "Public", "index.db")
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return OpenFile(path, true)
}

// realOpenPins returns a function that opens winget's pinning.db
// read-only.
func realOpenPins(getenv func(string) string) func() (*sql.DB, error) {
	return func() (*sql.DB, error) {
		dir := appInstallerState(getenv)
		if dir == "" {
			return nil, os.ErrNotExist
		}
		path := filepath.Join(dir, "pinning.db")
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
		return OpenFile(path, false)
	}
}
