package ui

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// inputs are the text inputs of a form or of the arguments of an entry. The
// Controller holds their values; buf is bound to each input, sent is what
// was last given to the Controller, and focus the Controller's focus last
// applied to the inputs.
type inputs struct {
	owner string // the form or entry the inputs belong to
	buf   map[string]*string
	sent  map[string]string
	in    map[string]*mui.Handle
	focus string
	// caret marks the input that just got the focus from the Controller, so
	// that the caret goes to the end instead of selecting the text.
	caret string
}

// placeCaret puts the caret at the end of input id's text if it just got the
// focus.
func (in *inputs) placeCaret(id string, el mui.Element, text string) {
	if in.caret == id && el.Focused() {
		n := utf8.RuneCountInString(text)
		el.SetTextSelection(n, n)
		in.caret = ""
	}
}

// reset starts the inputs of owner when they belong to another one.
func (in *inputs) reset(owner string) {
	if in.buf != nil && in.owner == owner {
		return
	}
	in.owner, in.focus, in.caret = owner, "", ""
	in.buf, in.sent, in.in = map[string]*string{}, map[string]string{}, map[string]*mui.Handle{}
}

// value returns the buffer of input id, taking the Controller's value when
// it changed it (a refreshed form, a new entry) unless edits are on their way.
func (in *inputs) value(id, value string, pending bool) *string {
	b, ok := in.buf[id]
	if !ok {
		v := value
		in.buf[id], in.sent[id] = &v, value
		return &v
	}
	if !pending && *b == in.sent[id] && value != *b {
		*b, in.sent[id] = value, value
	}
	return b
}

// handle returns the handle bound to input id.
func (in *inputs) handle(id string) *mui.Handle {
	h, ok := in.in[id]
	if !ok {
		h = &mui.Handle{}
		in.in[id] = h
	}
	return h
}

// changed records value as sent and reports whether it is new.
func (in *inputs) changed(id, value string) bool {
	if in.sent[id] == value {
		return false
	}
	in.sent[id] = value
	return true
}

func (l *Launcher) setField(id, value string) {
	if !l.fields.changed(id, value) {
		return
	}
	l.work.Do(func(context.Context) {
		if err := l.ctrl.SetField(id, value); err != nil {
			l.log.Warn("set field", "field", id, "err", err)
		}
		l.host.Invalidate()
	})
}

func (l *Launcher) focusField(id string) {
	l.fields.focus = id
	l.work.Do(func(context.Context) {
		if err := l.ctrl.FocusField(id); err != nil {
			l.log.Warn("focus field", "field", id, "err", err)
		}
		l.host.Invalidate()
	})
}

func (l *Launcher) setArgument(name, value string) {
	if !l.args.changed(name, value) {
		return
	}
	l.work.Do(func(context.Context) {
		if err := l.ctrl.SetArgument(name, value); err != nil {
			l.log.Warn("set argument", "argument", name, "err", err)
		}
		l.host.Invalidate()
	})
}

func (l *Launcher) focusArgument(name string) {
	l.args.focus = name
	l.work.Do(func(context.Context) {
		if err := l.ctrl.FocusArgument(name); err != nil {
			l.log.Warn("focus argument", "argument", name, "err", err)
		}
		l.host.Invalidate()
	})
}

// formView shows a form (docs/m5-plugin-platform.md §3.1). The Controller
// keeps the values and the focus; Tab, Shift+Tab, Enter, Ctrl+Enter and
// Escape go to it.
func (l *Launcher) formView(c *mui.Context, s core.Snapshot) {
	f := s.Form
	ids := make([]string, len(f.Fields))
	for i, fl := range f.Fields {
		ids[i] = fl.ID
	}
	l.fields.reset(strings.Join(append([]string{s.Title}, ids...), "\x00"))
	pending := l.pending
	apply := f.Focused != l.fields.focus
	form := mui.Scroll(c.Key("form")).Grow(1).Basis(0).Label(cmp.Or(s.Title, l.loc.T("ui.form.label"))).Children(func() {
		mui.Column(c).Padding(16, 24).Gap(12).Children(func() {
			for _, fl := range f.Fields {
				el := l.formField(c, fl, pending)
				if !apply && !pending && el.Focused() && fl.ID != f.Focused {
					l.focusField(fl.ID) // clicked
				}
			}
		})
	})
	for _, k := range []struct {
		mods mui.Modifiers
		key  mui.Key
	}{{0, mui.KeyTab}, {mui.Shift, mui.KeyTab}, {mui.Ctrl, mui.KeyEnter}, {0, mui.KeyEscape}} {
		if form.Shortcut(k.mods, k.key) {
			l.Key(KeyString(k.mods, k.key))
		}
	}
	if apply {
		l.fields.focus = f.Focused
		l.fields.handle(f.Focused).Focus()
	}
}

// formField builds the input of a form field and returns its control.
func (l *Launcher) formField(c *mui.Context, fl core.FieldSnapshot, pending bool) mui.Element {
	title := fl.Title
	if fl.Required {
		title += " *"
	}
	b := l.fields.value(fl.ID, fl.Value, pending)
	h := l.fields.handle(fl.ID)
	var control mui.Element
	field := mui.Field(c.Key("field:"+fl.ID), title, func() {
		switch fl.Type {
		case plugin.FieldCheckbox:
			on := *b == "true"
			control = mui.Checkbox(c, &on, "").Bind(h)
			if control.Changed() {
				*b = boolText(on)
				l.setField(fl.ID, *b)
			}
		case plugin.FieldDropdown:
			titles := make([]string, len(fl.Options))
			chosen := ""
			for i, o := range fl.Options {
				titles[i] = cmp.Or(o.Title, o.Value)
				if o.Value == *b {
					chosen = titles[i]
				}
			}
			control = mui.Select(c, &chosen, titles).Bind(h)
			if control.Changed() {
				if i := slices.Index(titles, chosen); i >= 0 {
					*b = fl.Options[i].Value
					l.setField(fl.ID, *b)
				}
			}
		case plugin.FieldTextArea:
			control = mui.TextArea(c, b).Lines(3, 6).Placeholder(fl.Placeholder).Bind(h)
			if control.Changed() {
				l.setField(fl.ID, *b)
			}
		default: // text, password, number, date, file
			placeholder := fl.Placeholder
			switch fl.Type {
			case plugin.FieldDate:
				placeholder = cmp.Or(placeholder, l.loc.T("ui.form.datePlaceholder"))
			case plugin.FieldFile:
				placeholder = cmp.Or(placeholder, l.loc.T("ui.form.filePlaceholder"))
			}
			id := fl.ID
			control = mui.TextInput(c, b).Placeholder(placeholder).Bind(h).OnTextKey(func(k mui.TextKey) bool {
				if k.Mods != 0 || k.Key != mui.KeyEnter {
					return false
				}
				l.setField(id, k.Text())
				l.Key("Enter") // the next field, or submit from the last one
				return true
			})
			if fl.Type == plugin.FieldPassword {
				control.Password()
			}
			if control.Changed() {
				l.setField(fl.ID, *b)
			}
		}
	})
	if fl.Info != "" {
		field.Description(fl.Info)
	}
	if fl.Error != "" {
		field.Error(fl.Error)
	}
	return control
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// argumentInputs shows the arguments of the selected entry next to the
// search field (docs/m5-plugin-platform.md §3.2). search is the search
// field's element; height the search bar's height inside its padding, which
// the inputs fit in.
func (l *Launcher) argumentInputs(c *mui.Context, s core.Snapshot, search mui.Element, height float32) {
	a := s.Arguments
	if a == nil {
		if l.args.focus != "" {
			l.args.focus = ""
			l.input.Focus()
		}
		l.args.reset("")
		return
	}
	l.args.reset(a.Entry)
	pending := l.pending
	apply := a.Focused != l.args.focus
	if !apply && !pending && a.Focused != "" && search.Focused() {
		l.focusArgument("") // clicked the search field
	}
	l.args.placeCaret("", search, l.text)
	row := mui.Row(c.Key("arguments")).Gap(6).AlignItems(mui.Center)
	row.Children(func() { l.argumentFields(c, a, apply, pending, height) })
	// Tab and Shift+Tab move between the inputs in the Controller's order,
	// also from a drop-down.
	for _, mods := range []mui.Modifiers{0, mui.Shift} {
		if row.Shortcut(mods, mui.KeyTab) {
			l.Key(KeyString(mods, mui.KeyTab))
		}
	}
	if apply {
		l.args.focus, l.args.caret = a.Focused, a.Focused
		if a.Focused == "" {
			l.input.Focus()
		} else {
			l.args.handle(a.Focused).Focus()
		}
	}
}

// argumentFields builds the inputs of the arguments, height high at most: a
// line of their text with as much of their padding (4 DIPs) as fits. The
// drop-downs have the text inputs' font and height, whatever the theme's
// font size.
func (l *Launcher) argumentFields(c *mui.Context, a *core.ArgumentsSnapshot, apply, pending bool, height float32) {
	t := c.Theme()
	line := lineHeight(c, argumentFontSize)
	padY := min(4, max(0, (height-line)/2))
	selectPadY := max(0, padY-1) // within the drop-down's border
	for _, arg := range a.Fields {
		b := l.args.value(arg.Name, arg.Value, pending)
		h := l.args.handle(arg.Name)
		var el mui.Element
		if arg.Type == plugin.ArgDropdown {
			titles := []string{arg.Placeholder}
			chosen := arg.Placeholder
			for _, o := range arg.Options {
				titles = append(titles, cmp.Or(o.Title, o.Value))
				if o.Value == *b {
					chosen = titles[len(titles)-1]
				}
			}
			el = mui.Select(c.Key("arg:"+arg.Name), &chosen, titles).Bind(h).Width(130).FontSize(argumentFontSize).
				Padding(selectPadY, t.Space(3.5)).Label(arg.Placeholder)
			if el.Changed() {
				*b = ""
				if i := slices.Index(titles, chosen); i > 0 {
					*b = arg.Options[i-1].Value
				}
				l.setArgument(arg.Name, *b)
			}
		} else {
			name := arg.Name
			el = mui.TextInputBase(c.Key("arg:"+arg.Name), b).Bind(h).Width(120).FontSize(argumentFontSize).Padding(padY, 8).Radius(6).
				Background(t.Text.Alpha(0.06)).Placeholder(arg.Placeholder).Label(arg.Placeholder).
				OnTextKey(func(k mui.TextKey) bool { return l.argumentKey(name, k) })
			if arg.Type == plugin.ArgPassword {
				el.Password()
			}
			if el.Changed() {
				l.setArgument(arg.Name, *b)
			}
			l.args.placeCaret(arg.Name, el, *b)
		}
		if !apply && !pending && el.Focused() && a.Focused != arg.Name {
			l.focusArgument(arg.Name) // clicked
		}
	}
}

// argumentKey handles a key pressed in an argument input: the keys that act
// on the list or move between the inputs go to the Controller.
func (l *Launcher) argumentKey(name string, k mui.TextKey) bool {
	l.setArgument(name, k.Text())
	switch key := KeyString(k.Mods, k.Key); key {
	case "Up", "Down", "Enter", "Escape", "Tab", "Shift+Tab", "Ctrl+Enter", "Ctrl+K":
		l.Key(key)
		return true
	}
	return false
}
