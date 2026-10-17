package ui

// The launcher's search bar.

import (
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/core"
)

// Font sizes of the search bar: its field, and the inputs of the selected
// command's arguments next to it.
const (
	searchFontSize   = 18
	argumentFontSize = 14
)

// lineHeight is the height of a line of text in the theme's font at size.
// MeasureText lays out spans in a paragraph of the theme's font size, whose
// lines are never lower than that size's: it measures a line at that size,
// which scales with the size.
func lineHeight(c *mui.Context, size float32) float32 {
	base := c.Theme().FontSize
	if base <= 0 {
		return 0
	}
	_, h := c.MeasureText(0, mui.Span{Text: "Ag"})
	return h * size / base
}

// searchBarContent is the height of the search bar inside its padding: a
// line of the search field, or the back button deeper in. The bar has
// this height whatever else shows in it (the arguments' inputs, the
// spinner), so the rows under it never move (R74).
func searchBarContent(c *mui.Context, s core.Snapshot) float32 {
	h := lineHeight(c, searchFontSize)
	if s.Depth > 0 {
		h = max(h, 28) // the back button
	}
	return h
}

func (l *Launcher) searchBar(c *mui.Context, s core.Snapshot) {
	t, tk := c.Theme(), &l.res.Tokens
	pad := l.sp(14)
	content := searchBarContent(c, s)
	mui.Row(c).Height(content+2*pad).Padding(pad, 16).Gap(10).AlignItems(mui.Center).Children(func() {
		if s.Depth > 0 {
			back := mui.Box(c).Size(28, 28).Center().Radius(l.rad(6)).Background(tk.Control).Label(l.loc.T("ui.back"))
			if back.Hovered() {
				back.Background(tk.ControlHover)
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
			// MyGo draws placeholders in the theme's muted text color; a
			// theme with a placeholder color of its own gets it drawn here.
			ownPlaceholder := tk.SearchPlaceholder != t.TextMuted
			placeholder := s.Placeholder
			if ownPlaceholder {
				placeholder = ""
			}
			in := mui.TextInputBase(c.Key("search"), &l.text).Bind(&l.input).AutoFocus().Grow(1).FontSize(searchFontSize).TextColor(tk.SearchText).
				Placeholder(placeholder).Label(l.loc.T("ui.search.label")).
				OnTextKey(func(k mui.TextKey) bool { return l.textKey(k, false) })
			if in.Changed() {
				l.setText(l.text, false)
			}
			if ownPlaceholder && l.text == "" {
				mui.Text(c, s.Placeholder).FontSize(searchFontSize).TextColor(tk.SearchPlaceholder).SingleLine().PassThrough().
					AttachTo(in, mui.AnchorLeft, mui.AnchorLeft)
			}
			l.argumentInputs(c, s, in, content)
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
