// Package plugin is the public SDK for golauncher plugins.
//
// Built-in plugins (apps, calculator, clipboard) are written against this
// package only; they never import golauncher internals. A plugin describes
// what to show (items, views, actions) and the host decides how to render it
// and how to carry out each action.
package plugin

import (
	"context"
	"errors"
	"io/fs"
)

// Plugin is implemented by every plugin.
type Plugin interface {
	Manifest() Manifest
	// Init is called once before any other call. The Host stays valid until
	// Close. Returning an error wrapping ErrUnavailable leaves the plugin
	// disabled without treating it as a failure; the error text is shown to
	// the user as the reason (e.g. "Everything is not installed").
	Init(ctx context.Context, h Host) error
	Close() error
}

// ErrUnavailable reports that a plugin cannot run on this system, e.g.
// because software it depends on is missing.
var ErrUnavailable = errors.New("plugin unavailable")

// RootProvider contributes items to the root search.
//
// Items without a Score are fuzzy-matched by the host against Title, Subtitle
// and Keywords, so a provider may return its full candidate set. Items with a
// Score > 0 are taken as already matched (e.g. a calculator result).
type RootProvider interface {
	RootItems(ctx context.Context, q Query) ([]Item, error)
}

// ViewProvider renders views pushed by commands with ModeView or by
// ActionPush actions. It is called again for every change of the search text.
type ViewProvider interface {
	View(ctx context.Context, req ViewRequest) (View, error)
}

// ActionHandler runs no-view commands (ActionRunCommand) and plugin
// callbacks (ActionCallback).
type ActionHandler interface {
	HandleAction(ctx context.Context, a ActionDo) (Result, error)
}

// ViewCloser is implemented by plugins that keep state per open view, such as
// a timer pushing Host.Render. The host calls ViewClosed (on a background
// goroutine) when the view instance (ViewRequest.Instance) is closed.
type ViewCloser interface {
	ViewClosed(instance string)
}

// LocaleProvider exposes the plugin's message catalogs: a filesystem holding
// "<lang>.json" files (for example "en.json"). Manifest strings written as
// "%key%" and Localizer lookups resolve against these catalogs.
type LocaleProvider interface {
	Locales() fs.FS
}

// PreferenceOptionsProvider supplies the options of preferences declared with
// DynamicOptions.
type PreferenceOptionsProvider interface {
	PreferenceOptions(ctx context.Context, name string) ([]Option, error)
}

// Query is the search text typed by the user.
type Query struct {
	Text string `json:"text"`
}

// ViewRequest identifies a view of a plugin.
type ViewRequest struct {
	// ID is the command name for views opened from a command, or the Target
	// of an ActionPush.
	ID      string  `json:"id"`
	Payload Payload `json:"payload,omitempty"`
	Query   Query   `json:"query"`
	// Instance identifies this open view (e.g. "v17") for Host.Render; it
	// changes every time the view is opened.
	Instance string `json:"instance,omitempty"`
	// Arguments are the values of the opening command's arguments.
	Arguments map[string]string `json:"arguments,omitempty"`
}
