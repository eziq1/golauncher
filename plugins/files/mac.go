package files

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/internal/winapi/macterm"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// On macOS (Options.Mac) the "open in" actions use the Mac's apps instead
// of wt.exe, PowerShell, cmd.exe and Code.exe: the default terminal (the
// one chosen in builtin.terminal's settings) and the other installed
// terminals, VS Code, Cursor and Zed when installed, Finder and Quick
// Look. Creating, copying, compressing and hashing are the same on both.

// Callbacks of the macOS mode.
const (
	cbTerminal  = "terminal"
	cbEditor    = "editor"
	cbQuickLook = "quickLook"
)

// macFolderItems are the actions for the current folder dir on macOS.
func (p *Plugin) macFolderItems(l plugin.Localizer, dir string) []plugin.Item {
	var items []plugin.Item
	copyPath := copyPathAction(l, dir)
	terms := macterm.Detect(p.o.AppPath)
	if def, ok := macterm.Pick(terms, p.o.PreferredTerminal()); ok {
		actions := []plugin.Action{callback("open", l.T("mac.action.openWindow"), cbTerminal, payload{Dir: dir, App: def.ID})}
		if def.Tabs {
			a := callback("new-tab", l.T("mac.action.openTab"), cbTerminal, payload{Dir: dir, App: def.ID, Tab: true})
			a.Shortcut = "Ctrl+Enter"
			actions = append(actions, a)
		}
		for _, t := range terms {
			if t.ID != def.ID {
				actions = append(actions, callback("open-"+t.ID, l.T("mac.action.openIn", "app", t.Name), cbTerminal, payload{Dir: dir, App: t.ID}))
			}
		}
		items = append(items, plugin.Item{
			ID: "terminal", Title: l.T("mac.item.terminal.title", "app", def.Name), Subtitle: dir, Icon: plugin.Icon{Name: "terminal"},
			Keywords: keywords(l.T("mac.keywords.terminal")),
			Actions:  append(actions, copyPath),
		})
	}
	for _, e := range macterm.InstalledEditors(p.o.AppPath) {
		items = append(items, plugin.Item{
			ID: e.ID, Title: l.T("mac.item.editor.title", "app", e.Name), Subtitle: dir, Icon: plugin.Icon{Name: "code"},
			Keywords: append(keywords(l.T("keywords.vscode")), strings.ToLower(e.Name)),
			Actions:  []plugin.Action{callback("open", l.T("action.open"), cbEditor, payload{App: e.ID, Paths: []string{dir}}), copyPath},
		})
	}
	items = append(items, plugin.Item{
		ID: "reveal", Title: l.T("mac.item.reveal.title"), Subtitle: dir, Icon: plugin.Icon{Name: "folder"},
		Keywords: keywords(l.T("mac.keywords.reveal")),
		Actions:  []plugin.Action{openAction("reveal", l.T("mac.action.reveal"), dir, plugin.OpenOptions{Reveal: true}), copyPath},
	})
	return append(items, createItems(l, dir)...)
}

// macSelectionItems are the actions for the selected files and folders on
// macOS.
func (p *Plugin) macSelectionItems(l plugin.Localizer, c plugin.Context) []plugin.Item {
	sel := c.Selected
	subtitle := l.N("subtitle.selected", len(sel), "name", filepath.Base(sel[0]))
	items := []plugin.Item{copySelectedItem(l, sel, subtitle, "\n")}
	for _, e := range macterm.InstalledEditors(p.o.AppPath) {
		items = append(items, plugin.Item{
			ID: e.ID + "-selected", Title: l.T("mac.item.editorSelected.title", "app", e.Name), Subtitle: subtitle, Icon: plugin.Icon{Name: "code"},
			Keywords: append(keywords(l.T("keywords.vscode")), strings.ToLower(e.Name)),
			Actions:  []plugin.Action{callback("open", l.T("action.open"), cbEditor, payload{App: e.ID, Paths: sel})},
		})
	}
	items = append(items,
		plugin.Item{
			ID: "quick-look", Title: l.T("mac.item.quickLook.title"), Subtitle: subtitle, Icon: plugin.Icon{Name: "file"},
			Keywords: keywords(l.T("mac.keywords.quickLook")),
			Actions:  []plugin.Action{callback("preview", l.T("mac.action.preview"), cbQuickLook, payload{Paths: sel})},
		},
		plugin.Item{
			ID: "reveal-selected", Title: l.T("mac.item.reveal.title"), Subtitle: subtitle, Icon: plugin.Icon{Name: "folder"},
			Keywords: keywords(l.T("mac.keywords.reveal")),
			Actions:  []plugin.Action{openAction("reveal", l.T("mac.action.reveal"), sel[0], plugin.OpenOptions{Reveal: true})},
		},
	)
	return append(items, p.fileItems(l, c, subtitle)...)
}

// handleMac carries out the macOS callbacks; ok is false for the others.
// The app is started in the background: the launcher hides at once, and a
// failure is shown as a toast.
func (p *Plugin) handleMac(l plugin.Localizer, target string, pl payload) (plugin.Result, bool) {
	var (
		launch macterm.Launch
		app    string
		err    error
	)
	switch target {
	case cbTerminal:
		terms := macterm.Detect(p.o.AppPath)
		i := slices.IndexFunc(terms, func(t macterm.Installed) bool { return t.ID == pl.App })
		if i < 0 {
			return failure(l.T("mac.toast.openFailed", "app", pl.App), ""), true
		}
		app = terms[i].Name
		launch, err = terms[i].Launch(macterm.Request{Dir: pl.Dir, Tab: pl.Tab, Shell: p.o.Shell})
	case cbEditor:
		e, ok := macterm.EditorByID(pl.App)
		if !ok || len(pl.Paths) == 0 {
			return failure(l.T("mac.toast.openFailed", "app", pl.App), ""), true
		}
		app, launch = e.Name, macterm.OpenWith(e.BundleID, pl.Paths...)
	case cbQuickLook:
		if len(pl.Paths) == 0 {
			return plugin.Result{}, false
		}
		app, launch = l.T("mac.item.quickLook.title"), macterm.QuickLook(pl.Paths...)
	default:
		return plugin.Result{}, false
	}
	if err != nil {
		return plugin.Result{Toast: macFailure(l, app, err)}, true
	}
	p.wg.Add(1)
	p.h.Go(func() {
		defer p.wg.Done()
		if err := p.o.Launch(launch); err != nil {
			p.h.Logger().Warn("files: opening", "app", app, "err", err)
			p.h.Toast(*macFailure(l, app, err))
		}
	})
	return plugin.Result{Hide: true}, true
}

// macFailure is the toast for an app that could not be opened, naming the
// permission to grant when one is missing.
func macFailure(l plugin.Localizer, app string, err error) *plugin.Toast {
	msg := err.Error()
	switch macterm.PermissionProblem(err) {
	case macterm.NeedAutomation:
		msg = l.T("mac.toast.needAutomation", "app", app)
	case macterm.NeedAccessibility:
		msg = l.T("mac.toast.needAccessibility")
	}
	return &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("mac.toast.openFailed", "app", app), Message: msg}
}
