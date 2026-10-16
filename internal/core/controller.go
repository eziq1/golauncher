package core

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/match"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Coalescing of plugin notifications (docs/m5-plugin-platform.md §2, §5.4).
var (
	// invalidateDelay gathers the Host.Invalidate calls of a burst into one
	// refresh.
	invalidateDelay = 50 * time.Millisecond
	// renderInterval is the shortest time between two pushed views applied
	// to one view instance; pushes in between are coalesced to the latest.
	renderInterval = 16 * time.Millisecond
)

// Controller is the launcher's interaction state machine: search text,
// selection, action panel, navigation stack, form and argument inputs. Front
// ends feed it text and key presses and render its Snapshot. It is safe for
// concurrent use; searches run in the background and OnChange is called when
// they finish.
type Controller struct {
	eng *Engine

	mu      sync.Mutex
	stack   []*frame // stack[0] is the root search
	panel   *panel
	confirm *pending // a destructive action waiting for confirmation
	// external are the confirmations AI clients asked for (external.go);
	// the first one is displayed.
	external   []*extPending
	onExternal func(ExternalRequest)
	toast      *plugin.Toast
	onChange   func()

	gen    int // incremented for every search; stale results are dropped
	cancel context.CancelFunc
	// running counts searches in flight and pending the invalidations and
	// pushed views waiting to be applied; idle is signalled when they
	// change. (A WaitGroup cannot be used: Wait may run concurrently with Add.)
	running int
	pending int
	idle    *sync.Cond

	instances int // view instances opened so far, for their IDs

	// Invalidations gathered while invTimer runs; invAfter asks for one more
	// refresh once the searches in flight have finished.
	inv      []invalidation
	invTimer bool
	invAfter bool
}

// invalidation is a Host.Invalidate call.
type invalidation struct{ plugin, view string }

// frame is one level of the navigation stack.
type frame struct {
	nav    *Nav // nil for the root
	text   string
	sel    int
	selKey string // key of the selected entry, kept across refreshes
	// moved is set once the user moved the selection (keys, hover, click)
	// since the text last changed; until then refreshes that add results
	// above (the context arriving) keep the first row selected.
	moved    bool
	loading  bool
	err      string
	sections []Section
	view     *plugin.View // the last view returned or pushed by the plugin
	form     *formState   // the inputs of a form view
	args     *argState    // the arguments typed for the selected entry

	// Views pushed with Host.Render: the latest one waiting for the rate
	// limit, whether a timer will apply it, and when the last one was applied.
	pushed    *plugin.View
	pushTimer bool
	lastPush  time.Time
}

// panel is the open action panel.
type panel struct {
	entry Entry
	text  string
	sel   int
}

// pending is an action awaiting confirmation.
type pending struct {
	entry  Entry
	action plugin.Action
}

// NewController returns a Controller showing the root search.
func NewController(eng *Engine) *Controller {
	c := &Controller{eng: eng, stack: []*frame{{}}}
	c.idle = sync.NewCond(&c.mu)
	eng.subscribe(c)
	return c
}

// OnChange sets fn to be called (on a background goroutine) whenever the
// snapshot changes asynchronously.
func (c *Controller) OnChange(fn func()) {
	c.mu.Lock()
	c.onChange = fn
	c.mu.Unlock()
}

func (c *Controller) top() *frame { return c.stack[len(c.stack)-1] }

// Wait blocks until pending searches have finished, including the refreshes
// of coalesced invalidations and the views pushed by plugins.
func (c *Controller) Wait() {
	c.mu.Lock()
	for c.running > 0 || c.pending > 0 {
		c.idle.Wait()
	}
	c.mu.Unlock()
}

// waitSearches blocks until the searches in flight have finished.
func (c *Controller) waitSearches() {
	c.mu.Lock()
	for c.running > 0 {
		c.idle.Wait()
	}
	c.mu.Unlock()
}

// searchDone marks one search finished. c.mu must be held.
func (c *Controller) searchDone() {
	c.running--
	c.idle.Broadcast()
}

// pendingDone marks a coalesced notification handled. c.mu must be held.
func (c *Controller) pendingDone() {
	c.pending--
	c.idle.Broadcast()
}

// truncate pops the stack down to n frames and returns the views closed.
// c.mu must be held; pass the result to closed after unlocking.
func (c *Controller) truncate(n int) []*Nav {
	var navs []*Nav
	for _, f := range c.stack[n:] {
		if f.nav != nil {
			navs = append(navs, f.nav)
		}
	}
	clear(c.stack[n:])
	c.stack = c.stack[:n]
	return navs
}

// toRoot pops every view and starts a new root search. c.mu must be held.
func (c *Controller) toRoot() []*Nav {
	navs := c.truncate(0)
	c.stack = append(c.stack, &frame{})
	return navs
}

// closed tells the engine about closed view instances.
func (c *Controller) closed(navs []*Nav) { c.eng.viewsClosed(navs) }

// Reset pops to the root, clears the text and closes the panel. Front ends
// call it when the window is shown.
func (c *Controller) Reset(ctx context.Context) {
	c.mu.Lock()
	navs := c.toRoot()
	c.panel = nil
	c.confirm = nil
	c.toast = nil
	c.mu.Unlock()
	c.closed(navs)
	c.refresh(ctx)
}

// Refresh re-runs the current search or view.
func (c *Controller) Refresh(ctx context.Context) { c.refresh(ctx) }

// SetText sets the search text of the current view, or the filter of the
// open action panel. A form has no search text.
func (c *Controller) SetText(ctx context.Context, text string) {
	c.mu.Lock()
	if c.panel != nil {
		c.panel.text = text
		c.panel.sel = 0
		c.mu.Unlock()
		return
	}
	f := c.top()
	if f.text == text || f.form != nil {
		c.mu.Unlock()
		return
	}
	f.text = text
	f.sel, f.selKey = 0, ""
	f.moved = false
	local := f.view != nil && f.view.FilterLocally
	if local {
		c.setSel(f, 0)
	}
	c.mu.Unlock()
	if !local {
		c.refresh(ctx)
	}
}

// refresh starts a search for the top frame in the background.
func (c *Controller) refresh(ctx context.Context) { c.search(ctx, false) }

// search starts a search for the top frame. A quiet search (the refresh of an
// invalidation) does not show as loading.
func (c *Controller) search(ctx context.Context, quiet bool) {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.gen++
	gen := c.gen
	f := c.top()
	if !quiet {
		f.loading = true
	}
	text, nav := f.text, f.nav
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.cancel = cancel
	c.running++
	c.mu.Unlock()

	go func() {
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
			}
		}
		// Notify outside the lock but before counting the search as done, so
		// Wait returns only after the front end has been told.
		var notify func()
		c.mu.Lock()
		if gen == c.gen && ctx.Err() == nil {
			f.loading = false
			f.err = ""
			switch {
			case err != nil:
				// Don't leave the previous results selectable under an error.
				f.err = err.Error()
				f.sections = nil
				c.eng.log.Warn("search failed", "err", err)
				f.sel = 0
			case view != nil:
				c.setView(f, view)
			default:
				f.sections = sections
				c.restoreSelection(f)
			}
			notify = c.onChange
		}
		c.mu.Unlock()
		if notify != nil {
			notify()
		}
		c.mu.Lock()
		c.searchDone()
		again := c.running == 0 && c.invAfter
		if again {
			c.invAfter = false
		}
		c.mu.Unlock()
		if again {
			c.search(context.Background(), true)
			c.mu.Lock()
			c.pendingDone()
			c.mu.Unlock()
		}
	}()
}

// setView shows view v of the plugin in frame f, keeping the selection and
// the values typed into a form. c.mu must be held.
func (c *Controller) setView(f *frame, v *plugin.View) {
	f.view = v
	f.sections = viewSections(f.nav.Plugin, *v)
	f.loading, f.err = false, ""
	if v.Kind == plugin.ViewForm && v.Form != nil {
		f.form = newFormState(f.form, v.Form)
	} else {
		f.form = nil
	}
	c.restoreSelection(f)
}

// restoreSelection selects the entry selected before the results of f
// changed, by its key, or else the row at the same index. c.mu must be held.
func (c *Controller) restoreSelection(f *frame) {
	vis := c.visible(f)
	n := countItems(vis)
	if i := indexOfKey(vis, f.selKey); i >= 0 {
		f.sel = i
	} else {
		f.sel = min(f.sel, max(0, n-1))
	}
	if e, ok := itemAt(vis, f.sel); ok {
		f.selKey = e.Key
	}
}

// setSel selects row i of f. c.mu must be held.
func (c *Controller) setSel(f *frame, i int) {
	vis := c.visible(f)
	f.sel = clamp(i, countItems(vis))
	if e, ok := itemAt(vis, f.sel); ok {
		f.selKey = e.Key
	}
}

func indexOfKey(sections []Section, key string) int {
	if key == "" {
		return -1
	}
	i := 0
	for _, s := range sections {
		for _, it := range s.Items {
			if it.Key == key {
				return i
			}
			i++
		}
	}
	return -1
}

// invalidate handles Host.Invalidate of a plugin: when it shows in the top
// frame, the frame is refreshed after a short delay that gathers bursts.
func (c *Controller) invalidate(pluginID, viewID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.affected(invalidation{pluginID, viewID}) {
		return
	}
	c.inv = append(c.inv, invalidation{pluginID, viewID})
	if c.invTimer {
		return
	}
	c.invTimer = true
	c.pending++
	time.AfterFunc(invalidateDelay, c.flushInvalidations)
}

// affected reports whether an invalidation concerns the top frame: the
// plugin's view with that ID (any view for ""), or the root search when the
// plugin contributes to it. c.mu must be held.
func (c *Controller) affected(inv invalidation) bool {
	f := c.top()
	if inv.plugin == "" { // the context changed (contextChanged)
		return f.nav == nil
	}
	if f.nav == nil {
		return c.eng.isRootProvider(inv.plugin)
	}
	return f.nav.Plugin == inv.plugin && (inv.view == "" || inv.view == f.nav.Request.ID)
}

// contextChanged handles a new context of the show: the root search is
// refreshed when it already ran without it (pulled); otherwise the front end
// is only told, for the context chip.
func (c *Controller) contextChanged(pulled bool) {
	c.mu.Lock()
	if f := c.top(); pulled && f.nav == nil {
		if !f.moved {
			// The context section appears above the results the user
			// has not touched yet: select its first row, not the old one.
			f.sel, f.selKey = 0, ""
		}
		c.mu.Unlock()
		c.invalidate("", "")
		return
	}
	notify := c.onChange
	c.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// CopyContextPath copies the folder of the current context (the footer's
// context chip) and shows a toast.
func (c *Controller) CopyContextPath() error {
	cur := c.eng.Context()
	if cur == nil || cur.Folder == "" {
		return nil
	}
	if err := c.eng.sys.CopyText(cur.Folder); err != nil {
		c.setToast(&plugin.Toast{Style: plugin.ToastFailure, Title: c.eng.core.T("toast.actionFailed"), Message: err.Error()})
		return err
	}
	c.setToast(&plugin.Toast{Style: plugin.ToastSuccess, Title: c.eng.core.T("toast.copied")})
	return nil
}

// flushInvalidations refreshes the top frame for the invalidations gathered,
// or, while a search is in flight, once it has finished.
func (c *Controller) flushInvalidations() {
	c.mu.Lock()
	invs := c.inv
	c.inv, c.invTimer = nil, false
	if !slices.ContainsFunc(invs, c.affected) {
		c.pendingDone()
		c.mu.Unlock()
		return
	}
	if c.running > 0 {
		// Refreshing now would cancel the search in flight, and a plugin
		// invalidating continuously would starve it.
		if c.invAfter {
			c.pendingDone()
		}
		c.invAfter = true // keeps the pending count until it refreshes
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	c.search(context.Background(), true)
	c.mu.Lock()
	c.pendingDone()
	c.mu.Unlock()
}

// push handles Host.Render: the view replaces the content of the open view
// instance, at most once per renderInterval; pushes for instances that are
// not open are dropped. Pushes are ordered by their arrival.
func (c *Controller) push(pluginID, instance string, v plugin.View) {
	c.mu.Lock()
	i := slices.IndexFunc(c.stack, func(f *frame) bool {
		return f.nav != nil && f.nav.Plugin == pluginID && f.nav.Request.Instance == instance
	})
	if i < 0 || instance == "" {
		c.mu.Unlock()
		return
	}
	f := c.stack[i]
	f.pushed = &v
	if f.pushTimer {
		c.mu.Unlock() // the scheduled flush applies the latest view
		return
	}
	if wait := renderInterval - time.Since(f.lastPush); wait > 0 {
		f.pushTimer = true
		c.pending++
		c.mu.Unlock()
		time.AfterFunc(wait, func() { c.flushPush(f) })
		return
	}
	notify := c.applyPush(f)
	c.mu.Unlock()
	if notify != nil {
		notify()
	}
}

func (c *Controller) flushPush(f *frame) {
	c.mu.Lock()
	f.pushTimer = false
	var notify func()
	if slices.Contains(c.stack, f) {
		notify = c.applyPush(f)
	} else {
		f.pushed = nil // closed meanwhile
	}
	c.mu.Unlock()
	if notify != nil {
		notify()
	}
	c.mu.Lock()
	c.pendingDone()
	c.mu.Unlock()
}

// applyPush shows the latest pushed view of f. c.mu must be held.
func (c *Controller) applyPush(f *frame) func() {
	v := f.pushed
	if v == nil {
		return nil
	}
	f.pushed, f.lastPush = nil, time.Now()
	c.setView(f, v)
	return c.onChange
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

// itemAt returns the i-th entry across sections. i may be -1 (no
// selection) and sections may be empty.
func itemAt(sections []Section, i int) (Entry, bool) {
	if i < 0 {
		return Entry{}, false
	}
	for _, s := range sections {
		if i < len(s.Items) {
			return s.Items[i], true
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
	c.setSel(c.top(), i)
	c.top().moved = true
}

// Hover selects row i under the mouse pointer. Unlike Select (a click) it
// does not count as the user choosing a row, so results arriving above (the
// context) still move the selection to the first row.
func (c *Controller) Hover(i int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.panel != nil {
		c.panel.sel = clamp(i, len(c.panelActions()))
		return
	}
	c.setSel(c.top(), i)
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

// KeyTasks opens Background Tasks from anywhere in the launcher; it takes
// precedence over action shortcuts.
const KeyTasks = "Ctrl+J"

// Key handles a key press such as "Down", "Enter", "Ctrl+K" or an action
// shortcut, and returns what the front end should do.
func (c *Controller) Key(ctx context.Context, key string) (Outcome, error) {
	key = normalizeShortcut(key)
	// Act on the results of what has been typed so far.
	if key != "Up" && key != "Down" {
		c.waitSearches()
	}
	c.mu.Lock()
	if len(c.external) > 0 {
		c.externalKey(key) // unlocks
		return Outcome{}, nil
	}
	if p := c.confirm; p != nil {
		// While confirming, only Enter (confirm) and Escape (cancel) count.
		switch key {
		case "Enter":
			c.confirm = nil
			c.mu.Unlock()
			return c.exec(ctx, p.entry, p.action)
		case "Escape":
			c.confirm = nil
		}
		c.mu.Unlock()
		return Outcome{}, nil
	}
	if key == KeyTasks && c.eng.tasks != nil {
		c.mu.Unlock()
		return c.OpenTasks(ctx, "")
	}
	if c.panel != nil {
		return c.panelKey(ctx, key) // unlocks
	}
	f := c.top()
	if f.form != nil {
		return c.formKey(ctx, key) // unlocks
	}
	if c.argumentKey(f, key) {
		c.mu.Unlock()
		return Outcome{}, nil
	}
	if key == "Tab" && f.nav == nil && strings.TrimSpace(f.text) != "" {
		// Tab with text in the root search, where the selected entry takes
		// no arguments: the plugin.TabProvider's action (e.g. ask AI).
		text := f.text
		c.mu.Unlock()
		entry, action, ok := c.eng.tabAction(ctx, text)
		if !ok {
			return Outcome{}, nil
		}
		return c.run(ctx, entry, action)
	}
	if key == "Escape" && f.view != nil && f.view.Escape != nil {
		// The view takes Escape (e.g. to stop an answer being written).
		entry, action := Entry{Plugin: f.nav.Plugin}, *f.view.Escape
		c.mu.Unlock()
		return c.run(ctx, entry, action)
	}
	n := countItems(c.visible(f))
	nav := key
	if f.view != nil && f.view.Kind == plugin.ViewDetail {
		// A detail view lists no rows: these keys are free for its actions
		// (Ctrl+N for "New Chat").
		switch key {
		case "Down", "Up", "Ctrl+N", "Ctrl+P":
			nav = ""
		}
	}
	switch nav {
	case "Down", "Ctrl+N":
		if n > 0 {
			c.setSel(f, (f.sel+1)%n)
			f.moved = true
		}
		c.mu.Unlock()
		return Outcome{}, nil
	case "Up", "Ctrl+P":
		if n > 0 {
			c.setSel(f, (f.sel-1+n)%n)
			f.moved = true
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
			c.back() // unlocks
			c.refresh(ctx)
			return Outcome{}, nil
		default:
			c.mu.Unlock()
			return Outcome{Hide: true}, nil
		}
	case "Backspace":
		// Backspace in an empty search field goes back, as in Raycast.
		if f.text == "" && len(c.stack) > 1 {
			c.back() // unlocks
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

// back pops the top view. c.mu is held on entry and released before
// returning.
func (c *Controller) back() {
	navs := c.truncate(len(c.stack) - 1)
	c.mu.Unlock()
	c.closed(navs)
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
	c.waitSearches()
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

// OpenCommand resets to the root and runs a command, as its hotkey does. A
// command with required arguments is shown selected in the root search
// instead, with its first argument focused.
func (c *Controller) OpenCommand(ctx context.Context, pluginID, name string) (Outcome, error) {
	c.mu.Lock()
	navs := c.toRoot()
	c.panel = nil
	c.confirm = nil
	c.mu.Unlock()
	c.closed(navs)
	entry, ok := c.eng.commandEntry(c.eng.cfg.Get(), pluginID, name)
	if !ok {
		return Outcome{}, fmt.Errorf("core: unknown command %s/%s", pluginID, name)
	}
	if first := missingArgument(entry, nil); first != "" {
		c.mu.Lock()
		f := c.top()
		f.text, f.sel, f.selKey = entry.Title, 0, entry.Key
		f.argsFor(entry.Key).focus = first
		c.mu.Unlock()
		c.refresh(ctx)
		c.waitSearches()
		return Outcome{}, nil
	}
	return c.run(ctx, entry, entry.Actions[0])
}

// run executes an action, first collecting the entry's arguments and asking
// for confirmation if it is destructive.
func (c *Controller) run(ctx context.Context, entry Entry, action plugin.Action) (Outcome, error) {
	if k := action.Do.Kind; (k == plugin.ActionCallback || k == plugin.ActionRunCommand) && action.Do.Query == "" {
		c.mu.Lock()
		action.Do.Query = c.top().text
		c.mu.Unlock()
	}
	if len(entry.Arguments) > 0 {
		c.mu.Lock()
		f := c.top()
		st := f.argsOf(entry.Key)
		if len(entry.Actions) > 0 && action.ID == entry.Actions[0].ID {
			// The primary action needs the required arguments.
			if name := missingArgument(entry, st); name != "" {
				f.argsFor(entry.Key).focus = name
				c.mu.Unlock()
				return Outcome{}, nil
			}
		}
		action.Do.Values = argumentValues(entry, st, action.Do.Values)
		c.mu.Unlock()
	}
	if action.Style == plugin.StyleDestructive {
		c.mu.Lock()
		c.confirm = &pending{entry: entry, action: action}
		c.mu.Unlock()
		return Outcome{}, nil
	}
	return c.exec(ctx, entry, action)
}

// exec executes an action and applies its outcome to the navigation state.
func (c *Controller) exec(ctx context.Context, entry Entry, action plugin.Action) (Outcome, error) {
	c.mu.Lock()
	fromRoot := len(c.stack) == 1
	c.mu.Unlock()
	return c.execFrom(ctx, entry, action, fromRoot)
}

// execFrom is exec; fromRoot records the use of the entry for ranking.
func (c *Controller) execFrom(ctx context.Context, entry Entry, action plugin.Action, fromRoot bool) (Outcome, error) {
	out, err := c.eng.Run(ctx, entry, action, fromRoot)
	if err != nil {
		c.setToast(&plugin.Toast{Style: plugin.ToastFailure, Title: c.eng.core.T("toast.actionFailed"), Message: err.Error()})
		return out, err
	}
	if out.Toast != nil {
		c.setToast(out.Toast)
	}
	if len(out.FieldErrors) > 0 {
		// The plugin rejected the form: it stays open with the messages.
		c.mu.Lock()
		if f := c.top(); f.form != nil {
			f.form.setErrors(out.FieldErrors)
		}
		c.mu.Unlock()
		out.Hide, out.Push, out.PopToRoot, out.Refresh = false, nil, false, false
		return out, nil
	}
	if out.ClearText {
		c.mu.Lock()
		if f := c.top(); f.form == nil {
			f.text, f.sel, f.selKey, f.moved = "", 0, "", false
		}
		c.mu.Unlock()
	}
	switch {
	case out.Push != nil:
		nav := *out.Push
		c.mu.Lock()
		c.instances++
		nav.Request.Instance = fmt.Sprintf("v%d", c.instances)
		c.stack = append(c.stack, &frame{nav: &nav})
		c.mu.Unlock()
		c.refresh(ctx)
	case out.PopToRoot:
		c.mu.Lock()
		navs := c.toRoot()
		c.mu.Unlock()
		c.closed(navs)
		c.refresh(ctx)
	case out.Refresh, out.ClearText:
		c.refresh(ctx)
	}
	return out, nil
}

func (c *Controller) setToast(t *plugin.Toast) {
	c.mu.Lock()
	c.toast = t
	c.mu.Unlock()
}

// ShowToast shows t in the footer, as if an action had returned it, and
// tells the front end (Host.Toast, a finished background task).
func (c *Controller) ShowToast(t plugin.Toast) {
	c.mu.Lock()
	c.toast = &t
	notify := c.onChange
	c.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// OpenTasks resets to the root and opens Background Tasks: the task with id,
// or the list of all tasks for "" (Ctrl+J, the footer's indicator, a
// notification about a task).
func (c *Controller) OpenTasks(ctx context.Context, id string) (Outcome, error) {
	if c.eng.tasks == nil {
		return Outcome{}, nil
	}
	c.mu.Lock()
	navs := c.toRoot()
	c.panel = nil
	c.confirm = nil
	c.mu.Unlock()
	c.closed(navs)
	return c.execFrom(ctx, Entry{}, plugin.Action{ID: "show-task", Do: plugin.ActionDo{Kind: plugin.ActionShowTask, Target: id}}, false)
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
	Depth int    `json:"depth"`
	Title string `json:"title,omitempty"`
	// Layout is "detail" for a plugin.ViewDetail, shown across the window
	// instead of the list; "" otherwise (a form has Form).
	Layout      string    `json:"layout,omitempty"`
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
	// Form is set when the view is a form; it has no search field then.
	Form *FormSnapshot `json:"form,omitempty"`
	// Arguments is set when the selected entry takes arguments.
	Arguments *ArgumentsSnapshot `json:"arguments,omitempty"`
	Panel     *PanelSnapshot     `json:"panel,omitempty"`
	// Confirm is set while a destructive action awaits confirmation:
	// Enter confirms, Escape cancels.
	Confirm *ConfirmSnapshot `json:"confirm,omitempty"`
	Toast   *plugin.Toast    `json:"toast,omitempty"`
	// Context is the context of the current show, for the footer's chip.
	Context *ContextSnapshot `json:"context,omitempty"`
	// Selection is the text selected when the launcher opened, for the
	// footer's chip (its length only; the text is in the root's section).
	Selection *SelectionSnapshot `json:"selection,omitempty"`
	// Tasks is the footer's background task indicator; nil when no task is
	// queued or running and no failure is unseen.
	Tasks *TasksSnapshot `json:"tasks,omitempty"`
}

// ContextSnapshot is the context captured when the launcher opened.
type ContextSnapshot struct {
	Kind   string `json:"kind,omitempty"`
	Name   string `json:"name"`             // short name: the folder's name, else title or app
	Folder string `json:"folder,omitempty"` // copied by a click on the chip
	App    string `json:"app,omitempty"`
}

// SelectionSnapshot describes the selected text of the current show.
type SelectionSnapshot struct {
	Chars int `json:"chars"`
	// Label is the chip's text, e.g. "52 characters selected".
	Label string `json:"label"`
}

// TasksSnapshot is the background task indicator.
type TasksSnapshot struct {
	// Active is the number of queued and running tasks (a spinner and the
	// number); Failed the failed tasks not seen yet (a red dot).
	Active int `json:"active,omitzero"`
	Failed int `json:"failed,omitzero"`
	// Label describes the indicator, e.g. "2 background tasks running".
	Label string `json:"label"`
}

// ConfirmSnapshot is a confirmation dialog.
type ConfirmSnapshot struct {
	Title   string `json:"title"`
	Message string `json:"message"`
	Confirm string `json:"confirm"` // confirm button label
	Cancel  string `json:"cancel"`  // cancel button label
	// Danger shows the confirm button in red (a destructive action).
	Danger bool `json:"danger,omitzero"`
	// External marks a confirmation asked for by an AI client (docs/ai.md
	// §5): Enter allows, Escape denies. AllowSession is the label of "Allow
	// for this session" (Ctrl+Enter) when it is offered, and Queued the
	// number of requests waiting after this one.
	External     bool   `json:"external,omitzero"`
	AllowSession string `json:"allowSession,omitempty"`
	Queued       int    `json:"queued,omitzero"`
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
	if cur := c.eng.Context(); cur != nil && (cur.Folder != "" || cur.Kind != "") {
		s.Context = &ContextSnapshot{Kind: cur.Kind, Name: ContextName(cur), Folder: cur.Folder, App: cur.App.Name}
	}
	if sel := c.eng.Selection(); sel != nil {
		n := SelectionChars(sel)
		s.Selection = &SelectionSnapshot{Chars: n, Label: core.N("ui.selection.chip", n)}
	}
	if f.view != nil {
		s.Title = f.view.Title
		s.Placeholder = cmp.Or(f.view.Placeholder, core.T("search.placeholderView"))
		s.ShowDetail = f.view.ShowDetail
		s.EmptyText = cmp.Or(f.view.EmptyText, s.EmptyText)
		s.Detail = f.view.Detail
		if f.view.Kind == plugin.ViewDetail {
			s.Layout = string(plugin.ViewDetail)
			s.ShowDetail = false
		}
	}
	if f.form != nil {
		s.Form = f.form.snapshot(f.view.Form.Submit.Title)
	}
	if n := countItems(sections); n > 0 {
		s.Selected = clamp(f.sel, n)
		e, _ := itemAt(sections, s.Selected)
		if s.ShowDetail && e.Detail != nil {
			s.Detail = e.Detail
		}
		s.Arguments = argumentsSnapshot(e, f.argsOf(e.Key))
	}
	if p := c.confirm; p != nil {
		s.Confirm = &ConfirmSnapshot{
			Title:   core.T("confirm.title", "action", p.action.Title, "item", p.entry.Title),
			Message: cmp.Or(p.action.ConfirmMessage, core.T("confirm.message")),
			Confirm: p.action.Title,
			Cancel:  core.T("confirm.cancel"),
			Danger:  true,
		}
		if strings.HasPrefix(p.entry.ID, cmdPrefix) || p.action.Title == p.entry.Title {
			// For a command (or an item named like its action, "Shut Down")
			// the entry title already names the action.
			s.Confirm.Title = core.T("confirm.titleCommand", "item", p.entry.Title)
			s.Confirm.Confirm = p.entry.Title
		}
		if p.action.EndsSession {
			// Shutting down or signing out also stops the background tasks.
			s.Confirm.Message = core.T("confirm.messageSession")
			if ts := c.eng.tasks; ts != nil {
				if active, _ := ts.Indicator(); active > 0 {
					s.Confirm.Message = core.N("confirm.tasksInterrupted", active)
				}
			}
		}
	}
	if ext := c.externalSnapshot(); ext != nil {
		s.Confirm = ext // over the launcher's own confirmation, which waits
	}
	if ts := c.eng.tasks; ts != nil {
		switch active, failed := ts.Indicator(); {
		case active > 0:
			s.Tasks = &TasksSnapshot{Active: active, Failed: failed, Label: core.N("ui.tasks.running", active)}
		case failed > 0:
			s.Tasks = &TasksSnapshot{Failed: failed, Label: core.N("ui.tasks.failed", failed)}
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
