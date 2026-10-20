package ui

// The launcher's results list and detail pane.

import (
	"math"
	"strconv"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// launcherRow is a row of the results list: a section header or an entry.
type launcherRow struct {
	header string
	entry  *core.Entry
	flat   int // index among the entries, as Snapshot.Selected counts
	// key identifies the row in the list; an entry may show in two sections
	// (Recent and Commands).
	key string
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
	keys := make([]string, len(l.rows))
	for i, r := range l.rows {
		keys[i] = r.key
	}
	sig := strings.Join(keys, "\x00")
	changed := sig != l.lastRows
	if changed {
		// Rows moved under a resting pointer (e.g. the context section
		// arrived above): hovering selects again only after it moves.
		l.lastRows, l.armed = sig, false
	}
	selKey := ""
	if e, ok := s.SelectedEntry(); ok {
		selKey = e.Key
	}
	// Compare keys, not indexes: the selected index can stay 0 while the
	// row at 0 changes.
	if selKey != l.lastSel || changed {
		l.lastSel = selKey
		if s.Selected == 0 {
			l.list.ScrollTo(0, mui.Start) // the first section's header too
		} else if r, ok := l.rowOf[s.Selected]; ok {
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
	t, tk := c.Theme(), &l.res.Tokens
	if r.entry == nil {
		l.shadowed(mui.Text(c, r.header).FontSize(11).Bold().TextColor(tk.SectionHeader).Padding(l.sp(8), 10, l.sp(4), 10).Background(tk.SectionBackground).SingleLine(), false)
		return
	}
	e := r.entry
	selected := r.flat == s.Selected
	text, muted := t.Text, t.TextMuted
	row := mui.Row(c).Padding(l.sp(6), 10).Gap(12).AlignItems(mui.Center).Radius(l.rad(8))
	if selected {
		row.Background(tk.RowSelected)
		text, muted = tk.RowSelectedText, tk.RowSelectedMuted
	} else if row.Hovered() {
		row.Background(tk.RowHover)
	}
	if row.Hovered() && r.flat != l.hoverRow {
		// The first row under the pointer after showing does not take the
		// choice: the pointer may just be resting there.
		if l.armed && !selected {
			l.hoverRowSelect(r.flat)
		}
		l.hoverRow, l.armed = r.flat, true
	}
	if row.Clicked() {
		l.activate(r.flat)
		l.input.Focus()
	}
	row.Children(func() {
		l.icon(c, e.Icon, rowIconSize, text, selected && !tk.TintSelected)
		mui.Row(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).AlignItems(mui.Center).ClipX().Children(func() {
			l.shadowed(mui.Text(c, e.Title).TextColor(text).SingleLine().Shrink(0), selected)
			if e.Subtitle != "" {
				l.shadowed(mui.Text(c, e.Subtitle).TextColor(muted).SingleLine().Shrink(1).MinWidth(0), selected)
			}
		})
		if e.Accessory != "" {
			l.shadowed(mui.Text(c, e.Accessory).FontSize(12).TextColor(muted).SingleLine().Shrink(0), selected)
		}
		for i := range e.Accessories {
			l.accessory(c, &e.Accessories[i], muted, selected)
		}
	})
}

// Sizes of an Item.Accessories value: fixed, so that values updated every
// second (a process's CPU use) do not move the row.
const (
	accessoryMeterWidth = 40
	accessoryTextWidth  = 56
)

// accessory shows one of an item's Accessories: an optional small bar, in
// the theme's Success color or, as a load, its Warning and Danger colors,
// then its text right-aligned in a fixed minimum width.
func (l *Launcher) accessory(c *mui.Context, a *plugin.Accessory, muted mui.Color, selected bool) {
	if a.Meter == nil && a.Text == "" {
		return
	}
	mui.Row(c).Gap(6).AlignItems(mui.Center).Shrink(0).Children(func() {
		if a.Meter != nil {
			var levels *mui.MeterLevels
			if a.Load {
				levels = &mui.MeterLevels{Warning: 0.5, Critical: 0.8}
			}
			mui.Meter(c, *a.Meter, 0, 1, levels).Width(accessoryMeterWidth).MinWidth(accessoryMeterWidth).Shrink(0)
		}
		if a.Text != "" {
			l.shadowed(mui.Text(c, a.Text).FontSize(12).TextColor(muted).SingleLine().MinWidth(accessoryTextWidth).TextAlign(mui.End).Shrink(0), selected)
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
				l.md.View(c, d.Markdown)
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

// fullDetail shows a plugin.ViewDetail across the window: its Markdown and
// metadata, following the end as the content grows (a chat answer being
// written) unless the user scrolled up.
func (l *Launcher) fullDetail(c *mui.Context, s core.Snapshot) {
	t := c.Theme()
	if sig := strconv.Itoa(s.Depth) + "\x00" + s.Title; sig != l.detailSig {
		l.detailSig = sig
		l.detailScroll.Y = math.MaxFloat32 // a new view starts at its end
	} else if l.detailScroll.Y >= l.detailScroll.MaxY-1 {
		l.detailScroll.Y = math.MaxFloat32 // at the end: keep following it
	}
	mui.Scroll(c.Key("detail-full")).TrackScroll(&l.detailScroll).Grow(1).Basis(0).Padding(14, 18).Children(func() {
		d := s.Detail
		if d == nil {
			return
		}
		mui.Column(c).Gap(10).Children(func() {
			if d.Markdown != "" {
				l.md.View(c, d.Markdown)
			}
			if len(d.Metadata) > 0 {
				mui.Row(c).Gap(12).Children(func() {
					for _, m := range d.Metadata {
						mui.Text(c, m.Label+" "+m.Value).FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(1)
					}
				})
			}
		})
	})
}
