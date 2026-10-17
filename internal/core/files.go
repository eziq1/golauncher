package core

// Files handed to the launcher (plugin.FileReceiver, docs/plugin-guide.md
// "Files"): dropped onto its window, pasted with Ctrl+V while the clipboard
// holds files or an image, or chosen in the open dialog of
// plugin.ActionChooseFiles; and the chips of a view (plugin.View.Chips).

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// FileChooser shows the system's open dialog and returns the files chosen,
// none when the user cancels. It blocks until the dialog closes.
type FileChooser func(ctx context.Context, o plugin.ChooseFilesOptions) ([]string, error)

// FilesClipboard is implemented by clipboards that can tell cheaply whether
// they hold files or an image that a paste takes (R96): files, or an image
// whose accompanying text, if any, only describes it (clip.DescribesImage:
// blank, its address, "[图片]"). Front ends ask HasFilesOrImage on Ctrl+V
// in a view that accepts files, and leave the key to the text field
// otherwise; Controller.PasteFiles then reads with ReadPaste, which returns
// the image only when it is to be taken, so both agree.
type FilesClipboard interface {
	HasFilesOrImage() bool
	ReadPaste() (plugin.ClipboardContent, error)
}

// ErrChooseFilesHere is returned for an ActionChooseFiles that does not run
// from a view accepting files, through the Controller (e.g. from an AI
// tool): only the user's own action shows the open dialog.
var ErrChooseFilesHere = errors.New("core: files can only be chosen in a view that accepts them")

// errNoFileChooser is returned for ActionChooseFiles without a FileChooser.
var errNoFileChooser = errors.New("core: no open dialog")

// SetFileChooser sets the open dialog of ActionChooseFiles. Without one the
// action fails.
func (e *Engine) SetFileChooser(fn FileChooser) {
	e.chooserMu.Lock()
	e.chooser = fn
	e.chooserMu.Unlock()
}

func (e *Engine) fileChooser() FileChooser {
	e.chooserMu.Lock()
	defer e.chooserMu.Unlock()
	return e.chooser
}

// receiveFiles hands f to plugin pluginID. A plugin that is off or does not
// implement FileReceiver does not take them (ErrFilesNotTaken).
func (e *Engine) receiveFiles(ctx context.Context, pluginID string, f plugin.Files) (Outcome, error) {
	l, err := e.running(pluginID)
	if err != nil {
		return Outcome{}, err
	}
	fr, ok := l.p.(plugin.FileReceiver)
	if !ok {
		return Outcome{}, plugin.ErrFilesNotTaken
	}
	r, err := callPlugin(ctx, e, pluginID, actionTimeout, func(ctx context.Context) (plugin.Result, error) { return fr.ReceiveFiles(ctx, f) })
	if err != nil {
		return Outcome{}, err
	}
	return e.result(ctx, l, r)
}

// accepting returns the top frame's plugin and request when its view
// accepts files. c.mu must be held.
func (c *Controller) accepting() (*Nav, bool) {
	f := c.top()
	if f.nav == nil || f.view == nil || !f.view.AcceptFiles {
		return nil, false
	}
	return f.nav, true
}

// ReceiveFiles hands files to the current view: to its plugin when it
// accepts files (plugin.View.AcceptFiles), with its instance and search
// text; in the root search to its Files section (receiveRoot, R97).
// Anywhere else, or when the plugin does not take them, a toast says they
// cannot be used here. The plugin's Result is applied as an action's.
func (c *Controller) ReceiveFiles(ctx context.Context, f plugin.Files) (Outcome, error) {
	if len(f.Paths) == 0 && len(f.Image) == 0 {
		return Outcome{}, nil
	}
	c.waitSearches()
	c.mu.Lock()
	f.Query = c.top().text
	if len(c.stack) == 1 {
		c.mu.Unlock()
		return c.receiveRoot(ctx, f)
	}
	var targets []string // none: a view that takes no files
	if nav, ok := c.accepting(); ok {
		f.Instance, f.View = nav.Request.Instance, nav.Request.ID
		targets = []string{nav.Plugin}
	}
	c.mu.Unlock()
	for _, id := range targets {
		out, err := c.eng.receiveFiles(ctx, id, f)
		if errors.Is(err, plugin.ErrFilesNotTaken) {
			continue
		}
		c.eng.log.Info("files received", "plugin", id, "source", f.Source, "files", len(f.Paths), "image", len(f.Image) > 0)
		return c.applyOutcome(ctx, out, err)
	}
	t := &plugin.Toast{Style: plugin.ToastFailure, Title: c.eng.core.T("toast.filesNotTaken")}
	c.ShowToast(*t)
	return Outcome{Toast: t}, nil
}

// PasteFiles pastes files or an image from the clipboard into the current
// view, when it accepts files (Ctrl+V): the files, else a PNG image, go to
// ReceiveFiles. It reports false, without reading the clipboard, in any
// other view, and false when the clipboard holds neither (text): the front
// end pastes it into the search field then.
func (c *Controller) PasteFiles(ctx context.Context) (Outcome, bool, error) {
	c.mu.Lock()
	_, ok := c.accepting()
	ok = ok || len(c.stack) == 1 // the root search takes files too (R97)
	c.mu.Unlock()
	cb := c.eng.svc.Clipboard
	if !ok || cb == nil {
		return Outcome{}, false, nil
	}
	read, decides := cb.Read, false
	if fc, ok := cb.(FilesClipboard); ok {
		read, decides = fc.ReadPaste, true
	}
	content, err := read()
	if err != nil {
		return Outcome{}, false, err
	}
	f := plugin.Files{Source: plugin.FilesPasted}
	switch {
	case content.Exclude:
		return Outcome{}, false, nil
	case len(content.Files) > 0:
		f.Paths = content.Files
	case len(content.Image) > 0 && (decides || strings.TrimSpace(content.Text) == ""):
		// ReadPaste returns the image only when it wins over the text next
		// to it; a plain Read, only without text.
		f.Image = content.Image
	default:
		return Outcome{}, false, nil
	}
	out, err := c.ReceiveFiles(ctx, f)
	return out, true, err
}

// chooseFiles runs ActionChooseFiles: the open dialog, then the files chosen
// go to the view the action ran from, which must accept files, or to the
// root search when it ran from one of its items (R97).
func (c *Controller) chooseFiles(ctx context.Context, entry Entry, action plugin.Action) (Outcome, error) {
	c.mu.Lock()
	nav, ok := c.accepting()
	root := len(c.stack) == 1
	c.mu.Unlock()
	if !root && (!ok || nav.Plugin != entry.Plugin) {
		return c.applyOutcome(ctx, Outcome{}, ErrChooseFilesHere)
	}
	var o plugin.ChooseFilesOptions
	if len(action.Do.Payload) > 0 {
		if err := json.Unmarshal(action.Do.Payload, &o); err != nil {
			return c.applyOutcome(ctx, Outcome{}, fmt.Errorf("core: ChooseFiles payload: %w", err))
		}
	}
	return c.choose(ctx, o)
}

// ChooseRootFiles shows the open dialog for the root search (Ctrl+O there):
// the files chosen show in its Files section.
func (c *Controller) ChooseRootFiles(ctx context.Context) (Outcome, error) {
	return c.choose(ctx, plugin.ChooseFilesOptions{Title: c.eng.core.T("files.chooseTitle"), Multiple: true})
}

// choose shows the open dialog with o and hands the files chosen to the
// current view (ReceiveFiles).
func (c *Controller) choose(ctx context.Context, o plugin.ChooseFilesOptions) (Outcome, error) {
	choose := c.eng.fileChooser()
	if choose == nil {
		return c.applyOutcome(ctx, Outcome{}, errNoFileChooser)
	}
	paths, err := choose(ctx, o)
	if err != nil {
		return c.applyOutcome(ctx, Outcome{}, err)
	}
	if len(paths) == 0 {
		return Outcome{}, nil // cancelled
	}
	if !o.Multiple {
		paths = paths[:1]
	}
	return c.ReceiveFiles(ctx, plugin.Files{Paths: paths, Source: plugin.FilesChosen})
}

// RemoveChip runs the Remove action of the current view's chip with id (its
// × button), as an action of the view's plugin.
func (c *Controller) RemoveChip(ctx context.Context, id string) (Outcome, error) {
	c.waitSearches()
	c.mu.Lock()
	if len(c.stack) == 1 {
		// A file handed to the root search (rootfiles.go).
		c.mu.Unlock()
		return c.removeRootFile(ctx, id)
	}
	f := c.top()
	var (
		remove   *plugin.Action
		pluginID string
	)
	if f.nav != nil && f.view != nil {
		pluginID = f.nav.Plugin
		for _, ch := range f.view.Chips {
			if ch.ID == id {
				remove = ch.Remove
				break
			}
		}
	}
	c.mu.Unlock()
	if remove == nil {
		return Outcome{}, errNoChip(id)
	}
	return c.run(ctx, Entry{Plugin: pluginID}, *remove)
}

// errNoChip is RemoveChip's error for an unknown chip.
func errNoChip(id string) error {
	return fmt.Errorf("core: no chip %q to remove in the current view", id)
}
