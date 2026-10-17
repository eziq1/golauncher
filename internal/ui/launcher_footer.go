package ui

// The launcher's footer: toast, context and selection chips, tasks
// indicator and action hints.

import (
	"cmp"
	"context"
	"strconv"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

func (l *Launcher) footer(c *mui.Context, s core.Snapshot) {
	t, tk := c.Theme(), &l.res.Tokens
	bar := mui.Row(c).Height(40).Padding(0, 12, 0, 16).Gap(10).AlignItems(mui.Center)
	if tk.Footer.A > 0 {
		bar.Background(tk.Footer)
	}
	bar.Children(func() {
		if s.Tasks != nil {
			l.tasksIndicator(c, s.Tasks)
		}
		if s.Toast != nil {
			color, icon := t.Text, "info"
			switch s.Toast.Style {
			case plugin.ToastSuccess:
				color, icon = t.Success, "check"
			case plugin.ToastFailure:
				color, icon = t.Danger, "alert"
			}
			mui.Icon(c, builtinIcon(icon)).FontSize(14).TextColor(color)
			mui.Row(c).Grow(1).Shrink(1).MinWidth(0).Gap(6).ClipX().Children(func() {
				mui.Text(c, s.Toast.Title).FontSize(12).SingleLine().Shrink(0)
				if s.Toast.Message != "" {
					mui.Text(c, s.Toast.Message).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
				}
			})
		} else {
			label := l.loc.T("app.name")
			if s.Depth > 0 && s.Title != "" {
				label = s.Title
			}
			mui.Icon(c, builtinIcon("command")).FontSize(14).TextColor(t.TextMuted)
			l.shadowed(mui.Text(c, label).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0), false)
			if s.Context != nil {
				l.contextChip(c, s.Context)
			}
			if s.Selection != nil && s.Depth == 0 {
				l.selectionChip(c, s.Selection)
			}
			if s.Hint != "" {
				l.keepHint(c, s.Hint)
			}
			mui.Box(c).Grow(1)
		}
		if s.Form != nil {
			if l.footerButton(c, cmp.Or(s.Form.Submit, l.loc.T("ui.form.submit")), "Ctrl+Enter") {
				l.Key("Ctrl+Enter")
			}
			return
		}
		entry, ok := s.SelectedEntry()
		if !ok || len(entry.Actions) == 0 {
			return
		}
		primary := l.footerButton(c, entry.Actions[0].Title, "Enter")
		if primary {
			l.Key("Enter")
		}
		mui.Box(c).Size(1, 16).Background(tk.Divider)
		if l.footerButton(c, l.loc.T("ui.footer.actions"), "Ctrl+K") {
			l.Key("Ctrl+K")
		}
	})
}

// contextChip shows the context captured when the launcher opened (e.g. the
// folder open in File Explorer); a click copies its path.
func (l *Launcher) contextChip(c *mui.Context, ctx *core.ContextSnapshot) {
	t := c.Theme()
	icon := "app"
	if ctx.Folder != "" {
		icon = "folder"
	}
	chip := mui.Row(c.Key("context-chip")).Gap(5).Padding(2, 7).Radius(l.rad(6)).AlignItems(mui.Center).Shrink(1).MinWidth(0).MaxWidth(200).
		Background(t.Text.Alpha(0.06)).Label(l.loc.T("ui.context.copy", "name", ctx.Name))
	if ctx.Folder != "" {
		if chip.Hovered() {
			chip.Background(t.Text.Alpha(0.12))
		}
		if chip.Clicked() {
			l.work.Do(func(context.Context) {
				if err := l.ctrl.CopyContextPath(); err != nil {
					l.log.Warn("copy context path", "err", err)
				}
				l.host.Invalidate()
			})
			l.input.Focus()
		}
	}
	chip.Children(func() {
		mui.Icon(c, builtinIcon(icon)).FontSize(12).TextColor(t.TextMuted)
		mui.Text(c, ctx.Name).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
	})
}

// selectionChip shows how much text was selected when the launcher opened
// (docs/selected-text.md §5.1); a click shows the text, as Enter on the
// preview row does.
func (l *Launcher) selectionChip(c *mui.Context, sel *core.SelectionSnapshot) {
	t := c.Theme()
	chip := mui.Row(c.Key("selection-chip")).Gap(5).Padding(2, 7).Radius(l.rad(6)).AlignItems(mui.Center).Shrink(1).MinWidth(0).MaxWidth(200).
		Background(t.Text.Alpha(0.06)).Label(sel.Label)
	if chip.Hovered() {
		chip.Background(t.Text.Alpha(0.12))
	}
	if chip.Clicked() {
		l.text, l.sent = "", ""
		l.work.Do(func(ctx context.Context) {
			out, err := l.ctrl.OpenSelection(ctx)
			l.apply(out, err)
		})
		l.input.Focus()
	}
	chip.Children(func() {
		mui.Icon(c, builtinIcon("text")).FontSize(12).TextColor(t.TextMuted)
		mui.Text(c, sel.Label).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
	})
}

// tasksIndicator shows the background tasks in the footer: a spinner and
// the number of queued and running tasks, or a red dot for failures not
// seen yet. Clicking it (like Ctrl+J) opens Background Tasks.
func (l *Launcher) tasksIndicator(c *mui.Context, ts *core.TasksSnapshot) {
	t := c.Theme()
	b := mui.Row(c.Key("tasks-indicator")).Gap(5).Padding(3, 6).Radius(l.rad(6)).AlignItems(mui.Center).
		Label(ts.Label).Tooltip(ts.Label)
	if b.Hovered() {
		b.Background(l.res.Tokens.Control)
	}
	if b.Clicked() {
		l.Key(core.KeyTasks)
		l.input.Focus()
	}
	b.Children(func() {
		if ts.Active > 0 {
			mui.Spinner(c).Size(12, 12)
			mui.Text(c, strconv.Itoa(ts.Active)).FontSize(12).TextColor(t.TextMuted).SingleLine()
		}
		if ts.Failed > 0 {
			mui.Box(c).Size(8, 8).Radius(4).Background(t.Danger)
		}
	})
}

// footerButton shows a label and its shortcut's key caps, and reports a click.
func (l *Launcher) footerButton(c *mui.Context, label, shortcut string) bool {
	b := mui.Row(c).Gap(6).Padding(4, 6).Radius(l.rad(6)).AlignItems(mui.Center)
	if b.Hovered() {
		b.Background(l.res.Tokens.Control)
	}
	clicked := b.Clicked()
	b.Children(func() {
		mui.Text(c, label).FontSize(12).SingleLine()
		keyCapsView(c, shortcut, l.res.Tokens.Control)
	})
	if clicked {
		l.input.Focus()
	}
	return clicked
}

// keyCapsView shows a shortcut as key caps on bg.
func keyCapsView(c *mui.Context, shortcut string, bg mui.Color) {
	t := c.Theme()
	mui.Row(c).Gap(3).Children(func() {
		for _, k := range keyCaps(shortcut) {
			mui.Text(c, k).FontSize(11).TextColor(t.TextMuted).Padding(1, 5).Radius(4).Background(bg).SingleLine()
		}
	})
}
