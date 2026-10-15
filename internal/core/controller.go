package core

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"golauncher/internal/match"
	"golauncher/pkg/plugin"
)

// Controller is the launcher's interaction state machine: search text,
// selection, action panel and navigation stack. Front ends feed it text and
// key presses and render its Snapshot. It is safe for concurrent use;
// searches run in the background and OnChange is called when they finish.
type Controller struct {
	eng *Engine

	mu       sync.Mutex
	stack    []*frame // stack[0] is the root search
	panel    *panel
	toast    *plugin.Toast
	onChange func()

	gen     int // incremented for every search; stale results are dropped
	cancel  context.CancelFunc
	pending sync.WaitGroup
}

// frame is one level of the navigation stack.
type frame struct {
	nav      *Nav // nil for the root
	text     string
	sel      int
	loading  bool
	err      string
	sections []Section
	view     *plugin.View // the last view returned by the plugin
}

// panel is the open action panel.
type panel struct {
	entry Entry
	text  string
	sel   int
}

// NewController returns a Controller showing the root search.
func NewController(eng *Engine) *Controller {
	return &Controller{eng: eng, stack: []*frame{{}}}
}

// OnChange sets fn to be called (on a background goroutine) whenever the
// snapshot changes asynchronously.
func (c *Controller) OnChange(fn func()) {
	c.mu.Lock()
	c.onChange = fn
	c.mu.Unlock()
}

func (c *Controller) top() *frame { return c.stack[len(c.stack)-1] }

// Wait blocks until pending searches have finished.
func (c *Controller) Wait() { c.pending.Wait() }

// Reset pops to the root, clears the text and closes the panel. Front ends
// call it when the window is shown.
func (c *Controller) Reset(ctx context.Context) {
	c.mu.Lock()
	c.stack = []*frame{{}}
	c.panel = nil
	c.toast = nil
	c.mu.Unlock()
	c.refresh(ctx)
}

// Refresh re-runs the current search or view.
func (c *Controller) Refresh(ctx context.Context) { c.refresh(ctx) }

// SetText sets the search text of the current view, or the filter of the
// open action panel.
func (c *Controller) SetText(ctx context.Context, text string) {
	c.mu.Lock()
	if c.panel != nil {
		c.panel.text = text
		c.panel.sel = 0
		c.mu.Unlock()
		return
	}
	f := c.top()
	if f.text == text {
		c.mu.Unlock()
		return
	}
	f.text = text
	f.sel = 0
	local := f.view != nil && f.view.FilterLocally
	c.mu.Unlock()
	if !local {
		c.refresh(ctx)
	}
}

// refresh starts a search for the top frame in the background.
func (c *Controller) refresh(ctx context.Context) {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.gen++
	gen := c.gen
	f := c.top()
	f.loading = true
	text, nav := f.text, f.nav
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.cancel = cancel
	c.pending.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.pending.Done()
		defer cancel()
		var (
			sections []Section
			view     *plugin.View
			err      error
		)
		if nav == nil {
			sections, err = c.eng.RootSearch(ctx, text)
		} else {
			req := nav.Request
			req.Query = plugin.Query{Text: text}
			var v plugin.View
			v, err = c.eng.View(ctx, Nav{Plugin: nav.Plugin, Request: req})
			if err == nil {
				view = &v
				sections = viewSections(nav.Plugin, v)
			}
		}
		c.mu.Lock()
		if gen != c.gen || ctx.Err() != nil {
			c.mu.Unlock()
			return
		}
		f.loading = false
		f.err = ""
		if err != nil {
			f.err = err.Error()
			c.eng.log.Warn("search failed", "err", err)
		} else {
			f.sections, f.view = sections, view
		}
		f.sel = min(f.sel, max(0, countItems(c.visible(f))-1))
		fn := c.onChange
		c.mu.Unlock()
		if fn != nil {
			fn()
		}
	}()
}

func viewSections(pluginID string, v plugin.View) []Section {
	out := make([]Section, 0, len(v.Sections))
	for _, s := range v.Sections {
		sec := Section{Title: s.Title, Items: make([]Entry, 0, len(s.Items))}
		for _, it := range s.Items {
			sec.Items = append(sec.Items, newEntry(pluginID, it))
		}
		out = append(out, sec)
	}
	return out
}

// visible returns the sections shown for f, applying local filtering.
func (c *Controller) visible(f *frame) []Section {
	if f.view == nil || !f.view.FilterLocally || strings.TrimSpace(f.text) == "" {
		return f.sections
	}
	var out []Section
	for _, s := range f.sections {
		var items []Entry
		for _, it := range s.Items {
			if m := entryMatch(f.text, it); m > 0 {
				it.Rank = m
				items = append(items, it)
			}
		}
		slices.SortStableFunc(items, func(a, b Entry) int { return cmp.Compare(b.Rank, a.Rank) })
		if len(items) > 0 {
			out = append(out, Section{Title: s.Title, Items: items})
		}
	}
	return out
}

func countItems(sections []Section) int {
	n := 0
	for _, s := range sections {
		n += len(s.Items)
	}
	return n
}

func itemAt(sections []Section, i int) (Entry, bool) {
	for _, s := range sections {
		if i < len(s.Items) {
			return s.Items[i], i >= 0
		}
		i -= len(s.Items)
	}
	return Entry{}, false
}

// Select sets the selected row (e.g. on mouse hover or click).
func (c *Controller) Select(i int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.panel != nil {
		c.panel.sel = clamp(i, len(c.panelActions()))
		return
	}
	f := c.top()
	f.sel = clamp(i, countItems(c.visible(f)))
}

func clamp(i, n int) int { return max(0, min(i, n-1)) }

// panelActions returns the panel's actions filtered by its text. c.mu must be held.
func (c *Controller) panelActions() []plugin.Action {
	if c.panel == nil {
		return nil
	}
	if strings.TrimSpace(c.panel.text) == "" {
		return c.panel.entry.Actions
	}
	var out []plugin.Action
	for _, a := range c.panel.entry.Actions {
		if match.Score(c.panel.text, a.Title) > 0 {
			out = append(out, a)
		}
	}
	return out
}

// Key handles a key press such as "Down", "Enter", "Ctrl+K" or an action
// shortcut, and returns what the front end should do.
func (c *Controller) Key(ctx context.Context, key string) (Outcome, error) {
	key = normalizeShortcut(key)
	// Act on the results of what has been typed so far.
	if key != "Up" && key != "Down" {
		c.Wait()
	}
	c.mu.Lock()
	if c.panel != nil {
		return c.panelKey(ctx, key) // unlocks
	}
	f := c.top()
	n := countItems(c.visible(f))
	switch key {
	case "Down", "Ctrl+N":
		if n > 0 {
			f.sel = (f.sel + 1) % n
		}
		c.mu.Unlock()
		return Outcome{}, nil
	case "Up", "Ctrl+P":
		if n > 0 {
			f.sel = (f.sel - 1 + n) % n
		}
		c.mu.Unlock()
		return Outcome{}, nil
	case "Escape":
		switch {
		case f.text != "":
			c.mu.Unlock()
			c.SetText(ctx, "")
			return Outcome{}, nil
		case len(c.stack) > 1:
			c.stack = c.stack[:len(c.stack)-1]
			c.mu.Unlock()
			c.refresh(ctx)
			return Outcome{}, nil
		default:
			c.mu.Unlock()
			return Outcome{Hide: true}, nil
		}
	case "Backspace":
		// Backspace in an empty search field goes back, as in Raycast.
		if f.text == "" && len(c.stack) > 1 {
			c.stack = c.stack[:len(c.stack)-1]
			c.mu.Unlock()
			c.refresh(ctx)
			return Outcome{}, nil
		}
		c.mu.Unlock()
		return Outcome{}, nil
	}
	entry, ok := itemAt(c.visible(f), f.sel)
	if !ok {
		c.mu.Unlock()
		return Outcome{}, nil
	}
	if key == "Ctrl+K" {
		c.panel = &panel{entry: entry}
		c.mu.Unlock()
		return Outcome{}, nil
	}
	var action plugin.Action
	switch key {
	case "Enter":
		if len(entry.Actions) == 0 {
			c.mu.Unlock()
			return Outcome{}, nil
		}
		action = entry.Actions[0]
	case "Ctrl+Enter":
		if len(entry.Actions) < 2 {
			c.mu.Unlock()
			return Outcome{}, nil
		}
		action = entry.Actions[1]
	default:
		i := slices.IndexFunc(entry.Actions, func(a plugin.Action) bool {
			return a.Shortcut != "" && normalizeShortcut(a.Shortcut) == key
		})
		if i < 0 {
			c.mu.Unlock()
			return Outcome{}, nil
		}
		action = entry.Actions[i]
	}
	c.mu.Unlock()
	return c.run(ctx, entry, action)
}

// panelKey handles a key while the action panel is open. c.mu is held on
// entry and released before returning.
func (c *Controller) panelKey(ctx context.Context, key string) (Outcome, error) {
	actions := c.panelActions()
	n := len(actions)
	switch key {
	case "Down", "Ctrl+N":
		if n > 0 {
			c.panel.sel = (c.panel.sel + 1) % n
		}
	case "Up", "Ctrl+P":
		if n > 0 {
			c.panel.sel = (c.panel.sel - 1 + n) % n
		}
	case "Escape", "Ctrl+K":
		c.panel = nil
	case "Enter":
		if n == 0 {
			break
		}
		entry, action := c.panel.entry, actions[clamp(c.panel.sel, n)]
		c.panel = nil
		c.mu.Unlock()
		return c.run(ctx, entry, action)
	default:
		i := slices.IndexFunc(c.panel.entry.Actions, func(a plugin.Action) bool {
			return a.Shortcut != "" && normalizeShortcut(a.Shortcut) == key
		})
		if i >= 0 {
			entry := c.panel.entry
			c.panel = nil
			c.mu.Unlock()
			return c.run(ctx, entry, entry.Actions[i])
		}
	}
	c.mu.Unlock()
	return Outcome{}, nil
}

// Activate runs the primary action of row i (e.g. on double click).
func (c *Controller) Activate(ctx context.Context, i int) (Outcome, error) {
	c.Select(i)
	return c.Key(ctx, "Enter")
}

// RunAction runs the action with id of the entry with key in the current view.
func (c *Controller) RunAction(ctx context.Context, entryKey, actionID string) (Outcome, error) {
	c.Wait()
	c.mu.Lock()
	entry, ok := FindEntry(c.visible(c.top()), entryKey)
	c.mu.Unlock()
	if !ok {
		return Outcome{}, fmt.Errorf("core: no entry %q in the current view", entryKey)
	}
	action, ok := FindAction(entry, actionID)
	if !ok {
		return Outcome{}, fmt.Errorf("core: entry %q has no action %q", entryKey, actionID)
	}
	return c.run(ctx, entry, action)
}

// OpenCommand resets to the root and runs a command, as its hotkey does.
func (c *Controller) OpenCommand(ctx context.Context, pluginID, name string) (Outcome, error) {
	c.mu.Lock()
	c.stack = []*frame{{}}
	c.panel = nil
	c.mu.Unlock()
	entry, ok := c.eng.commandEntry(c.eng.cfg.Get(), pluginID, name)
	if !ok {
		return Outcome{}, fmt.Errorf("core: unknown command %s/%s", pluginID, name)
	}
	return c.run(ctx, entry, entry.Actions[0])
}

// run executes an action and applies its outcome to the navigation state.
func (c *Controller) run(ctx context.Context, entry Entry, action plugin.Action) (Outcome, error) {
	c.mu.Lock()
	fromRoot := len(c.stack) == 1
	c.mu.Unlock()
	out, err := c.eng.Run(ctx, entry, action, fromRoot)
	if err != nil {
		c.setToast(&plugin.Toast{Style: plugin.ToastFailure, Title: c.eng.core.T("toast.actionFailed"), Message: err.Error()})
		return out, err
	}
	if out.Toast != nil {
		c.setToast(out.Toast)
	}
	switch {
	case out.Push != nil:
		c.mu.Lock()
		c.stack = append(c.stack, &frame{nav: out.Push})
		c.mu.Unlock()
		c.refresh(ctx)
	case out.PopToRoot:
		c.mu.Lock()
		c.stack = []*frame{{}}
		c.mu.Unlock()
		c.refresh(ctx)
	case out.Refresh:
		c.refresh(ctx)
	}
	return out, nil
}

func (c *Controller) setToast(t *plugin.Toast) {
	c.mu.Lock()
	c.toast = t
	c.mu.Unlock()
}

// ClearToast removes the current toast.
func (c *Controller) ClearToast() {
	c.mu.Lock()
	c.toast = nil
	c.mu.Unlock()
}

// Snapshot is everything a front end needs to render the launcher.
type Snapshot struct {
	// Depth is 0 for the root search and grows with each pushed view.
	Depth       int       `json:"depth"`
	Title       string    `json:"title,omitempty"`
	Placeholder string    `json:"placeholder"`
	Text        string    `json:"text"`
	Loading     bool      `json:"loading,omitzero"`
	Error       string    `json:"error,omitempty"`
	Sections    []Section `json:"sections"`
	// Selected is the index of the selected item across all sections, -1 if none.
	Selected   int            `json:"selected"`
	ShowDetail bool           `json:"showDetail,omitzero"`
	Detail     *plugin.Detail `json:"detail,omitempty"`
	EmptyText  string         `json:"emptyText,omitempty"`
	Panel      *PanelSnapshot `json:"panel,omitempty"`
	Toast      *plugin.Toast  `json:"toast,omitempty"`
}

// PanelSnapshot is the open action panel.
type PanelSnapshot struct {
	Title    string          `json:"title"`
	Text     string          `json:"text"`
	Actions  []plugin.Action `json:"actions"`
	Selected int             `json:"selected"`
}

// SelectedEntry returns the selected entry of s.
func (s Snapshot) SelectedEntry() (Entry, bool) {
	return itemAt(s.Sections, s.Selected)
}

// Snapshot returns the current state.
func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.top()
	core := c.eng.core
	sections := c.visible(f)
	s := Snapshot{
		Depth:       len(c.stack) - 1,
		Placeholder: core.T("search.placeholder"),
		Text:        f.text,
		Loading:     f.loading,
		Error:       f.err,
		Sections:    sections,
		Selected:    -1,
		Toast:       c.toast,
		EmptyText:   core.T("search.empty"),
	}
	if f.view != nil {
		s.Title = f.view.Title
		s.Placeholder = cmp.Or(f.view.Placeholder, core.T("search.placeholderView"))
		s.ShowDetail = f.view.ShowDetail
		s.EmptyText = cmp.Or(f.view.EmptyText, s.EmptyText)
		s.Detail = f.view.Detail
	}
	if n := countItems(sections); n > 0 {
		s.Selected = clamp(f.sel, n)
		if e, ok := itemAt(sections, s.Selected); ok && s.ShowDetail && e.Detail != nil {
			s.Detail = e.Detail
		}
	}
	if c.panel != nil {
		actions := c.panelActions()
		s.Panel = &PanelSnapshot{
			Title:    c.panel.entry.Title,
			Text:     c.panel.text,
			Actions:  actions,
			Selected: clamp(c.panel.sel, len(actions)),
		}
	}
	return s
}
