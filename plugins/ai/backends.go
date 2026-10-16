package ai

import (
	"context"
	"errors"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// DefaultBackendID is the ID of the Claude Code backend, the default of the
// "Backend" preference (claudecode.ID in internal/ai/claudecode).
const DefaultBackendID = "claude-code"

// BackendOption is a backend the "Backend" preference can choose (R71).
type BackendOption struct {
	// ID is stored in the preferences, e.g. "claude-code".
	ID string
	// Backend answers; its Name is the option's title.
	Backend backend.Backend
}

// chooser is the backend chosen in the "Backend" preference. Its methods
// follow the preference: a chat opened with one backend reopens its
// conversation when the choice changes (Plugin.conversation).
type chooser struct {
	p    *Plugin
	opts []BackendOption
}

var _ backend.Backend = (*chooser)(nil)

// preference is the "Backend" preference, with one option per backend.
func (c *chooser) preference() plugin.Preference {
	options := make([]plugin.Option, 0, len(c.opts))
	for _, o := range c.opts {
		options = append(options, plugin.Option{Value: o.ID, Title: o.Backend.Name()})
	}
	return plugin.Preference{Name: prefBackend, Type: plugin.PrefSelect, Title: "%pref.backend.title%", Description: "%pref.backend.description%",
		Default: plugin.MustPayload(c.opts[0].ID), Options: options}
}

// id is the ID of the chosen backend.
func (c *chooser) id() string {
	if c.p.h != nil {
		if id := c.p.h.Preferences().String(prefBackend); id != "" {
			return id
		}
	}
	return c.opts[0].ID
}

// current returns the chosen backend's ID and the backend; one that only
// reports an error when no backend has that ID.
func (c *chooser) current() (string, backend.Backend) {
	id := c.id()
	for _, o := range c.opts {
		if o.ID == id {
			return id, o.Backend
		}
	}
	return id, unavailable(id)
}

// Name implements backend.Backend.
func (c *chooser) Name() string {
	_, b := c.current()
	return b.Name()
}

// Check implements backend.Backend.
func (c *chooser) Check(ctx context.Context) (backend.Status, error) {
	_, b := c.current()
	return b.Check(ctx)
}

// Open implements backend.Backend.
func (c *chooser) Open(ctx context.Context, o backend.OpenOptions) (backend.Conversation, error) {
	_, b := c.current()
	return b.Open(ctx, o)
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

// unavailable is the backend of an ID that no backend has (a configuration
// written by another version): it explains that in the chat.
type unavailable string

func (u unavailable) Name() string { return string(u) }

func (u unavailable) err() error {
	return &backend.Error{Kind: backend.ErrUnavailable, Message: string(u)}
}

func (u unavailable) Check(context.Context) (backend.Status, error) { return backend.Status{}, u.err() }

func (u unavailable) Open(context.Context, backend.OpenOptions) (backend.Conversation, error) {
	return nil, u.err()
}
