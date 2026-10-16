package core

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/host"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// The selected text (docs/selected-text.md): when the launcher shows, the
// front end hands the engine a reader of the text selected in the window
// that was active; the engine runs it in the background within
// the selection budget, keeps the result for this show only (never on disk,
// never logged), clears it when the launcher hides, and gives it to the
// plugins declaring plugin.PermSelection (plugin.SelectionProvider), whose
// items form the "Selected Text" section at the top of the root search.

// defaultSelectionBudget bounds reading the selection (phase 2, §3.4);
// Options.SelectionBudget overrides it.
const defaultSelectionBudget = 250 * time.Millisecond

// maxSelectionRows is the number of rows of the selected-text section while
// nothing is typed: the preview row and at most 6 actions (§12.5).
const maxSelectionRows = 7

// SelectionReader reads the text selected in the window that was active
// when the launcher opened. It reports false when there is none.
type SelectionReader func(ctx context.Context) (plugin.Selection, bool)

// selectionState is the selection of the current show. It lives in
// contextState (same lock) so that a new show drops both.
type selectionState struct {
	sel *plugin.Selection // nil while unknown or none
	// gen is incremented when the selection is cleared (the launcher hid),
	// so that a read still running is dropped when it ends.
	gen int
}

// DetectSelection starts reading the selection for the show begun by the
// last DetectContext: read runs in the background and the root search is
// refreshed when the text arrives. With context awareness off, read is not
// called. Call it after DetectContext and before the root search starts.
func (e *Engine) DetectSelection(read SelectionReader) {
	if read == nil || !e.cfg.Get().ContextAware {
		return
	}
	e.ctx.mu.Lock()
	gen, selGen := e.ctx.gen, e.ctx.selection.gen
	e.ctx.mu.Unlock()
	host.Go(e.log, "selection", func() {
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), e.selBudget)
		defer cancel()
		type result struct {
			s  plugin.Selection
			ok bool
		}
		done := make(chan result, 1)
		host.Go(e.log, "selection reader", func() {
			s, ok := read(ctx)
			done <- result{s, ok}
		})
		select {
		case r := <-done:
			// Lengths and times only: the text is never logged.
			e.log.Debug("selection read", "ok", r.ok, "source", r.s.Source, "app", r.s.App.Name,
				"chars", utf8.RuneCountInString(r.s.Text), "truncated", r.s.Truncated, "took", time.Since(start).Round(time.Millisecond))
			if r.ok {
				e.setSelection(gen, selGen, &r.s)
			}
		case <-ctx.Done():
			e.log.Info("selection read timed out", "after", e.selBudget)
		}
	})
}

// SetSelection replaces the selection of the current show, e.g. with one
// injected by glctl (nil clears it). It ignores the contextAware setting.
func (e *Engine) SetSelection(s *plugin.Selection) {
	e.ctx.mu.Lock()
	gen, selGen := e.ctx.gen, e.ctx.selection.gen
	e.ctx.mu.Unlock()
	if s == nil {
		e.ClearSelection()
		return
	}
	cp := *s
	e.setSelection(gen, selGen, &cp)
}

// ClearSelection drops the selection, e.g. when the launcher hides
// (§4.1); a read still running is dropped too.
func (e *Engine) ClearSelection() {
	e.ctx.mu.Lock()
	had := e.ctx.selection.sel != nil
	e.ctx.selection.sel = nil
	e.ctx.selection.gen++
	pulled := e.ctx.pulled
	e.ctx.mu.Unlock()
	if had {
		for _, ctl := range e.subscribers() {
			ctl.contextChanged(pulled)
		}
	}
}

// setSelection stores s if the show (gen) and the selection generation are
// still current, and refreshes the controllers. Text that is blank, from an
// excluded app, or too long (truncated) is handled here too, whatever the
// reader did.
func (e *Engine) setSelection(gen, selGen int, s *plugin.Selection) {
	cfg := e.cfg.Get().Selection
	if strings.TrimSpace(s.Text) == "" || excludedApp(cfg.ExcludeApps, s.App.Name) {
		return
	}
	if t, cut := truncateRunes(s.Text, cfg.EffectiveMaxChars()); cut {
		s.Text, s.Truncated = t, true
	}
	e.ctx.mu.Lock()
	if gen != e.ctx.gen || selGen != e.ctx.selection.gen {
		e.ctx.mu.Unlock()
		return
	}
	e.ctx.selection.sel = s
	pulled := e.ctx.pulled
	e.ctx.mu.Unlock()
	for _, ctl := range e.subscribers() {
		ctl.contextChanged(pulled)
	}
}

// Selection returns a copy of the selection of the current show, or nil.
func (e *Engine) Selection() *plugin.Selection {
	e.ctx.mu.Lock()
	defer e.ctx.mu.Unlock()
	if e.ctx.selection.sel == nil {
		return nil
	}
	s := *e.ctx.selection.sel
	return &s
}

// excludedApp reports whether app (an executable name) is in list, whose
// entries may be names or paths, in any case.
func excludedApp(list []string, app string) bool {
	if app == "" {
		return false
	}
	app = filepath.Base(app)
	return slices.ContainsFunc(list, func(x string) bool {
		x = strings.TrimSpace(x)
		return x != "" && strings.EqualFold(filepath.Base(x), app)
	})
}

// truncateRunes cuts s to at most n characters.
func truncateRunes(s string, n int) (string, bool) {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s, false
	}
	i := 0
	for j := range s {
		if i == n {
			return s[:j], true
		}
		i++
	}
	return s, false
}

// SelectionChars is the number of characters of a selection, for the
// footer's chip.
func SelectionChars(s *plugin.Selection) int {
	if s == nil {
		return 0
	}
	return utf8.RuneCountInString(s.Text)
}

// selectionEntries collects the items of the selection providers for sel
// and text, the root search's text: plugins declaring plugin.PermSelection,
// running in this process (plugins in their own process never see the
// selection, §10). The entries are never recorded, never shown to AI
// (Sensitive) and carry the section title.
func (e *Engine) selectionEntries(ctx context.Context, cfg config.Config, sel *plugin.Selection, text string) []Entry {
	if sel == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, e.rootTimeout)
	defer cancel()
	group := e.core.T("section.selection")
	var (
		mu  sync.Mutex
		out []Entry
		wg  sync.WaitGroup
	)
	for _, l := range e.enabled(cfg) {
		sp, ok := l.p.(plugin.SelectionProvider)
		if !ok || !slices.Contains(l.m.Permissions, plugin.PermSelection) {
			continue
		}
		if _, external := l.p.(ProcessPlugin); external {
			continue
		}
		q := plugin.SelectionQuery{Selection: *sel, Text: text}
		wg.Go(func() {
			done := make(chan []plugin.Item, 1)
			go func() {
				items, err := guard(e, l.m.ID, func() ([]plugin.Item, error) { return sp.SelectionItems(ctx, q) })
				if err != nil {
					e.log.Warn("selection items", "plugin", l.m.ID, "err", err)
				}
				done <- items
			}()
			select {
			case items := <-done:
				mu.Lock()
				for _, it := range items {
					it.Group = group
					it.Sensitive, it.Transient = true, true
					ent := newEntry(l.m.ID, it)
					ent.Context, ent.Selection = true, true
					out = append(out, ent)
				}
				mu.Unlock()
			case <-ctx.Done():
				e.log.Warn("selection items timed out", "plugin", l.m.ID)
			}
		})
	}
	wg.Wait()
	// Plugins answer concurrently: a stable order. While nothing is typed
	// the clear hits (Score) come first; a generic catch-all (a plugin with
	// a late fallback order: the web search) after the others.
	order := map[string]int{}
	for i, l := range e.plugins {
		order[l.m.ID] = i
	}
	key := func(ent Entry) int { return fallbackOrder(e.byID[ent.Plugin].p) }
	slices.SortStableFunc(out, func(a, b Entry) int {
		if text == "" {
			if c := cmp.Compare(b.Score, a.Score); c != 0 {
				return c
			}
		}
		if c := cmp.Compare(key(a), key(b)); c != 0 {
			return c
		}
		return cmp.Compare(order[a.Plugin], order[b.Plugin])
	})
	return out
}

// OpenSelection shows the selected text in full: it runs the primary action
// of the first row of the selected-text section (the preview row), as a
// click on the footer's chip does.
func (c *Controller) OpenSelection(ctx context.Context) (Outcome, error) {
	sel := c.eng.Selection()
	if sel == nil {
		return Outcome{}, nil
	}
	entries := c.eng.selectionEntries(ctx, c.eng.cfg.Get(), sel, "")
	if len(entries) == 0 || len(entries[0].Actions) == 0 {
		return Outcome{}, nil
	}
	return c.run(ctx, entries[0], entries[0].Actions[0])
}
