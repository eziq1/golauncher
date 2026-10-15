package shell

import (
	"errors"
	"os"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	wshell "github.com/eziq1/golauncher/internal/winapi/shell"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// errNoTarget is returned by Paste when no window was active before the
// launcher showed.
var errNoTarget = errors.New("shell: no window to paste into")

// system performs action side effects on the real desktop.
type system struct{ s *Shell }

var _ core.System = system{}

func (system) Open(target string, opts plugin.OpenOptions) error { return wshell.Open(target, opts) }
func (system) CopyText(text string) error                        { return clip.WriteText(text) }
func (system) CopyFiles(paths []string) error                    { return clip.WriteFiles(paths) }

func (system) CopyImage(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return clip.WriteImage(data)
}

// Paste puts the content on the clipboard, hides the launcher (waiting until
// it is hidden) and pastes into the window that was active before it showed.
func (sy system) Paste(text string, content plugin.PasteContent) error {
	var err error
	switch {
	case text != "":
		err = clip.WriteText(text)
	case content.ImagePath != "":
		err = sy.CopyImage(content.ImagePath)
	case len(content.Files) > 0:
		err = clip.WriteFiles(content.Files)
	}
	if err != nil {
		return err
	}
	sy.s.hideLauncher()
	hwnd := sy.s.prevForeground.Load()
	if hwnd == 0 {
		return errNoTarget
	}
	return clip.PasteTo(hwnd)
}

// clipboard is the system clipboard for plugins.
type clipboard struct{}

var _ plugin.Clipboard = clipboard{}

func (clipboard) Read() (plugin.ClipboardContent, error) { return clip.Read() }
func (clipboard) WriteText(text string) error            { return clip.WriteText(text) }
func (clipboard) WriteSecret(text string) error          { return clip.WriteSecret(text) }
func (clipboard) WriteImage(png []byte) error            { return clip.WriteImage(png) }
func (clipboard) WriteFiles(paths []string) error        { return clip.WriteFiles(paths) }
