package tasks

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/eziq1/golauncher/internal/winapi/proc"
)

// output is the console output of a task: cleaned lines kept in memory up
// to a size limit (the oldest are dropped) and written in full to a log
// file. It is an io.Writer safe for concurrent use.
type output struct {
	max    int
	onLine func() // called after lines were added (without the lock)

	lw proc.LineWriter

	mu      sync.Mutex
	lines   []string // lines[start:] are kept
	start   int
	size    int // bytes of the kept lines, counting a newline each
	dropped bool
	last    string // the last non-empty line
	log     io.WriteCloser
	added   bool // lines were added since the last onLine call
}

func newOutput(max int, log io.WriteCloser, onLine func()) *output {
	o := &output{max: max, log: log, onLine: onLine}
	o.lw.OnLine = o.addLine
	return o
}

// Write implements io.Writer.
func (o *output) Write(p []byte) (int, error) {
	n, err := o.lw.Write(p)
	o.notify()
	return n, err
}

// addLine is the LineWriter's callback.
func (o *output) addLine(line string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines = append(o.lines, line)
	o.size += len(line) + 1
	if strings.TrimSpace(line) != "" {
		o.last = strings.TrimSpace(line)
	}
	if o.log != nil {
		_, _ = io.WriteString(o.log, line+"\n")
	}
	for o.size > o.max && len(o.lines)-o.start > 1 {
		o.size -= len(o.lines[o.start]) + 1
		o.lines[o.start] = ""
		o.start++
		o.dropped = true
	}
	if o.start > 1024 && o.start > len(o.lines)/2 {
		o.lines = append([]string(nil), o.lines[o.start:]...)
		o.start = 0
	}
	o.added = true
}

func (o *output) notify() {
	o.mu.Lock()
	added := o.added
	o.added = false
	o.mu.Unlock()
	if added && o.onLine != nil {
		o.onLine()
	}
}

// writeLine adds a line written by the host itself (e.g. an error).
func (o *output) writeLine(s string) {
	o.lw.Flush()
	for line := range strings.Lines(s) {
		o.addLine(strings.TrimRight(line, "\r\n"))
	}
	o.notify()
}

// finish flushes an incomplete last line and closes the log file.
func (o *output) finish() {
	o.lw.Flush()
	o.notify()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.log != nil {
		_ = o.log.Close()
		o.log = nil
	}
}

// tail returns the last n lines (all for n <= 0), with an incomplete last
// line, and whether earlier output exists that is not returned.
func (o *output) tail(n int) (string, bool) {
	partial := o.lw.Partial()
	o.mu.Lock()
	defer o.mu.Unlock()
	kept := o.lines[o.start:]
	more := o.dropped
	if n > 0 && len(kept) > n {
		kept = kept[len(kept)-n:]
		more = true
	}
	s := strings.Join(kept, "\n")
	if partial != "" {
		if s != "" {
			s += "\n"
		}
		s += partial
	}
	return s, more
}

// lastLine returns the last non-empty line.
func (o *output) lastLine() string {
	if p := strings.TrimSpace(o.lw.Partial()); p != "" {
		return p
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.last
}

// readTail reads up to limit bytes from the end of the log file at path and
// returns its last n lines (all for n <= 0) and whether earlier output
// exists that is not returned. It is used for tasks of a previous run.
func readTail(path string, limit, n int) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", false
	}
	off := max(0, st.Size()-int64(limit))
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return "", false
	}
	more := off > 0
	if more {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
		more = true
	}
	return strings.Join(lines, "\n"), more
}
