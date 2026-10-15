package tasks

import (
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// metaPath is where the description of task id is kept.
func (m *Manager) metaPath(id string) string { return filepath.Join(m.o.Dir, id+".json") }

// saveLocked writes the description of t to the log directory. m.mu is held.
func (m *Manager) saveLocked(t *task) {
	if m.o.Dir == "" || m.o.ReadOnly || t.info.History {
		return
	}
	b, err := json.Marshal(t.info, jsontext.WithIndent("  "))
	if err == nil {
		err = os.WriteFile(m.metaPath(t.info.ID), b, 0o644)
	}
	if err != nil {
		m.log.Warn("tasks: saving a task", "task", t.info.ID, "err", err)
	}
}

// pruneLocked forgets the oldest finished tasks beyond KeepFinished and
// returns them; their files are deleted by deleteFiles. m.mu is held.
func (m *Manager) pruneLocked() []Info {
	var finished []*task
	for _, t := range m.tasks {
		if t.info.State.Finished() {
			finished = append(finished, t)
		}
	}
	if len(finished) <= m.o.KeepFinished {
		return nil
	}
	slices.SortStableFunc(finished, func(a, b *task) int { return a.info.Ended.Compare(b.info.Ended) })
	var removed []Info
	for _, t := range finished[:len(finished)-m.o.KeepFinished] {
		removed = append(removed, t.info)
		m.tasks = slices.DeleteFunc(m.tasks, func(x *task) bool { return x == t })
	}
	return removed
}

// deleteFiles deletes the log and description of tasks.
func (m *Manager) deleteFiles(infos []Info) {
	if m.o.Dir == "" || m.o.ReadOnly {
		return
	}
	for _, i := range infos {
		for _, p := range []string{m.metaPath(i.ID), filepath.Join(m.o.Dir, i.ID+".log")} {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				m.log.Warn("tasks: deleting a task file", "path", p, "err", err)
			}
		}
	}
}

// loadHistory reads the tasks of previous runs from the log directory.
// Tasks that had not ended were interrupted by the launcher quitting. The
// oldest beyond KeepFinished are deleted.
func (m *Manager) loadHistory() {
	entries, err := os.ReadDir(m.o.Dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			m.log.Warn("tasks: reading the log directory", "dir", m.o.Dir, "err", err)
		}
		return
	}
	var loaded []*task
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(m.o.Dir, name))
		if err != nil {
			continue
		}
		var i Info
		if err := json.Unmarshal(b, &i); err != nil || i.ID == "" || i.ID+".json" != name {
			m.log.Warn("tasks: ignoring a bad task file", "file", name, "err", err)
			continue
		}
		i.History = true
		if !i.State.Finished() {
			i.State, i.Interrupted, i.ExitCode = plugin.TaskFailed, true, -1
			if i.Ended.IsZero() {
				i.Ended = cmp.Or(i.Started, i.Created)
			}
		}
		if i.LogPath == "" || filepath.Dir(i.LogPath) != filepath.Clean(m.o.Dir) {
			// The directory may have moved; the log is next to the description.
			i.LogPath = filepath.Join(m.o.Dir, i.ID+".log")
		}
		if _, err := os.Stat(i.LogPath); err != nil {
			i.LogPath = ""
		}
		loaded = append(loaded, &task{info: i})
	}
	slices.SortStableFunc(loaded, func(a, b *task) int {
		return cmp.Or(a.info.Ended.Compare(b.info.Ended), strings.Compare(a.info.ID, b.info.ID))
	})
	m.mu.Lock()
	m.tasks = append(loaded, m.tasks...)
	removed := m.pruneLocked()
	m.mu.Unlock()
	m.deleteFiles(removed)
}
