package scoop

// Copied from plugins/scripts (plugins may not import each other).

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// maxOutput caps the output kept of a run; older output is dropped.
const maxOutput = 1 << 20

// ansiRe matches ANSI escape sequences (colors, cursor movement, titles).
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|\x1b[@-Z\\\\-_]")

// output collects the combined stdout and stderr of a run as text. It is an
// io.Writer safe for concurrent use; onWrite is called after each write.
type output struct {
	onWrite func()

	mu        sync.Mutex
	text      strings.Builder // complete lines, decoded
	pending   []byte          // an incomplete last line
	truncated bool
}

// Write implements io.Writer.
func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.pending = append(o.pending, p...)
	if i := bytes.LastIndexByte(o.pending, '\n'); i >= 0 {
		for line := range bytes.Lines(o.pending[:i+1]) {
			o.text.WriteString(cleanLine(line))
			o.text.WriteByte('\n')
		}
		o.pending = append(o.pending[:0], o.pending[i+1:]...)
	}
	if len(o.pending) > maxOutput {
		o.pending = o.pending[len(o.pending)-maxOutput:]
		o.truncated = true
	}
	if o.text.Len() > maxOutput {
		s := o.text.String()
		s = s[len(s)-maxOutput*3/4:]
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		o.text.Reset()
		o.text.WriteString(s)
		o.truncated = true
	}
	fn := o.onWrite
	o.mu.Unlock()
	if fn != nil {
		fn()
	}
	return len(p), nil
}

// String returns the output so far, including an incomplete last line.
func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.text.String()
	if len(o.pending) > 0 {
		s += cleanLine(o.pending)
	}
	return strings.TrimRight(s, "\n")
}

// Truncated reports whether output was dropped because of the size cap.
func (o *output) Truncated() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.truncated
}

// LastLine returns the last non-empty line.
func (o *output) LastLine() string {
	s := o.String()
	for s != "" {
		i := strings.LastIndexByte(s, '\n')
		line := strings.TrimSpace(s[i+1:])
		if line != "" {
			return line
		}
		if i < 0 {
			break
		}
		s = s[:i]
	}
	return ""
}

// cleanLine decodes a line and removes terminal control: ANSI escapes and
// text overwritten by a carriage return (progress bars).
func cleanLine(b []byte) string {
	b = bytes.TrimRight(b, "\r\n")
	if i := bytes.LastIndexByte(b, '\r'); i >= 0 {
		b = b[i+1:]
	}
	var s string
	if utf8.Valid(b) {
		s = string(b)
	} else {
		s = decodeLegacy(b)
	}
	if strings.IndexByte(s, 0x1b) >= 0 {
		s = ansiRe.ReplaceAllString(s, "")
	}
	return s
}
