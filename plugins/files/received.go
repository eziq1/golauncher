package files

import (
	"context"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Files handed to the root search (R97, docs/root-files.md): the plugin
// offers its actions for them in the root's Files section
// (plugin.FileActionsProvider), the same it offers for files selected in
// File Explorer or Finder, and a "Choose Files…" root item that hands files
// to the root search through the open dialog.

var (
	_ plugin.FileActionsProvider = (*Plugin)(nil)
	_ plugin.RootProvider        = (*Plugin)(nil)
)

// FileItems implements plugin.FileActionsProvider: open, open with
// (Windows), reveal, copy the paths or the names, compress to ZIP, the
// checksum of one file and, for one PNG image, copy the image. A pasted
// image (a temporary file) is only opened or copied.
func (p *Plugin) FileItems(_ context.Context, q plugin.FilesQuery) ([]plugin.Item, error) {
	files := q.Files
	if len(files) == 0 {
		return nil, nil
	}
	l := p.h.I18n()
	paths := make([]string, len(files))
	names := make([]string, len(files))
	pasted := false
	for i, f := range files {
		paths[i], names[i] = f.Path, f.Name
		pasted = pasted || f.Pasted
	}
	first := files[0]
	one := len(files) == 1
	subtitle := l.N("subtitle.selected", len(files), "name", first.Name)
	sep := "\r\n"
	if p.o.Mac {
		sep = "\n"
	}
	var items []plugin.Item
	if one {
		items = append(items, plugin.Item{ID: "open", Title: l.T("received.open"), Subtitle: subtitle, Icon: plugin.Icon{Name: "file"},
			Keywords: keywords(l.T("keywords.open")),
			Actions:  []plugin.Action{openAction("open", l.T("action.open"), first.Path, plugin.OpenOptions{})}})
		if !p.o.Mac && !first.Dir {
			items = append(items, plugin.Item{ID: "open-with", Title: l.T("received.openWith"), Subtitle: subtitle, Icon: plugin.Icon{Name: "app"},
				Keywords: keywords(l.T("keywords.openWith")),
				Actions:  []plugin.Action{openAction("open-with", l.T("action.open"), first.Path, plugin.OpenOptions{Verb: "openas"})}})
		}
		if first.MediaType == "image/png" && !first.Dir {
			items = append(items, plugin.Item{ID: "copy-image", Title: l.T("received.copyImage"), Subtitle: subtitle, Icon: plugin.Icon{Name: "image"},
				Keywords: keywords(l.T("keywords.copyImage")),
				Actions:  []plugin.Action{{ID: "copy", Title: l.T("action.copy"), Do: plugin.ActionDo{Kind: plugin.ActionCopyImage, Target: first.Path}}}})
		}
	}
	if pasted {
		// The host's temporary file: nothing to reveal, copy or compress.
		return items, nil
	}
	reveal, revealKeys := l.T("received.reveal"), l.T("keywords.reveal")
	if p.o.Mac {
		reveal, revealKeys = l.T("mac.item.reveal.title"), l.T("mac.keywords.reveal")
	}
	items = append(items,
		plugin.Item{ID: "reveal", Title: reveal, Subtitle: subtitle, Icon: plugin.Icon{Name: "folder"}, Keywords: keywords(revealKeys),
			Actions: []plugin.Action{openAction("reveal", reveal, first.Path, plugin.OpenOptions{Reveal: true})}},
		copySelectedItem(l, paths, subtitle, sep),
		plugin.Item{ID: "copy-name", Title: l.N("received.copyName", len(files)), Subtitle: subtitle, Icon: plugin.Icon{Name: "text"},
			Keywords: keywords(l.T("keywords.copyName")),
			Actions:  []plugin.Action{{ID: "copy", Title: l.T("action.copy"), Do: plugin.ActionDo{Kind: plugin.ActionCopyText, Target: strings.Join(names, sep)}}}},
	)
	items = append(items, p.fileItems(l, plugin.Context{Selected: paths}, subtitle)...)
	return items, nil
}

// RootItems implements plugin.RootProvider: "Choose Files…", which hands
// files chosen in the open dialog to the root search.
func (p *Plugin) RootItems(_ context.Context, q plugin.Query) ([]plugin.Item, error) {
	l := p.h.I18n()
	title, keys := l.T("received.choose.title"), keywords(l.T("keywords.choose"))
	if !startsWord(q.Text, append([]string{title}, keys...)) {
		// Only for what starts its words: a loose match would show it for
		// almost anything.
		return nil, nil
	}
	return []plugin.Item{{
		ID: "choose-files", Title: title, Subtitle: l.T("received.choose.subtitle"), Icon: plugin.Icon{Name: "file"},
		Keywords: keys,
		// The open dialog is the user's: AI tools could not open it anyway.
		Sensitive: true,
		Actions: []plugin.Action{{ID: "choose", Title: l.T("received.choose.action"), Shortcut: "Ctrl+O",
			Do: plugin.ActionDo{Kind: plugin.ActionChooseFiles, Payload: plugin.MustPayload(plugin.ChooseFilesOptions{Title: l.T("received.choose.dialog"), Multiple: true})}}},
	}}, nil
}

// startsWord reports whether text (at least two characters) starts one of
// the names or one of their words, ignoring case.
func startsWord(text string, names []string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if len([]rune(t)) < 2 {
		return false
	}
	for _, n := range names {
		n = strings.ToLower(n)
		if strings.HasPrefix(n, t) {
			return true
		}
		for _, w := range strings.Fields(n) {
			if strings.HasPrefix(w, t) {
				return true
			}
		}
	}
	return false
}
