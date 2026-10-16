package timer

import (
	"encoding/json/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// fakeClock is a settable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeTicks drives the refresh loops: each tick is one receive.
type fakeTicks struct {
	ch chan time.Time
	mu sync.Mutex
	n  int
}

func (f *fakeTicks) after(time.Duration) <-chan time.Time {
	f.mu.Lock()
	f.n++
	f.mu.Unlock()
	return f.ch
}

func (f *fakeTicks) tick(t *testing.T) {
	t.Helper()
	select {
	case f.ch <- time.Time{}:
	case <-time.After(10 * time.Second):
		t.Fatal("no refresh loop is waiting")
	}
}

// fakeWaits records the scheduler's waits; fire ends all of them.
type fakeWaits struct {
	mu    sync.Mutex
	waits []time.Duration
	chans []chan time.Time
}

func (f *fakeWaits) after(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	f.waits = append(f.waits, d)
	f.chans = append(f.chans, ch)
	return ch
}

func (f *fakeWaits) fire() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.chans {
		select {
		case ch <- time.Time{}:
		default:
		}
	}
	f.chans = nil
}

func (f *fakeWaits) last() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.waits) == 0 {
		return -1
	}
	return f.waits[len(f.waits)-1]
}

type env struct {
	p     *Plugin
	h     *hostfake.Host
	clock *fakeClock
	ticks *fakeTicks
	waits *fakeWaits
}

func newEnv(t *testing.T) *env {
	t.Helper()
	return newEnvWith(t, nil, -1)
}

// newEnvWith starts the plugin on kv (a new store when nil) with the
// startup delay d.
func newEnvWith(t *testing.T, kv *hostfake.KV, d time.Duration) *env {
	t.Helper()
	clock := &fakeClock{t: testNow}
	ticks := &fakeTicks{ch: make(chan time.Time)}
	waits := &fakeWaits{}
	p := NewWithOptions(Options{After: ticks.after, WaitAfter: waits.after, StartupDelay: d})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Clock = clock.now
	if kv != nil {
		h.KV = kv
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	e := &env{p, h, clock, ticks, waits}
	if d <= 0 {
		e.settle(t)
	}
	return e
}

// newKV returns an empty store that several plugin instances can share.
func newKV(t *testing.T) *hostfake.KV {
	t.Helper()
	h, err := hostfake.New(New(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return h.KV
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// settle waits until the scheduler waits with nothing left to do.
func (e *env) settle(t *testing.T) {
	t.Helper()
	eventually(t, "the scheduler is idle", func() bool { return e.p.idle.Load() && len(e.p.wake) == 0 })
}

// check makes the scheduler look at the clock again, as after its wait
// ended, and waits until it did.
func (e *env) check(t *testing.T) {
	t.Helper()
	e.settle(t)
	n := e.p.checks.Load()
	e.p.wake <- struct{}{}
	eventually(t, "the scheduler checked", func() bool { return e.p.checks.Load() > n })
	e.settle(t)
}

func (e *env) root(t *testing.T, text string) []plugin.Item {
	t.Helper()
	items, err := e.p.RootItems(t.Context(), plugin.Query{Text: text})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func (e *env) run(t *testing.T, a plugin.Action) plugin.Result {
	t.Helper()
	r, err := e.p.HandleAction(t.Context(), a.Do)
	if err != nil {
		t.Fatalf("%s: %v", a.ID, err)
	}
	return r
}

func action(t *testing.T, it plugin.Item, id string) plugin.Action {
	t.Helper()
	for _, a := range it.Actions {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("item %s has no action %q (has %v)", it.ID, id, actionIDs(it))
	return plugin.Action{}
}

func actionIDs(it plugin.Item) []string {
	var out []string
	for _, a := range it.Actions {
		out = append(out, a.ID)
	}
	return out
}

func ids(items []plugin.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

// start starts a timer from the root search text and returns its item.
func (e *env) start(t *testing.T, text string) plugin.Item {
	t.Helper()
	items := e.root(t, text)
	if len(items) == 0 || !strings.HasPrefix(items[0].ID, "new:") {
		t.Fatalf("%q: no preview (%v)", text, ids(items))
	}
	e.run(t, items[0].Actions[0])
	active, _ := e.p.snapshot()
	if len(active) == 0 {
		t.Fatal("nothing started")
	}
	newest := slices.MaxFunc(active, func(a, b entry) int { return strings.Compare(a.Start.String()+a.ID, b.Start.String()+b.ID) })
	for _, it := range e.root(t, "timer") {
		if it.ID == "timer:"+newest.ID {
			return it
		}
	}
	t.Fatal("started timer not listed")
	return plugin.Item{}
}

func (e *env) item(t *testing.T, id string) plugin.Item {
	t.Helper()
	for _, it := range e.root(t, "timer") {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("no item %s", id)
	return plugin.Item{}
}

func TestManifest(t *testing.T) {
	m := New().Manifest()
	if m.ID != ID || len(m.Commands) != 1 || m.Commands[0].Name != cmdTimers || m.Commands[0].Mode != plugin.ModeView {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestPrefixes(t *testing.T) {
	e := newEnv(t)
	for _, tt := range []struct {
		text  string
		first string
	}{
		{"timer 25m", "new:timer"},
		{"TIMER 25m", "new:timer"},
		{"计时25分钟", "new:timer"},
		{"计时 半小时", "new:timer"},
		{"倒计时 10分钟", "new:timer"},
		{"remind 15:30 meeting", "new:reminder"},
		{"提醒明天9点交周报", "new:reminder"},
		{"timer", "preset:1500000"},
		{"remind", "preset:1500000"},
	} {
		items := e.root(t, tt.text)
		if len(items) == 0 || items[0].ID != tt.first {
			t.Errorf("%q: %v, want %s first", tt.text, ids(items), tt.first)
		}
		for _, it := range items {
			if it.Score <= 0 || it.Group != "Timers" {
				t.Errorf("%q: item %s score %v group %q", tt.text, it.ID, it.Score, it.Group)
			}
		}
	}
	for _, text := range []string{"", "25m", "timers", "timer25m", "reminder 15:30", "vsc", "timer abc", "remind 25m"} {
		if items := e.root(t, text); len(items) != 0 {
			t.Errorf("%q: %v, want nothing", text, ids(items))
		}
	}
	// The reminder prefix does not start countdowns, nor the timer prefix
	// reminders at a time ("timer 15:30" is 15 min 30 s).
	if items := e.root(t, "timer 15:30"); len(items) != 1 || items[0].Title != "Start 15 min 30 s Timer" {
		t.Errorf("timer 15:30: %v", items)
	}
	e.h.Prefs.Set(prefPrefixes, plugin.MustPayload([]string{"t"}))
	if items := e.root(t, "t 5m"); len(items) != 1 || items[0].ID != "new:timer" {
		t.Errorf("custom prefix: %v", ids(items))
	}
}

func TestPreviews(t *testing.T) {
	e := newEnv(t)
	for _, tt := range []struct{ text, title, subtitle string }{
		{"timer 25m", "Start 25 min Timer", "Ends at 10:25"},
		{"timer 1h30m 喝水", "Start 1 h 30 min Timer: 喝水", "Ends at 11:30"},
		{"timer 15h", "Start 15 h Timer", "Ends tomorrow at 01:00"},
		{"remind 15:30 开会", "Remind Me: 开会", "Due at 15:30 · in 5 h 30 min"},
		{"remind 9am standup", "Remind Me: standup", "Due tomorrow at 09:00 · in 23 h · that time has passed today"},
		{"remind monday 9am", "Remind Me Monday at 09:00", "Due Monday at 09:00 · in 3 d 23 h"},
		{"remind tomorrow report", "Remind Me: report", "Due tomorrow at 09:00 · in 23 h · no time given, 09:00"},
		{"remind in 20m tea", "Remind Me: tea", "Due at 10:20 · in 20 min"},
		{"remind 2026-12-01 9:00", "Remind Me on 12/1 at 09:00", "Due on 12/1 at 09:00 · in 46 d 23 h"},
	} {
		items := e.root(t, tt.text)
		if len(items) == 0 || items[0].Title != tt.title || items[0].Subtitle != tt.subtitle {
			if len(items) == 0 {
				t.Errorf("%q: no items", tt.text)
				continue
			}
			t.Errorf("%q: %q / %q; want %q / %q", tt.text, items[0].Title, items[0].Subtitle, tt.title, tt.subtitle)
		}
	}
	e.h.Prefs.Set(prefHour24, plugin.MustPayload(false))
	if it := e.root(t, "remind 15:30")[0]; it.Title != "Remind Me at 3:30 PM" {
		t.Errorf("12-hour: %q", it.Title)
	}
}

func TestChinese(t *testing.T) {
	e := newEnv(t)
	e.h.I18N.SetLocale("zh-CN")
	for _, tt := range []struct{ text, title, subtitle string }{
		{"计时 25分钟 喝水", "开始 25 分钟计时器：喝水", "将于 10:25 结束"},
		{"提醒 明天 9点 交周报", "提醒：交周报", "明天 09:00 提醒 · 23 小时后"},
		{"提醒 下午3点", "在 15:00 提醒", "15:00 提醒 · 5 小时后"},
	} {
		items := e.root(t, tt.text)
		if len(items) == 0 || items[0].Title != tt.title || items[0].Subtitle != tt.subtitle {
			t.Errorf("%q: %v; want %q / %q", tt.text, items, tt.title, tt.subtitle)
		}
		if items[0].Group != "计时器" {
			t.Errorf("group %q", items[0].Group)
		}
	}
}

func TestStartAndList(t *testing.T) {
	e := newEnv(t)
	items := e.root(t, "timer 25m tea")
	r := e.run(t, items[0].Actions[0])
	if !r.Hide || r.Toast == nil || r.Toast.Title != "Started tea" || r.Toast.Message != "Ends at 10:25" {
		t.Fatalf("result = %+v %+v", r, r.Toast)
	}
	e.clock.advance(90 * time.Second)
	it := e.item(t, "timer:1")
	if it.Title != "23:30  tea" || it.Subtitle != "Ends at 10:25" || it.Accessory != "6%" {
		t.Errorf("item = %q / %q / %q", it.Title, it.Subtitle, it.Accessory)
	}
	want := []string{"pause", "add5", "add1", "restart", "rename", "copy", "cancel", "open"}
	if got := actionIDs(it); !slices.Equal(got, want) {
		t.Errorf("actions = %v", got)
	}
	if a := action(t, it, "copy"); a.Do.Kind != plugin.ActionCopyText || a.Do.Target != "10:25" {
		t.Errorf("copy = %+v", a.Do)
	}
	// The root lists running timers first, then presets.
	got := ids(e.root(t, "timer"))
	if !slices.Equal(got, []string{"timer:1", "preset:1500000", "preset:300000", "preset:900000"}) {
		t.Errorf("root = %v", got)
	}
	// Text that is not a duration filters by label.
	if got := ids(e.root(t, "timer te")); !slices.Equal(got, []string{"timer:1"}) {
		t.Errorf("filter = %v", got)
	}
	if got := ids(e.root(t, "timer coffee")); len(got) != 0 {
		t.Errorf("filter = %v", got)
	}
	// Without a label the name says the length; the clock shows hours.
	e.start(t, "timer 2h")
	if it := e.item(t, "timer:2"); it.Title != "2:00:00  2 h timer" {
		t.Errorf("unlabeled: %q", it.Title)
	}
	// None of the actions is read-only: they all change the timers.
	for _, a := range it.Actions {
		if a.ReadOnly || a.Style == plugin.StyleDestructive {
			t.Errorf("action %s: read-only %v, style %q", a.ID, a.ReadOnly, a.Style)
		}
	}
}

func TestPauseResume(t *testing.T) {
	e := newEnv(t)
	it := e.start(t, "timer 10m")
	e.clock.advance(4 * time.Minute)
	r := e.run(t, action(t, it, "pause"))
	if !r.Refresh || r.Toast.Title != "Paused 10 min timer" || r.Toast.Message != "Paused · 06:00 left" {
		t.Errorf("pause = %+v", r.Toast)
	}
	e.clock.advance(time.Hour)
	e.check(t)
	if len(e.h.Toasts()) != 0 {
		t.Fatal("a paused timer fired")
	}
	it = e.item(t, "timer:1")
	if it.Title != "06:00  10 min timer" || it.Accessory != "Paused" || it.Icon.Name != "pause" || it.Actions[0].ID != "resume" {
		t.Errorf("paused item = %+v", it)
	}
	// Paused timers are listed after running ones.
	e.start(t, "timer 30m")
	if got := ids(e.root(t, "timer"))[:2]; !slices.Equal(got, []string{"timer:2", "timer:1"}) {
		t.Errorf("order = %v", got)
	}
	e.run(t, action(t, it, "resume"))
	it = e.item(t, "timer:1")
	if it.Subtitle != "Ends at 11:10" {
		t.Errorf("resumed: %q", it.Subtitle)
	}
	e.clock.advance(6 * time.Minute)
	e.check(t)
	if n := len(e.h.Toasts()); n != 1 {
		t.Errorf("%d notifications", n)
	}
}

func TestAddRestartRename(t *testing.T) {
	e := newEnv(t)
	it := e.start(t, "timer 10m tea")
	e.clock.advance(8 * time.Minute)
	r := e.run(t, action(t, it, "add5"))
	if r.Toast.Title != "Added 5 min to tea" || r.Toast.Message != "Ends at 10:15" {
		t.Errorf("add = %+v", r.Toast)
	}
	it = e.item(t, "timer:1")
	if it.Title != "07:00  tea" || it.Accessory != "53%" {
		t.Errorf("after +5: %q %q", it.Title, it.Accessory)
	}
	r = e.run(t, action(t, it, "restart"))
	if r.Toast.Title != "Restarted tea" || r.Toast.Message != "Ends at 10:23" {
		t.Errorf("restart = %+v", r.Toast)
	}
	// Rename: the form has the label; submitting changes it.
	push := action(t, it, "rename").Do
	v, err := e.p.View(t.Context(), plugin.ViewRequest{ID: push.Target, Payload: push.Payload})
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != plugin.ViewForm || string(v.Form.Fields[0].Default) != `"tea"` {
		t.Fatalf("form = %+v", v.Form)
	}
	submit := v.Form.Submit.Do
	submit.Values = map[string]plugin.Payload{fieldLabel: plugin.MustPayload("  green tea ")}
	r, err = e.p.HandleAction(t.Context(), submit)
	if err != nil || !r.PopToRoot || r.Toast.Title != "Renamed to green tea" {
		t.Fatalf("rename = %+v, %v", r, err)
	}
	if it := e.item(t, "timer:1"); it.Title != "15:00  green tea" {
		t.Errorf("renamed: %q", it.Title)
	}
}

func TestCancelRestore(t *testing.T) {
	e := newEnv(t)
	it := e.start(t, "timer 10m tea")
	e.clock.advance(4 * time.Minute)
	r := e.run(t, action(t, it, "cancel"))
	if r.Toast.Title != "Cancelled tea" {
		t.Errorf("cancel = %+v", r.Toast)
	}
	if got := ids(e.root(t, "timer")); slices.Contains(got, "timer:1") {
		t.Fatalf("still listed: %v", got)
	}
	// The view lists it in the history, with Restore.
	v := e.view(t, "", "")
	h := section(t, v, "History")
	if len(h) != 1 || h[0].Subtitle != "Cancelled · 06:00 left" || h[0].Actions[0].ID != "restore" {
		t.Fatalf("history = %+v", h)
	}
	e.clock.advance(time.Hour) // a cancelled timer never fires
	e.check(t)
	r = e.run(t, h[0].Actions[0])
	if r.Toast.Title != "Restored tea" || r.Toast.Message != "Ends at 11:10" {
		t.Errorf("restore = %+v", r.Toast)
	}
	if v := e.view(t, "", ""); len(section(t, v, "Running")) != 1 || hasSection(v, "History") {
		t.Errorf("after restore: %+v", v.Sections)
	}
	// Restoring again (a stale item) says it is gone.
	r = e.run(t, h[0].Actions[0])
	if r.Toast == nil || r.Toast.Style != plugin.ToastFailure {
		t.Errorf("stale restore = %+v", r.Toast)
	}
	if len(e.h.Toasts()) != 0 {
		t.Errorf("notified: %v", e.h.Toasts())
	}
}

func TestFiresOnce(t *testing.T) {
	e := newEnv(t)
	e.start(t, "timer 25m tea")
	e.start(t, "remind 10:30 meeting")
	e.clock.advance(25*time.Minute - time.Second)
	e.check(t)
	if len(e.h.Toasts()) != 0 {
		t.Fatal("fired early")
	}
	e.clock.advance(time.Second)
	e.check(t)
	e.check(t)
	toasts := e.h.Toasts()
	if len(toasts) != 1 || toasts[0].Title != "Timer done: tea" || toasts[0].Message != "25 min · ended at 10:25" || toasts[0].Silent {
		t.Fatalf("toasts = %+v", toasts)
	}
	e.clock.advance(5 * time.Minute)
	e.check(t)
	toasts = e.h.Toasts()
	if len(toasts) != 2 || toasts[1].Title != "Reminder: meeting" || toasts[1].Message != "10:30" {
		t.Fatalf("toasts = %+v", toasts)
	}
	for range 3 {
		e.check(t)
	}
	if n := len(e.h.Toasts()); n != 2 || e.p.fired.Load() != 2 {
		t.Errorf("%d notifications, want 2", n)
	}
	// Both are in the history, newest first; the countdown can start again.
	v := e.view(t, "", "")
	h := section(t, v, "History")
	if got := ids(h); !slices.Equal(got, []string{"history:2", "history:1"}) {
		t.Fatalf("history = %v", got)
	}
	if h[1].Subtitle != "Ended at 10:25" || !slices.Equal(actionIDs(h[1]), []string{"again", "snooze", "forget"}) {
		t.Errorf("history item = %q %v", h[1].Subtitle, actionIDs(h[1]))
	}
	if !slices.Equal(actionIDs(h[0]), []string{"snooze", "forget"}) {
		t.Errorf("reminder history actions = %v", actionIDs(h[0]))
	}
	// Snooze: a new reminder with the label in 5 minutes.
	r := e.run(t, action(t, h[0], "snooze"))
	if r.Toast.Title != "Reminder set: meeting" || r.Toast.Message != "Due at 10:35" || !r.ClearText {
		t.Errorf("snooze = %+v", r)
	}
	// The root offers the countdown again under Recent.
	root := e.root(t, "timer")
	if !slices.Contains(ids(root), "recent:1500000\x00tea") {
		t.Errorf("root = %v", ids(root))
	}
	e.run(t, action(t, h[1], "forget"))
	if got := ids(section(t, e.view(t, "", ""), "History")); slices.Contains(got, "history:1") {
		t.Errorf("forgotten still listed: %v", got)
	}
}

func TestSound(t *testing.T) {
	e := newEnv(t)
	e.h.Prefs.Set(prefSound, plugin.MustPayload(false))
	e.start(t, "timer 1m")
	e.clock.advance(time.Minute)
	e.check(t)
	if toasts := e.h.Toasts(); len(toasts) != 1 || !toasts[0].Silent || toasts[0].Title != "Timer done" {
		t.Errorf("toasts = %+v", toasts)
	}
}

// TestSleep: the clock jumps (the computer slept through the end); the
// timer fires once at the next check and says it was missed.
func TestSleep(t *testing.T) {
	e := newEnv(t)
	e.start(t, "remind in 10m stretch")
	e.clock.advance(3 * time.Hour)
	e.check(t)
	toasts := e.h.Toasts()
	if len(toasts) != 1 || toasts[0].Title != "Reminder: stretch" || toasts[0].Message != "Missed: was due at 10:10" {
		t.Fatalf("toasts = %+v", toasts)
	}
	if h := section(t, e.view(t, "", ""), "History"); h[0].Subtitle != "Missed: was due at 10:10" {
		t.Errorf("history = %q", h[0].Subtitle)
	}
	// The window being shown makes the scheduler look again.
	e.start(t, "timer 1m")
	e.clock.advance(time.Hour)
	e.settle(t)
	n := e.p.checks.Load()
	e.h.Bus.PublishSync(plugin.EventWindowShown)
	eventually(t, "checked", func() bool { return e.p.checks.Load() > n })
	e.settle(t)
	if len(e.h.Toasts()) != 2 {
		t.Errorf("toasts = %+v", e.h.Toasts())
	}
}

// TestSchedulerWaits checks the waits: until the first end (plus a little),
// at most MaxWait, none without running timers; and that a wait ending
// makes it check.
func TestSchedulerWaits(t *testing.T) {
	e := newEnv(t)
	if e.waits.last() != -1 {
		t.Fatalf("waits without timers: %v", e.waits.last())
	}
	e.start(t, "timer 3s")
	e.settle(t)
	if got := e.waits.last(); got != 3*time.Second+tickSlack {
		t.Errorf("wait = %v", got)
	}
	e.start(t, "timer 1h")
	e.settle(t)
	if got := e.waits.last(); got != 3*time.Second+tickSlack {
		t.Errorf("wait = %v (the first end)", got)
	}
	e.clock.advance(3 * time.Second)
	n := e.p.checks.Load()
	e.waits.fire()
	eventually(t, "checked", func() bool { return e.p.checks.Load() > n })
	e.settle(t)
	if len(e.h.Toasts()) != 1 {
		t.Fatalf("toasts = %v", e.h.Toasts())
	}
	if got := e.waits.last(); got != 5*time.Second {
		t.Errorf("wait = %v, want MaxWait", got)
	}
}

func TestPersistence(t *testing.T) {
	kv := newKV(t)
	e := newEnvWith(t, kv, -1)
	e.start(t, "timer 25m tea")
	it := e.start(t, "timer 10m")
	e.clock.advance(time.Minute)
	e.run(t, action(t, it, "pause"))
	e.start(t, "remind tomorrow 9:00 report")
	if err := e.p.Close(); err != nil {
		t.Fatal(err)
	}
	// A new instance on the same store, 5 minutes later.
	e2 := newEnvWith(t, kv, -1)
	e2.clock.advance(6 * time.Minute)
	got := e2.root(t, "timer")
	want := []struct{ id, title, subtitle string }{
		{"timer:1", "19:00  tea", "Ends at 10:25"},
		{"timer:3", "22:54:00  report", "Due tomorrow at 09:00"},
		{"timer:2", "09:00  10 min timer", "Paused · 09:00 left"},
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].Title != w.title || got[i].Subtitle != w.subtitle {
			t.Errorf("item %d = %s %q %q; want %v", i, got[i].ID, got[i].Title, got[i].Subtitle, w)
		}
	}
	// New IDs continue.
	e2.start(t, "timer 1m")
	if _, ok := e2.p.update("4", func(*entry, time.Time) bool { return false }); !ok {
		t.Error("ID 4 not used")
	}
}

func TestBadState(t *testing.T) {
	kv := newKV(t)
	if err := kv.Set(kvState, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	e := newEnvWith(t, kv, -1)
	if got := ids(e.root(t, "timer")); got[0] != "preset:1500000" {
		t.Errorf("root = %v", got)
	}
	e.start(t, "timer 1m")
}

// TestMissedOnStartup: reminders that came due while golauncher was not
// running are announced once, after the startup delay.
func TestMissedOnStartup(t *testing.T) {
	kv := newKV(t)
	st := state{Version: 1, Next: 3, Active: []*entry{
		{ID: "1", Kind: kindReminder, Label: "meeting", TotalMs: ms(time.Hour), Start: testNow.Add(-2 * time.Hour), End: testNow.Add(-90 * time.Minute)},
		{ID: "2", Kind: kindTimer, TotalMs: ms(time.Hour), Start: testNow.Add(-time.Hour), End: testNow.Add(time.Hour)},
	}}
	b, _ := json.Marshal(st)
	if err := kv.Set(kvState, b); err != nil {
		t.Fatal(err)
	}
	e := newEnvWith(t, kv, 3*time.Second)
	eventually(t, "the startup wait", func() bool { return e.waits.last() == 3*time.Second })
	if len(e.h.Toasts()) != 0 {
		t.Fatal("notified before the startup delay")
	}
	e.waits.fire()
	eventually(t, "checked", func() bool { return e.p.checks.Load() > 0 })
	e.settle(t)
	toasts := e.h.Toasts()
	if len(toasts) != 1 || toasts[0].Title != "Reminder: meeting" || toasts[0].Message != "Missed: was due at 08:30" {
		t.Fatalf("toasts = %+v", toasts)
	}
	if got := ids(e.root(t, "timer")); got[0] != "timer:2" {
		t.Errorf("root = %v", got)
	}
	// Restarting again does not announce it again.
	if err := e.p.Close(); err != nil {
		t.Fatal(err)
	}
	e2 := newEnvWith(t, kv, -1)
	e2.check(t)
	if len(e2.h.Toasts()) != 0 {
		t.Errorf("announced again: %v", e2.h.Toasts())
	}
}

func TestHistorySize(t *testing.T) {
	e := newEnv(t)
	e.h.Prefs.Set(prefHistory, plugin.MustPayload(2))
	for range 4 {
		e.start(t, "timer 1m")
	}
	e.clock.advance(time.Minute)
	e.check(t)
	if n := len(e.h.Toasts()); n != 4 {
		t.Fatalf("%d toasts", n)
	}
	if h := section(t, e.view(t, "", ""), "History"); len(h) != 2 {
		t.Errorf("history = %v", ids(h))
	}
	// With no history, a cancelled timer is still kept for an undo.
	e.h.Prefs.Set(prefHistory, plugin.MustPayload(0))
	it := e.start(t, "timer 5m")
	e.run(t, action(t, it, "cancel"))
	if h := section(t, e.view(t, "", ""), "History"); len(h) != 1 || h[0].Actions[0].ID != "restore" {
		t.Errorf("history = %v", ids(h))
	}
}

func TestReminderAction(t *testing.T) {
	e := newEnv(t)
	items := e.root(t, "remind 15:30 开会")
	// The text is parsed again when the action runs: at 15:31 it is the
	// next 15:30.
	e.clock.advance(5*time.Hour + 31*time.Minute)
	r := e.run(t, items[0].Actions[0])
	if r.Toast.Title != "Reminder set: 开会" || r.Toast.Message != "Due tomorrow at 15:30" {
		t.Errorf("result = %+v", r.Toast)
	}
	it := e.item(t, "timer:1")
	if it.Icon.Name != "alarm-clock" || it.Accessory != "in 23 h 59 min" || slices.Contains(actionIDs(it), "restart") {
		t.Errorf("reminder item = %+v", it)
	}
	if _, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbRemind,
		Payload: plugin.MustPayload(remindPayload{Text: "whenever"})}); err == nil {
		t.Error("an unreadable reminder was set")
	}
}

func TestBadActions(t *testing.T) {
	e := newEnv(t)
	for _, do := range []plugin.ActionDo{
		{Kind: plugin.ActionCallback, Target: cbStart, Payload: plugin.MustPayload(startPayload{Ms: 0})},
		{Kind: plugin.ActionCallback, Target: cbStart, Payload: plugin.MustPayload(startPayload{Ms: ms(8 * 24 * time.Hour)})},
		{Kind: plugin.ActionCallback, Target: cbStart, Payload: []byte("nope")},
		{Kind: plugin.ActionCallback, Target: "nope", Payload: plugin.MustPayload(idPayload{ID: "1"})},
	} {
		if _, err := e.p.HandleAction(t.Context(), do); err == nil {
			t.Errorf("%s %s: no error", do.Target, do.Payload)
		}
	}
	for _, target := range []string{cbPause, cbResume, cbCancel, cbRestart, cbRestore, cbSnooze, cbForget} {
		r, err := e.p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: target, Payload: plugin.MustPayload(idPayload{ID: "9"})})
		if err != nil || r.Toast == nil || r.Toast.Title != "This timer no longer exists" {
			t.Errorf("%s on a missing timer: %+v, %v", target, r.Toast, err)
		}
	}
}

func (e *env) view(t *testing.T, instance, text string) plugin.View {
	t.Helper()
	v, err := e.p.View(t.Context(), plugin.ViewRequest{ID: cmdTimers, Instance: instance, Query: plugin.Query{Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func hasSection(v plugin.View, title string) bool {
	return slices.ContainsFunc(v.Sections, func(s plugin.Section) bool { return s.Title == title })
}

func section(t *testing.T, v plugin.View, title string) []plugin.Item {
	t.Helper()
	for _, s := range v.Sections {
		if s.Title == title {
			return s.Items
		}
	}
	t.Fatalf("no section %q in %+v", title, v.Sections)
	return nil
}

func TestView(t *testing.T) {
	e := newEnv(t)
	v := e.view(t, "", "")
	if v.Title != "Timers" || len(v.Sections) != 1 || v.Sections[0].Title != "Presets" {
		t.Fatalf("empty view = %+v", v.Sections)
	}
	// The search field starts timers and reminders too; both kinds when the
	// text could be either.
	v = e.view(t, "", "15:30 call")
	if got := ids(section(t, v, "New")); !slices.Equal(got, []string{"new:timer", "new:reminder"}) {
		t.Errorf("new = %v", got)
	}
	v = e.view(t, "", "remind 15:30 call")
	if got := ids(section(t, v, "New")); !slices.Equal(got, []string{"new:reminder"}) {
		t.Errorf("new = %v", got)
	}
	r := e.run(t, section(t, e.view(t, "", "25m tea"), "New")[0].Actions[0])
	if !r.ClearText || r.Hide {
		t.Errorf("start from the view = %+v", r)
	}
	v = e.view(t, "", "")
	if got := ids(section(t, v, "Running")); !slices.Equal(got, []string{"timer:1"}) {
		t.Errorf("running = %v", got)
	}
	if slices.Contains(actionIDs(section(t, v, "Running")[0]), "open") {
		t.Error("the view offers to open itself")
	}
	if !v.ShowDetail || section(t, v, "Running")[0].Detail == nil {
		t.Error("no detail")
	}
	// Filtering by label.
	if v := e.view(t, "", "te"); hasSection(v, "Presets") || len(section(t, v, "Running")) != 1 {
		t.Errorf("filtered = %+v", v.Sections)
	}
	if v := e.view(t, "", "zzz"); len(v.Sections) != 0 {
		t.Errorf("filtered = %+v", v.Sections)
	}
}

func TestDetail(t *testing.T) {
	e := newEnv(t)
	e.start(t, "timer 25m tea")
	e.clock.advance(5 * time.Minute)
	it := e.item(t, "timer:1")
	var got []string
	for _, m := range it.Detail.Metadata {
		got = append(got, m.Label+"="+m.Value)
	}
	want := []string{"Kind=Countdown", "Label=tea", "Started=10/15 10:00", "Length=25 min", "Ends=10/15 10:25", "Left=20:00", "Progress=20%"}
	if !slices.Equal(got, want) {
		t.Errorf("detail = %v", got)
	}
}

func TestFormat(t *testing.T) {
	e := newEnv(t)
	f := e.p.formatter()
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0 s"}, {time.Second, "1 s"}, {90 * time.Second, "1 min 30 s"}, {time.Hour, "1 h"},
		{26*time.Hour + 5*time.Minute, "1 d 2 h 5 min"},
	} {
		if got := f.duration(tt.d); got != tt.want {
			t.Errorf("duration(%v) = %q", tt.d, got)
		}
	}
	for _, tt := range []struct {
		d    time.Duration
		want string
	}{
		{0, "00:00"}, {time.Millisecond, "00:01"}, {25 * time.Minute, "25:00"}, {time.Hour, "1:00:00"}, {-time.Second, "00:00"},
	} {
		if got := clock(tt.d); got != tt.want {
			t.Errorf("clock(%v) = %q", tt.d, got)
		}
	}
	for _, tt := range []struct {
		t    time.Time
		want string
	}{
		{testNow.Add(time.Hour), "at 11:00"},
		{testNow.Add(24 * time.Hour), "tomorrow at 10:00"},
		{testNow.Add(-24 * time.Hour), "yesterday at 10:00"},
		{testNow.Add(3 * 24 * time.Hour), "Sunday at 10:00"},
		{testNow.Add(10 * 24 * time.Hour), "on 10/25 at 10:00"},
		{testNow.AddDate(1, 0, 0), "on 10/15/2027 at 10:00"},
	} {
		if got := f.when(tt.t, testNow); got != tt.want {
			t.Errorf("when(%v) = %q", tt.t, got)
		}
	}
	if got := f.approx(5*time.Hour + 29*time.Minute + time.Second); got != "5 h 30 min" {
		t.Errorf("approx = %q", got)
	}
}

// TestRealClock runs the scheduler with the real clock and timers: a timer
// of one second is announced once, about a second later. (Nothing is shown
// on the desktop: the fake host only records the toast.)
func TestRealClock(t *testing.T) {
	if testing.Short() {
		t.Skip("waits a second")
	}
	p := NewWithOptions(Options{StartupDelay: -1})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Clock = time.Now
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	start := time.Now()
	if _, err := p.HandleAction(t.Context(), plugin.ActionDo{Kind: plugin.ActionCallback, Target: cbStart,
		Payload: plugin.MustPayload(startPayload{Ms: 1000})}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the timer fired", func() bool { return len(h.Toasts()) == 1 })
	if d := time.Since(start); d < time.Second || d > 3*time.Second {
		t.Errorf("fired after %v", d)
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(h.Toasts()); n != 1 {
		t.Errorf("%d toasts", n)
	}
}
