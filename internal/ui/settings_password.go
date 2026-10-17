package ui

// Password preferences: secrets kept in the system credential store, never
// in the configuration.

import (
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// password builds the editor of a password preference. Its value lives in
// the Windows Credential Manager, never in the configuration: the field
// starts empty, Save (or Enter) stores what was typed and Clear removes it.
func (s *Settings) password(c *mui.Context, id string, pref plugin.Preference) {
	t := c.Theme()
	key := "pref/" + id + "/" + pref.Name
	stored, ok := s.stored[key]
	if !ok {
		_, has, err := s.env.Services.Secrets.Get(id, pref.Name)
		if err != nil {
			s.errs[key] = err.Error()
		}
		stored, s.stored[key] = has, has
	}
	field := mui.Field(c.Key(key), pref.Title, func() {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			b := s.buf(key, "")
			placeholder := ""
			if stored {
				placeholder = s.loc.T("settings.plugins.secretReplace")
			}
			in := mui.TextInput(c, b).Password().Placeholder(placeholder).Grow(1).Shrink(1)
			if (button(c, s.loc.T("settings.plugins.secretSave")) || in.Submitted()) && *b != "" {
				s.setSecret(key, id, pref.Name, *b)
				*b = ""
			}
			if stored && button(c, s.loc.T("settings.plugins.secretClear")) {
				s.setSecret(key, id, pref.Name, "")
				*b = ""
			}
		})
		status := s.loc.T("settings.plugins.secretNotSet")
		if stored {
			status = s.loc.T("settings.plugins.secretStored")
		}
		mui.Text(c, status).FontSize(12).TextColor(t.TextMuted)
	})
	field.Description(strings.TrimSpace(pref.Description + " " + s.loc.T("settings.plugins.secretHint")))
	if msg := s.errs[key]; msg != "" {
		field.Error(msg)
	}
}

// setSecret stores the secret of a password preference, or removes it when
// secret is "", and tells the plugins.
func (s *Settings) setSecret(key, id, name, secret string) {
	sec := s.env.Services.Secrets
	var err error
	if secret == "" {
		err = sec.Delete(id, name)
	} else {
		err = sec.Set(id, name, secret)
	}
	delete(s.stored, key)
	if err != nil {
		s.errs[key] = s.loc.T("settings.plugins.secretFailed", "err", err.Error())
		return
	}
	delete(s.errs, key)
	s.env.Services.Bus.Publish(plugin.EventConfigChanged)
}
