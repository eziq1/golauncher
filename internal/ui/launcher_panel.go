package ui

// The launcher's action panel and confirmation dialog.

import (
	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

func (l *Launcher) actionPanel(c *mui.Context, p *core.PanelSnapshot) {
	t, tk, colors := c.Theme(), &l.res.Tokens, l.res.Colors
	if p.Selected != l.lastPanelSel {
		l.lastPanelSel = p.Selected
		l.actions.ScrollIntoView(p.Selected)
	}
	actions := p.Actions
	l.actions.Key = func(i int) any { return actions[i].ID }
	l.actions.Label = func(i int) string { return actions[i].Title }
	mui.Column(c.Key("action-panel")).Attach(mui.AnchorBottomRight, mui.AnchorBottomRight).Right(10).Bottom(46).
		Width(340).MaxHeight(320).Radius(l.rad(10)).Background(colors["background"]).Border(1, t.Border).
		Shadow(0, 8, 24, 0, tk.Shadow).Label(l.loc.T("ui.panel.label")).Children(func() {
		mui.Text(c, p.Title).FontSize(11).Bold().TextColor(t.TextMuted).Padding(10, 12, 4, 12).SingleLine()
		mui.List(c.Key("actions"), &l.actions, len(actions), func(i int) {
			a := actions[i]
			fg := t.Text
			if a.Style == plugin.StyleDestructive {
				fg = t.Danger
			}
			// The panel is opaque: its rows are not scaled to the
			// background's opacity.
			row := mui.Row(c).Padding(l.sp(6), 10).Gap(8).Radius(l.rad(6)).AlignItems(mui.Center)
			if i == p.Selected {
				row.Background(colors["rowSelected"])
				if a.Style != plugin.StyleDestructive {
					fg = colors["rowSelectedText"]
				}
			} else if row.Hovered() {
				row.Background(colors["rowHover"])
			}
			if row.Clicked() {
				l.activate(i)
				l.input.Focus()
			}
			row.Children(func() {
				mui.Text(c, a.Title).TextColor(fg).SingleLine().Grow(1).Shrink(1)
				// In the panel, Enter runs the selected row; other rows show
				// their own shortcut, which the panel also accepts.
				shortcut := a.Shortcut
				if i == p.Selected {
					shortcut = "Enter"
				}
				if shortcut != "" {
					keyCapsView(c, shortcut, tk.Control)
				}
			})
		}).Grow(1).Shrink(1).Padding(4).Children(func() {
			if len(actions) == 0 {
				mui.Text(c, l.loc.T("ui.panel.empty")).TextColor(t.TextMuted).Padding(10)
			}
		})
		mui.Divider(c)
		in := mui.TextInputBase(c.Key("action-filter"), &l.panelText).Bind(&l.panelInput).Padding(10, 12).
			Placeholder(l.loc.T("ui.panel.placeholder")).Label(l.loc.T("ui.panel.placeholder")).
			OnTextKey(func(k mui.TextKey) bool { return l.textKey(k, true) })
		if in.Changed() {
			l.setText(l.panelText, true)
		}
	})
}

// confirmDialog asks before a destructive action runs.
func (l *Launcher) confirmDialog(c *mui.Context, d *core.ConfirmSnapshot) {
	t, tk := c.Theme(), &l.res.Tokens
	mui.Box(c.Key("confirm")).Absolute().Top(0).Left(0).Fill().Center().Background(tk.Overlay).Children(func() {
		width := float32(360)
		if d.External {
			width = 440 // three buttons
		}
		mui.Column(c).Width(width).Padding(18).Gap(10).Radius(l.rad(12)).Background(l.res.Colors["background"]).Border(1, t.Border).
			Shadow(0, 12, 32, 0, tk.DialogShadow).Label(d.Title).Children(func() {
			mui.Text(c, d.Title).Bold()
			if d.Message != "" {
				mui.Text(c, d.Message).FontSize(13).TextColor(t.TextMuted)
			}
			confirmBg := t.Accent
			if d.Danger {
				confirmBg = t.Danger
			}
			mui.Row(c).Gap(8).Justify(mui.End).MarginY(6).Wrap().Children(func() {
				if dialogButton(c, d.Cancel, "Escape", t.Text, tk.Control) {
					l.Key("Escape")
				}
				if d.AllowSession != "" && dialogButton(c, d.AllowSession, "Ctrl+Enter", t.Text, tk.Control) {
					l.Key("Ctrl+Enter")
				}
				if dialogButton(c, d.Confirm, "Enter", t.AccentText, confirmBg) {
					l.Key("Enter")
				}
			})
		})
	})
}

// dialogButton is a button with a label and its key, and reports a click.
func dialogButton(c *mui.Context, label, key string, fg, bg mui.Color) bool {
	b := mui.Row(c).Gap(6).Padding(6, 12).Radius(6).AlignItems(mui.Center).Background(bg)
	if b.Hovered() {
		b.Opacity(0.9)
	}
	clicked := b.Clicked()
	b.Children(func() {
		mui.Text(c, label).FontSize(13).TextColor(fg).SingleLine()
		hint := "esc"
		switch key {
		case "Enter":
			hint = "↵"
		case "Ctrl+Enter":
			hint = "ctrl ↵"
		}
		mui.Text(c, hint).FontSize(11).TextColor(fg.Alpha(0.75)).SingleLine()
	})
	return clicked
}
