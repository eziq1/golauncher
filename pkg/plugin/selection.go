package plugin

import "context"

// PermSelection lets a plugin receive the text selected in the window that
// was active when the launcher opened (docs/selected-text.md), through
// SelectionProvider. Only built-in plugins get it for now: plugins in their
// own process are never given the selection (a later version will ask the
// user to allow it per plugin).
const PermSelection Permission = "selection"

// Selection sources (Selection.Source).
const (
	// SelectionUIA is text read through UI Automation, without side effects.
	SelectionUIA = "uia"
	// SelectionClipboard is text copied with a simulated Ctrl+C; the
	// clipboard was restored afterwards.
	SelectionClipboard = "clipboard"
)

// Selection is the text selected in the foreground window when the launcher
// opened. The host reads it once per show, keeps it in memory only, clears
// it when the launcher hides and never stores or logs it. It is not part of
// Context, which every ContextProvider and RootProvider sees: only
// SelectionProvider receives it.
type Selection struct {
	// Text is the selected text, at most the user's maximum length (10,000
	// characters by default); see Truncated.
	Text string `json:"text"`
	// App is the application the text was selected in.
	App AppInfo `json:"app"`
	// Source is SelectionUIA or SelectionClipboard.
	Source string `json:"source"`
	// Truncated is set when the selection was longer than Text.
	Truncated bool `json:"truncated,omitzero"`
	// Editable is set when the text is known to be in an editable control,
	// so that pasting into the previous window (ActionPaste) replaces it.
	Editable bool `json:"editable,omitzero"`
}

// SelectionQuery is passed to SelectionProvider.
type SelectionQuery struct {
	Selection Selection `json:"selection"`
	// Text is what the user typed in the root search, "" when nothing.
	Text string `json:"text"`
}

// SelectionProvider contributes items for the selected text. The host shows
// them in the "Selected Text" section at the top of the root search, and
// they take part in the search when text is typed. Only plugins declaring
// PermSelection are called; return nil when nothing applies.
//
// The host marks the items as Transient, Sensitive and part of this show's
// context (never recorded as recently used), so an action must carry the
// text it needs in its Payload or Target. Item.Score orders the section
// while nothing is typed (highest first; e.g. 1 for "this is a URL"); with
// text typed, an item with a Score is taken as matching it with that score,
// and the others are fuzzy-matched like any root item. While nothing is
// typed the section shows at most a few items.
type SelectionProvider interface {
	SelectionItems(ctx context.Context, q SelectionQuery) ([]Item, error)
}
