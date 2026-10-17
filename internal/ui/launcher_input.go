package ui

// Launcher input: keys, text and clicks forwarded to core.Controller.

import (
	"context"
	"slices"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Key queues a key press for the Controller, e.g. "Down" or "Ctrl+K".
func (l *Launcher) Key(key string) {
	l.work.Do(func(ctx context.Context) {
		out, err := l.ctrl.Key(ctx, key)
		l.apply(out, err)
	})
}

// OpenCommand queues running a plugin command, as its global hotkey does.
func (l *Launcher) OpenCommand(pluginID, name string) {
	l.work.Do(func(ctx context.Context) {
		out, err := l.ctrl.OpenCommand(ctx, pluginID, name)
		l.apply(out, err)
	})
}

// OpenTasks queues opening Background Tasks at task id ("" for all tasks),
// as a notification about a task does.
func (l *Launcher) OpenTasks(id string) {
	l.work.Do(func(ctx context.Context) {
		out, err := l.ctrl.OpenTasks(ctx, id)
		l.apply(out, err)
	})
}

// apply carries out an outcome of the Controller. It runs on the worker.
func (l *Launcher) apply(out core.Outcome, err error) {
	if err != nil {
		l.log.Warn("action failed", "err", err)
	}
	if out.Hide {
		l.host.Hide()
	}
	l.host.Invalidate()
}

// setText sends the search text (or the action panel's filter) to the
// Controller if it changed. UI thread only.
func (l *Launcher) setText(text string, panel bool) {
	sent := &l.sent
	if panel {
		sent = &l.panelSent
	}
	if text == *sent {
		return
	}
	*sent = text
	l.work.Do(func(ctx context.Context) {
		l.ctrl.SetText(ctx, text)
		l.host.Invalidate()
	})
}

// back leaves a pushed view.
func (l *Launcher) back() {
	l.text, l.sent = "", ""
	l.work.Do(func(ctx context.Context) {
		l.ctrl.SetText(ctx, "")
		out, err := l.ctrl.Key(ctx, "Escape")
		l.apply(out, err)
	})
}

// hoverRowSelect selects the row under the pointer (Controller.Hover: not a
// deliberate choice, see moved in core).
func (l *Launcher) hoverRowSelect(i int) {
	l.work.Do(func(context.Context) {
		l.ctrl.Hover(i)
		l.host.Invalidate()
	})
}

func (l *Launcher) activate(i int) {
	l.work.Do(func(ctx context.Context) {
		out, err := l.ctrl.Activate(ctx, i)
		l.apply(out, err)
	})
}

// textKey handles a key pressed in the search field or the action panel's
// filter before the field does. It reports whether it took the key.
func (l *Launcher) textKey(k mui.TextKey, panel bool) bool {
	// What was typed before the key goes first, so that Enter acts on it.
	l.setText(k.Text(), panel)
	mods, key := k.Mods, k.Key
	if l.confirmOpen {
		// Only Enter (confirm), Escape (cancel) and, for an AI client's
		// request, Ctrl+Enter (allow for the session) act on the dialog.
		if mods == 0 && (key == mui.KeyEnter || key == mui.KeyEscape) || mods == Primary() && key == mui.KeyEnter {
			l.Key(KeyString(mods, key))
		}
		return true
	}
	if mods == Primary() && key == mui.KeyComma {
		l.host.OpenSettings()
		return true
	}
	if !panel && mods == Primary() && key == mui.KeyV && l.pasteFiles() {
		// Files or an image into a view that accepts them; text is pasted
		// by the field as usual.
		return true
	}
	name := KeyString(mods, key)
	switch {
	case name == "":
		return false
	case mods == 0:
		switch key {
		case mui.KeyUp, mui.KeyDown, mui.KeyEnter, mui.KeyEscape:
			l.Key(name)
			return true
		case mui.KeyBackspace:
			if k.Text() == "" {
				l.Key(name)
				return true
			}
		case mui.KeyTab:
			l.Key(name) // into the arguments of the selected entry
			return true
		}
		return l.forwardShortcut(name)
	case mods == mui.Shift && key == mui.KeyTab:
		l.Key(name)
		return true
	case mods == Primary() && (key == mui.KeyEnter || key == mui.KeyK || key == mui.KeyN || key == mui.KeyP || key == mui.KeyJ):
		l.Key(name)
		return true
	case mods == Primary() && key == mui.KeyO && !panel && l.ctrl.Snapshot().Depth == 0:
		// Choose Files… for the root search (R97), unless the selected
		// entry's action has the key: the Controller decides.
		l.Key(name)
		return true
	case core.MacKeys && mods == mui.Ctrl && (key == mui.KeyN || key == mui.KeyP):
		// ⌃N and ⌃P move the selection on macOS too, as in its text fields.
		l.Key(KeyString(Primary(), key))
		return true
	}
	return l.forwardShortcut(name)
}

// forwardShortcut forwards a key combination that is the shortcut of an
// action of the selected item, or of the open action panel.
func (l *Launcher) forwardShortcut(name string) bool {
	s := l.ctrl.Snapshot()
	var actions []plugin.Action
	if s.Panel != nil {
		actions = s.Panel.Actions
	} else if e, ok := s.SelectedEntry(); ok {
		actions = e.Actions
	}
	if !slices.ContainsFunc(actions, func(a plugin.Action) bool {
		return a.Shortcut != "" && NormalizeShortcut(a.Shortcut) == name
	}) {
		return false
	}
	l.Key(name)
	return true
}
