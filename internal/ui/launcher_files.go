package ui

// Files handed to the launcher (docs/plugin-guide.md "Files"): dropped onto
// its window, pasted with Ctrl+V (⌘V), chosen in the open dialog; the chips
// row of a view; and the local images of Markdown.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// DropFiles hands files dropped onto the launcher's window to the current
// view (core.Controller.ReceiveFiles). It is safe from any goroutine.
func (l *Launcher) DropFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	paths = slices.Clone(paths)
	l.work.Do(func(ctx context.Context) {
		out, err := l.ctrl.ReceiveFiles(ctx, plugin.Files{Paths: paths, Source: plugin.FilesDropped})
		l.apply(out, err)
	})
}

// pasteFiles takes Ctrl+V (or a paste from the Edit menu, OnPaste) when the
// view accepts files and the clipboard holds files or an image that the
// paste takes (core.FilesClipboard): they are pasted into the view instead
// of text into the search field. The clipboard is only probed here, and
// only in a view that accepts files. UI thread only.
func (l *Launcher) pasteFiles() bool {
	probe, ok := l.clip.(core.FilesClipboard)
	if !ok {
		return false
	}
	// The root search takes files too (R97).
	if s := l.ctrl.Snapshot(); !s.AcceptFiles && (s.Depth > 0 || s.Form != nil) || !probe.HasFilesOrImage() {
		return false
	}
	l.work.Do(func(ctx context.Context) {
		out, _, err := l.ctrl.PasteFiles(ctx)
		l.apply(out, err)
	})
	return true
}

// removeChip runs the Remove action of chip id (its × button).
func (l *Launcher) removeChip(id string) {
	l.work.Do(func(ctx context.Context) {
		out, err := l.ctrl.RemoveChip(ctx, id)
		l.apply(out, err)
	})
}

// chipImageSize is the size of a chip's thumbnail or icon, in DIPs.
const chipImageSize = 28

// chips shows the view's chips in a row that scrolls horizontally, between
// the content and the footer. It never changes the search bar.
func (l *Launcher) chips(c *mui.Context, chips []plugin.Chip) {
	mui.ScrollHorizontal(c.Key("chips")).Shrink(0).Gap(6).Padding(l.sp(6), 12).Children(func() {
		for i := range chips {
			l.chip(c, &chips[i])
		}
	})
}

func (l *Launcher) chip(c *mui.Context, ch *plugin.Chip) {
	t, tk := c.Theme(), &l.res.Tokens
	fg, muted, border := t.Text, t.TextMuted, t.Border
	if ch.Error {
		fg, muted, border = t.Danger, t.Danger, t.Danger
	}
	box := mui.Row(c.Key("chip:"+ch.ID)).Gap(8).Padding(4, 6, 4, 4).Radius(l.rad(8)).AlignItems(mui.Center).
		Border(1, border).Background(tk.Control).MaxWidth(240).Shrink(0).Label(ch.Title)
	if ch.Tooltip != "" {
		box.Tooltip(ch.Tooltip)
	}
	box.Children(func() {
		if src := l.chipImage(ch); src != nil {
			mui.Image(c, src).Size(chipImageSize, chipImageSize).Fit(mui.Cover).Radius(l.rad(4))
		} else {
			l.icon(c, ch.Icon, chipImageSize, muted, false)
		}
		mui.Column(c).Shrink(1).MinWidth(0).Children(func() {
			mui.Text(c, ch.Title).FontSize(12).TextColor(fg).SingleLine()
			if ch.Subtitle != "" {
				mui.Text(c, ch.Subtitle).FontSize(11).TextColor(muted).SingleLine()
			}
		})
		if ch.Remove == nil {
			return
		}
		x := mui.Box(c.Key("chip-remove:"+ch.ID)).Size(18, 18).Center().Radius(9).Shrink(0).
			Label(l.loc.T("ui.chip.remove", "name", ch.Title))
		if x.Hovered() {
			x.Background(tk.ControlHover)
		}
		if x.Clicked() {
			l.removeChip(ch.ID)
			l.input.Focus()
		}
		x.Children(func() {
			mui.Icon(c, builtinIcon("x")).FontSize(12).TextColor(muted)
		})
	})
}

// chipImage is the chip's thumbnail once loaded, else nil.
func (l *Launcher) chipImage(ch *plugin.Chip) mui.ImageSource {
	if ch.Image == "" {
		return nil
	}
	return l.images.get(ch.Image)
}

// maxMarkdownImage bounds the image files Markdown shows.
const maxMarkdownImage = 4 << 20

// errNotLocalImage is returned for Markdown images that are not local
// image files.
var errNotLocalImage = errors.New("ui: not a local image file")

// localImage is the launcher's Markdown LoadImage: only image files (PNG,
// JPEG, GIF, WebP, BMP) of at most 4 MB at absolute local paths, optionally
// as a file: URL. No network address, no data: URL, no relative path (the
// Markdown comes from plugins and AI answers).
func localImage(dest string) (mui.ImageSource, error) {
	p, ok := localImagePath(dest)
	if !ok {
		return nil, errNotLocalImage
	}
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxMarkdownImage {
		return nil, errNotLocalImage
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return mui.DecodeBitmap(data)
}

// localImagePath is the path of a Markdown image destination when it names
// a local image file by an absolute path.
func localImagePath(dest string) (string, bool) {
	p := strings.TrimSpace(dest)
	if strings.HasPrefix(p, "<") && strings.HasSuffix(p, ">") {
		p = p[1 : len(p)-1]
	}
	if rest, ok := strings.CutPrefix(p, "file:"); ok {
		p = rest
		if rest, ok := strings.CutPrefix(p, "//"); ok {
			p = rest // file:///C:/x or file:///Users/x: an empty host
			if !strings.HasPrefix(p, "/") {
				return "", false // a host: not local
			}
		}
		if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:] // /C:/x
		}
	}
	if strings.Contains(p, "://") || strings.HasPrefix(strings.ToLower(p), "data:") {
		return "", false
	}
	p = filepath.FromSlash(p)
	if !filepath.IsAbs(p) || strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//") {
		// Relative, or a network share (\\server\share).
		return "", false
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return p, true
	}
	return "", false
}
