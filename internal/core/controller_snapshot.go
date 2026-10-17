package core

// Snapshot: the immutable state that front ends render.

import (
	"cmp"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

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
	// Hint is the footer's hint on a view restored after a hide, e.g. "Esc:
	// back to main screen" (docs/keep-view.md §5); a click goes to the root
	// (Controller.PopToRoot).
	Hint string `json:"hint,omitempty"`
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
	s.Hint = c.keepHint()
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
