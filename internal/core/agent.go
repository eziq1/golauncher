package core

import (
	"encoding/json/jsontext"
	"slices"
	"sync"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// What the AI tool layer (internal/agent, docs/ai.md §4) needs from the
// engine besides searching, rendering views and running actions.

// CommandEntry returns the root entry of a command of an enabled or
// disabled plugin, as the root search lists it.
func (e *Engine) CommandEntry(pluginID, name string) (Entry, bool) {
	return e.commandEntry(e.cfg.Get(), pluginID, name)
}

// CommandDestructive reports whether a command is declared destructive.
func (e *Engine) CommandDestructive(pluginID, name string) bool {
	l, ok := e.byID[pluginID]
	if !ok {
		return false
	}
	i := slices.IndexFunc(l.m.Commands, func(c plugin.Command) bool { return c.Name == name })
	return i >= 0 && l.m.Commands[i].Destructive
}

// invalidateHooks are told about every Host.Invalidate.
type invalidateHooks struct {
	mu  sync.Mutex
	n   int
	fns map[int]func(pluginID, viewID string)
}

// OnInvalidate registers fn to be called (on the plugin's goroutine; it must
// not block) for every Host.Invalidate of a plugin. It returns a function
// that removes it.
func (e *Engine) OnInvalidate(fn func(pluginID, viewID string)) (remove func()) {
	h := &e.invHooks
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fns == nil {
		h.fns = map[int]func(string, string){}
	}
	h.n++
	id := h.n
	h.fns[id] = fn
	return func() {
		h.mu.Lock()
		delete(h.fns, id)
		h.mu.Unlock()
	}
}

func (h *invalidateHooks) call(pluginID, viewID string) {
	h.mu.Lock()
	fns := make([]func(string, string), 0, len(h.fns))
	for _, fn := range h.fns {
		fns = append(fns, fn)
	}
	h.mu.Unlock()
	for _, fn := range fns {
		fn(pluginID, viewID)
	}
}

// CloseViews tells the plugins that view instances outside any Controller
// (the tool layer's) are closed, so they can free their state.
func (e *Engine) CloseViews(navs []Nav) {
	ptrs := make([]*Nav, len(navs))
	for i := range navs {
		ptrs[i] = &navs[i]
	}
	e.viewsClosed(ptrs)
}

// FormValues checks values typed for a form (as text, see formState: "true"
// for a checked checkbox, an option's value, "YYYY-MM-DD") like the
// launcher does before submitting, and returns the values for
// ActionDo.Values, or the messages by field ID when some are invalid.
// Fields without a value get their default.
func (e *Engine) FormValues(form *plugin.Form, values map[string]string) (map[string]jsontext.Value, map[string]string) {
	s := newFormState(nil, form)
	for id, v := range values {
		if s.index(id) >= 0 {
			s.values[id] = v
		}
	}
	c := &Controller{eng: e}
	if errs := c.validate(s); len(errs) > 0 {
		return nil, errs
	}
	return s.jsonValues(), nil
}

// MatchEntry scores text against an entry as a view's local filtering does
// (View.FilterLocally): 0 for no match, else up to 1.
func MatchEntry(text string, e Entry) float64 { return entryMatch(text, e) }

// ViewEntries returns the entries of a view's sections.
func ViewEntries(pluginID string, v plugin.View) []Section { return viewSections(pluginID, v) }

// ArgumentValues returns ActionDo.Values for the arguments of an entry typed
// as values, or the name of the first required argument without a value.
func ArgumentValues(entry Entry, values map[string]string, base map[string]jsontext.Value) (map[string]jsontext.Value, string) {
	st := &argState{key: entry.Key, values: values}
	if st.values == nil {
		st.values = map[string]string{}
	}
	if name := missingArgument(entry, st); name != "" {
		return nil, name
	}
	return argumentValues(entry, st, base), ""
}
