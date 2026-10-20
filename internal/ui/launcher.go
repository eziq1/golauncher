// Package ui draws golauncher's windows with MyGo's native UI: the launcher
// panel and the settings window. It only renders the state of the engine
// (core.Controller) and forwards input to it; everything platform-specific
// (windows, hotkeys, the tray) lives in package shell.
package ui

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	mui "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"

	"github.com/eziq1/golauncher/internal/app"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/i18n"
	"github.com/eziq1/golauncher/internal/theme"
	"github.com/eziq1/golauncher/internal/ui/mathview"
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
	// ChooseFiles shows the system's open dialog for the launcher window
	// (plugin.ActionChooseFiles) and returns the files chosen, none when
	// cancelled. It blocks until the dialog closes.
	ChooseFiles(o plugin.ChooseFilesOptions) ([]string, error)
}

// Launcher is the launcher panel's view: a search field, the results with
// an optional detail pane, a footer and the action panel. Input goes to the
// Controller through a Dispatcher, never on the UI thread.
type Launcher struct {
	ctrl   *core.Controller
	clip   plugin.Clipboard // probed on Ctrl+V (core.FilesClipboard)
	loc    *i18n.Localizer
	host   Host
	work   *Dispatcher
	log    *slog.Logger
	images *imageCache
	themes *theme.Manager
	// appearance is the configuration's (config.Appearance), kept here so
	// that a frame does not copy the whole configuration.
	appearance atomic.Pointer[config.Appearance]
	// res is what the frame being built draws with (UI thread only).
	res *theme.Resolved

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
	lastSel       string      // key of the selected row, to scroll when it changes
	lastRows      string      // the rows' keys, to notice the results changing
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

	// md shows Markdown (a plugin's detail, an AI answer) with its math.
	md mathview.Markdown

	// The scrolling of a full-window detail view (fullDetail) and the view
	// it belongs to.
	detailScroll mui.ScrollState
	detailSig    string
}

// NewLauncher returns the launcher view of env. host is the window it is
// shown in and work the queue its input runs on.
func NewLauncher(env *app.Env, host Host, work *Dispatcher) *Launcher {
	l := &Launcher{
		ctrl:     env.Controller,
		clip:     env.Services.Clipboard,
		loc:      env.I18n.Localizer(i18n.CoreNamespace),
		host:     host,
		work:     work,
		log:      env.Services.Log,
		hoverRow: -1,
	}
	l.images = newImageCache(host.Invalidate)
	l.md.Options.LoadImage = localImage
	// Math between single dollars too, as language models write it.
	l.md.Options.Extensions = parser.Default | parser.ExtDollarMath
	// ActionChooseFiles runs on the Dispatcher's goroutine, which waits for
	// the dialog.
	env.Engine.SetFileChooser(func(_ context.Context, o plugin.ChooseFilesOptions) ([]string, error) {
		return host.ChooseFiles(o)
	})
	l.themes = env.Themes
	a := env.Config.Get().Appearance
	l.appearance.Store(&a)
	env.Config.OnChange(func(c config.Config) {
		l.appearance.Store(&c.Appearance)
		host.Invalidate()
	})
	// The themes changed on disk, a setting is previewed, or the
	// transparency effects were turned on or off.
	env.Themes.OnChange(host.Invalidate)
	// The footer's task indicator follows the background tasks.
	env.Tasks.OnChange(func(state bool) {
		if state {
			host.Invalidate()
		}
	})
	return l
}

// Shown prepares the launcher for being shown: back to the root search with
// an empty field that has the keyboard focus, or to the views kept by the
// last hide (keep.go). Call it on the UI thread.
func (l *Launcher) Shown() { l.ShownWith(core.ShowOptions{}) }

// Wait blocks until the queued input has been handled and the searches it
// started have finished.
func (l *Launcher) Wait() {
	l.work.Wait()
	l.ctrl.Wait()
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
	// The settings, which show no material, ask whether the launcher does.
	l.themes.SetNoMaterial(!c.Vibrancy())
	l.res = resolveTheme(c, l.themes, *l.appearance.Load())
	ui := l.res.UI
	c.SetTheme(&ui)
	l.md.CopyAsMarkdown = l.loc.T("ui.markdown.copyAsMarkdown")
	// After SetTheme, which gives the root the theme's background.
	c.Root().Background(l.res.Background)
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
			} else if s.Layout == string(plugin.ViewDetail) {
				l.fullDetail(c, s)
			} else {
				mui.Row(c).Grow(1).AlignItems(mui.Stretch).Children(func() {
					l.results(c, s)
					if s.ShowDetail {
						mui.Divider(c).Vertical()
						l.detail(c, s.Detail)
					}
				})
			}
			if len(s.Chips) > 0 {
				l.chips(c, s.Chips)
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

// systemOf is the state of the system as the window c sees it. Call it
// before SetTheme: the frame's theme starts as MyGo's, of the window's
// appearance.
func systemOf(c *mui.Context) theme.System {
	p := c.Preferences()
	return theme.System{
		Dark:         c.Theme().Dark,
		Accent:       p.Accent,
		HighContrast: p.HighContrast,
		TextScale:    p.TextScale,
		NoMaterial:   !c.Vibrancy(),
	}
}

// resolveTheme is the theme the launcher draws with in the window c. The
// manager keeps it until something it depends on changes.
func resolveTheme(c *mui.Context, m *theme.Manager, a config.Appearance) *theme.Resolved {
	return m.Resolve(a, systemOf(c))
}

// rad scales a corner radius of the launcher with the theme's radius.
func (l *Launcher) rad(r float32) float32 { return r * l.res.RadiusScale }

// sp scales a padding of the launcher with the theme's density.
func (l *Launcher) sp(v float32) float32 { return v * l.res.SpaceScale }

// shadowed gives text e the faint backdrop of a very translucent
// background (docs/themes.md §4), if any. Text on the selected row has the
// row's own fill under it and gets none.
func (l *Launcher) shadowed(e mui.Element, selected bool) mui.Element {
	if s := l.res.Tokens.TextShadow; s != mui.Transparent && !selected {
		e.TextBackground(s)
	}
	return e
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
