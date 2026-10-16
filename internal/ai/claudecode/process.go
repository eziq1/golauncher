package claudecode

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/proc"
)

// maxLine caps a line of the CLI's output; a longer one is dropped.
const maxLine = 64 << 20

// process is a running CLI: its stdout lines are queued for the turn that
// reads them, its stderr's end is kept for error messages.
type process struct {
	p      *proc.Process
	stdin  io.WriteCloser
	lines  *lineQueue
	stderr *tail
	done   chan struct{} // closed when the process ended and its output was read
	code   int           // exit code, valid after done

	// turns counts the turns sent; only the running turn touches it.
	turns int

	wmu sync.Mutex
}

// startProcess starts the CLI; onExit is called once it ended.
func startProcess(argv []string, dir string, env []string, onExit func(*process)) (*process, error) {
	pr := &process{lines: newLineQueue(), stderr: &tail{max: 4096}, done: make(chan struct{})}
	split := &splitter{q: pr.lines}
	p, err := proc.Start(proc.Options{Argv: argv, Dir: dir, Env: env, Output: split, Stderr: pr.stderr, StdinPipe: true})
	if err != nil {
		return nil, err
	}
	pr.p, pr.stdin = p, p.Stdin()
	go func() {
		code, _ := p.Wait()
		split.flush()
		pr.code = code
		close(pr.done)
		if onExit != nil {
			onExit(pr)
		}
	}()
	return pr, nil
}

// alive reports whether the process is still running.
func (pr *process) alive() bool {
	select {
	case <-pr.done:
		return false
	default:
		return true
	}
}

// writeJSON writes v as one line to the process's input.
func (pr *process) writeJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return pr.write(append(b, '\n'))
}

// write writes b to the process's input.
func (pr *process) write(b []byte) error {
	pr.wmu.Lock()
	defer pr.wmu.Unlock()
	_, err := pr.stdin.Write(b)
	return err
}

// closeStdin ends the process's input.
func (pr *process) closeStdin() {
	pr.wmu.Lock()
	defer pr.wmu.Unlock()
	_ = pr.stdin.Close()
}

// stop kills the process and everything it started.
func (pr *process) stop() { pr.p.Stop() }

// shutdown ends the input so the process exits by itself, and kills it when
// it did not within grace.
func (pr *process) shutdown(grace time.Duration) {
	pr.closeStdin()
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-pr.done:
	case <-t.C:
		pr.stop()
	}
}

// stderrText is the end of what the process wrote to stderr.
func (pr *process) stderrText() string { return strings.TrimSpace(pr.stderr.String()) }

// lineQueue is an unbounded queue of output lines, so that the process's
// output is never blocked by a slow reader.
type lineQueue struct {
	mu    sync.Mutex
	items [][]byte
	sig   chan struct{}
}

func newLineQueue() *lineQueue { return &lineQueue{sig: make(chan struct{}, 1)} }

func (q *lineQueue) push(b []byte) {
	q.mu.Lock()
	q.items = append(q.items, b)
	q.mu.Unlock()
	select {
	case q.sig <- struct{}{}:
	default:
	}
}

func (q *lineQueue) pop() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil, false
	}
	b := q.items[0]
	q.items[0] = nil
	q.items = q.items[1:]
	return b, true
}

// splitter is the io.Writer for stdout: it queues each complete line.
type splitter struct {
	q       *lineQueue
	mu      sync.Mutex
	pending []byte
	skip    bool // dropping the rest of a line that was too long
}

func (s *splitter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if !s.skip {
				s.pending = append(s.pending, p...)
				if len(s.pending) > maxLine {
					s.pending, s.skip = nil, true
				}
			}
			break
		}
		if !s.skip {
			line := append(s.pending, p[:i]...)
			s.pending = nil
			s.q.push(bytes.TrimRight(line, "\r"))
		}
		s.skip = false
		p = p[i+1:]
	}
	return n, nil
}

// flush queues an incomplete last line.
func (s *splitter) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) > 0 && !s.skip {
		s.q.push(s.pending)
	}
	s.pending = nil
}

// tail is an io.Writer that keeps the last max bytes written.
type tail struct {
	max int
	mu  sync.Mutex
	b   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append(t.b[:0], t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.ToValidUTF8(string(t.b), "")
}
