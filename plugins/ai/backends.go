package ai

import (
	"context"
	"errors"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// DefaultBackendID is the ID of the Claude Code backend, the default of the
// "Backend" preference (claudecode.ID in internal/ai/claudecode). Chats
// recorded before chats remembered their backend (R72) were Claude Code's.
const DefaultBackendID = "claude-code"

// BackendOption is a backend the "Backend" preference can choose (R71).
type BackendOption struct {
	// ID is stored in the preferences and with each chat, e.g. "claude-code".
	ID string
	// Backend answers; its Name is the option's title.
	Backend backend.Backend
	// Models, when set, are the choices of the backend's own "Model"
	// preference (prefModel + "." + ID; the empty value, the CLI's default,
	// comes first by itself). Without it the backend uses the "Model"
	// preference (Claude's models).
	Models []plugin.Option
	// DynamicModels gives the backend its own "Model" preference whose
	// choices come from its CLI (backend.ModelLister, Codex; R109): the
	// models it lists, with the reasoning efforts each accepts.
	DynamicModels bool
	// Efforts gives the backend a "Reasoning effort" preference (prefEffort
	// + "." + ID, R109), next to its model: the levels the chosen model
	// accepts (DynamicModels), or the levels the CLI takes for every model
	// (backend.EffortLister, Claude Code).
	Efforts bool
}

// ownModels reports whether o has its own model preference.
func (o BackendOption) ownModels() bool { return o.Models != nil || o.DynamicModels }

// chooser holds the backends. New chats use the one chosen in the "Backend"
// preference; a chat keeps the backend it was started with (Session.BackendID,
// R72).
type chooser struct {
	p    *Plugin
	opts []BackendOption
}

// modelPrefText are the titles of the backends' own model preferences.
var modelPrefText = map[string][2]string{
	"codex":  {"%pref.model.codex.title%", "%pref.model.codex.description%"},
	"gemini": {"%pref.model.gemini.title%", "%pref.model.gemini.description%"},
}

// preference is the "Backend" preference, with one option per backend.
func (c *chooser) preference() plugin.Preference {
	options := make([]plugin.Option, 0, len(c.opts))
	for _, o := range c.opts {
		options = append(options, plugin.Option{Value: o.ID, Title: o.Backend.Name()})
	}
	return plugin.Preference{Name: prefBackend, Type: plugin.PrefSelect, Title: "%pref.backend.title%", Description: "%pref.backend.description%",
		Default: plugin.MustPayload(c.opts[0].ID), Options: options}
}

// effortPrefText are the titles of the backends' reasoning effort
// preferences.
var effortPrefText = map[string][2]string{
	DefaultBackendID: {"%pref.effort.claude.title%", "%pref.effort.claude.description%"},
	"codex":          {"%pref.effort.codex.title%", "%pref.effort.codex.description%"},
}

// modelPreferences are the preferences that follow the "Model" preference:
// the reasoning efforts of the backends using it, then for each backend with
// its own models its model preference and its effort preference (R109), so
// that each backend's choices stay together. They show whichever backend is
// chosen, as the model preferences always did: a chat keeps the backend it
// was started with.
func (c *chooser) modelPreferences() []plugin.Preference {
	var out []plugin.Preference
	for _, o := range c.opts {
		if !o.ownModels() && o.Efforts {
			out = append(out, effortPreference(o))
		}
	}
	for _, o := range c.opts {
		if !o.ownModels() {
			continue
		}
		text, ok := modelPrefText[o.ID]
		if !ok {
			text = [2]string{"%pref.model.title%", "%pref.model.description%"}
		}
		pref := plugin.Preference{Name: modelPref(o), Type: plugin.PrefSelect, Title: text[0], Description: text[1],
			Default: plugin.MustPayload("")}
		if o.DynamicModels {
			pref.DynamicOptions = true
		} else {
			pref.Options = append([]plugin.Option{{Value: "", Title: "%pref.model.default%"}}, o.Models...)
		}
		out = append(out, pref)
		if o.Efforts {
			out = append(out, effortPreference(o))
		}
	}
	return out
}

// effortPreference is o's reasoning effort preference; its choices come
// from the backend.
func effortPreference(o BackendOption) plugin.Preference {
	text, ok := effortPrefText[o.ID]
	if !ok {
		text = [2]string{"%pref.effort.title%", "%pref.effort.description%"}
	}
	return plugin.Preference{Name: effortPref(o), Type: plugin.PrefSelect, Title: text[0], Description: text[1],
		Default: plugin.MustPayload(""), DynamicOptions: true}
}

// modelPref is the name of the preference that chooses o's model.
func modelPref(o BackendOption) string {
	if !o.ownModels() {
		return prefModel
	}
	return prefModel + "." + o.ID
}

// effortPref is the name of the preference that chooses o's reasoning
// effort.
func effortPref(o BackendOption) string { return prefEffort + "." + o.ID }

// id is the ID of the backend chosen in the preferences.
func (c *chooser) id() string {
	if c.p.h != nil {
		if id := c.p.h.Preferences().String(prefBackend); id != "" {
			return id
		}
	}
	return c.opts[0].ID
}

// option returns the backend with id.
func (c *chooser) option(id string) (BackendOption, bool) {
	for _, o := range c.opts {
		if o.ID == id {
			return o, true
		}
	}
	return BackendOption{}, false
}

// current returns the chosen backend's ID and the backend; one that only
// reports an error when no backend has that ID.
func (c *chooser) current() (string, backend.Backend) {
	id := c.id()
	if o, ok := c.option(id); ok {
		return id, o.Backend
	}
	return id, unavailable{id: id}
}

// of returns the backend of a chat: the one it was started with (Claude
// Code for older records); one that explains in the chat when this version
// does not have it.
func (c *chooser) of(sess Session) (string, backend.Backend) {
	id := sess.BackendID
	if id == "" {
		id = DefaultBackendID
	}
	if o, ok := c.option(id); ok {
		return id, o.Backend
	}
	return id, unavailable{id: id, chat: true}
}

// Name is the name of the chosen backend.
func (c *chooser) Name() string {
	_, b := c.current()
	return b.Name()
}

// forget drops what the chat's backend keeps about a deleted chat.
func (c *chooser) forget(sess Session) {
	_, b := c.of(sess)
	if f, ok := b.(backend.Forgetter); ok {
		f.Forget(sess.ID)
	}
}

// Close closes every backend that can be closed.
func (c *chooser) Close() error {
	var errs []error
	for _, o := range c.opts {
		if cl, ok := o.Backend.(interface{ Close() error }); ok {
			errs = append(errs, cl.Close())
		}
	}
	return errors.Join(errs...)
}

// reopen undoes Close for the backends that can be reopened (the plugin is
// initialized again after Close).
func (c *chooser) reopen() {
	for _, o := range c.opts {
		if r, ok := o.Backend.(interface{ Reopen() }); ok {
			r.Reopen()
		}
	}
}

// unavailable is the backend of an ID that no backend has (a configuration
// or a chat of another version): it explains that in the chat.
type unavailable struct {
	id   string
	chat bool // a chat's own backend (else the one chosen in the preferences)
}

func (u unavailable) Name() string { return u.id }

func (u unavailable) err() error {
	if u.chat {
		return errChatUnavailable{u.id}
	}
	return &backend.Error{Kind: backend.ErrUnavailable, Message: u.id}
}

func (u unavailable) Check(context.Context) (backend.Status, error) { return backend.Status{}, u.err() }

func (u unavailable) Open(context.Context, backend.OpenOptions) (backend.Conversation, error) {
	return nil, u.err()
}

// errChatUnavailable: the backend a chat was started with does not exist in
// this version; recorded as "unavailable-chat:<id>".
type errChatUnavailable struct{ id string }

func (e errChatUnavailable) Error() string { return "ai: backend " + e.id + " is not available" }

// recordedChatUnavailable is the recorded kind of errChatUnavailable.
const recordedChatUnavailable = "unavailable-chat"
