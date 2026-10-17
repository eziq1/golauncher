package core

// Running actions and commands, opening Background Tasks, and toasts.

import (
	"context"
	"fmt"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// CopyContextPath copies the folder of the current context (the footer's
// context chip) and shows a toast.
func (c *Controller) CopyContextPath() error {
	cur := c.eng.Context()
	if cur == nil || cur.Folder == "" {
		return nil
	}
	if err := c.eng.sys.CopyText(cur.Folder); err != nil {
		c.setToast(&plugin.Toast{Style: plugin.ToastFailure, Title: c.eng.core.T("toast.actionFailed"), Message: err.Error()})
		return err
	}
	c.setToast(&plugin.Toast{Style: plugin.ToastSuccess, Title: c.eng.core.T("toast.copied")})
	return nil
}

// Activate runs the primary action of row i (e.g. on double click).
func (c *Controller) Activate(ctx context.Context, i int) (Outcome, error) {
	c.Select(i)
	return c.Key(ctx, "Enter")
}

// RunAction runs the action with id of the entry with key in the current view.
func (c *Controller) RunAction(ctx context.Context, entryKey, actionID string) (Outcome, error) {
	c.waitSearches()
	c.mu.Lock()
	entry, ok := FindEntry(c.visible(c.top()), entryKey)
	c.mu.Unlock()
	if !ok {
		return Outcome{}, fmt.Errorf("core: no entry %q in the current view", entryKey)
	}
	action, ok := FindAction(entry, actionID)
	if !ok {
		return Outcome{}, fmt.Errorf("core: entry %q has no action %q", entryKey, actionID)
	}
	return c.run(ctx, entry, action)
}

// OpenCommand resets to the root and runs a command, as its hotkey does. A
// command with required arguments is shown selected in the root search
// instead, with its first argument focused.
func (c *Controller) OpenCommand(ctx context.Context, pluginID, name string) (Outcome, error) {
	c.mu.Lock()
	navs := c.toRoot()
	c.panel = nil
	c.confirm = nil
	c.mu.Unlock()
	c.closed(navs)
	entry, ok := c.eng.commandEntry(c.eng.cfg.Get(), pluginID, name)
	if !ok {
		return Outcome{}, fmt.Errorf("core: unknown command %s/%s", pluginID, name)
	}
	if first := missingArgument(entry, nil); first != "" {
		c.mu.Lock()
		f := c.top()
		f.text, f.sel, f.selKey = entry.Title, 0, entry.Key
		f.argsFor(entry.Key).focus = first
		c.mu.Unlock()
		c.refresh(ctx)
		c.waitSearches()
		return Outcome{}, nil
	}
	return c.run(ctx, entry, entry.Actions[0])
}

// run executes an action, first collecting the entry's arguments and asking
// for confirmation if it is destructive.
func (c *Controller) run(ctx context.Context, entry Entry, action plugin.Action) (Outcome, error) {
	if k := action.Do.Kind; (k == plugin.ActionCallback || k == plugin.ActionRunCommand) && action.Do.Query == "" {
		c.mu.Lock()
		action.Do.Query = c.top().text
		c.mu.Unlock()
	}
	if len(entry.Arguments) > 0 {
		c.mu.Lock()
		f := c.top()
		st := f.argsOf(entry.Key)
		if len(entry.Actions) > 0 && action.ID == entry.Actions[0].ID {
			// The primary action needs the required arguments.
			if name := missingArgument(entry, st); name != "" {
				f.argsFor(entry.Key).focus = name
				c.mu.Unlock()
				return Outcome{}, nil
			}
		}
		action.Do.Values = argumentValues(entry, st, action.Do.Values)
		c.mu.Unlock()
	}
	if action.Style == plugin.StyleDestructive {
		c.mu.Lock()
		c.confirm = &pending{entry: entry, action: action}
		c.mu.Unlock()
		return Outcome{}, nil
	}
	return c.exec(ctx, entry, action)
}

// exec executes an action and applies its outcome to the navigation state.
func (c *Controller) exec(ctx context.Context, entry Entry, action plugin.Action) (Outcome, error) {
	c.mu.Lock()
	fromRoot := len(c.stack) == 1
	c.mu.Unlock()
	return c.execFrom(ctx, entry, action, fromRoot)
}

// execFrom is exec; fromRoot records the use of the entry for ranking.
func (c *Controller) execFrom(ctx context.Context, entry Entry, action plugin.Action, fromRoot bool) (Outcome, error) {
	if action.Do.Kind == plugin.ActionChooseFiles {
		// The open dialog, for the view the action runs from (files.go).
		return c.chooseFiles(ctx, entry, action)
	}
	out, err := c.eng.Run(ctx, entry, action, fromRoot)
	return c.applyOutcome(ctx, out, err)
}

// applyOutcome applies the outcome of an action (or of files received) to
// the navigation state: its toast, a form's field errors, the views pushed
// or popped, a refresh.
func (c *Controller) applyOutcome(ctx context.Context, out Outcome, err error) (Outcome, error) {
	if err != nil {
		c.setToast(&plugin.Toast{Style: plugin.ToastFailure, Title: c.eng.core.T("toast.actionFailed"), Message: err.Error()})
		return out, err
	}
	if out.Toast != nil {
		c.setToast(out.Toast)
	}
	if len(out.FieldErrors) > 0 {
		// The plugin rejected the form: it stays open with the messages.
		c.mu.Lock()
		if f := c.top(); f.form != nil {
			f.form.setErrors(out.FieldErrors)
		}
		c.mu.Unlock()
		out.Hide, out.Push, out.PopToRoot, out.Refresh = false, nil, false, false
		return out, nil
	}
	if out.ClearText {
		c.mu.Lock()
		if f := c.top(); f.form == nil {
			f.text, f.sel, f.selKey, f.moved = "", 0, "", false
		}
		c.mu.Unlock()
	}
	switch {
	case out.Push != nil:
		nav := *out.Push
		c.mu.Lock()
		c.instances++
		nav.Request.Instance = fmt.Sprintf("v%d", c.instances)
		c.stack = append(c.stack, &frame{nav: &nav})
		c.mu.Unlock()
		c.refresh(ctx)
	case out.PopToRoot:
		c.mu.Lock()
		navs := c.toRoot()
		c.mu.Unlock()
		c.closed(navs)
		c.refresh(ctx)
	case out.Refresh, out.ClearText:
		c.refresh(ctx)
	}
	return out, nil
}

func (c *Controller) setToast(t *plugin.Toast) {
	c.mu.Lock()
	c.toast = t
	c.mu.Unlock()
}

// ShowToast shows t in the footer, as if an action had returned it, and
// tells the front end (Host.Toast, a finished background task).
func (c *Controller) ShowToast(t plugin.Toast) {
	c.mu.Lock()
	c.toast = &t
	notify := c.onChange
	c.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// OpenTasks resets to the root and opens Background Tasks: the task with id,
// or the list of all tasks for "" (Ctrl+J, the footer's indicator, a
// notification about a task).
func (c *Controller) OpenTasks(ctx context.Context, id string) (Outcome, error) {
	if c.eng.tasks == nil {
		return Outcome{}, nil
	}
	c.mu.Lock()
	navs := c.toRoot()
	c.panel = nil
	c.confirm = nil
	c.mu.Unlock()
	c.closed(navs)
	return c.execFrom(ctx, Entry{}, plugin.Action{ID: "show-task", Do: plugin.ActionDo{Kind: plugin.ActionShowTask, Target: id}}, false)
}

// ClearToast removes the current toast.
func (c *Controller) ClearToast() {
	c.mu.Lock()
	c.toast = nil
	c.mu.Unlock()
}
