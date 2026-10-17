//go:build !windows

package proc

import (
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// command returns the command for argv, set up to lead a process group of
// its own so that Stop can end everything it starts.
func command(argv []string) *exec.Cmd {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// envKey is how environment variable names compare: case-sensitively.
func envKey(name string) string { return name }

// group is the process group the started process leads (its pgid is the
// process's pid), so that Stop ends the whole tree, as a Job Object does on
// Windows. A process that leaves the group (setsid, a daemon) escapes it.
type group struct {
	mu   sync.Mutex
	pgid int
}

func newGroup(pid int) *group { return &group{pgid: pid} }

// rekill are the delays after which kill signals the group again: a process
// that was being forked when the first signal arrived can escape it.
var rekill = []time.Duration{20 * time.Millisecond, 200 * time.Millisecond}

// kill ends every process of the group.
func (g *group) kill() {
	if !g.signal() {
		return
	}
	go func() {
		for _, d := range rekill {
			time.Sleep(d)
			if !g.signal() {
				return
			}
		}
	}()
}

// signal sends SIGKILL to the group; false when it is gone or forgotten.
func (g *group) signal() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pgid > 0 && syscall.Kill(-g.pgid, syscall.SIGKILL) == nil
}

// close forgets the group once the leader was waited for. Processes still
// running (e.g. an app the program opened) keep running.
func (g *group) close() {
	g.mu.Lock()
	g.pgid = 0
	g.mu.Unlock()
}

// DecodeLegacy replaces invalid UTF-8.
func DecodeLegacy(b []byte) string { return strings.ToValidUTF8(string(b), "�") }
