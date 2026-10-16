package timer

import (
	"encoding/json/v2"
	"slices"
	"strconv"
	"time"
)

// The timers and their history (docs/timers.md §5). Everything is kept in
// memory under Plugin.mu and written back to the plugin's KV as a whole
// after every change. Times are wall-clock times without a monotonic
// reading, so a timer ends at the same moment after a restart, a sleep or a
// change of the system clock.

const (
	kvState = "state"

	kindTimer    = "timer"
	kindReminder = "reminder"

	stateDone      = "done"
	stateCancelled = "cancelled"

	// lateAfter: a timer found due later than this was missed (golauncher
	// was not running or the computer slept), and its notice says so.
	lateAfter = time.Minute
	// recentMax is the number of recent countdowns offered for a restart.
	recentMax = 5
	// maxHistory bounds the historySize preference.
	maxHistory = 100
)

// entry is a running or paused timer, or one in the history.
type entry struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
	// TotalMs is the whole length: what it was started with, plus time
	// added.
	TotalMs int64     `json:"totalMs"`
	Start   time.Time `json:"start"`
	// End is when a running timer ends, or when one in the history ended
	// (was due).
	End time.Time `json:"end,omitzero"`
	// LeftMs is what remained when it was paused or cancelled.
	LeftMs int64 `json:"leftMs,omitzero"`
	Paused bool  `json:"paused,omitzero"`

	// History only.
	State    string    `json:"state,omitempty"`   // stateDone, stateCancelled
	Finished time.Time `json:"finished,omitzero"` // when it fired or was cancelled
	Missed   bool      `json:"missed,omitzero"`   // fired late (see lateAfter)
}

func (e *entry) total() time.Duration { return time.Duration(e.TotalMs) * time.Millisecond }

// left is the time remaining at now.
func (e *entry) left(now time.Time) time.Duration {
	if e.Paused || e.State == stateCancelled {
		return time.Duration(e.LeftMs) * time.Millisecond
	}
	if e.State == stateDone {
		return 0
	}
	return max(e.End.Sub(now), 0)
}

// percent is the share of the total that has passed, 0 to 100, rounded
// down.
func (e *entry) percent(now time.Time) int {
	total := e.total()
	if total <= 0 {
		return 100
	}
	return int(min(max((total-e.left(now))*100/total, 0), 100))
}

// state is what is stored.
type state struct {
	Version int      `json:"version"`
	Next    int      `json:"next"`
	Active  []*entry `json:"active"`
	// History is newest first.
	History []*entry `json:"history"`
}

func ms(d time.Duration) int64 { return int64(d / time.Millisecond) }

// wall drops the monotonic clock reading, so comparisons use the wall clock.
func wall(t time.Time) time.Time { return t.Round(0) }

// load reads the stored state. A missing or unreadable state is empty (an
// unreadable one is logged and kept until the next write).
func (p *Plugin) load() {
	p.st = state{Version: 1, Next: 1}
	b, ok, err := p.h.Storage().Get(kvState)
	if err != nil {
		p.h.Logger().Warn("timer: read state", "err", err)
		return
	}
	if !ok {
		return
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		p.h.Logger().Warn("timer: bad state", "err", err)
		return
	}
	st.Active = slices.DeleteFunc(st.Active, func(e *entry) bool { return e == nil })
	st.History = slices.DeleteFunc(st.History, func(e *entry) bool { return e == nil })
	st.Version = 1
	st.Next = max(st.Next, 1)
	p.st = st
}

// saveLocked writes the state back. p.mu must be held.
func (p *Plugin) saveLocked() {
	b, err := json.Marshal(p.st)
	if err == nil {
		err = p.h.Storage().Set(kvState, b)
	}
	if err != nil {
		p.h.Logger().Warn("timer: write state", "err", err)
	}
}

// changedLocked saves the state and wakes the scheduler. p.mu must be
// held; call p.shown after unlocking.
func (p *Plugin) changedLocked() {
	p.saveLocked()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// shown asks the host to pull what is shown again after a change made
// outside a view's own action (an action run by AI, a timer that fired).
func (p *Plugin) shown() { p.h.Invalidate("") }

func (p *Plugin) findLocked(id string) (*entry, int) {
	for i, e := range p.st.Active {
		if e.ID == id {
			return e, i
		}
	}
	return nil, -1
}

func (p *Plugin) findHistoryLocked(id string) (*entry, int) {
	for i, e := range p.st.History {
		if e.ID == id {
			return e, i
		}
	}
	return nil, -1
}

// historySize is the number of finished timers kept.
func (p *Plugin) historySize() int {
	return min(max(p.h.Preferences().Int(prefHistory), 0), maxHistory)
}

// toHistoryLocked moves e to the front of the history.
func (p *Plugin) toHistoryLocked(e *entry) {
	p.st.History = slices.Insert(p.st.History, 0, e)
	if n := p.historySize(); len(p.st.History) > n {
		p.st.History = p.st.History[:n]
	}
}

// add starts a timer of kind that ends at end and returns it.
func (p *Plugin) add(kind, label string, now, end time.Time) entry {
	now, end = wall(now), wall(end)
	p.mu.Lock()
	e := &entry{ID: strconv.Itoa(p.st.Next), Kind: kind, Label: label, TotalMs: ms(end.Sub(now)), Start: now, End: end}
	p.st.Next++
	p.st.Active = append(p.st.Active, e)
	p.changedLocked()
	out := *e
	p.mu.Unlock()
	p.shown()
	return out
}

// update runs fn on the running timer id and saves the change; fn reports
// whether it changed anything. It returns a copy of the timer after fn, and
// false when there is no such timer.
func (p *Plugin) update(id string, fn func(e *entry, now time.Time) bool) (entry, bool) {
	p.mu.Lock()
	e, _ := p.findLocked(id)
	if e == nil {
		p.mu.Unlock()
		return entry{}, false
	}
	changed := fn(e, wall(p.h.Now()))
	if changed {
		p.changedLocked()
	}
	out := *e
	p.mu.Unlock()
	if changed {
		p.shown()
	}
	return out, true
}

func pause(e *entry, now time.Time) bool {
	if e.Paused {
		return false
	}
	e.LeftMs, e.Paused, e.End = ms(e.End.Sub(now)), true, time.Time{}
	return true
}

func resume(e *entry, now time.Time) bool {
	if !e.Paused {
		return false
	}
	e.End, e.Paused, e.LeftMs = now.Add(time.Duration(e.LeftMs)*time.Millisecond), false, 0
	return true
}

func addTime(d time.Duration) func(e *entry, now time.Time) bool {
	return func(e *entry, now time.Time) bool {
		if e.Paused {
			e.LeftMs += ms(d)
		} else {
			e.End = e.End.Add(d)
		}
		e.TotalMs += ms(d)
		return true
	}
}

func restart(e *entry, now time.Time) bool {
	e.Start, e.End, e.Paused, e.LeftMs = now, now.Add(e.total()), false, 0
	return true
}

func rename(label string) func(e *entry, now time.Time) bool {
	return func(e *entry, now time.Time) bool {
		changed := e.Label != label
		e.Label = label
		return changed
	}
}

// cancel moves timer id to the history as cancelled.
func (p *Plugin) cancel(id string) (entry, bool) {
	p.mu.Lock()
	e, i := p.findLocked(id)
	if e == nil {
		p.mu.Unlock()
		return entry{}, false
	}
	now := wall(p.h.Now())
	e.LeftMs, e.State, e.Finished = ms(e.left(now)), stateCancelled, now
	e.End, e.Paused = time.Time{}, false
	p.st.Active = slices.Delete(p.st.Active, i, i+1)
	// Kept in the history for an undo even when no history is kept.
	p.st.History = slices.Insert(p.st.History, 0, e)
	if n := max(p.historySize(), 1); len(p.st.History) > n {
		p.st.History = p.st.History[:n]
	}
	p.changedLocked()
	out := *e
	p.mu.Unlock()
	p.shown()
	return out, true
}

// restore starts a cancelled timer again with what remained of it.
func (p *Plugin) restore(id string) (entry, bool) {
	p.mu.Lock()
	e, i := p.findHistoryLocked(id)
	if e == nil || e.State != stateCancelled || e.LeftMs <= 0 {
		p.mu.Unlock()
		return entry{}, false
	}
	now := wall(p.h.Now())
	p.st.History = slices.Delete(p.st.History, i, i+1)
	e.End, e.State, e.Finished, e.LeftMs = now.Add(time.Duration(e.LeftMs)*time.Millisecond), "", time.Time{}, 0
	p.st.Active = append(p.st.Active, e)
	p.changedLocked()
	out := *e
	p.mu.Unlock()
	p.shown()
	return out, true
}

// forget removes entry id from the history.
func (p *Plugin) forget(id string) bool {
	p.mu.Lock()
	_, i := p.findHistoryLocked(id)
	if i < 0 {
		p.mu.Unlock()
		return false
	}
	p.st.History = slices.Delete(p.st.History, i, i+1)
	p.changedLocked()
	p.mu.Unlock()
	p.shown()
	return true
}

// historyEntry returns a copy of history entry id.
func (p *Plugin) historyEntry(id string) (entry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, _ := p.findHistoryLocked(id)
	if e == nil {
		return entry{}, false
	}
	return *e, true
}

// snapshot returns copies of the running timers (in the order they end,
// paused ones last) and of the history.
func (p *Plugin) snapshot() (active, history []entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.st.Active {
		active = append(active, *e)
	}
	for _, e := range p.st.History {
		history = append(history, *e)
	}
	slices.SortStableFunc(active, func(a, b entry) int {
		switch {
		case a.Paused != b.Paused:
			if a.Paused {
				return 1
			}
			return -1
		case a.Paused:
			return 0
		}
		return a.End.Compare(b.End)
	})
	return active, history
}

// fireDue moves the timers that are due at now to the history, saves, and
// then notifies once for each. Because a due timer leaves the running list
// (and that is saved) before its notice is sent, it can never be notified
// twice, also not after a restart.
func (p *Plugin) fireDue() {
	now := wall(p.h.Now())
	p.mu.Lock()
	var fired []*entry
	keep := p.st.Active[:0]
	for _, e := range p.st.Active {
		if e.Paused || e.End.After(now) {
			keep = append(keep, e)
			continue
		}
		e.State, e.Finished, e.Missed = stateDone, now, now.Sub(e.End) > lateAfter
		fired = append(fired, e)
	}
	clear(p.st.Active[len(keep):])
	p.st.Active = keep
	slices.SortStableFunc(fired, func(a, b *entry) int { return a.End.Compare(b.End) })
	var due []entry
	for _, e := range fired { // the last one ends up first in the history
		p.toHistoryLocked(e)
		due = append(due, *e)
	}
	if len(due) > 0 {
		p.saveLocked()
	}
	p.mu.Unlock()
	if len(due) == 0 {
		return
	}
	for _, e := range due {
		p.h.Toast(p.notice(e))
	}
	p.fired.Add(int64(len(due)))
	p.shown()
}

// nextWait is how long the scheduler may wait before it must look again:
// until the first running timer ends (plus a little), at most maxWait. ok is
// false when no timer runs.
func (p *Plugin) nextWait() (time.Duration, bool) {
	now := wall(p.h.Now())
	p.mu.Lock()
	defer p.mu.Unlock()
	var first time.Time
	for _, e := range p.st.Active {
		if !e.Paused && (first.IsZero() || e.End.Before(first)) {
			first = e.End
		}
	}
	if first.IsZero() {
		return 0, false
	}
	return min(max(first.Sub(now)+tickSlack, 0), p.opts.MaxWait), true
}

// schedule is the scheduler loop (docs/timers.md §5). It runs on Host.Go
// from Init until Close. It waits StartupDelay first, so that timers which
// came due while golauncher was not running are announced once the
// launcher can show notifications.
func (p *Plugin) schedule() {
	defer p.wg.Done()
	if d := p.opts.StartupDelay; d > 0 {
		select {
		case <-p.done:
			return
		case <-p.opts.WaitAfter(d):
		}
	}
	for {
		p.fireDue()
		p.checks.Add(1)
		var tc <-chan time.Time
		if d, ok := p.nextWait(); ok {
			tc = p.opts.WaitAfter(d)
		}
		p.idle.Store(true)
		select {
		case <-p.done:
			return
		case <-p.wake:
		case <-tc:
		}
		p.idle.Store(false)
	}
}
