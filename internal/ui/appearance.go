package ui

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/theme"
)

// Thumbnail size in the theme gallery, in DIPs.
const (
	thumbWidth  = 156
	thumbHeight = 98
)

// appearanceState is the state of the Appearance page.
type appearanceState struct {
	opacity, windowOpacity, radius float64 // slider values while dragged
	dragging                       string  // the slider being dragged, "" for none
	paste                          string  // theme JSON being pasted
	pasting                        bool
	custom                         mui.Color
	thumbs                         map[string]*theme.Resolved
	thumbKey                       thumbKey
}

// thumbKey is what the thumbnails were resolved for.
type thumbKey struct {
	gen  uint64
	sys  theme.System
	lang string
}

// systemFor is the state of the system for the settings window c, which
// shows no material itself: the launcher reports whether its window does.
func (s *Settings) systemFor(c *mui.Context) theme.System {
	sys := systemOf(c)
	sys.NoMaterial = s.env.Themes.NoMaterial()
	return sys
}

// resolved is the appearance the launcher has now, as the settings see it.
func (s *Settings) resolved(c *mui.Context, cfg config.Config) *theme.Resolved {
	return s.env.Themes.Resolve(cfg.Appearance, s.systemFor(c))
}

// setAppearance changes the appearance, and the old theme setting that
// older versions read (config.LegacyTheme).
func (s *Settings) setAppearance(fn func(a *config.Appearance)) {
	lib := s.env.Themes.Library()
	s.update(func(c *config.Config) {
		fn(&c.Appearance)
		dark := false
		if t, ok := lib.Get(c.Appearance.Theme); ok {
			dark = t.Dark
		}
		c.Theme = config.LegacyTheme(c.Appearance, dark)
	})
}

// preview shows a changed appearance in the launcher without saving it,
// while a slider is dragged; done saves it.
func (s *Settings) preview(a config.Appearance, done bool) {
	if done {
		s.env.Themes.SetPreview(nil)
		s.setAppearance(func(cur *config.Appearance) { *cur = a })
		return
	}
	s.env.Themes.SetPreview(&a)
	s.host.PreviewLauncher()
}

func (s *Settings) appearancePage(c *mui.Context, cfg config.Config) {
	t := c.Theme()
	a := cfg.Appearance
	r := s.resolved(c, cfg)
	s.heading(c, s.loc.T("settings.nav.appearance"))
	if r.Missing {
		mui.Text(c, s.loc.T("settings.appearance.missing", "id", r.Requested, "name", s.themeName(r.Theme))).FontSize(12).TextColor(t.Danger)
	}

	// The gallery.
	s.gallery(c, a, r)
	s.toggle(c, s.loc.T("settings.appearance.followSystem"), s.loc.T("settings.appearance.followSystem.desc"), a.FollowSystem, func(v bool) {
		s.setAppearance(func(a *config.Appearance) {
			a.FollowSystem = v
			if !v {
				a.Theme = r.Theme.ID // keep what shows now
			}
		})
	})
	if a.FollowSystem {
		s.themeSelect(c, "light", s.loc.T("settings.appearance.lightTheme"), a.LightTheme, func(t *theme.Theme) bool { return !t.Dark },
			func(id string) { s.setAppearance(func(a *config.Appearance) { a.LightTheme = id }) })
		s.themeSelect(c, "dark", s.loc.T("settings.appearance.darkTheme"), a.DarkTheme, func(t *theme.Theme) bool { return t.Dark },
			func(id string) { s.setAppearance(func(a *config.Appearance) { a.DarkTheme = id }) })
	} else {
		s.themeSelect(c, "fixed", s.loc.T("settings.appearance.theme"), a.Theme, func(*theme.Theme) bool { return true },
			func(id string) { s.setAppearance(func(a *config.Appearance) { a.Theme = id }) })
	}

	// Background.
	s.subheading(c, s.loc.T("settings.appearance.background"))
	materials := []theme.Material{theme.MaterialAcrylic, theme.MaterialMica, theme.MaterialNone}
	s.setting(c, s.loc.T("settings.appearance.material"), s.loc.T("settings.appearance.material.desc"), func() {
		i := max(0, slices.Index(materials, r.Material))
		if mui.Segmented(c, &i, s.loc.T("settings.material.acrylic"), s.loc.T("settings.material.mica"), s.loc.T("settings.material.none")).
			Label(s.loc.T("settings.appearance.material")).Changed() {
			m := string(materials[i])
			s.setAppearance(func(a *config.Appearance) { a.Material = &m })
			s.host.PreviewLauncher()
		}
	})
	disabled := r.Opaque != ""
	s.slider(c, "opacity", s.loc.T("settings.appearance.opacity"), s.loc.T("settings.appearance.opacity.desc"), r.ThemeOpacity, 0, 1, 0.01, disabled,
		func(v float64) config.Appearance { n := a.Clone(); n.Opacity = &v; return n })
	if disabled {
		mui.Text(c, s.opaqueReason(r.Opaque)).FontSize(12).TextColor(t.TextMuted)
	}
	// What the adjustments make hard to read; what the theme itself does
	// not pass is reported with the theme's problems, or known.
	base := theme.CheckContrast(r.Theme)
	for i, res := range r.Contrast {
		if !res.OK && base[i].OK {
			mui.Text(c, s.loc.T("settings.appearance.lowContrast", "pair", res.Pair, "ratio", fmt.Sprintf("%.1f", res.Ratio), "min", fmt.Sprintf("%.1f", res.Min))).
				FontSize(12).TextColor(t.Warning)
		}
	}

	// Accent.
	s.accentSetting(c, a)

	// Layout.
	s.subheading(c, s.loc.T("settings.appearance.layout"))
	s.slider(c, "radius", s.loc.T("settings.appearance.radius"), "", float64(r.UI.Radius), theme.MinRadius, theme.MaxRadius, 1, false,
		func(v float64) config.Appearance { n := a.Clone(); n.Radius = &v; return n })
	densities := []theme.Density{theme.DensityCompact, theme.DensityStandard, theme.DensityComfortable}
	s.setting(c, s.loc.T("settings.appearance.density"), "", func() {
		i := max(0, slices.IndexFunc(densities, func(d theme.Density) bool { return d.Spacing() == r.UI.Spacing }))
		if mui.Segmented(c, &i, s.loc.T("settings.density.compact"), s.loc.T("settings.density.standard"), s.loc.T("settings.density.comfortable")).
			Label(s.loc.T("settings.appearance.density")).Changed() {
			d := string(densities[i])
			s.setAppearance(func(a *config.Appearance) { a.Density = &d })
			s.host.PreviewLauncher()
		}
	})
	s.setting(c, s.loc.T("settings.appearance.fontSize"), "", func() {
		options := []string{s.loc.T("settings.appearance.fontSize.system")}
		for n := theme.MinFontSize; n <= theme.MaxFontSize; n++ {
			options = append(options, strconv.Itoa(n))
		}
		chosen := options[0]
		if a.FontSize != nil {
			chosen = strconv.Itoa(int(*a.FontSize))
		}
		if mui.Select(c, &chosen, options).Label(s.loc.T("settings.appearance.fontSize")).Changed() {
			s.setAppearance(func(a *config.Appearance) {
				a.FontSize = nil
				if n, err := strconv.Atoi(chosen); err == nil {
					v := float64(n)
					a.FontSize = &v
				}
			})
			s.host.PreviewLauncher()
		}
	})
	s.setting(c, s.loc.T("settings.appearance.reset"), s.loc.T("settings.appearance.reset.desc"), func() {
		if button(c, s.loc.T("settings.appearance.resetButton")) {
			s.setAppearance(func(a *config.Appearance) { a.ResetToTheme() })
			s.host.PreviewLauncher()
		}
	})

	// Advanced.
	s.subheading(c, s.loc.T("settings.appearance.advanced"))
	s.slider(c, "windowOpacity", s.loc.T("settings.appearance.windowOpacity"), s.loc.T("settings.appearance.windowOpacity.desc"),
		r.WindowOpacity, config.MinWindowOpacity, config.MaxWindowOpacity, 0.01, false,
		func(v float64) config.Appearance { n := a.Clone(); n.WindowOpacity = v; return n })
	s.toggle(c, s.loc.T("settings.appearance.textShadow"), s.loc.T("settings.appearance.textShadow.desc"), a.TextShadow, func(v bool) {
		s.setAppearance(func(a *config.Appearance) { a.TextShadow = v })
	})

	s.customThemes(c, cfg, r)
	s.noticeView(c)
}

// opaqueReason says why the background cannot be translucent.
func (s *Settings) opaqueReason(r theme.OpaqueReason) string {
	switch r {
	case theme.OpaqueHighContrast:
		return s.loc.T("settings.appearance.opaque.highContrast")
	case theme.OpaqueTransparencyOff:
		return s.loc.T("settings.appearance.opaque.transparencyOff")
	case theme.OpaqueUnsupported:
		return s.loc.T("settings.appearance.opaque.unsupported")
	}
	return s.loc.T("settings.appearance.opaque.materialNone")
}

// themeName is the name of t in the UI language.
func (s *Settings) themeName(t *theme.Theme) string { return t.DisplayName(s.env.I18n.Locale()) }

// themeSelect chooses one of the themes keep allows.
func (s *Settings) themeSelect(c *mui.Context, key, label, current string, keep func(*theme.Theme) bool, set func(id string)) {
	var ids, names []string
	for _, t := range s.env.Themes.Library().Themes() {
		if keep(t) || t.ID == current {
			ids, names = append(ids, t.ID), append(names, s.themeName(t))
		}
	}
	s.setting(c, label, "", func() {
		chosen := ""
		if i := slices.Index(ids, current); i >= 0 {
			chosen = names[i]
		}
		if mui.Select(c.Key("theme-select:"+key), &chosen, names).Label(label).Changed() {
			if i := slices.Index(names, chosen); i >= 0 {
				set(ids[i])
				s.host.PreviewLauncher()
			}
		}
	})
}

// slider is a setting with a slider whose value shows as a percentage (or
// a number when step is 1). Dragging it previews the change in the
// launcher; letting go saves it.
func (s *Settings) slider(c *mui.Context, key, label, desc string, value, lo, hi, step float64, disabled bool, with func(float64) config.Appearance) {
	st := &s.appearance
	v := value
	held := map[string]*float64{"opacity": &st.opacity, "windowOpacity": &st.windowOpacity, "radius": &st.radius}[key]
	if st.dragging == key {
		v = *held
	}
	s.setting(c, label, desc, func() {
		mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
			text := strconv.Itoa(int(math.Round(v)))
			if step < 1 {
				text = strconv.Itoa(int(math.Round(v*100))) + "%"
			}
			sl := mui.StepSlider(c.Key("slider:"+key), &v, lo, hi, step).Width(200).Label(label).Disabled(disabled)
			mui.Text(c, text).FontSize(12).Width(40).TextAlign(mui.End)
			adjusting := sl.Dragging() || sl.Pressed()
			switch {
			case sl.Changed() && adjusting:
				st.dragging, *held = key, v
				s.preview(with(v), false)
			case sl.Changed():
				// The keyboard: each step is saved.
				st.dragging = ""
				s.host.PreviewLauncher()
				s.preview(with(v), true)
			case st.dragging == key && !adjusting:
				st.dragging = ""
				s.preview(with(v), true)
			}
		})
	})
}

// accentSetting chooses the accent color.
func (s *Settings) accentSetting(c *mui.Context, a config.Appearance) {
	t := c.Theme()
	s.subheading(c, s.loc.T("settings.appearance.accent"))
	modes := []string{config.AccentTheme, config.AccentWindows, "custom"}
	mode := a.Accent
	if mode != config.AccentTheme && mode != config.AccentWindows {
		mode = "custom"
	}
	st := &s.appearance
	if mode == "custom" {
		if col, err := theme.ParseColor(a.Accent); err == nil {
			st.custom = col
		}
	}
	if st.custom.A == 0 {
		st.custom = t.Accent
	}
	s.setting(c, s.loc.T("settings.appearance.accent"), "", func() {
		mui.Row(c).Gap(10).AlignItems(mui.Center).Children(func() {
			i := slices.Index(modes, mode)
			if mui.Segmented(c, &i, s.loc.T("settings.accent.theme"), s.loc.T("settings.accent.windows"), s.loc.T("settings.accent.custom")).
				Label(s.loc.T("settings.appearance.accent")).Changed() {
				v := modes[i]
				if v == "custom" {
					v = theme.FormatColor(opaqueColor(st.custom))
				}
				s.setAppearance(func(a *config.Appearance) { a.Accent = v })
				s.host.PreviewLauncher()
			}
			if mode == "custom" {
				col := st.custom
				if mui.ColorWell(c, &col).Label(s.loc.T("settings.accent.custom")).Changed() {
					st.custom = col
					v := theme.FormatColor(opaqueColor(col))
					s.setAppearance(func(a *config.Appearance) { a.Accent = v })
					s.host.PreviewLauncher()
				}
			}
		})
	})
	if mode == config.AccentWindows && c.Preferences().Accent.A == 0 {
		mui.Text(c, s.loc.T("settings.appearance.accent.noWindows")).FontSize(12).TextColor(t.TextMuted)
	}
}

func opaqueColor(c mui.Color) mui.Color {
	c.A = 255
	return c
}

// gallery shows a thumbnail of every theme; a click uses it.
func (s *Settings) gallery(c *mui.Context, a config.Appearance, r *theme.Resolved) {
	t := c.Theme()
	themes := s.env.Themes.Library().Themes()
	s.resolveThumbs(c, themes)
	current := map[string]bool{}
	if a.FollowSystem {
		current[a.LightTheme], current[a.DarkTheme] = true, true
	} else {
		current[a.Theme] = true
	}
	mui.Row(c.Key("gallery")).Wrap().Gap(12).Children(func() {
		for _, th := range themes {
			name := s.themeName(th)
			tip := name
			if th.Author != "" {
				tip = s.loc.T("settings.appearance.byAuthor", "name", name, "author", th.Author)
			}
			card := mui.Column(c.Key("thumb:" + th.ID)).Gap(4).Padding(3).Radius(10).Cursor(mui.CursorPointer).Label(name).Tooltip(tip)
			switch {
			case th.ID == r.Theme.ID:
				card.Border(2, t.Accent)
			case current[th.ID]:
				card.Border(2, t.Accent.Alpha(0.5))
			case card.Hovered():
				card.Border(2, t.Border)
			default:
				card.Border(2, mui.Transparent)
			}
			if card.Clicked() {
				id, dark := th.ID, th.Dark
				s.setAppearance(func(a *config.Appearance) {
					switch {
					case !a.FollowSystem:
						a.Theme = id
					case dark:
						a.DarkTheme = id
					default:
						a.LightTheme = id
					}
				})
				s.host.PreviewLauncher()
			}
			card.Children(func() {
				themeThumbnail(c, s.appearance.thumbs[th.ID], thumbWidth, thumbHeight)
				mui.Text(c, name).FontSize(12).SingleLine().MaxWidth(thumbWidth).PaddingX(2)
			})
		}
	})
}

// resolveThumbs resolves every theme as it comes, for its thumbnail, once
// per change of the themes or the system.
func (s *Settings) resolveThumbs(c *mui.Context, themes []*theme.Theme) {
	st := &s.appearance
	sys := s.systemFor(c)
	sys.NoMaterial, sys.ReduceTransparency, sys.TextScale = false, false, 0 // the theme itself
	key := thumbKey{gen: s.env.Themes.Library().Generation(), sys: sys, lang: s.env.I18n.Locale()}
	if st.thumbs != nil && key == st.thumbKey {
		return
	}
	st.thumbs, st.thumbKey = map[string]*theme.Resolved{}, key
	lib := s.env.Themes.Library()
	for _, th := range themes {
		a := config.DefaultAppearance()
		a.FollowSystem, a.Theme = false, th.ID
		st.thumbs[th.ID] = theme.Resolve(lib, a, sys)
	}
}

// themeThumbnail draws the launcher in theme r at a small size, w by h
// DIPs: a search field, three results with the first selected, and the
// footer, over a stand-in for the desktop and the material.
func themeThumbnail(c *mui.Context, r *theme.Resolved, w, h float32) {
	if r == nil {
		return
	}
	col, tk := r.Colors, &r.Tokens
	// The material, as the contrast checks assume it: a tint of the
	// system's over what is behind the window.
	material := theme.MaterialTint(r.Dark)
	rs := r.RadiusScale
	desk := mui.Box(c).Size(w, h).Radius(8*rs).Clip().Gradient(mui.Hex("#5b8def"), mui.Hex("#f2a65a"), 135)
	desk.Children(func() {
		layer := mui.Box(c).Fill()
		if r.Opaque == "" {
			layer.Background(material)
		}
		layer.Children(func() {
			mui.Column(c).Fill().Background(r.Background).Children(func() {
				// The search field.
				mui.Row(c).Padding(7, 8).Gap(5).AlignItems(mui.Center).Children(func() {
					mui.Icon(c, builtinIcon("search")).FontSize(9).TextColor(col["textMuted"])
					mui.Box(c).Size(46, 4).Radius(2).Background(tk.SearchPlaceholder.Alpha(0.6))
				})
				mui.Box(c).Height(1).Background(tk.Divider)
				mui.Column(c).Grow(1).Padding(3).Gap(1).Children(func() {
					mui.Box(c).Size(22, 3).Margin(2, 4).Radius(1).Background(tk.SectionHeader.Alpha(0.7))
					for i, tint := range []string{"accent", "success", "danger"} {
						bg, fg := mui.Transparent, col["text"]
						if i == 0 {
							bg, fg = tk.RowSelected, tk.RowSelectedText
						}
						icon := tk.Tint(tint, fg)
						if i == 0 && !tk.TintSelected {
							icon = fg
						}
						mui.Row(c).Padding(3, 4).Gap(5).Radius(4 * rs).AlignItems(mui.Center).Background(bg).Children(func() {
							mui.Box(c).Size(9, 9).Radius(2 * rs).Background(tk.Control).Center().Children(func() {
								mui.Box(c).Size(5, 5).Radius(1).Background(icon)
							})
							mui.Box(c).Size(float32(48-10*i), 4).Radius(2).Background(fg)
							mui.Box(c).Grow(1)
							mui.Box(c).Size(16, 3).Radius(1).Background(fg.Alpha(0.5))
						})
					}
				})
				mui.Box(c).Height(1).Background(tk.Divider)
				foot := mui.Row(c).Height(14).Padding(0, 6).Gap(4).AlignItems(mui.Center)
				if tk.Footer.A > 0 {
					foot.Background(tk.Footer)
				}
				foot.Children(func() {
					mui.Box(c).Size(20, 3).Radius(1).Background(col["textMuted"])
					mui.Box(c).Grow(1)
					mui.Box(c).Size(12, 6).Radius(2).Background(col["accent"])
					mui.Box(c).Size(10, 6).Radius(2).Background(tk.Control)
				})
			})
		})
	})
}

// customThemes is the section about theme files: the folder, importing,
// exporting, duplicating, and what is wrong with them.
func (s *Settings) customThemes(c *mui.Context, cfg config.Config, r *theme.Resolved) {
	t := c.Theme()
	lib := s.env.Themes.Library()
	dir := lib.Dir()
	s.subheading(c, s.loc.T("settings.appearance.custom"))
	mui.Text(c, s.loc.T("settings.appearance.custom.desc")).FontSize(12).TextColor(t.TextMuted)
	if dir != "" {
		s.setting(c, s.loc.T("settings.appearance.folder"), dir, func() {
			if button(c, s.loc.T("settings.appearance.openFolder")) {
				s.openThemeFolder()
			}
		})
	}
	mui.Row(c).Gap(8).Wrap().Children(func() {
		if dir != "" && button(c, s.loc.T("settings.appearance.importFile")) {
			s.importThemeFile()
		}
		if dir != "" && button(c, s.loc.T("settings.appearance.paste")) {
			s.appearance.pasting = !s.appearance.pasting
		}
		if button(c, s.loc.T("settings.appearance.export")) {
			s.exportTheme(c, cfg)
		}
		if dir != "" && button(c, s.loc.T("settings.appearance.duplicate")) {
			s.duplicateTheme(r.Theme)
		}
	})
	if s.appearance.pasting {
		mui.Column(c).Gap(8).Children(func() {
			mui.TextArea(c, &s.appearance.paste).Lines(4, 10).Label(s.loc.T("settings.appearance.pasteLabel")).
				Placeholder(s.loc.T("settings.appearance.pasteLabel"))
			if mui.Button(c, s.loc.T("settings.appearance.pasteImport")).AlignSelf(mui.Start).Disabled(strings.TrimSpace(s.appearance.paste) == "").Clicked() {
				if s.importTheme([]byte(s.appearance.paste), "") {
					s.appearance.paste, s.appearance.pasting = "", false
				}
			}
		})
	}
	problems := lib.Problems()
	if len(problems) == 0 {
		return
	}
	mui.Text(c, s.loc.T("settings.appearance.problems")).FontSize(13).Bold().MarginY(6).Level(3)
	for _, p := range problems {
		switch {
		case p.Error != "" && p.Stale:
			mui.Text(c, s.loc.T("settings.appearance.problemStale", "id", p.ID, "err", p.Error)).FontSize(12).TextColor(t.Danger)
		case p.Error != "":
			mui.Text(c, s.loc.T("settings.appearance.problemError", "id", p.ID, "err", p.Error)).FontSize(12).TextColor(t.Danger)
		}
		for _, w := range p.Warnings {
			mui.Text(c, s.loc.T("settings.appearance.problemWarning", "id", p.ID, "warning", w)).FontSize(12).TextColor(t.TextMuted)
		}
	}
}

func (s *Settings) openThemeFolder() {
	dir := s.env.Themes.Library().Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.setNotice(err.Error(), true)
		return
	}
	s.open(dir)
}

// importThemeFile asks for a theme file and copies it into the folder.
func (s *Settings) importThemeFile() {
	s.setNotice("", false)
	s.host.ChooseThemeFile(false, "", func(path string, err error) {
		if err == nil && path == "" {
			return // canceled
		}
		var data []byte
		if err == nil {
			data, err = os.ReadFile(path)
		}
		if err != nil {
			s.setNotice(s.loc.T("settings.appearance.importFailed", "err", err.Error()), true)
			return
		}
		s.importTheme(data, theme.IDOf(path))
	})
}

// importTheme checks a theme and writes it into the theme folder as id
// (or a name made from the theme's own). It reports whether it did.
func (s *Settings) importTheme(data []byte, id string) bool {
	s.setNotice("", false)
	lib := s.env.Themes.Library()
	f, _, err := theme.ParseFile(data)
	if err == nil {
		err = os.MkdirAll(lib.Dir(), 0o755)
	}
	if err == nil {
		var path string
		path, err = s.writeNewTheme(cmp.Or(slug(id), slug(f.Name), "theme"), data)
		id = theme.IDOf(path)
	}
	if err == nil {
		err = lib.Reload()
	}
	if err == nil {
		for _, p := range lib.Problems() {
			if p.ID == id && p.Error != "" {
				err = errors.New(p.Error)
			}
		}
	}
	if err != nil {
		s.setNotice(s.loc.T("settings.appearance.importFailed", "err", err.Error()), true)
		return false
	}
	name := id
	if t, ok := lib.Get(id); ok {
		name = s.themeName(t)
	}
	s.setNotice(s.loc.T("settings.appearance.imported", "name", name), false)
	return true
}

// writeNewTheme writes data as a new theme file named after id, with a
// number when a built-in theme or a file has that name, and returns its
// path.
func (s *Settings) writeNewTheme(id string, data []byte) (string, error) {
	dir := s.env.Themes.Library().Dir()
	for n := 1; ; n++ {
		name := id
		if n > 1 {
			name = id + "-" + strconv.Itoa(n)
		}
		path := filepath.Join(dir, name+".json")
		_, builtin := theme.BuiltinSource(name)
		if _, err := os.Stat(path); builtin || err == nil {
			continue
		}
		return path, os.WriteFile(path, data, 0o644)
	}
}

// exportTheme saves the theme in use, with the adjustments of the
// appearance, as a file.
func (s *Settings) exportTheme(c *mui.Context, cfg config.Config) {
	s.setNotice("", false)
	r := s.resolved(c, cfg)
	name := s.themeName(r.Theme)
	f := theme.ExportAppearance(s.env.Themes.Library(), cfg.Appearance, s.systemFor(c), name)
	data, err := f.Encode()
	if err != nil {
		s.setNotice(s.loc.T("settings.appearance.exportFailed", "err", err.Error()), true)
		return
	}
	s.host.ChooseThemeFile(true, cmp.Or(slug(name), "theme")+".json", func(path string, err error) {
		if err == nil && path == "" {
			return
		}
		if err == nil {
			err = os.WriteFile(path, data, 0o644)
		}
		if err != nil {
			s.setNotice(s.loc.T("settings.appearance.exportFailed", "err", err.Error()), true)
			return
		}
		s.setNotice(s.loc.T("settings.appearance.exported", "path", path), false)
	})
}

// duplicateTheme writes an editable copy of t into the theme folder and
// opens it.
func (s *Settings) duplicateTheme(t *theme.Theme) {
	s.setNotice("", false)
	f := theme.ExportTheme(t)
	f.Name = s.loc.T("settings.appearance.copyName", "name", s.themeName(t))
	f.Names = nil
	data, err := f.Encode()
	var path string
	if err == nil {
		if err = os.MkdirAll(s.env.Themes.Library().Dir(), 0o755); err == nil {
			path, err = s.writeNewTheme(t.ID+"-copy", data)
		}
	}
	if err == nil {
		err = s.env.Themes.Library().Reload()
	}
	if err != nil {
		s.setNotice(s.loc.T("settings.appearance.importFailed", "err", err.Error()), true)
		return
	}
	s.setNotice(s.loc.T("settings.appearance.duplicated", "path", path), false)
	s.open(filepath.Dir(path))
}

// slug makes a theme ID of a name: lower case letters and digits joined
// by hyphens.
func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		default:
			dash = true
		}
	}
	return b.String()
}
