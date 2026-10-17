package core

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
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

	// keep is the stack kept across a hide and its restore (keep.go).
	keep keepState

	// files are the files handed to the root search (rootfiles.go).
	files *rootFiles

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

	// restoring is set while the view of a restored stack is requested
	// again (plugin.ViewRequest.Restore): an error goes back to the root.
	restoring bool
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
// of coalesced invalidations and the views pushed by plugins, and the
// plugins turned on or off by a change of the settings (Engine.WaitPlugins).
func (c *Controller) Wait() {
	c.eng.WaitPlugins()
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
	c.keep.restored, c.keep.fresh = nil, false
	return navs
}

// closed tells the engine about closed view instances.
func (c *Controller) closed(navs []*Nav) { c.eng.viewsClosed(navs) }

// Reset pops to the root, clears the text and closes the panel. Front ends
// call it (or Show, which may restore a kept stack) when the window is shown.
func (c *Controller) Reset(ctx context.Context) {
	c.mu.Lock()
	c.keep = keepState{}
	navs := c.toRoot()
	c.panel = nil
	c.confirm = nil
	c.toast = nil
	c.mu.Unlock()
	c.closed(navs)
	c.clearFiles() // the files handed to the root search live for one show
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
	c.keep.fresh = false
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
	text, nav, restore, files := f.text, f.nav, f.restoring, c.files
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
			sections, err = c.eng.rootSearch(ctx, text, files)
		} else {
			req := nav.Request
			req.Query = plugin.Query{Text: text}
			req.Restore = restore
			var v plugin.View
			v, err = c.eng.View(ctx, Nav{Plugin: nav.Plugin, Request: req})
			if err == nil {
				view = &v
			}
		}
		// Notify outside the lock but before counting the search as done, so
		// Wait returns only after the front end has been told.
		var (
			notify  func()
			refused []*Nav // the kept views dropped when the plugin refused the restore
		)
		c.mu.Lock()
		if gen == c.gen && ctx.Err() == nil {
			f.loading = false
			f.err = ""
			if f.restoring && slices.Contains(c.stack, f) {
				f.restoring = false
				if err != nil {
					c.eng.log.Info("view not restored", "plugin", nav.Plugin, "view", nav.Request.ID, "err", err)
					refused = c.toRoot()
					err = nil
				}
			}
			switch {
			case refused != nil:
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
		if refused != nil {
			// Before this search counts as done, so that Wait covers the root's.
			c.closed(refused)
			c.refresh(ctx)
		}
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
