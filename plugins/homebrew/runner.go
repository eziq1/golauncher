package homebrew

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/eziq1/golauncher/internal/winapi/proc"
)

// Runner runs a read-only brew command (info, --cache) and returns what it
// printed on stdout. Changes never go through a Runner: they are background
// tasks of the host.
type Runner func(ctx context.Context, args []string) ([]byte, error)

// ExitError reports a brew command that ended with a non-zero exit code.
type ExitError struct {
	Code   int
	Output string // stderr
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("brew exited with code %d: %s", e.Code, lastLine(e.Output))
}

// lastLine returns the last non-empty line of s.
func lastLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// readEnv is added to the environment of read-only commands: they must not
// update Homebrew or its API files (which can take a minute and goes
// online), nor print hints.
var readEnv = []string{"HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ENV_HINTS=1", "HOMEBREW_NO_COLOR=1", "HOMEBREW_NO_EMOJI=1"}

// lockedBuffer is a bytes.Buffer safe for concurrent writes.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *lockedBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// ExecRunner runs the brew at exe. Cancelling ctx ends it (and the Ruby
// processes it started).
func ExecRunner(exe string) Runner {
	return func(ctx context.Context, args []string) ([]byte, error) {
		var out, errOut lockedBuffer
		p, err := proc.Start(proc.Options{Argv: append([]string{exe}, args...), Env: proc.MergeEnv(os.Environ(), readEnv),
			Output: &out, Stderr: &errOut})
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
			return b, &ExitError{Code: code, Output: errOut.b.String()}
		}
		return b, nil
	}
}

// brewLocations are where Homebrew installs itself: Apple silicon, Intel
// Macs, Linux.
var brewLocations = []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew", "/home/linuxbrew/.linuxbrew/bin/brew"}

// FindBrew returns the path of brew: in one of Homebrew's default
// locations (a launcher started from the Finder has a minimal PATH), else
// on PATH, else "". exists and lookPath stand in for the file system in
// tests (nil: the real ones).
func FindBrew(exists func(string) bool, lookPath func(string) (string, error)) string {
	if exists == nil {
		exists = func(p string) bool {
			st, err := os.Stat(p)
			return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
		}
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if runtime.GOOS == "windows" {
		return ""
	}
	for _, p := range brewLocations {
		if exists(p) {
			return p
		}
	}
	if p, err := lookPath("brew"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	return ""
}

// defaultCache is Homebrew's default cache directory.
func defaultCache(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Caches", "Homebrew")
	}
	return filepath.Join(home, ".cache", "Homebrew")
}
