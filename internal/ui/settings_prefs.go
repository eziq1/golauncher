package ui

// Plugin preference widgets: values, dynamic options and button preferences.

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strconv"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// optionsResult is the outcome of loading the dynamic options of a preference.
type optionsResult struct {
	loading bool
	options []plugin.Option
	err     string
}

// buttonResult is the outcome of the last click on a button preference.
type buttonResult struct {
	running bool
	toast   *plugin.Toast
	err     string
}

// loadOptions asks the plugin p for the dynamic options of its preferences.
// Asked again for the same plugin (reloadOptions), the options it had stay
// until the new ones arrive.
func (s *Settings) loadOptions(p core.PluginInfo) {
	s.asyncMu.Lock()
	defer s.asyncMu.Unlock()
	again := s.loadedFor == p.ID
	s.loadedFor, s.optionsStale = p.ID, false
	if !again {
		clear(s.options)
	}
	for _, pref := range p.Preferences {
		if !pref.DynamicOptions {
			continue
		}
		key, id, name := prefKey(p.ID, pref.Name), p.ID, pref.Name
		r := s.options[key]
		r.loading = true
		s.options[key] = r
		s.async(func() {
			opts, err := s.env.Engine.PreferenceOptions(context.Background(), id, name)
			r := optionsResult{options: opts}
			if err != nil {
				r.err = err.Error()
			}
			s.asyncMu.Lock()
			if s.loadedFor == id {
				s.options[key] = r
			}
			s.asyncMu.Unlock()
		})
	}
}

// reloadOptions asks the plugin page for the dynamic options again with the
// next frame: a choice changed that they may depend on (the AI plugin's
// reasoning efforts depend on the chosen model, its model lists on the
// program, R109).
func (s *Settings) reloadOptions() {
	s.asyncMu.Lock()
	s.optionsStale = true
	s.asyncMu.Unlock()
}

// runButton runs the action of a button preference.
func (s *Settings) runButton(id, name string) {
	key := prefKey(id, name)
	s.asyncMu.Lock()
	s.buttons[key] = buttonResult{running: true}
	s.asyncMu.Unlock()
	s.async(func() {
		out, err := s.env.Engine.RunPreferenceAction(context.Background(), id, name)
		r := buttonResult{toast: out.Toast}
		if err != nil {
			r.err = err.Error()
		}
		s.asyncMu.Lock()
		s.buttons[key] = r
		s.asyncMu.Unlock()
	})
}

func prefKey(id, name string) string { return "pref/" + id + "/" + name }

// prefValue returns the value of a preference: the configured one, or its default.
func prefValue(pref plugin.Preference, values map[string]jsontext.Value) jsontext.Value {
	if v, ok := values[pref.Name]; ok {
		return v
	}
	return pref.Default
}

// decodePref decodes a preference value into a T, or returns the zero T.
func decodePref[T any](v jsontext.Value) T {
	var out T
	if len(v) > 0 {
		_ = json.Unmarshal(v, &out)
	}
	return out
}

func (s *Settings) setPref(id, name string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		s.saveErr = err.Error()
		return
	}
	s.updatePlugin(id, func(p *config.PluginConfig) {
		if p.Preferences == nil {
			p.Preferences = map[string]jsontext.Value{}
		}
		p.Preferences[name] = b
	})
}

// preference builds the editor of a plugin preference, by its type.
func (s *Settings) preference(c *mui.Context, id string, pref plugin.Preference, values map[string]jsontext.Value) {
	raw := prefValue(pref, values)
	key := "pref/" + id + "/" + pref.Name
	if pref.Type == plugin.PrefBool {
		s.toggle(c, pref.Title, pref.Description, decodePref[bool](raw), func(v bool) { s.setPref(id, pref.Name, v) })
		return
	}
	if pref.Type == plugin.PrefHotkey {
		s.setting(c, pref.Title, pref.Description, func() {
			s.hotkeyRecorder(c, key, decodePref[string](raw), func(v string) { s.setPref(id, pref.Name, v) })
		})
		return
	}
	if pref.Type == plugin.PrefPassword {
		s.password(c, id, pref)
		return
	}
	if pref.Type == plugin.PrefButton {
		s.prefButton(c, id, pref)
		return
	}
	options, optionsErr := pref.Options, ""
	if pref.DynamicOptions {
		s.asyncMu.Lock()
		r := s.options[key]
		s.asyncMu.Unlock()
		options = r.options
		if r.err != "" {
			optionsErr = s.loc.T("settings.plugins.optionsFailed", "err", r.err)
		}
	}
	field := mui.Field(c.Key(key), pref.Title, func() {
		switch pref.Type {
		case plugin.PrefNumber:
			var n float64
			if len(raw) > 0 {
				n = decodePref[float64](raw)
			}
			b := s.buf(key, strconv.FormatFloat(n, 'f', -1, 64))
			if mui.TextInput(c, b).Width(160).Changed() {
				v, err := strconv.ParseFloat(strings.TrimSpace(*b), 64)
				if err != nil {
					s.errs[key] = s.loc.T("settings.plugins.invalidNumber")
					return
				}
				delete(s.errs, key)
				s.setPref(id, pref.Name, v)
			}
		case plugin.PrefSelect:
			current := decodePref[string](raw)
			titles := make([]string, len(options))
			chosen := ""
			for i, o := range options {
				titles[i] = cmp.Or(o.Title, o.Value)
				if o.Value == current {
					chosen = titles[i]
				}
			}
			if mui.Select(c, &chosen, titles).Changed() {
				if i := slices.Index(titles, chosen); i >= 0 {
					s.setPref(id, pref.Name, options[i].Value)
					s.reloadOptions()
				}
			}
		case plugin.PrefStringList:
			b := s.buf(key, strings.Join(decodePref[[]string](raw), "\n"))
			if mui.TextArea(c, b).Lines(3, 8).Changed() {
				var list []string
				for line := range strings.Lines(*b) {
					if line = strings.TrimSpace(line); line != "" {
						list = append(list, line)
					}
				}
				s.setPref(id, pref.Name, list)
			}
		default: // text, path
			b := s.buf(key, decodePref[string](raw))
			if mui.TextInput(c, b).Changed() {
				s.setPref(id, pref.Name, *b)
			}
		}
	})
	desc := pref.Description
	if pref.Type == plugin.PrefStringList {
		desc = strings.TrimSpace(desc + " " + s.loc.T("settings.plugins.listHint"))
	}
	if desc != "" {
		field.Description(desc)
	}
	if msg := cmp.Or(s.errs[key], optionsErr); msg != "" {
		field.Error(msg)
	}
}

// prefButton builds a button preference: its description, the button and
// the toast of its last run.
func (s *Settings) prefButton(c *mui.Context, id string, pref plugin.Preference) {
	t := c.Theme()
	key := prefKey(id, pref.Name)
	s.asyncMu.Lock()
	r := s.buttons[key]
	s.asyncMu.Unlock()
	mui.Row(c.Key(key)).Gap(16).Padding(10, 0).AlignItems(mui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		mui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			if pref.Description != "" {
				mui.Text(c, pref.Description)
			}
			switch {
			case r.err != "":
				mui.Text(c, s.loc.T("settings.plugins.buttonFailed", "err", r.err)).FontSize(12).TextColor(t.Danger)
			case r.toast != nil:
				color := t.TextMuted
				switch r.toast.Style {
				case plugin.ToastSuccess:
					color = t.Success
				case plugin.ToastFailure:
					color = t.Danger
				}
				mui.Text(c, r.toast.Title).FontSize(12).TextColor(color)
				if r.toast.Message != "" {
					mui.Text(c, r.toast.Message).FontSize(12).TextColor(t.TextMuted)
				}
			}
		})
		if r.running {
			mui.Spinner(c).Size(16, 16).Label(s.loc.T("ui.loading"))
		}
		if mui.Button(c, pref.Title).Disabled(r.running).Clicked() {
			s.runButton(id, pref.Name)
		}
	})
}
