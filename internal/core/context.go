package core

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Context awareness (docs/context-awareness.md §3): when the launcher shows,
// the front end hands the engine a detector for the window that was active;
// the engine runs it in the background within contextBudget, keeps the
// result for this show only (never on disk) and refreshes the root search
// when it arrives after the root was first pulled.

// contextBudget bounds a context detection.
var contextBudget = 300 * time.Millisecond

// contextBonus lifts context items a little in the ranked root search.
const contextBonus = 0.1

// Detector finds the context of the window that was active when the
// launcher opened. It reports false when it found nothing worth showing.
type Detector func(ctx context.Context) (plugin.Context, bool)

// contextState is the context of the current show.
type contextState struct {
	mu  sync.Mutex
	gen int             // incremented for every show (DetectContext/SetContext)
	cur *plugin.Context // nil while unknown
	// pulled is set when a root search read the context of this generation,
	// so a context (or selection) arriving later must refresh it.
	pulled bool
	// selection is the selected text of this show (selection.go).
	selection selectionState
}

// DetectContext starts capturing the context for a new show: the previous
// one is dropped at once and detect runs in the background. When context
// awareness is turned off, detect is not called, so no other window is
// read. Call it before the root search of the show starts.
func (e *Engine) DetectContext(detect Detector) {
	e.ctx.mu.Lock()
	e.ctx.gen++
	gen := e.ctx.gen
	e.ctx.cur, e.ctx.pulled = nil, false
	e.ctx.selection.sel = nil
	e.ctx.mu.Unlock()
	if detect == nil || !e.cfg.Get().ContextAware {
		return
	}
	host.Go(e.log, "context detection", func() {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), contextBudget)
		defer cancel()
		type result struct {
			c  plugin.Context
			ok bool
		}
		done := make(chan result, 1)
		host.Go(e.log, "context detector", func() {
			c, ok := detect(ctx)
			done <- result{c, ok}
		})
		select {
		case r := <-done:
			e.log.Debug("context detected", "kind", r.c.Kind, "app", r.c.App.Name, "ok", r.ok, "took", time.Since(start))
			if r.ok {
				e.setContext(gen, &r.c)
			}
		case <-ctx.Done():
			e.log.Info("context detection timed out", "after", contextBudget)
		}
	})
}

// SetContext replaces the context of the current show, e.g. with a context
// injected by glctl (nil clears it). It ignores the contextAware setting.
func (e *Engine) SetContext(c *plugin.Context) {
	e.ctx.mu.Lock()
	e.ctx.gen++
	gen := e.ctx.gen
	e.ctx.mu.Unlock()
	if c != nil {
		cc := c.Clone()
		c = &cc
	}
	e.setContext(gen, c)
}

// setContext stores c if gen is still the current show, and refreshes the
// controllers.
func (e *Engine) setContext(gen int, c *plugin.Context) {
	e.ctx.mu.Lock()
	if gen != e.ctx.gen {
		e.ctx.mu.Unlock()
		return
	}
	e.ctx.cur = c
	pulled := e.ctx.pulled
	e.ctx.mu.Unlock()
	for _, ctl := range e.subscribers() {
		ctl.contextChanged(pulled)
	}
}

// Context returns a copy of the context of the current show, or nil.
func (e *Engine) Context() *plugin.Context {
	e.ctx.mu.Lock()
	defer e.ctx.mu.Unlock()
	if e.ctx.cur == nil {
		return nil
	}
	c := e.ctx.cur.Clone()
	return &c
}

// pullContext returns the context and the selection for a root search and
// remembers that the root has seen this show's context.
func (e *Engine) pullContext() (*plugin.Context, *plugin.Selection) {
	e.ctx.mu.Lock()
	defer e.ctx.mu.Unlock()
	e.ctx.pulled = true
	var sel *plugin.Selection
	if s := e.ctx.selection.sel; s != nil {
		cp := *s
		sel = &cp
	}
	if e.ctx.cur == nil {
		return nil, sel
	}
	c := e.ctx.cur.Clone()
	return &c, sel
}

// clearContextIfOff drops the context when context awareness was turned off.
func (e *Engine) clearContextIfOff(cfg config.Config) {
	if cfg.ContextAware {
		return
	}
	e.ctx.mu.Lock()
	had := e.ctx.cur != nil || e.ctx.selection.sel != nil
	e.ctx.gen++
	e.ctx.cur = nil
	e.ctx.selection.sel = nil
	e.ctx.mu.Unlock()
	if had {
		for _, ctl := range e.subscribers() {
			ctl.contextChanged(true)
		}
	}
}

// ContextName is the short name of a context shown to the user: the folder's
// name, else the window title, else the app.
func ContextName(c *plugin.Context) string {
	if c == nil {
		return ""
	}
	if c.Folder != "" {
		f := strings.TrimRight(c.Folder, `\/`)
		name := f
		if i := strings.LastIndexAny(f, `\/`); i >= 0 {
			name = f[i+1:]
		}
		if name != "" && !strings.HasSuffix(name, ":") {
			return name
		}
		return c.Folder // a drive root such as C:\
	}
	return cmp.Or(c.Title, strings.TrimSuffix(c.App.Name, ".exe"))
}

// contextEntries collects the items of the context providers for c.
func (e *Engine) contextEntries(ctx context.Context, cfg config.Config, c *plugin.Context) []Entry {
	if c == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, e.rootTimeout)
	defer cancel()
	group := e.core.T("section.context", "name", ContextName(c))
	var (
		mu  sync.Mutex
		out []Entry
		wg  sync.WaitGroup
	)
	for _, l := range e.enabled(cfg) {
		cp, ok := l.p.(plugin.ContextProvider)
		if !ok {
			continue
		}
		wg.Go(func() {
			done := make(chan []plugin.Item, 1)
			go func() {
				items, err := guard(e, l.m.ID, func() ([]plugin.Item, error) { return cp.ContextItems(ctx, c.Clone()) })
				if err != nil {
					e.log.Warn("context items", "plugin", l.m.ID, "err", err)
				}
				done <- items
			}()
			select {
			case items := <-done:
				mu.Lock()
				for _, it := range items {
					it.Group = group
					ent := newEntry(l.m.ID, it)
					ent.Context = true
					out = append(out, ent)
				}
				mu.Unlock()
			case <-ctx.Done():
				e.log.Warn("context items timed out", "plugin", l.m.ID)
			}
		})
	}
	wg.Wait()
	// Plugins answer concurrently: keep a stable order (plugin order, then
	// the plugin's own order).
	order := map[string]int{}
	for i, l := range e.plugins {
		order[l.m.ID] = i
	}
	slices.SortStableFunc(out, func(a, b Entry) int { return cmp.Compare(order[a.Plugin], order[b.Plugin]) })
	return out
}
