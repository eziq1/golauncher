package agent

import (
	"bufio"
	"bytes"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Retention is how long tool calls stay in the activity log (docs/ai.md §4.4).
var Retention = 30 * 24 * time.Hour

// maxEntries caps the log whatever its age.
const maxEntries = 5000

// Outcome is how a tool call ended.
type Outcome string

const (
	OutcomeOK       Outcome = "ok"
	OutcomeDeclined Outcome = "declined" // the user said no, or did not answer
	OutcomeRefused  Outcome = "refused"  // not allowed: access off, plugin not exposed
	OutcomeError    Outcome = "error"
)

// Entry is a tool call in the activity log. It never holds message content:
// only the tool, a short summary of its arguments (a search's words, an
// action and the item's title) and the outcome.
type Entry struct {
	Time    time.Time `json:"time"`
	Client  string    `json:"client"`
	Tool    string    `json:"tool"`
	Summary string    `json:"summary,omitempty"`
	Outcome Outcome   `json:"outcome"`
	Detail  string    `json:"detail,omitempty"`
}

// Activity is the log of tool calls, kept in a JSON-lines file (or in
// memory) for Retention. It is safe for concurrent use.
type Activity struct {
	path string
	now  func() time.Time

	mu       sync.Mutex
	entries  []Entry // oldest first
	onChange []func()
}

// OpenActivity loads the log at path ("" keeps it in memory) and drops the
// entries older than Retention.
func OpenActivity(path string, now func() time.Time) (*Activity, error) {
	a := &Activity{path: path, now: now}
	if path == "" {
		return a, nil
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			a.entries = append(a.entries, e)
		}
	}
	if a.prune() {
		if err := a.rewrite(); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// prune drops old entries; it reports whether any were dropped. a.mu must
// be held (or a not yet shared).
func (a *Activity) prune() bool {
	cut := a.now().Add(-Retention)
	n := len(a.entries)
	a.entries = slices.DeleteFunc(a.entries, func(e Entry) bool { return e.Time.Before(cut) })
	if len(a.entries) > maxEntries {
		a.entries = slices.Clone(a.entries[len(a.entries)-maxEntries:])
	}
	return len(a.entries) != n
}

// rewrite writes all entries to the file. a.mu must be held.
func (a *Activity) rewrite() error {
	if a.path == "" {
		return nil
	}
	var buf bytes.Buffer
	for _, e := range a.entries {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(append(b, '\n'))
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return err
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.path)
}

// Add records e.
func (a *Activity) Add(e Entry) {
	a.mu.Lock()
	a.entries = append(a.entries, e)
	var err error
	if a.prune() {
		err = a.rewrite()
	} else if a.path != "" {
		err = appendLine(a.path, e)
	}
	fns := slices.Clone(a.onChange)
	a.mu.Unlock()
	_ = err // the log is best effort; the entry stays in memory
	for _, fn := range fns {
		fn()
	}
}

func appendLine(path string, e Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return errors.Join(err, f.Close())
}

// Entries returns the log, newest first, without entries older than
// Retention.
func (a *Activity) Entries() []Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.prune() {
		_ = a.rewrite()
	}
	out := slices.Clone(a.entries)
	slices.Reverse(out)
	return out
}

// Clear empties the log and returns how many entries it had.
func (a *Activity) Clear() (int, error) {
	a.mu.Lock()
	n := len(a.entries)
	a.entries = nil
	var err error
	if a.path != "" {
		err = os.Remove(a.path)
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
	}
	fns := slices.Clone(a.onChange)
	a.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
	return n, err
}

// OnChange registers fn to run after the log changed.
func (a *Activity) OnChange(fn func()) {
	a.mu.Lock()
	a.onChange = append(a.onChange, fn)
	a.mu.Unlock()
}
