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

// FallbackProvider offers items that act on the search text itself, such as
// "Ask AI: <text>" or "Search the web for <text>". The host asks for them
// only when the root search found nothing good — no results, or none
// matching the text well (see core's fallback threshold) — and lists them
// last, under their own heading. They are never recorded as recently used,
// so the action must carry the text itself (Payload).
type FallbackProvider interface {
	FallbackItems(ctx context.Context, q Query) ([]Item, error)
}

// TabProvider takes the Tab key in the root search: when the user pressed
// Tab with some text typed and the selected entry takes no arguments (Tab
// fills in arguments then), the host runs the action of the first enabled
// plugin whose TabAction answers ok, e.g. opening an AI chat with the text.
// It is called on the key press and must be fast.
type TabProvider interface {
	TabAction(q Query) (Action, bool)
}

// Query is the search text typed by the user.
type Query struct {
	Text string `json:"text"`
	// Context is what the user was doing when the launcher opened (root
	// search only; nil when unknown or turned off). See ContextProvider.
	Context *Context `json:"context,omitempty"`
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
