package proc

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// ansiRe matches ANSI escape sequences (colors, cursor movement, titles).
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-Z\\\\-_]")

// CleanLine decodes a line of console output and removes terminal control:
// the line break, ANSI escapes and text overwritten by a carriage return
// (progress bars). Lines that are not UTF-8 are decoded from the OEM code
// page, which console programs such as cmd.exe write to pipes.
func CleanLine(b []byte) string {
	b = bytes.TrimRight(b, "\r\n")
	if i := bytes.LastIndexByte(b, '\r'); i >= 0 {
		b = b[i+1:]
	}
	var s string
	if utf8.Valid(b) {
		s = string(b)
	} else {
		s = DecodeLegacy(b)
	}
	if strings.IndexByte(s, 0x1b) >= 0 {
		s = ansiRe.ReplaceAllString(s, "")
	}
	return s
}

// maxPending caps an incomplete line kept by LineWriter.
const maxPending = 1 << 20

// LineWriter is an io.Writer that splits what is written into cleaned lines
// (CleanLine) and passes each complete one to OnLine. It is safe for
// concurrent use; OnLine is called with the writer's lock held, in order.
type LineWriter struct {
	OnLine func(line string)

	mu      sync.Mutex
	pending []byte
}

// Write implements io.Writer.
func (w *LineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	if i := bytes.LastIndexByte(w.pending, '\n'); i >= 0 {
		for line := range bytes.Lines(w.pending[:i+1]) {
			w.OnLine(CleanLine(line))
		}
		w.pending = append(w.pending[:0], w.pending[i+1:]...)
	}
	if len(w.pending) > maxPending {
		// A "line" this long is binary noise; keep its end.
		w.pending = append(w.pending[:0], w.pending[len(w.pending)-maxPending:]...)
	}
	return len(p), nil
}

// Partial returns the incomplete last line, cleaned.
func (w *LineWriter) Partial() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return ""
	}
	return CleanLine(w.pending)
}

// Flush passes an incomplete last line to OnLine.
func (w *LineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		w.OnLine(CleanLine(w.pending))
		w.pending = w.pending[:0]
	}
}
