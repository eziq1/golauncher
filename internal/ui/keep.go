package ui

import (
	"context"
	"slices"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
)

// Views kept across a hide (docs/keep-view.md): the window tells the
// Controller when it hides, and the next show may return to the views kept.

// ShownWith is Shown for a show described by o (core.ShowOptions.Root: the
// selected-text hotkey, or a show that opens a command). Call it on the UI
// thread.
func (l *Launcher) ShownWith(o core.ShowOptions) {
	l.text, l.sent, l.panelText, l.panelSent = "", "", "", ""
	l.hoverRow, l.armed = -1, false
	l.input.Focus()
	l.work.Do(func(ctx context.Context) {
		l.ctrl.Show(ctx, o)
		l.host.Invalidate()
	})
}

// Hidden tells the Controller that the window was hidden, after the input
// queued before. Safe from any goroutine.
func (l *Launcher) Hidden() {
	l.work.Do(func(context.Context) { l.ctrl.Hidden() })
}

// returnToRootSetting is the General setting "Return to the main screen":
// when a view kept across a hide gives way to the root search.
func (s *Settings) returnToRootSetting(c *mui.Context, cfg config.Config) {
	label := s.loc.T("settings.general.returnToRoot")
	s.setting(c, label, s.loc.T("settings.general.returnToRoot.desc"), func() {
		values := []string{config.ReturnToRootImmediately, config.ReturnToRootAfter, config.ReturnToRootNever}
		i := slices.Index(values, cfg.EffectiveReturnToRoot())
		if mui.Segmented(c, &i, s.loc.T("settings.returnToRoot.immediately"), s.loc.T("settings.returnToRoot.after10m"),
			s.loc.T("settings.returnToRoot.never")).Label(label).Changed() && i >= 0 && i < len(values) {
			s.update(func(c *config.Config) { c.ReturnToRoot = values[i] })
		}
	})
}

// keepHint shows the footer's hint on a view restored after a hide: its
// label and the Escape key cap; a click goes back to the root search.
func (l *Launcher) keepHint(c *mui.Context, hint string) {
	t := c.Theme()
	b := mui.Row(c.Key("keep-hint")).Gap(6).Padding(2, 6).Radius(l.rad(6)).AlignItems(mui.Center).Shrink(1).MinWidth(0).Label(hint)
	if b.Hovered() {
		b.Background(l.res.Tokens.Control)
	}
	if b.Clicked() {
		l.text, l.sent = "", ""
		l.work.Do(func(ctx context.Context) {
			l.ctrl.PopToRoot(ctx)
			l.host.Invalidate()
		})
		l.input.Focus()
	}
	b.Children(func() {
		// A key cap: key names are not translated, as in keyCaps.
		mui.Text(c, "Esc").FontSize(11).TextColor(t.TextMuted).Padding(1, 5).Radius(4).Background(l.res.Tokens.Control).SingleLine() // lint:allow-literal
		mui.Text(c, hint).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
	})
}
