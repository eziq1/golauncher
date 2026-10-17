package ui

// The General and About pages of the settings window.

import (
	"slices"
	"strconv"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/i18n"
)

func (s *Settings) generalPage(c *mui.Context, cfg config.Config) {
	t := c.Theme()
	s.heading(c, s.loc.T("settings.nav.general"))
	s.setting(c, s.loc.T("settings.general.hotkey"), s.loc.T("settings.general.hotkey.desc"), func() {
		s.hotkeyRecorder(c, "launcher", cfg.Hotkey, func(v string) {
			if v != "" {
				s.hotkeyErr = ""
				s.update(func(c *config.Config) { c.Hotkey = v })
			}
		})
	})
	if s.hotkeyErr != "" {
		mui.Text(c, s.loc.T("settings.general.hotkeyFailed", "hotkey", s.hotkeyErr)).FontSize(12).TextColor(t.Danger)
	}
	s.toggle(c, s.loc.T("settings.general.launchAtLogin"), "", cfg.LaunchAtLogin, func(v bool) {
		s.update(func(c *config.Config) { c.LaunchAtLogin = v })
	})

	s.toggle(c, s.loc.T("settings.general.hideOnBlur"), "", cfg.HideOnBlur, func(v bool) {
		s.update(func(c *config.Config) { c.HideOnBlur = v })
	})
	s.returnToRootSetting(c, cfg)
	s.toggle(c, s.loc.T("settings.general.contextAware"), s.loc.T("settings.general.contextAware.desc"), cfg.ContextAware, func(v bool) {
		s.update(func(c *config.Config) { c.ContextAware = v })
	})
	if locales := s.env.I18n.Available(); len(locales) > 1 {
		s.setting(c, s.loc.T("settings.general.language"), "", func() {
			// Each language is named in itself ("English", "简体中文"), so
			// that it can be found whatever the current language is.
			options := []string{s.loc.T("settings.language.system")}
			for _, loc := range locales {
				options = append(options, i18n.DisplayName(loc))
			}
			chosen := options[0]
			if i := slices.Index(locales, cfg.Language); i >= 0 {
				chosen = options[i+1]
			}
			if mui.Select(c, &chosen, options).Label(s.loc.T("settings.general.language")).Changed() {
				lang := "system"
				if i := slices.Index(options, chosen); i > 0 {
					lang = locales[i-1]
				}
				s.update(func(c *config.Config) { c.Language = lang })
			}
		})
	}
	if cfg.ContextAware {
		s.subheading(c, s.loc.T("settings.selection.heading"))
		s.selectionSettings(c, cfg.Selection)
	}
}

// ClipboardHistoryHost is implemented by a SettingsHost that can tell
// whether Windows keeps a clipboard history (Win+V) or syncs the clipboard
// to the cloud, which the copying method of reading the selected text
// cannot keep out (docs/selected-text.md §2.2).
type ClipboardHistoryHost interface {
	ClipboardHistory() (history, cloud bool)
}

// selectionSettings are the settings of reading the selected text, part of
// context awareness (docs/selected-text.md §8, §12): shown only while
// context awareness is on.
func (s *Settings) selectionSettings(c *mui.Context, sel config.SelectionConfig) {
	t := c.Theme()
	setSel := func(fn func(*config.SelectionConfig)) {
		s.update(func(c *config.Config) { fn(&c.Selection) })
	}
	s.toggle(c, s.loc.T("settings.selection.clipboard"), s.loc.T("settings.selection.clipboard.desc"), sel.Clipboard, func(v bool) {
		setSel(func(sc *config.SelectionConfig) { sc.Clipboard = v })
	})
	if sel.Clipboard {
		if hh, ok := s.host.(ClipboardHistoryHost); ok {
			if history, cloud := hh.ClipboardHistory(); history || cloud {
				mui.Text(c, s.loc.T("settings.selection.historyWarning")).FontSize(12).TextColor(t.Warning)
			}
		}
		s.stringList(c, "selection/clipboardApps", s.loc.T("settings.selection.clipboardApps"), s.loc.T("settings.selection.clipboardApps.desc"),
			sel.ClipboardApps, func(v []string) { setSel(func(sc *config.SelectionConfig) { sc.ClipboardApps = v }) })
	}
	s.setting(c, s.loc.T("settings.selection.hotkey"), s.loc.T("settings.selection.hotkey.desc"), func() {
		s.hotkeyRecorder(c, "selection", sel.Hotkey, func(v string) {
			setSel(func(sc *config.SelectionConfig) { sc.Hotkey = v })
		})
	})
	s.stringList(c, "selection/excludeApps", s.loc.T("settings.selection.excludeApps"), s.loc.T("settings.selection.excludeApps.desc"),
		sel.ExcludeApps, func(v []string) { setSel(func(sc *config.SelectionConfig) { sc.ExcludeApps = v }) })
	key := "selection/maxChars"
	field := mui.Field(c.Key(key), s.loc.T("settings.selection.maxChars"), func() {
		b := s.buf(key, strconv.Itoa(sel.EffectiveMaxChars()))
		if mui.TextInput(c, b).Width(160).Changed() {
			n, err := strconv.Atoi(strings.TrimSpace(*b))
			if err != nil || n < config.MinSelectionMaxChars || n > config.MaxSelectionMaxChars {
				s.errs[key] = s.loc.T("settings.selection.maxChars.invalid",
					"min", config.MinSelectionMaxChars, "max", config.MaxSelectionMaxChars)
				return
			}
			delete(s.errs, key)
			setSel(func(sc *config.SelectionConfig) { sc.MaxChars = n })
		}
	})
	field.Description(s.loc.T("settings.selection.maxChars.desc"))
	if msg := s.errs[key]; msg != "" {
		field.Error(msg)
	}
}

// stringList is a text area editing a list of strings, one per line.
func (s *Settings) stringList(c *mui.Context, key, label, desc string, list []string, set func([]string)) {
	field := mui.Field(c.Key(key), label, func() {
		b := s.buf(key, strings.Join(list, "\n"))
		if mui.TextArea(c, b).Lines(2, 6).Changed() {
			out := []string{} // an emptied list stays empty (not the default)
			for line := range strings.Lines(*b) {
				if line = strings.TrimSpace(line); line != "" {
					out = append(out, line)
				}
			}
			set(out)
		}
	})
	field.Description(strings.TrimSpace(desc + " " + s.loc.T("settings.plugins.listHint")))
}

func (s *Settings) aboutPage(c *mui.Context, cfg config.Config) {
	t := c.Theme()
	s.heading(c, s.loc.T("settings.nav.about"))
	mui.Row(c).Gap(14).AlignItems(mui.Center).Children(func() {
		mui.Box(c).Size(56, 56).Center().Radius(14).Background(t.Accent).Children(func() {
			mui.Icon(c, builtinIcon("command")).FontSize(30).TextColor(t.AccentText)
		})
		mui.Column(c).Gap(4).Children(func() {
			mui.Text(c, s.loc.T("app.name")).FontSize(22).Bold()
			mui.Text(c, s.loc.T("settings.about.version", "version", s.version)).TextColor(t.TextMuted)
		})
	})
	mui.Text(c, s.loc.T("settings.about.description"))
	mui.Link(c, s.loc.T("settings.about.builtWith"), "https://github.com/egoist/mygo")
	s.updatesSection(c, cfg)
}
