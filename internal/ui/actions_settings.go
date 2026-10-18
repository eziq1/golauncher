package ui

import (
	"slices"
	"strconv"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/plugins/ai/actions"
)

// actionsState is the "AI Actions" section of the AI plugin's page (R103,
// docs/ai-actions.md §2): the action being edited in the dialog and the
// confirmation of "Restore Defaults". UI thread only.
type actionsState struct {
	editing bool // the dialog is open
	index   int  // the action edited, -1 for a new one
	action  actions.Action
	// The dialog's fields.
	title, prompt, keywords, minLength string
	targets, mode                      int
	icon                               string
	problem                            string

	confirmRestore bool
}

// actionTargets are the choices of the dialog's "Use on", in order.
var actionTargets = []actions.Target{actions.TargetSelection, actions.TargetFiles, actions.TargetBoth}

// actionModes are the choices of its "Result", in order.
var actionModes = []actions.Mode{actions.ModeChat, actions.ModeResult}

// actionsSection lists the AI actions: each with a switch, its title,
// whether it is built in, what it applies to and how it answers, and Edit,
// ↑, ↓ and (custom ones) Delete; "New Action" and "Restore Defaults" above.
// Every change is stored at once in the AI plugin's preference
// actions.Pref, as what differs from the built-in actions.
func (s *Settings) actionsSection(c *mui.Context, cfg config.Config) {
	if s.env.AIActions == nil {
		return
	}
	t := c.Theme()
	st := &s.actions
	defaults := s.env.AIActions.BuiltinActions()
	stored, _ := actions.Decode(cfg.Plugin(aiPluginID).Preferences[actions.Pref])
	list := actions.Merge(defaults, stored)
	save := func(list []actions.Action) {
		s.setPref(aiPluginID, actions.Pref, actions.Encode(actions.Diff(defaults, list)))
	}

	s.subheading(c, s.loc.T("settings.actions.heading"))
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.Text(c, s.loc.T("settings.actions.intro")).FontSize(12).TextColor(t.TextMuted).Grow(1).Shrink(1)
		if mui.Button(c.Key("actions:new"), s.loc.T("settings.actions.new")).Clicked() {
			s.editAction(-1, actions.Action{ID: actions.NewID(list)})
		}
		if mui.Button(c.Key("actions:restore"), s.loc.T("settings.actions.restore")).Clicked() {
			st.confirmRestore = true
		}
	})
	// Changes are saved from a copy: the rows of this frame keep the list.
	changed := func(fn func(out []actions.Action) []actions.Action) {
		save(fn(slices.Clone(list)))
	}
	for i, a := range list {
		mui.Row(c.Key("action:"+a.ID)).Gap(8).Padding(8, 0).AlignItems(mui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			on := !a.Disabled
			if mui.Checkbox(c, &on, "").Label(s.loc.T("settings.actions.enabledOf", "name", a.Title)).Changed() {
				changed(func(out []actions.Action) []actions.Action { out[i].Disabled = !on; return out })
			}
			mui.Icon(c, builtinIcon(a.IconName())).FontSize(16)
			mui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
				mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
					mui.Text(c, a.Title).Shrink(1)
					if a.Builtin {
						mui.Badge(c, s.loc.T("settings.actions.builtin"))
					} else {
						mui.Badge(c, s.loc.T("settings.actions.custom"))
					}
				})
				mui.Text(c, s.actionSummary(a)).FontSize(12).TextColor(t.TextMuted)
			})
			if mui.Button(c, s.loc.T("settings.actions.edit")).Label(s.loc.T("settings.actions.editOf", "name", a.Title)).Clicked() {
				s.editAction(i, a)
			}
			if mui.Button(c, "↑").Label(s.loc.T("settings.actions.upOf", "name", a.Title)).Disabled(i == 0).Clicked() {
				changed(func(out []actions.Action) []actions.Action { actions.Move(out, i, -1); return out })
			}
			if mui.Button(c, "↓").Label(s.loc.T("settings.actions.downOf", "name", a.Title)).Disabled(i == len(list)-1).Clicked() {
				changed(func(out []actions.Action) []actions.Action { actions.Move(out, i, 1); return out })
			}
			if !a.Builtin && mui.Button(c, s.loc.T("settings.actions.delete")).Label(s.loc.T("settings.actions.deleteOf", "name", a.Title)).Clicked() {
				changed(func(out []actions.Action) []actions.Action { return slices.Delete(out, i, i+1) })
			}
		})
	}

	if st.editing {
		mui.Modal(c.Key("actions:dialog"), &st.editing, func() {
			s.actionDialog(c, list, save)
		})
	}
	if st.confirmRestore {
		mui.Modal(c.Key("actions:confirm"), &st.confirmRestore, func() {
			mui.Column(c).Gap(12).Padding(20).Width(420).Children(func() {
				mui.Text(c, s.loc.T("settings.actions.restore.title")).Bold().Level(2)
				mui.Text(c, s.loc.T("settings.actions.restore.message"))
				mui.Row(c).Gap(8).Children(func() {
					mui.Spacer(c)
					if button(c, s.loc.T("settings.actions.cancel")) {
						st.confirmRestore = false
					}
					if mui.PrimaryButton(c, s.loc.T("settings.actions.restore.confirm")).Clicked() {
						st.confirmRestore = false
						save(actions.Restore(defaults, list))
					}
				})
			})
		})
	}
}

// actionSummary words what an action applies to and how it answers.
func (s *Settings) actionSummary(a actions.Action) string {
	var parts []string
	switch a.Targets {
	case actions.TargetFiles:
		parts = append(parts, s.loc.T("settings.actions.target.files"))
	case actions.TargetBoth:
		parts = append(parts, s.loc.T("settings.actions.target.both"))
	default:
		parts = append(parts, s.loc.T("settings.actions.target.selection"))
	}
	if a.MinLength > 0 && a.OnSelection() {
		parts = append(parts, s.loc.N("settings.actions.minLength.summary", a.MinLength))
	}
	if a.ResultMode() {
		parts = append(parts, s.loc.T("settings.actions.mode.result"))
	} else {
		parts = append(parts, s.loc.T("settings.actions.mode.chat"))
	}
	return strings.Join(parts, " · ")
}

// editAction opens the dialog on a (at index i of the list, -1 for new).
func (s *Settings) editAction(i int, a actions.Action) {
	st := &s.actions
	st.editing, st.index, st.action, st.problem = true, i, a, ""
	st.title, st.prompt, st.keywords = a.Title, a.Prompt, a.Keywords
	st.minLength = ""
	if a.MinLength > 0 {
		st.minLength = strconv.Itoa(a.MinLength)
	}
	st.targets = max(0, slices.Index(actionTargets, a.Targets))
	st.mode = max(0, slices.Index(actionModes, a.Result))
	st.icon = a.IconName()
}

// actionDialog is the dialog editing an action: its name and prompt and,
// for an action of the user's, what it applies to, how it answers, its
// keywords, icon and minimum length. A name and a prompt are needed.
func (s *Settings) actionDialog(c *mui.Context, list []actions.Action, save func([]actions.Action)) {
	t := c.Theme()
	st := &s.actions
	builtin := st.action.Builtin
	mui.Column(c).Gap(12).Padding(20).Width(560).Children(func() {
		heading := s.loc.T("settings.actions.dialog.edit")
		if st.index < 0 {
			heading = s.loc.T("settings.actions.dialog.new")
		}
		mui.Text(c, heading).FontSize(16).Bold().Level(2)
		if builtin {
			mui.Text(c, s.loc.T("settings.actions.dialog.builtinNote")).FontSize(12).TextColor(t.TextMuted)
		}
		mui.Field(c, s.loc.T("settings.actions.field.title"), func() {
			mui.TextInput(c, &st.title).Label(s.loc.T("settings.actions.field.title"))
		})
		mui.Field(c, s.loc.T("settings.actions.field.prompt"), func() {
			mui.TextArea(c, &st.prompt).Lines(4, 10).Label(s.loc.T("settings.actions.field.prompt"))
		}).Description(s.loc.T("settings.actions.field.prompt.help"))
		if !builtin {
			mui.Field(c, s.loc.T("settings.actions.field.targets"), func() {
				mui.Segmented(c, &st.targets, s.loc.T("settings.actions.target.selection"), s.loc.T("settings.actions.target.files"),
					s.loc.T("settings.actions.target.both")).Label(s.loc.T("settings.actions.field.targets"))
			})
			mui.Field(c, s.loc.T("settings.actions.field.mode"), func() {
				mui.Segmented(c, &st.mode, s.loc.T("settings.actions.mode.chat"), s.loc.T("settings.actions.mode.result")).
					Label(s.loc.T("settings.actions.field.mode"))
			}).Description(s.loc.T("settings.actions.field.mode.help"))
			mui.Field(c, s.loc.T("settings.actions.field.keywords"), func() {
				mui.TextInput(c, &st.keywords).Label(s.loc.T("settings.actions.field.keywords"))
			}).Description(s.loc.T("settings.actions.field.keywords.help"))
			mui.Field(c, s.loc.T("settings.actions.field.icon"), func() {
				mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
					mui.Icon(c, builtinIcon(st.icon)).FontSize(18)
					mui.Select(c, &st.icon, actions.Icons).Label(s.loc.T("settings.actions.field.icon"))
				})
			})
			mui.Field(c, s.loc.T("settings.actions.field.minLength"), func() {
				mui.TextInput(c, &st.minLength).Label(s.loc.T("settings.actions.field.minLength"))
			}).Description(s.loc.T("settings.actions.field.minLength.help"))
		}
		if st.problem != "" {
			mui.Text(c, st.problem).FontSize(12).TextColor(t.Danger)
		}
		mui.Row(c).Gap(8).Children(func() {
			mui.Spacer(c)
			if button(c, s.loc.T("settings.actions.cancel")) {
				st.editing = false
			}
			if mui.PrimaryButton(c, s.loc.T("settings.actions.save")).Clicked() {
				if a, ok := s.dialogAction(); ok {
					out := slices.Clone(list)
					if st.index >= 0 && st.index < len(out) {
						out[st.index] = a
					} else {
						out = append(out, a)
					}
					st.editing = false
					save(out)
				}
			}
		})
	})
}

// dialogAction is the action the dialog's fields describe, or false with
// the problem shown when a field is wrong.
func (s *Settings) dialogAction() (actions.Action, bool) {
	st := &s.actions
	a := st.action
	a.Title, a.Prompt = strings.TrimSpace(st.title), strings.TrimRight(st.prompt, " \t\r\n")
	if !a.Builtin {
		a.Targets = actionTargets[min(max(st.targets, 0), len(actionTargets)-1)]
		a.Result = actionModes[min(max(st.mode, 0), len(actionModes)-1)]
		a.Keywords = strings.TrimSpace(st.keywords)
		a.Icon = st.icon
		if a.Icon == actions.DefaultIcon {
			a.Icon = ""
		}
		a.MinLength = 0
		if v := strings.TrimSpace(st.minLength); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				st.problem = s.loc.T("settings.actions.problem.minLength")
				return a, false
			}
			a.MinLength = n
		}
	}
	switch actions.Check(a) {
	case actions.NoTitle:
		st.problem = s.loc.T("settings.actions.problem.noTitle")
		return a, false
	case actions.NoPrompt:
		st.problem = s.loc.T("settings.actions.problem.noPrompt")
		return a, false
	}
	st.problem = ""
	return a, true
}
