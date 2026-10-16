// Package timer is the built-in timers and reminders plugin
// (docs/timers.md).
//
// "timer 25m", "timer 1h30m tea", "计时 半小时" start a countdown;
// "remind 15:30 meeting", "提醒 明天 9点 交周报", "remind in 20m tea" set a
// reminder. "timer" alone and the Timers command list the running timers
// with the time left, ticking every second while shown. When a timer ends
// the plugin notifies with Host.Toast (a Windows notification while the
// launcher is hidden). Timers are stored with their wall-clock end time, so
// they survive restarts; reminders that came due while golauncher was not
// running are announced when it starts.
package timer

import (
	"context"
	"embed"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// ID is the plugin's manifest ID.
const ID = "builtin.timer"

const (
	cmdTimers  = "timers"
	viewRename = "rename"

	prefPrefixes = "prefixes"
	prefRemind   = "remindPrefixes"
	prefSound    = "sound"
	prefHistory  = "historySize"
	prefHour24   = "hour24"

	cbStart   = "start"
	cbRemind  = "remind"
	cbPause   = "pause"
	cbResume  = "resume"
	cbAdd     = "add"
	cbRestart = "restart"
	cbCancel  = "cancel"
	cbRestore = "restore"
	cbSnooze  = "snooze"
	cbForget  = "forget"
	cbRename  = "rename"

	fieldLabel = "label"

	snooze = 5 * time.Minute
)

var (
	defaultPrefixes = []string{"timer", "计时", "倒计时"}
	defaultRemind   = []string{"remind", "提醒"}
	// presets are offered for a one-tap start.
	presets = []struct {
		d    time.Duration
		name func(plugin.Localizer) string
	}{
		{25 * time.Minute, func(l plugin.Localizer) string { return l.T("preset.pomodoro") }},
		{5 * time.Minute, func(l plugin.Localizer) string { return l.T("preset.shortBreak") }},
		{15 * time.Minute, func(l plugin.Localizer) string { return l.T("preset.longBreak") }},
	}
)

//go:embed locales/*.json
var locales embed.FS

// Options change how the plugin waits; tests replace them.
type Options struct {
	// After returns a channel that receives once d has passed (default
	// time.After). The refresh loops of the list wait on it.
	After func(d time.Duration) <-chan time.Time
	// WaitAfter is the same for the scheduler (default time.After).
	WaitAfter func(d time.Duration) <-chan time.Time
	// StartupDelay is waited before the first check for timers that are due,
	// so the launcher can show notifications by then (default 3s; negative:
	// none).
	StartupDelay time.Duration
	// MaxWait is the longest the scheduler waits before it looks at the wall
	// clock again (default 5s): after a sleep or a change of the system
	// clock, a timer that is due is noticed within it.
	MaxWait time.Duration
}

// Plugin is the timers plugin.
type Plugin struct {
	opts Options
	h    plugin.Host

	unsub func()
	wg    sync.WaitGroup
	done  chan struct{} // closed by Close
	wake  chan struct{} // the scheduler looks again

	// For tests.
	running atomic.Int32 // refresh loops
	checks  atomic.Int64 // scheduler checks for due timers
	idle    atomic.Bool  // the scheduler waits
	fired   atomic.Int64 // notifications sent

	mu sync.Mutex // the timers
	st state

	lm     sync.Mutex // the refresh loops
	closed bool
	root   *rootLoop
	views  map[string]*viewLoop
}

var (
	_ plugin.Plugin         = (*Plugin)(nil)
	_ plugin.RootProvider   = (*Plugin)(nil)
	_ plugin.ViewProvider   = (*Plugin)(nil)
	_ plugin.ActionHandler  = (*Plugin)(nil)
	_ plugin.LocaleProvider = (*Plugin)(nil)
	_ plugin.ViewCloser     = (*Plugin)(nil)
)

// New returns the timers plugin.
func New() *Plugin { return NewWithOptions(Options{}) }

// NewWithOptions returns the plugin with o.
func NewWithOptions(o Options) *Plugin {
	if o.After == nil {
		o.After = time.After
	}
	if o.WaitAfter == nil {
		o.WaitAfter = time.After
	}
	if o.StartupDelay == 0 {
		o.StartupDelay = 3 * time.Second
	}
	if o.MaxWait <= 0 {
		o.MaxWait = 5 * time.Second
	}
	return &Plugin{opts: o, done: make(chan struct{}), wake: make(chan struct{}, 1), views: map[string]*viewLoop{}}
}

// Manifest implements plugin.Plugin.
func (p *Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		ID:            ID,
		Name:          "%plugin.name%",
		Description:   "%plugin.description%",
		Version:       "1.0.0",
		Icon:          plugin.Icon{Name: "timer"},
		DefaultLocale: "en",
		Permissions:   []plugin.Permission{plugin.PermStorage},
		Commands: []plugin.Command{{
			Name:     cmdTimers,
			Title:    "%command.timers.title%",
			Subtitle: "%command.timers.subtitle%",
			Mode:     plugin.ModeView,
			Icon:     plugin.Icon{Name: "timer"},
			Keywords: []string{"%command.timers.keywords%"},
		}},
		Preferences: []plugin.Preference{
			{Name: prefPrefixes, Type: plugin.PrefStringList, Title: "%pref.prefixes.title%",
				Description: "%pref.prefixes.description%", Default: plugin.MustPayload(defaultPrefixes)},
			{Name: prefRemind, Type: plugin.PrefStringList, Title: "%pref.remindPrefixes.title%",
				Description: "%pref.remindPrefixes.description%", Default: plugin.MustPayload(defaultRemind)},
			{Name: prefSound, Type: plugin.PrefBool, Title: "%pref.sound.title%",
				Description: "%pref.sound.description%", Default: plugin.MustPayload(true)},
			{Name: prefHistory, Type: plugin.PrefNumber, Title: "%pref.historySize.title%",
				Description: "%pref.historySize.description%", Default: plugin.MustPayload(10)},
			{Name: prefHour24, Type: plugin.PrefBool, Title: "%pref.hour24.title%",
				Description: "%pref.hour24.description%", Default: plugin.MustPayload(true)},
		},
	}
}

// Locales implements plugin.LocaleProvider.
func (p *Plugin) Locales() fs.FS {
	sub, err := fs.Sub(locales, "locales")
	if err != nil {
		panic(err)
	}
	return sub
}

// Init implements plugin.Plugin: it reads the stored timers and starts the
// scheduler.
func (p *Plugin) Init(_ context.Context, h plugin.Host) error {
	p.h = h
	p.mu.Lock()
	p.load()
	p.mu.Unlock()
	hidden := h.Events().Subscribe(plugin.EventWindowHidden, p.stopAll)
	shown := h.Events().Subscribe(plugin.EventWindowShown, func() {
		// Look again: the computer may have slept.
		select {
		case p.wake <- struct{}{}:
		default:
		}
	})
	p.unsub = func() { hidden(); shown() }
	p.wg.Add(1)
	h.Go(p.schedule)
	return nil
}

// Close implements plugin.Plugin.
func (p *Plugin) Close() error {
	p.lm.Lock()
	if p.closed {
		p.lm.Unlock()
		return nil
	}
	p.closed = true
	p.lm.Unlock()
	close(p.done)
	p.stopAll()
	if p.unsub != nil {
		p.unsub()
	}
	p.wg.Wait()
	return nil
}

// cutPrefix returns the text after a prefix and whether it is a reminder
// prefix ("timer 25m" -> "25m", false). The longest matching prefix wins.
// A prefix must be followed by a space or the end, except prefixes ending
// in a non-ASCII letter ("计时25分钟").
func (p *Plugin) cutPrefix(text string) (rest string, remind, ok bool) {
	text = strings.TrimSpace(text)
	bestLen := -1
	for _, group := range []struct {
		pref   string
		remind bool
	}{{prefPrefixes, false}, {prefRemind, true}} {
		for _, pfx := range p.h.Preferences().Strings(group.pref) {
			pfx = strings.TrimSpace(pfx)
			if pfx == "" || len(pfx) > len(text) || len(pfx) <= bestLen || !strings.EqualFold(text[:len(pfx)], pfx) {
				continue
			}
			r := text[len(pfx):]
			last, _ := utf8.DecodeLastRuneInString(pfx)
			if r != "" && r[0] != ' ' && last < utf8.RuneSelf {
				continue
			}
			rest, remind, ok, bestLen = strings.TrimSpace(r), group.remind, true, len(pfx)
		}
	}
	return rest, remind, ok
}

// RootItems implements plugin.RootProvider. Only prefixed queries get
// results, scored here (the host cannot match text that has the prefix).
func (p *Plugin) RootItems(ctx context.Context, q plugin.Query) ([]plugin.Item, error) {
	rest, remind, ok := p.cutPrefix(q.Text)
	if !ok {
		p.stopRoot()
		return nil, nil
	}
	p.rootPulled()
	b := p.newBuilder(true)
	var items []plugin.Item
	previews := b.previews(rest, !remind, remind)
	items = append(items, previews...)
	for i, e := range b.active {
		if rest == "" || len(previews) > 0 || b.matches(e, rest) {
			it := b.activeItem(e)
			it.Score = 0.9 - 0.001*float64(i)
			items = append(items, it)
		}
	}
	if rest == "" {
		for i, it := range append(b.presetItems(), b.recentItems()...) {
			it.Score = 0.5 - 0.001*float64(i)
			items = append(items, it)
		}
	}
	group := b.f.l.T("group")
	for i := range items {
		items[i].Group = group
	}
	return items, ctx.Err()
}

// builder builds the items of one list.
type builder struct {
	p       *Plugin
	f       formatter
	now     time.Time
	root    bool
	active  []entry
	history []entry
}

func (p *Plugin) newBuilder(root bool) *builder {
	active, history := p.snapshot()
	return &builder{p: p, f: p.formatter(), now: wall(p.h.Now()), root: root, active: active, history: history}
}

func (b *builder) matches(e entry, q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	return q != "" && strings.Contains(strings.ToLower(b.f.name(e)), q)
}

// previews are the items that start what the text describes: a countdown
// when timer is set and the text is a duration, a reminder when remind is
// set and it is a time.
func (b *builder) previews(text string, timer, remind bool) []plugin.Item {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var items []plugin.Item
	if timer {
		if d, label, ok := parseTimer(text); ok {
			items = append(items, b.timerPreview(d, label))
		}
	}
	if remind {
		if r, ok := parseReminder(text, b.now); ok {
			items = append(items, b.reminderPreview(text, r))
		}
	}
	for i := range items {
		items[i].Score = 1 - 0.001*float64(i)
	}
	return items
}

// startPayload is the payload of cbStart.
type startPayload struct {
	Ms    int64  `json:"ms"`
	Label string `json:"label,omitempty"`
	Kind  string `json:"kind,omitempty"` // default kindTimer
	View  bool   `json:"view,omitzero"`  // started from the Timers view
}

// remindPayload is the payload of cbRemind: the text is parsed again when
// the action runs, so that "15:30" means the next 15:30 from then.
type remindPayload struct {
	Text string `json:"text"`
	View bool   `json:"view,omitzero"`
}

// idPayload is the payload of the actions on one timer.
type idPayload struct {
	ID string `json:"id"`
	Ms int64  `json:"ms,omitzero"` // cbAdd
}

func callback(target string, payload any) plugin.ActionDo {
	return plugin.ActionDo{Kind: plugin.ActionCallback, Target: target, Payload: plugin.MustPayload(payload)}
}

func (b *builder) startAction(d time.Duration, label, kind string) plugin.Action {
	title := b.f.l.T("action.start")
	if kind == kindReminder {
		title = b.f.l.T("action.setReminder")
	}
	return plugin.Action{ID: "start", Title: title, Do: callback(cbStart, startPayload{Ms: ms(d), Label: label, Kind: kind, View: !b.root})}
}

func (b *builder) timerPreview(d time.Duration, label string) plugin.Item {
	l := b.f.l
	title := l.T("preview.timer", "duration", b.f.duration(d))
	if label != "" {
		title = l.T("preview.timerLabel", "duration", b.f.duration(d), "label", label)
	}
	return plugin.Item{
		ID:       "new:timer",
		Title:    title,
		Subtitle: l.T("preview.ends", "when", b.f.when(b.now.Add(d), b.now)),
		Icon:     plugin.Icon{Name: "timer"},
		Actions:  []plugin.Action{b.startAction(d, label, kindTimer)},
	}
}

func (b *builder) reminderPreview(text string, r reminder) plugin.Item {
	l := b.f.l
	when := b.f.when(r.At, b.now)
	title := l.T("preview.reminder", "when", when)
	if r.Label != "" {
		title = l.T("preview.reminderLabel", "label", r.Label)
	}
	sub := l.T("preview.due", "when", when, "in", b.f.approx(r.At.Sub(b.now)))
	if r.Rolled {
		sub = l.T("preview.rolled", "subtitle", sub)
	}
	if r.DefaultTime {
		sub = l.T("preview.defaultTime", "subtitle", sub, "time", b.f.timeOfDay(r.At))
	}
	return plugin.Item{
		ID:       "new:reminder",
		Title:    title,
		Subtitle: sub,
		Icon:     plugin.Icon{Name: "alarm-clock"},
		Actions: []plugin.Action{{ID: "start", Title: l.T("action.setReminder"),
			Do: callback(cbRemind, remindPayload{Text: text, View: !b.root})}},
	}
}

// activeItem is the item of a running or paused timer.
func (b *builder) activeItem(e entry) plugin.Item {
	l, f := b.f.l, b.f
	left := e.left(b.now)
	it := plugin.Item{
		ID:    "timer:" + e.ID,
		Title: l.T("item.title", "left", clock(left), "name", f.name(e)),
		Icon:  plugin.Icon{Name: "timer", Tint: plugin.TintAccent},
	}
	if e.Kind == kindReminder {
		it.Icon.Name = "alarm-clock"
	}
	switch {
	case e.Paused:
		it.Subtitle = l.T("item.paused", "left", clock(left))
		it.Accessory = l.T("item.pausedAccessory")
		it.Icon = plugin.Icon{Name: "pause", Tint: plugin.TintMuted}
	case e.Kind == kindReminder:
		it.Subtitle = l.T("item.due", "when", f.when(e.End, b.now))
		it.Accessory = l.T("item.in", "in", f.approx(left))
	default:
		it.Subtitle = l.T("item.ends", "when", f.when(e.End, b.now))
		it.Accessory = l.T("item.progress", "percent", e.percent(b.now))
	}
	it.Detail = b.detail(e)
	id := idPayload{ID: e.ID}
	var acts []plugin.Action
	if e.Paused {
		acts = append(acts, plugin.Action{ID: "resume", Title: l.T("action.resume"), Do: callback(cbResume, id)})
	} else {
		acts = append(acts, plugin.Action{ID: "pause", Title: l.T("action.pause"), Do: callback(cbPause, id)})
	}
	acts = append(acts,
		plugin.Action{ID: "add5", Title: l.T("action.add", "duration", f.duration(5*time.Minute)), Do: callback(cbAdd, idPayload{ID: e.ID, Ms: ms(5 * time.Minute)})},
		plugin.Action{ID: "add1", Title: l.T("action.add", "duration", f.duration(time.Minute)), Do: callback(cbAdd, idPayload{ID: e.ID, Ms: ms(time.Minute)})},
	)
	if e.Kind == kindTimer {
		acts = append(acts, plugin.Action{ID: "restart", Title: l.T("action.restart"), Shortcut: "Ctrl+R", Do: callback(cbRestart, id)})
	}
	acts = append(acts, plugin.Action{ID: "rename", Title: l.T("action.rename"),
		Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: viewRename, Payload: plugin.MustPayload(id)}})
	if !e.Paused {
		acts = append(acts, plugin.Action{ID: "copy", Title: l.T("action.copyEnd"), Shortcut: "Ctrl+Shift+C",
			Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: f.timeOfDay(e.End.In(b.now.Location()))}})
	}
	acts = append(acts, plugin.Action{ID: "cancel", Title: l.T("action.cancel"), Shortcut: "Ctrl+X", Do: callback(cbCancel, id)})
	if b.root {
		acts = append(acts, plugin.Action{ID: "open", Title: l.T("action.openView"), Do: plugin.ActionDo{Kind: plugin.ActionPush, Target: cmdTimers}})
	}
	it.Actions = acts
	return it
}

// detail is the detail pane of a timer.
func (b *builder) detail(e entry) *plugin.Detail {
	l, f := b.f.l, b.f
	kind := l.T("detail.kindTimer")
	if e.Kind == kindReminder {
		kind = l.T("detail.kindReminder")
	}
	md := []plugin.Metadata{{Label: l.T("detail.kind"), Value: kind}}
	if e.Label != "" {
		md = append(md, plugin.Metadata{Label: l.T("detail.label"), Value: e.Label})
	}
	md = append(md,
		plugin.Metadata{Label: l.T("detail.started"), Value: f.stamp(e.Start, b.now)},
		plugin.Metadata{Label: l.T("detail.length"), Value: f.duration(e.total())},
	)
	switch {
	case e.State == stateDone:
		md = append(md, plugin.Metadata{Label: l.T("detail.ended"), Value: f.stamp(e.End, b.now)})
	case e.State == stateCancelled:
		md = append(md, plugin.Metadata{Label: l.T("detail.cancelled"), Value: f.stamp(e.Finished, b.now)},
			plugin.Metadata{Label: l.T("detail.left"), Value: clock(e.left(b.now))})
	case e.Paused:
		md = append(md, plugin.Metadata{Label: l.T("detail.left"), Value: clock(e.left(b.now))})
	default:
		md = append(md, plugin.Metadata{Label: l.T("detail.ends"), Value: f.stamp(e.End, b.now)},
			plugin.Metadata{Label: l.T("detail.left"), Value: clock(e.left(b.now))},
			plugin.Metadata{Label: l.T("detail.progress"), Value: l.T("item.progress", "percent", e.percent(b.now))})
	}
	return &plugin.Detail{Metadata: md}
}

// presetItems start the built-in presets.
func (b *builder) presetItems() []plugin.Item {
	l := b.f.l
	var items []plugin.Item
	for _, ps := range presets {
		name := ps.name(l)
		items = append(items, plugin.Item{
			ID:       "preset:" + strconv.FormatInt(ms(ps.d), 10),
			Title:    l.T("preset.title", "name", name, "duration", b.f.duration(ps.d)),
			Subtitle: l.T("preview.ends", "when", b.f.when(b.now.Add(ps.d), b.now)),
			Icon:     plugin.Icon{Name: "timer"},
			Actions:  []plugin.Action{b.startAction(ps.d, name, kindTimer)},
		})
	}
	return items
}

// recentItems start again the last countdowns used (distinct lengths and
// labels, not the presets).
func (b *builder) recentItems() []plugin.Item {
	l := b.f.l
	seen := map[string]bool{}
	for _, ps := range presets {
		seen[strconv.FormatInt(ms(ps.d), 10)+"\x00"+ps.name(l)] = true
	}
	var items []plugin.Item
	for _, e := range b.history {
		key := strconv.FormatInt(e.TotalMs, 10) + "\x00" + e.Label
		if e.Kind != kindTimer || seen[key] || e.TotalMs <= 0 {
			continue
		}
		seen[key] = true
		title := l.T("name.timer", "duration", b.f.duration(e.total()))
		if e.Label != "" {
			title = l.T("recent.title", "label", e.Label, "duration", b.f.duration(e.total()))
		}
		items = append(items, plugin.Item{
			ID:        "recent:" + key,
			Title:     title,
			Subtitle:  l.T("recent.subtitle"),
			Accessory: l.RelativeTime(e.Finished, b.now),
			Icon:      plugin.Icon{Name: "history"},
			Actions:   []plugin.Action{b.startAction(e.total(), e.Label, kindTimer)},
		})
		if len(items) == recentMax {
			break
		}
	}
	return items
}

// historyItem is a finished or cancelled timer in the Timers view.
func (b *builder) historyItem(e entry) plugin.Item {
	l, f := b.f.l, b.f
	it := plugin.Item{
		ID:        "history:" + e.ID,
		Title:     f.name(e),
		Accessory: l.RelativeTime(e.Finished, b.now),
		Icon:      plugin.Icon{Name: "history", Tint: plugin.TintMuted},
		Detail:    b.detail(e),
	}
	id := idPayload{ID: e.ID}
	var acts []plugin.Action
	switch {
	case e.State == stateCancelled:
		it.Subtitle = l.T("history.cancelled", "left", clock(e.left(b.now)))
		acts = append(acts, plugin.Action{ID: "restore", Title: l.T("action.restore"), Do: callback(cbRestore, id)})
	case e.Missed:
		it.Subtitle = l.T("history.missed", "when", f.when(e.End, b.now))
	default:
		it.Subtitle = l.T("history.done", "when", f.when(e.End, b.now))
	}
	if e.Kind == kindTimer {
		acts = append(acts, plugin.Action{ID: "again", Title: l.T("action.again"), Do: callback(cbStart, startPayload{Ms: e.TotalMs, Label: e.Label, View: true})})
	}
	acts = append(acts,
		plugin.Action{ID: "snooze", Title: l.T("action.snooze", "duration", f.duration(snooze)), Do: callback(cbSnooze, id)},
		plugin.Action{ID: "forget", Title: l.T("action.forget"), Shortcut: "Ctrl+X", Do: callback(cbForget, id)},
	)
	it.Actions = acts
	return it
}

// View implements plugin.ViewProvider: the Timers view and the rename form.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	switch req.ID {
	case cmdTimers:
		if req.Instance == "" {
			return p.timersView(req.Query.Text), nil
		}
		vl := p.viewPulled(req.Instance)
		if vl == nil {
			return p.timersView(req.Query.Text), nil
		}
		// The loop renders with the query under the same lock, so no push
		// made after this pull uses the old one.
		vl.mu.Lock()
		defer vl.mu.Unlock()
		vl.query = req.Query.Text
		return p.timersView(vl.query), ctx.Err()
	case viewRename:
		return p.renameView(req)
	}
	return plugin.View{}, fmt.Errorf("timer: unknown view %q", req.ID)
}

// timersView is the Timers view for search text q, which may also describe
// a new timer or reminder.
func (p *Plugin) timersView(q string) plugin.View {
	b := p.newBuilder(false)
	l := b.f.l
	v := plugin.View{
		Kind:        plugin.ViewList,
		Title:       l.T("view.title"),
		Placeholder: l.T("view.placeholder"),
		EmptyText:   l.T("view.empty"),
		ShowDetail:  true,
	}
	text := strings.TrimSpace(q)
	timer, remind := true, true
	if rest, isRemind, ok := p.cutPrefix(text); ok {
		text, timer, remind = rest, !isRemind, isRemind
	}
	if previews := b.previews(text, timer, remind); len(previews) > 0 {
		for i := range previews {
			previews[i].Score = 0
		}
		v.Sections = append(v.Sections, plugin.Section{Title: l.T("section.new"), Items: previews})
	}
	filter := func(e entry) bool { return text == "" || b.matches(e, text) }
	if len(v.Sections) > 0 {
		filter = func(entry) bool { return true }
	}
	var running, history []plugin.Item
	for _, e := range b.active {
		if filter(e) {
			running = append(running, b.activeItem(e))
		}
	}
	for _, e := range b.history {
		if filter(e) {
			history = append(history, b.historyItem(e))
		}
	}
	if len(running) > 0 {
		v.Sections = append(v.Sections, plugin.Section{Title: l.T("section.running"), Items: running})
	}
	if text == "" {
		v.Sections = append(v.Sections, plugin.Section{Title: l.T("section.presets"), Items: b.presetItems()})
	}
	if len(history) > 0 {
		v.Sections = append(v.Sections, plugin.Section{Title: l.T("section.history"), Items: history})
	}
	return v
}

// renameView is the form that renames a timer.
func (p *Plugin) renameView(req plugin.ViewRequest) (plugin.View, error) {
	var pl idPayload
	if err := json.Unmarshal(req.Payload, &pl); err != nil {
		return plugin.View{}, err
	}
	l := p.h.I18n()
	label := ""
	p.mu.Lock()
	if e, _ := p.findLocked(pl.ID); e != nil {
		label = e.Label
	}
	p.mu.Unlock()
	return plugin.View{Kind: plugin.ViewForm, Title: l.T("rename.title"), Form: &plugin.Form{
		Fields: []plugin.Field{{ID: fieldLabel, Type: plugin.FieldText, Title: l.T("rename.label"),
			Placeholder: l.T("rename.placeholder"), Default: plugin.MustPayload(label)}},
		Submit: plugin.Action{ID: "rename", Title: l.T("action.rename"), Do: callback(cbRename, pl)},
	}}, nil
}

// HandleAction implements plugin.ActionHandler.
func (p *Plugin) HandleAction(_ context.Context, a plugin.ActionDo) (plugin.Result, error) {
	f := p.formatter()
	l := f.l
	now := wall(p.h.Now())
	switch a.Target {
	case cbStart:
		var pl startPayload
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, err
		}
		d := time.Duration(pl.Ms) * time.Millisecond
		if d <= 0 || d > maxDuration {
			return plugin.Result{}, errors.New(l.T("error.duration"))
		}
		kind := kindTimer
		if pl.Kind == kindReminder {
			kind = kindReminder
		}
		e := p.add(kind, strings.TrimSpace(pl.Label), now, now.Add(d))
		return p.started(f, e, now, pl.View), nil
	case cbRemind:
		var pl remindPayload
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, err
		}
		r, ok := parseReminder(pl.Text, now)
		if !ok {
			return plugin.Result{}, errors.New(l.T("error.reminder", "text", pl.Text))
		}
		e := p.add(kindReminder, r.Label, now, r.At)
		return p.started(f, e, now, pl.View), nil
	case cbSnooze:
		var pl idPayload
		if err := json.Unmarshal(a.Payload, &pl); err != nil {
			return plugin.Result{}, err
		}
		old, ok := p.historyEntry(pl.ID)
		if !ok {
			return gone(l), nil
		}
		e := p.add(old.Kind, old.Label, now, now.Add(snooze))
		return p.started(f, e, now, true), nil
	}
	var pl idPayload
	if err := json.Unmarshal(a.Payload, &pl); err != nil {
		return plugin.Result{}, err
	}
	var (
		e     entry
		ok    bool
		toast func(name string) string
	)
	switch a.Target {
	case cbPause:
		e, ok = p.update(pl.ID, pause)
		toast = func(n string) string { return l.T("toast.paused", "name", n) }
	case cbResume:
		e, ok = p.update(pl.ID, resume)
		toast = func(n string) string { return l.T("toast.resumed", "name", n) }
	case cbAdd:
		d := time.Duration(pl.Ms) * time.Millisecond
		if d <= 0 || d > time.Hour {
			return plugin.Result{}, errors.New(l.T("error.duration"))
		}
		e, ok = p.update(pl.ID, addTime(d))
		if ok {
			return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess,
				Title: l.T("toast.added", "duration", f.duration(d), "name", f.name(e)), Message: endsText(f, e, now)}}, nil
		}
	case cbRestart:
		e, ok = p.update(pl.ID, restart)
		toast = func(n string) string { return l.T("toast.restarted", "name", n) }
	case cbCancel:
		e, ok = p.cancel(pl.ID)
		toast = func(n string) string { return l.T("toast.cancelled", "name", n) }
	case cbRestore:
		e, ok = p.restore(pl.ID)
		toast = func(n string) string { return l.T("toast.restored", "name", n) }
	case cbForget:
		if !p.forget(pl.ID) {
			return gone(l), nil
		}
		return plugin.Result{Refresh: true}, nil
	case cbRename:
		var label string
		if raw, ok := a.Values[fieldLabel]; ok {
			if err := json.Unmarshal(raw, &label); err != nil {
				return plugin.Result{}, err
			}
		}
		e, ok = p.update(pl.ID, rename(strings.TrimSpace(label)))
		if !ok {
			return plugin.Result{PopToRoot: true, Toast: gone(l).Toast}, nil
		}
		return plugin.Result{PopToRoot: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: l.T("toast.renamed", "name", f.name(e))}}, nil
	default:
		return plugin.Result{}, fmt.Errorf("timer: unknown action %q", a.Target)
	}
	if !ok {
		return gone(l), nil
	}
	return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: toast(f.name(e)), Message: endsText(f, e, now)}}, nil
}

// started is the result of starting a timer: from the root search the
// launcher hides; in the Timers view the search field is cleared.
func (p *Plugin) started(f formatter, e entry, now time.Time, view bool) plugin.Result {
	t := &plugin.Toast{Style: plugin.ToastSuccess, Title: f.l.T("toast.started", "name", f.name(e)), Message: endsText(f, e, now)}
	if e.Kind == kindReminder {
		t.Title = f.l.T("toast.reminderSet", "name", f.name(e))
	}
	if view {
		return plugin.Result{ClearText: true, Toast: t}
	}
	return plugin.Result{Hide: true, Toast: t}
}

// endsText says when a timer ends, or what is left of a paused one.
func endsText(f formatter, e entry, now time.Time) string {
	switch {
	case e.State == stateCancelled:
		return ""
	case e.Paused:
		return f.l.T("item.paused", "left", clock(e.left(now)))
	case e.Kind == kindReminder:
		return f.l.T("item.due", "when", f.when(e.End, now))
	}
	return f.l.T("item.ends", "when", f.when(e.End, now))
}

// gone is the result of an action on a timer that no longer exists (it
// ended, or the action came from an old entry of the recently used list).
func gone(l plugin.Localizer) plugin.Result {
	return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("toast.gone")}}
}
