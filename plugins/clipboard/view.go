package clipboard

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/eziq1/golauncher/pkg/plugin"
)

const (
	viewLimit     = 200
	maxTitleRunes = 80
)

// View implements plugin.ViewProvider for the "history" view.
func (p *Plugin) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	if req.ID != "history" {
		return plugin.View{}, fmt.Errorf("clipboard: unknown view %q", req.ID)
	}
	h, db, err := p.state()
	if err != nil {
		return plugin.View{}, err
	}
	l := h.I18n()
	query := strings.TrimSpace(req.Query.Text)
	clips, err := db.list(ctx, query, viewLimit)
	if err != nil {
		return plugin.View{}, err
	}
	pinned := plugin.Section{Title: l.T("section.pinned"), Items: []plugin.Item{}}
	recent := plugin.Section{Title: l.T("section.recent"), Items: []plugin.Item{}}
	for _, c := range clips {
		it := p.item(h, c)
		if c.Pinned {
			pinned.Items = append(pinned.Items, it)
		} else {
			recent.Items = append(recent.Items, it)
		}
	}
	v := plugin.View{
		Kind:        plugin.ViewList,
		Title:       l.T("view.title"),
		Placeholder: l.T("view.placeholder"),
		ShowDetail:  true,
		EmptyText:   l.T("view.empty"),
	}
	if query != "" {
		v.EmptyText = l.T("view.noResults")
	}
	if len(pinned.Items) > 0 {
		v.Sections = append(v.Sections, pinned)
	}
	v.Sections = append(v.Sections, recent)
	return v, nil
}

func (p *Plugin) item(h plugin.Host, c clip) plugin.Item {
	l := h.I18n()
	now := h.Now()
	it := plugin.Item{
		ID:        strconv.FormatInt(c.ID, 10),
		Accessory: l.RelativeTime(c.LastUsedAt, now),
		Detail:    &plugin.Detail{},
	}
	var meta []plugin.Metadata
	if c.SourceApp != "" {
		meta = append(meta, plugin.Metadata{Label: l.T("meta.source"), Value: c.SourceApp})
	}
	meta = append(meta, plugin.Metadata{Label: l.T("meta.copied"), Value: l.RelativeTime(c.LastUsedAt, now)})

	var paste, copyDo plugin.ActionDo
	switch c.Kind {
	case kindImage:
		path := filepath.Join(imagesDir(h), c.Image)
		it.Title = l.T("image.title", "width", c.Width, "height", c.Height)
		it.Icon = plugin.Icon{Name: "image"}
		it.Detail.Image = path
		meta = append(meta,
			plugin.Metadata{Label: l.T("meta.dimensions"), Value: l.T("meta.dimensionsValue", "width", c.Width, "height", c.Height)},
			plugin.Metadata{Label: l.T("meta.size"), Value: formatSize(l, c.Size)})
		paste = plugin.ActionDo{Kind: plugin.ActionPaste, Payload: plugin.MustPayload(plugin.PasteContent{ImagePath: path})}
		copyDo = plugin.ActionDo{Kind: plugin.ActionCopyImage, Target: path}
	case kindFiles:
		names := make([]string, len(c.Files))
		var md strings.Builder
		for i, f := range c.Files {
			names[i] = filepath.Base(f)
			md.WriteString("- " + escapeMarkdown(f) + "\n")
		}
		it.Title = truncate(strings.Join(names, ", "), maxTitleRunes)
		it.Icon = plugin.Icon{Name: "file"}
		it.Detail.Markdown = md.String()
		meta = append(meta, plugin.Metadata{Label: l.T("meta.files"), Value: l.Number(float64(len(c.Files)))})
		paste = plugin.ActionDo{Kind: plugin.ActionPaste, Payload: plugin.MustPayload(plugin.PasteContent{Files: c.Files})}
		copyDo = plugin.ActionDo{Kind: plugin.ActionCopyFiles, Payload: plugin.MustPayload(c.Files)}
	default:
		it.Title = textTitle(c.Text)
		it.Icon = plugin.Icon{Name: "text"}
		it.Detail.Markdown = fenced(c.Text)
		meta = append(meta, plugin.Metadata{Label: l.T("meta.characters"), Value: l.Number(float64(utf8.RuneCountInString(c.Text)))})
		paste = plugin.ActionDo{Kind: plugin.ActionPaste, Target: c.Text}
		copyDo = plugin.ActionDo{Kind: plugin.ActionCopyText, Target: c.Text}
	}
	it.Detail.Metadata = meta

	idPayload := plugin.MustPayload(idArgs{ID: c.ID})
	pin := plugin.Action{ID: "pin", Title: l.T("action.pin"), Shortcut: "Ctrl+Shift+P",
		Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "pin", Payload: idPayload}}
	if c.Pinned {
		pin = plugin.Action{ID: "unpin", Title: l.T("action.unpin"), Shortcut: "Ctrl+Shift+P",
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "unpin", Payload: idPayload}}
	}
	it.Actions = []plugin.Action{
		{ID: "paste", Title: l.T("action.paste"), Do: paste},
		{ID: "copy", Title: l.T("action.copy"), Shortcut: "Ctrl+Enter", Do: copyDo},
		pin,
		{ID: "delete", Title: l.T("action.delete"), Shortcut: "Ctrl+X", Style: plugin.StyleDestructive,
			Do: plugin.ActionDo{Kind: plugin.ActionCallback, Target: "delete", Payload: idPayload}},
	}
	return it
}

// idArgs is the payload of the pin, unpin and delete callbacks.
type idArgs struct {
	ID int64 `json:"id"`
}

// HandleAction implements plugin.ActionHandler.
func (p *Plugin) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	h, db, err := p.state()
	if err != nil {
		return plugin.Result{}, err
	}
	l := h.I18n()
	switch {
	case a.Kind == plugin.ActionRunCommand && a.Target == "clear":
		n, images, err := db.clearUnpinned()
		if err != nil {
			return plugin.Result{}, err
		}
		removeImages(h, images)
		return plugin.Result{
			Toast:     &plugin.Toast{Style: plugin.ToastSuccess, Title: l.N("toast.cleared", n)},
			PopToRoot: true,
		}, nil
	case a.Kind == plugin.ActionCallback:
		var args idArgs
		if err := json.Unmarshal(a.Payload, &args); err != nil {
			return plugin.Result{}, fmt.Errorf("clipboard: %s: bad payload: %w", a.Target, err)
		}
		var found bool
		var title string
		switch a.Target {
		case "pin", "unpin":
			found, err = db.setPinned(args.ID, a.Target == "pin")
			title = l.T("toast.unpinned")
			if a.Target == "pin" {
				title = l.T("toast.pinned")
			}
		case "delete":
			var image string
			found, image, err = db.remove(args.ID)
			if image != "" {
				removeImages(h, []string{image})
			}
			title = l.T("toast.deleted")
		default:
			return plugin.Result{}, fmt.Errorf("clipboard: unknown callback %q", a.Target)
		}
		if err != nil {
			return plugin.Result{}, err
		}
		if !found {
			return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastFailure, Title: l.T("toast.notFound")}}, nil
		}
		return plugin.Result{Refresh: true, Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: title}}, nil
	}
	return plugin.Result{}, fmt.Errorf("clipboard: unsupported action %s %q", a.Kind, a.Target)
}

// textTitle is the first non-blank line of s, trimmed and shortened.
func textTitle(s string) string {
	for line := range strings.Lines(s) {
		if line = strings.TrimSpace(line); line != "" {
			return truncate(strings.ReplaceAll(line, "\t", " "), maxTitleRunes)
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// fenced wraps text in a Markdown code block whose fence is longer than any
// backtick run inside it.
func fenced(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	fence := "```"
	for strings.Contains(text, fence) {
		fence += "`"
	}
	return fence + "\n" + strings.TrimSuffix(text, "\n") + "\n" + fence
}

var markdownEscaper = strings.NewReplacer(
	`\`, `\\`, "`", "\\`", `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`, `<`, `\<`, `>`, `\>`, `#`, `\#`,
)

func escapeMarkdown(s string) string { return markdownEscaper.Replace(s) }

// formatSize formats a byte count for the current locale.
func formatSize(l plugin.Localizer, n int64) string {
	round := func(v float64) float64 { return math.Round(v*10) / 10 }
	switch {
	case n < 1024:
		return l.N("size.bytes", int(n))
	case n < 1024*1024:
		return l.T("size.kb", "size", l.Number(round(float64(n)/1024)))
	default:
		return l.T("size.mb", "size", l.Number(round(float64(n)/(1024*1024))))
	}
}
