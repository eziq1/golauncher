// Package ui draws golauncher's windows with MyGo's native UI: the launcher
// panel and the settings window. It only renders the state of the engine
// (core.Controller) and forwards input to it; everything platform-specific
// (windows, hotkeys, the tray) lives in package shell.
package ui

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	mui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Launcher sizes, in DIPs.
const (
	LauncherWidth  = 750
	LauncherHeight = 475
	rowIconSize    = 28
)

// toastDuration is how long a toast shows in the footer.
const toastDuration = 2500 * time.Millisecond

// loadingDelay is how long a search runs before a spinner shows, so that
// fast searches do not flicker.
const loadingDelay = 200 * time.Millisecond

// Host is what the launcher view needs from the window that shows it. Its
// methods must be safe to call from any goroutine.
type Host interface {
	// Hide hides the launcher window and returns once it is hidden.
	Hide()
	// OpenSettings shows the settings window.
	OpenSettings()
	// Invalidate redraws the launcher window.
	Invalidate()
}

// Launcher is the launcher panel's view: a search field, the results with
// an optional detail pane, a footer and the action panel. Input goes to the
// Controller through a Dispatcher, never on the UI thread.
type Launcher struct {
	ctrl   *core.Controller
	loc    *i18n.Localizer
	host   Host
	work   *Dispatcher
	log    *slog.Logger
	images *imageCache
	theme  atomic.Value // string: config.Config.Theme

	// The search field: text is bound to the input, sent is the text last
	// given to the Controller.
	text, sent           string
	panelText, panelSent string
	input, panelInput    mui.Handle
	panelOpen            bool
	confirmOpen          bool // a destructive action awaits confirmation

	list, actions mui.ListState
	rows          []launcherRow
	rowOf         map[int]int // flat item index -> list row
	lastSel       int
	lastPanelSel  int
	hoverRow      int  // flat index of the row last hovered, -1 for none
	armed         bool // hovering selects only once the pointer moved after showing

	// The inputs of the open form and of the selected entry's arguments.
	fields, args inputs
	formOpen     bool
	pending      bool // input was queued when the snapshot was taken

	toast   *plugin.Toast
	toastAt time.Time
	loading time.Time // when the current search started showing as loading

	md markdown.Options
}

// launcherRow is a row of the results list: a section header or an entry.
type launcherRow struct {
	header string
	entry  *core.Entry
	flat   int // index among the entries, as Snapshot.Selected counts
	// key identifies the row in the list; an entry may show in two sections
	// (Recent and Commands).
	key string
}

// NewLauncher returns the launcher view of env. host is the window it is
// shown in and work the queue its input runs on.
func NewLauncher(env *app.Env, host Host, work *Dispatcher) *Launcher {
	l := &Launcher{
		ctrl:     env.Controller,
		loc:      env.I18n.Localizer(i18n.CoreNamespace),
		host:     host,
		work:     work,
		log:      env.Services.Log,
		lastSel:  -1,
		hoverRow: -1,
	}
	l.images = newImageCache(host.Invalidate)
	l.theme.Store(env.Config.Get().Theme)
	env.Config.OnChange(func(c config.Config) {
		l.theme.Store(c.Theme)
		host.Invalidate()
	})
	// The footer's task indicator follows the background tasks.
	env.Tasks.OnChange(func(state bool) {
		if state {
			host.Invalidate()
		}
	})
	return l
}

// Shown prepares the launcher for being shown: back to the root search with
// an empty field that has the keyboard focus. Call it on the UI thread.
func (l *Launcher) Shown() {
	l.text, l.sent, l.panelText, l.panelSent = "", "", "", ""
	l.hoverRow, l.armed = -1, false
	l.input.Focus()
	l.work.Do(func(ctx context.Context) {
		l.ctrl.Reset(ctx)
		l.host.Invalidate()
	})
}

// Wait blocks until the queued input has been handled and the searches it
// started have finished.
func (l *Launcher) Wait() {
	l.work.Wait()
	l.ctrl.Wait()
}

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

func (l *Launcher) selectRow(i int) {
	l.work.Do(func(context.Context) {
		l.ctrl.Select(i)
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
		// Only Enter (confirm) and Escape (cancel) act on the dialog.
		if mods == 0 && (key == mui.KeyEnter || key == mui.KeyEscape) {
			l.Key(KeyString(mods, key))
		}
		return true
	}
	if mods == mui.Ctrl && key == mui.KeyComma {
		l.host.OpenSettings()
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
	case mods == mui.Ctrl && (key == mui.KeyEnter || key == mui.KeyK || key == mui.KeyN || key == mui.KeyP || key == mui.KeyJ):
		l.Key(name)
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

// sync takes the search text from the Controller when it changed it
// (Escape clearing it, a pushed view, Reset), unless edits are on their way.
func (l *Launcher) sync(s core.Snapshot) {
	if l.pending {
		return
	}
	if l.text == l.sent && s.Text != l.text {
		l.text, l.sent = s.Text, s.Text
	}
	if s.Panel != nil && l.panelText == l.panelSent && s.Panel.Text != l.panelText {
		l.panelText, l.panelSent = s.Panel.Text, s.Panel.Text
	}
}

// View builds the launcher.
func (l *Launcher) View(c *mui.Context) {
	applyTheme(c, l.theme.Load().(string))
	t := c.Theme()
	if c.Vibrancy() {
		c.Root().Background(mui.Transparent)
	} else {
		c.Root().Background(t.Background)
	}
	// Whether edits are on their way, asked before the snapshot is taken: it
	// may otherwise miss edits that were made in between.
	l.pending = l.work.Pending()
	s := l.ctrl.Snapshot()
	l.sync(s)
	l.updateToast(c, s)

	// The keyboard focus follows the action panel.
	if open := s.Panel != nil; open != l.panelOpen {
		l.panelOpen = open
		if open {
			l.panelText, l.panelSent = "", ""
			l.lastPanelSel = -1
			l.panelInput.Focus()
		} else {
			l.input.Focus()
		}
	}

	// A form has no search field: the focus is in its fields.
	if open := s.Form != nil; open != l.formOpen {
		l.formOpen = open
		if !open {
			l.fields.reset("")
			l.input.Focus()
		}
	}

	mui.Box(c).Fill().Children(func() {
		mui.Column(c).Fill().Children(func() {
			l.searchBar(c, s)
			mui.Divider(c)
			if s.Form != nil {
				l.formView(c, s)
			} else {
				mui.Row(c).Grow(1).AlignItems(mui.Stretch).Children(func() {
					l.results(c, s)
					if s.ShowDetail {
						mui.Divider(c).Vertical()
						l.detail(c, s.Detail)
					}
				})
			}
			mui.Divider(c)
			l.footer(c, s)
		})
		if s.Panel != nil {
			l.actionPanel(c, s.Panel)
		}
		l.confirmOpen = s.Confirm != nil
		if s.Confirm != nil {
			l.confirmDialog(c, s.Confirm)
		}
	})
}

// applyTheme forces the light or dark theme when the configuration asks for
// one; "system" keeps the theme that follows the desktop.
func applyTheme(c *mui.Context, theme string) {
	switch dark := c.Theme().Dark; {
	case theme == "light" && dark:
		c.SetTheme(mui.LightTheme())
	case theme == "dark" && !dark:
		c.SetTheme(mui.DarkTheme())
	}
}

func (l *Launcher) updateToast(c *mui.Context, s core.Snapshot) {
	if s.Toast != l.toast {
		l.toast, l.toastAt = s.Toast, c.Now()
	}
	if l.toast == nil {
		return
	}
	if left := toastDuration - c.Now().Sub(l.toastAt); left > 0 {
		c.After(left)
		return
	}
	l.ctrl.ClearToast()
	l.toast = nil
}

func (l *Launcher) searchBar(c *mui.Context, s core.Snapshot) {
	t := c.Theme()
	mui.Row(c).Padding(14, 16).Gap(10).AlignItems(mui.Center).Children(func() {
		if s.Depth > 0 {
			back := mui.Box(c).Size(28, 28).Center().Radius(6).Background(t.Text.Alpha(0.08)).Label(l.loc.T("ui.back"))
			if back.Hovered() {
				back.Background(t.Text.Alpha(0.14))
			}
			if back.Clicked() {
				l.back()
				l.input.Focus()
			}
			back.Children(func() {
				mui.Icon(c, builtinIcon("chevron-left")).FontSize(16).TextColor(t.Text)
			})
			if s.Title != "" {
				mui.Text(c, s.Title).FontSize(13).TextColor(t.TextMuted).SingleLine().MaxWidth(200)
			}
		} else {
			mui.Icon(c, builtinIcon("search")).FontSize(20).TextColor(t.TextMuted)
		}
		if s.Form != nil {
			mui.Box(c).Grow(1).Height(28)
		} else {
			in := mui.TextInputBase(c.Key("search"), &l.text).Bind(&l.input).AutoFocus().Grow(1).FontSize(18).
				Placeholder(s.Placeholder).Label(l.loc.T("ui.search.label")).
				OnTextKey(func(k mui.TextKey) bool { return l.textKey(k, false) })
			if in.Changed() {
				l.setText(l.text, false)
			}
			l.argumentInputs(c, s, in)
		}
		if s.Loading {
			if l.loading.IsZero() {
				l.loading = c.Now()
			}
			if wait := loadingDelay - c.Now().Sub(l.loading); wait > 0 {
				c.After(wait)
			} else {
				mui.Spinner(c).Size(16, 16).Label(l.loc.T("ui.loading"))
			}
		} else {
			l.loading = time.Time{}
		}
	})
}

// buildRows flattens the sections into list rows.
func (l *Launcher) buildRows(s core.Snapshot) {
	l.rows = l.rows[:0]
	clear(l.rowOf)
	if l.rowOf == nil {
		l.rowOf = map[int]int{}
	}
	flat := 0
	for si := range s.Sections {
		sec := &s.Sections[si]
		if sec.Title != "" {
			l.rows = append(l.rows, launcherRow{header: sec.Title, flat: -1, key: "section:" + sec.Title})
		}
		for i := range sec.Items {
			l.rowOf[flat] = len(l.rows)
			// Keys follow the section's title, not its index, so rows keep
			// their identity when a section appears above (the context).
			l.rows = append(l.rows, launcherRow{entry: &sec.Items[i], flat: flat, key: sec.Title + "/" + sec.Items[i].Key})
			flat++
		}
	}
}

func (l *Launcher) results(c *mui.Context, s core.Snapshot) {
	t := c.Theme()
	l.buildRows(s)
	if s.Selected != l.lastSel {
		l.lastSel = s.Selected
		if r, ok := l.rowOf[s.Selected]; ok {
			l.list.ScrollIntoView(r)
		}
	}
	rows := l.rows
	l.list.Key = func(i int) any { return rows[i].key }
	// Section headers are plain rows: MyGo pins a list's headers to the top
	// while their rows scroll, which needs an opaque header background that
	// would cover the Acrylic material (and overlapped the first row).
	l.list.Header = nil
	l.list.Label = func(i int) string {
		if rows[i].entry == nil {
			return rows[i].header
		}
		return rows[i].entry.Title
	}
	list := mui.List(c.Key("results"), &l.list, len(rows), func(i int) { l.row(c, s, rows[i]) }).Grow(1).Basis(0).Padding(6)
	list.Children(func() {
		if len(rows) > 0 {
			return
		}
		switch {
		case s.Error != "":
			mui.Text(c, s.Error).TextColor(t.Danger).Padding(16)
		case !s.Loading && (s.Text != "" || s.Depth > 0):
			mui.Text(c, s.EmptyText).TextColor(t.TextMuted).Padding(16)
		}
	})
}

func (l *Launcher) row(c *mui.Context, s core.Snapshot, r launcherRow) {
	t := c.Theme()
	if r.entry == nil {
		bg := t.Background.Alpha(0.9)
		if c.Vibrancy() {
			bg = mui.Transparent
		}
		mui.Text(c, r.header).FontSize(11).Bold().TextColor(t.TextMuted).Padding(8, 10, 4, 10).Background(bg).SingleLine()
		return
	}
	e := r.entry
	selected := r.flat == s.Selected
	text, muted := t.Text, t.TextMuted
	row := mui.Row(c).Padding(6, 10).Gap(12).AlignItems(mui.Center).Radius(8)
	if selected {
		row.Background(t.Accent.Alpha(0.9))
		text, muted = t.AccentText, t.AccentText.Alpha(0.75)
	} else if row.Hovered() {
		row.Background(t.Text.Alpha(0.05))
	}
	if row.Hovered() && r.flat != l.hoverRow {
		// The first row under the pointer after showing does not take the
		// choice: the pointer may just be resting there.
		if l.armed && !selected {
			l.selectRow(r.flat)
		}
		l.hoverRow, l.armed = r.flat, true
	}
	if row.Clicked() {
		l.activate(r.flat)
		l.input.Focus()
	}
	row.Children(func() {
		l.icon(c, e.Icon, rowIconSize, text)
		mui.Row(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).AlignItems(mui.Center).ClipX().Children(func() {
			mui.Text(c, e.Title).TextColor(text).SingleLine().Shrink(0)
			if e.Subtitle != "" {
				mui.Text(c, e.Subtitle).TextColor(muted).SingleLine().Shrink(1).MinWidth(0)
			}
		})
		if e.Accessory != "" {
			mui.Text(c, e.Accessory).FontSize(12).TextColor(muted).SingleLine().Shrink(0)
		}
	})
}

func (l *Launcher) detail(c *mui.Context, d *plugin.Detail) {
	t := c.Theme()
	mui.Scroll(c.Key("detail")).WidthPercent(40).Shrink(0).Padding(14).Children(func() {
		if d == nil {
			return
		}
		mui.Column(c).Gap(12).Children(func() {
			if d.Image != "" {
				if src := l.images.get(d.Image); src != nil {
					mui.Image(c, src).MaxHeight(200).Fit(mui.Contain).Radius(6)
				}
			}
			if d.Markdown != "" {
				markdown.View(c, d.Markdown, &l.md)
			}
			if len(d.Metadata) > 0 {
				mui.Divider(c)
				for _, m := range d.Metadata {
					mui.Row(c).Gap(8).Children(func() {
						mui.Text(c, m.Label).FontSize(12).TextColor(t.TextMuted).Grow(1).SingleLine()
						mui.Text(c, m.Value).FontSize(12).SingleLine().Shrink(1)
					})
				}
			}
		})
	})
}

func (l *Launcher) footer(c *mui.Context, s core.Snapshot) {
	t := c.Theme()
	mui.Row(c).Height(40).Padding(0, 12, 0, 16).Gap(10).AlignItems(mui.Center).Children(func() {
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
			mui.Text(c, label).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(1).MinWidth(0)
			if s.Context != nil {
				l.contextChip(c, s.Context)
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
		mui.Box(c).Size(1, 16).Background(t.Border)
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
	chip := mui.Row(c.Key("context-chip")).Gap(5).Padding(2, 7).Radius(6).AlignItems(mui.Center).Shrink(1).MinWidth(0).MaxWidth(200).
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

// tasksIndicator shows the background tasks in the footer: a spinner and
// the number of queued and running tasks, or a red dot for failures not
// seen yet. Clicking it (like Ctrl+J) opens Background Tasks.
func (l *Launcher) tasksIndicator(c *mui.Context, ts *core.TasksSnapshot) {
	t := c.Theme()
	b := mui.Row(c.Key("tasks-indicator")).Gap(5).Padding(3, 6).Radius(6).AlignItems(mui.Center).
		Label(ts.Label).Tooltip(ts.Label)
	if b.Hovered() {
		b.Background(t.Text.Alpha(0.08))
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
	t := c.Theme()
	b := mui.Row(c).Gap(6).Padding(4, 6).Radius(6).AlignItems(mui.Center)
	if b.Hovered() {
		b.Background(t.Text.Alpha(0.08))
	}
	clicked := b.Clicked()
	b.Children(func() {
		mui.Text(c, label).FontSize(12).SingleLine()
		keyCapsView(c, shortcut)
	})
	if clicked {
		l.input.Focus()
	}
	return clicked
}

// keyCapsView shows a shortcut as key caps.
func keyCapsView(c *mui.Context, shortcut string) {
	t := c.Theme()
	mui.Row(c).Gap(3).Children(func() {
		for _, k := range keyCaps(shortcut) {
			mui.Text(c, k).FontSize(11).TextColor(t.TextMuted).Padding(1, 5).Radius(4).Background(t.Text.Alpha(0.08)).SingleLine()
		}
	})
}

func (l *Launcher) actionPanel(c *mui.Context, p *core.PanelSnapshot) {
	t := c.Theme()
	if p.Selected != l.lastPanelSel {
		l.lastPanelSel = p.Selected
		l.actions.ScrollIntoView(p.Selected)
	}
	actions := p.Actions
	l.actions.Key = func(i int) any { return actions[i].ID }
	l.actions.Label = func(i int) string { return actions[i].Title }
	mui.Column(c.Key("action-panel")).Attach(mui.AnchorBottomRight, mui.AnchorBottomRight).Right(10).Bottom(46).
		Width(340).MaxHeight(320).Radius(10).Background(t.Background).Border(1, t.Border).
		Shadow(0, 8, 24, 0, mui.RGBA(0, 0, 0, 0.25)).Label(l.loc.T("ui.panel.label")).Children(func() {
		mui.Text(c, p.Title).FontSize(11).Bold().TextColor(t.TextMuted).Padding(10, 12, 4, 12).SingleLine()
		mui.List(c.Key("actions"), &l.actions, len(actions), func(i int) {
			a := actions[i]
			fg := t.Text
			if a.Style == plugin.StyleDestructive {
				fg = t.Danger
			}
			row := mui.Row(c).Padding(6, 10).Gap(8).Radius(6).AlignItems(mui.Center)
			if i == p.Selected {
				row.Background(t.Accent.Alpha(0.9))
				if a.Style != plugin.StyleDestructive {
					fg = t.AccentText
				}
			} else if row.Hovered() {
				row.Background(t.Text.Alpha(0.05))
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
					keyCapsView(c, shortcut)
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
	t := c.Theme()
	mui.Box(c.Key("confirm")).Absolute().Top(0).Left(0).Fill().Center().Background(mui.RGBA(0, 0, 0, 0.35)).Children(func() {
		mui.Column(c).Width(360).Padding(18).Gap(10).Radius(12).Background(t.Background).Border(1, t.Border).
			Shadow(0, 12, 32, 0, mui.RGBA(0, 0, 0, 0.3)).Label(d.Title).Children(func() {
			mui.Text(c, d.Title).Bold()
			if d.Message != "" {
				mui.Text(c, d.Message).FontSize(13).TextColor(t.TextMuted)
			}
			mui.Row(c).Gap(8).Justify(mui.End).MarginY(6).Children(func() {
				if dialogButton(c, d.Cancel, "Escape", t.Text, t.Text.Alpha(0.08)) {
					l.Key("Escape")
				}
				if dialogButton(c, d.Confirm, "Enter", t.AccentText, t.Danger) {
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
		if key == "Enter" {
			hint = "↵"
		}
		mui.Text(c, hint).FontSize(11).TextColor(fg.Alpha(0.75)).SingleLine()
	})
	return clicked
}
