package process

// The platform-independent part of the macOS backend (process_darwin.go):
// parsing ps output and KERN_PROCARGS2 buffers, the app bundle of an
// executable, the processes macOS must keep, and the AppleScript that runs
// the elevated helper. Kept here so that it is tested on any platform.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"
)

// unixFiletime converts a Unix time in microseconds to a FILETIME, the unit
// of ID.Created on every platform.
func unixFiletime(usec int64) int64 { return usec*10 + 116444736000000000 }

// psRow is a line of `ps -axo pid=,rss=,time=`.
type psRow struct {
	PID uint32
	// RSS is the resident size in bytes.
	RSS uint64
	// CPU is the processor time used so far, in 100 ns.
	CPU int64
}

// parsePS reads the output of `ps -axo pid=,rss=,time=`. Lines that do not
// parse are skipped.
func parsePS(out []byte) []psRow {
	var rows []psRow
	for line := range strings.Lines(string(out)) {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		pid, err1 := strconv.ParseUint(f[0], 10, 32)
		rss, err2 := strconv.ParseUint(f[1], 10, 64)
		cpu, err3 := parseCPUTime(f[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		rows = append(rows, psRow{PID: uint32(pid), RSS: rss * 1024, CPU: int64(cpu / 100)})
	}
	return rows
}

// parseCPUTime reads a processor time as ps prints it: "0:01.23",
// "123:45.67" (minutes can exceed 59), "1:02:03" or "2-01:02:03".
func parseCPUTime(s string) (time.Duration, error) {
	bad := fmt.Errorf("process: bad CPU time %q", s)
	var days int64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil || n < 0 {
			return 0, bad
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, bad
	}
	secs, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil || secs < 0 || strings.ContainsAny(parts[len(parts)-1], "eE+-") {
		return 0, bad
	}
	var whole int64
	for _, p := range parts[:len(parts)-1] {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return 0, bad
		}
		whole = whole*60 + n
	}
	// whole is in minutes: "mm" or "hh:mm".
	total := time.Duration(days)*24*time.Hour + time.Duration(whole)*time.Minute
	return total + time.Duration(secs*float64(time.Second)+0.5), nil
}

// cpuSmoother turns the processor times of processes that only ps can
// read (those of other users), read every few seconds, into counters that
// grow smoothly at every snapshot: between two readings a counter grows at
// the rate measured between the two previous ones. Counters never go back,
// so Rates sees a steady use rather than nothing and then a burst.
type cpuSmoother struct {
	m map[ID]smoothed
}

type smoothed struct {
	real int64     // last reading
	at   time.Time // when it was read
	base int64     // the counter at at
	rate float64   // growth per second
}

// update records a reading of id's counter at t.
func (s *cpuSmoother) update(id ID, counter int64, t time.Time) {
	if s.m == nil {
		s.m = map[ID]smoothed{}
	}
	old, ok := s.m[id]
	if !ok || !t.After(old.at) {
		s.m[id] = smoothed{real: counter, at: t, base: counter}
		return
	}
	dt := t.Sub(old.at).Seconds()
	rate := max(float64(counter-old.real)/dt, 0)
	s.m[id] = smoothed{real: counter, at: t, base: max(counter, old.value(t)), rate: rate}
}

func (v smoothed) value(t time.Time) int64 {
	d := t.Sub(v.at).Seconds()
	if d < 0 {
		d = 0
	}
	return v.base + int64(v.rate*d)
}

// value is id's counter at t, and whether id has been read.
func (s *cpuSmoother) value(id ID, t time.Time) (int64, bool) {
	v, ok := s.m[id]
	if !ok {
		return 0, false
	}
	return v.value(t), true
}

// keep forgets the processes not in alive.
func (s *cpuSmoother) keep(alive map[ID]bool) {
	for id := range s.m {
		if !alive[id] {
			delete(s.m, id)
		}
	}
}

// parseProcArgs reads a KERN_PROCARGS2 buffer: argc (int32), the
// executable path, NUL padding, then argc NUL-terminated arguments (the
// environment follows, and is never read).
func parseProcArgs(b []byte) (exe string, args []string, err error) {
	if len(b) < 4 {
		return "", nil, errors.New("process: short argument buffer")
	}
	argc := int(int32(binary.LittleEndian.Uint32(b)))
	if argc < 0 || argc > 1<<16 {
		return "", nil, errors.New("process: bad argument count")
	}
	b = b[4:]
	i := bytes.IndexByte(b, 0)
	if i < 0 {
		return "", nil, errors.New("process: no executable path")
	}
	exe, b = string(b[:i]), b[i:]
	for len(b) > 0 && b[0] == 0 {
		b = b[1:]
	}
	for len(args) < argc && len(b) > 0 {
		i := bytes.IndexByte(b, 0)
		if i < 0 {
			args = append(args, string(b))
			break
		}
		args = append(args, string(b[:i]))
		b = b[i+1:]
	}
	return exe, args, nil
}

// joinArgs joins arguments into one command line, quoting those with
// spaces, quotes or nothing in double quotes, as RedactCommandLine expects.
func joinArgs(args []string) string {
	var sb strings.Builder
	for i, a := range args {
		if i > 0 {
			sb.WriteByte(' ')
		}
		if a != "" && !strings.ContainsAny(a, " \t\"") {
			sb.WriteString(a)
			continue
		}
		sb.WriteByte('"')
		sb.WriteString(strings.ReplaceAll(a, `"`, `\"`))
		sb.WriteByte('"')
	}
	return sb.String()
}

// outermostBundle returns the outermost .app bundle the executable at p is
// in ("/Applications/Fabrikam.app" for its helpers deep inside), or "".
func outermostBundle(p string) string {
	out := ""
	for d := p; d != "/" && d != "." && d != ""; d = path.Dir(d) {
		if strings.HasSuffix(d, ".app") {
			out = d
		}
	}
	return out
}

// innermostBundle returns the .app bundle the executable at p is directly
// in, or "".
func innermostBundle(p string) string {
	for d := p; d != "/" && d != "." && d != ""; d = path.Dir(d) {
		if strings.HasSuffix(d, ".app") {
			return d
		}
	}
	return ""
}

// macCritical reports whether macOS needs the process to keep running:
// launchd, and the window server and login window from /System (ending
// either signs the user out).
func macCritical(pid uint32, name, exe string) bool {
	if pid == 1 {
		return true
	}
	switch name {
	case "WindowServer", "loginwindow":
		return exe == "" || strings.HasPrefix(exe, "/System/")
	}
	return false
}

// shellQuote quotes s for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// elevateScript is the AppleScript that runs argv as root after macOS
// asked for an administrator's password: argv is passed to osascript as
// arguments (never pasted into the script), the first being the prompt.
// The shell command prints the exit code, so a failing helper is not taken
// for a declined prompt.
const elevateScript = `on run argv
do shell script (item 2 of argv) with prompt (item 1 of argv) with administrator privileges
end run`

// elevateArgv is the osascript command line that runs exe with args as
// root.
func elevateArgv(prompt, exe string, args []string) []string {
	parts := []string{shellQuote(exe)}
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	cmd := strings.Join(parts, " ") + "; echo $?"
	if prompt == "" {
		prompt = "golauncher needs administrator rights to end a process."
	}
	return []string{"/usr/bin/osascript", "-e", elevateScript, prompt, cmd}
}

// elevateResult reads the outcome of elevateArgv: the helper's exit code
// from its output, or ErrCancelled when the user declined (AppleScript
// error -128).
func elevateResult(stdout, stderr string, runErr error) (int, error) {
	if runErr != nil {
		if strings.Contains(stderr, "(-128)") {
			return 0, ErrCancelled
		}
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			return 0, fmt.Errorf("process: osascript: %w", runErr)
		}
		return 0, fmt.Errorf("process: osascript: %w: %s", runErr, msg)
	}
	lines := strings.Fields(stdout)
	if len(lines) == 0 {
		return 0, errors.New("process: the helper printed no exit code")
	}
	code, err := strconv.Atoi(lines[len(lines)-1])
	if err != nil {
		return 0, fmt.Errorf("process: the helper printed %q", lines[len(lines)-1])
	}
	return code, nil
}

type promptKey struct{}

// WithPrompt returns ctx carrying the text of the administrator prompt
// that RunElevated shows on macOS (Windows' UAC prompt has its own).
func WithPrompt(ctx context.Context, prompt string) context.Context {
	return context.WithValue(ctx, promptKey{}, prompt)
}

func promptOf(ctx context.Context) string {
	s, _ := ctx.Value(promptKey{}).(string)
	return s
}
