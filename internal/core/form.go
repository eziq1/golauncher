package core

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// formState is the state of a form view, kept by the host like the
// navigation stack (docs/m5-plugin-platform.md §3.1): the values typed, the
// errors shown and the focused field. Values are kept as text: "true" or
// "false" for a checkbox, the option value for a dropdown, "YYYY-MM-DD" for
// a date and the path for a file.
type formState struct {
	fields  []plugin.Field
	values  map[string]string
	errors  map[string]string
	focused int
}

// newFormState returns the state of form, keeping the values typed into old
// for the fields that still exist.
func newFormState(old *formState, form *plugin.Form) *formState {
	s := &formState{fields: form.Fields, values: map[string]string{}, errors: map[string]string{}}
	for _, fl := range form.Fields {
		if old != nil {
			if v, ok := old.values[fl.ID]; ok {
				s.values[fl.ID] = v
				if e, ok := old.errors[fl.ID]; ok {
					s.errors[fl.ID] = e
				}
				continue
			}
		}
		s.values[fl.ID] = fieldDefault(fl)
	}
	if old != nil && old.focused < len(old.fields) {
		s.focused = max(0, s.index(old.fields[old.focused].ID))
	}
	return s
}

// fieldDefault is the initial text of a field.
func fieldDefault(fl plugin.Field) string {
	var v any
	if len(fl.Default) == 0 || json.Unmarshal(fl.Default, &v) != nil {
		if fl.Type == plugin.FieldCheckbox {
			return "false"
		}
		return ""
	}
	switch v := v.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case nil:
		return fieldDefault(plugin.Field{Type: fl.Type})
	default:
		return string(fl.Default)
	}
}

func (s *formState) index(id string) int {
	return slices.IndexFunc(s.fields, func(f plugin.Field) bool { return f.ID == id })
}

// setErrors shows errs and focuses the first field with an error.
func (s *formState) setErrors(errs map[string]string) {
	s.errors = maps.Clone(errs)
	if s.errors == nil {
		s.errors = map[string]string{}
	}
	if i := slices.IndexFunc(s.fields, func(f plugin.Field) bool { return s.errors[f.ID] != "" }); i >= 0 {
		s.focused = i
	}
}

// jsonValues are the values passed in ActionDo.Values: a number as a JSON
// number (null when empty), a checkbox as a boolean, the others as strings.
func (s *formState) jsonValues() map[string]jsontext.Value {
	out := make(map[string]jsontext.Value, len(s.fields))
	for _, fl := range s.fields {
		v := s.values[fl.ID]
		switch fl.Type {
		case plugin.FieldNumber:
			n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				out[fl.ID] = jsontext.Value("null")
				continue
			}
			b, _ := json.Marshal(n)
			out[fl.ID] = b
		case plugin.FieldCheckbox:
			out[fl.ID] = jsontext.Value(strconv.FormatBool(v == "true"))
		default:
			out[fl.ID] = stringValue(v)
		}
	}
	return out
}

// validate checks the values before the form is submitted: required fields,
// numbers, dates and dropdown choices. It returns the messages by field ID.
func (c *Controller) validate(s *formState) map[string]string {
	loc := c.eng.core
	errs := map[string]string{}
	for _, fl := range s.fields {
		v := strings.TrimSpace(s.values[fl.ID])
		empty := v == "" || fl.Type == plugin.FieldCheckbox && v != "true"
		switch {
		case fl.Required && empty:
			errs[fl.ID] = loc.T("form.required")
		case v == "":
		case fl.Type == plugin.FieldNumber:
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				errs[fl.ID] = loc.T("form.invalidNumber")
			}
		case fl.Type == plugin.FieldDate:
			if _, err := time.Parse(time.DateOnly, v); err != nil {
				errs[fl.ID] = loc.T("form.invalidDate")
			}
		case fl.Type == plugin.FieldDropdown:
			if !slices.ContainsFunc(fl.Options, func(o plugin.Option) bool { return o.Value == v }) {
				errs[fl.ID] = loc.T("form.invalidOption")
			}
		}
	}
	return errs
}

// formKey handles a key in a form view: Tab and Shift+Tab move between the
// fields, Enter moves to the next one and submits from the last one,
// Ctrl+Enter submits and Escape goes back. c.mu is held on entry and
// released before returning.
func (c *Controller) formKey(ctx context.Context, key string) (Outcome, error) {
	f := c.top()
	s := f.form
	n := len(s.fields)
	focused := plugin.Field{}
	if n > 0 {
		s.focused = clamp(s.focused, n)
		focused = s.fields[s.focused]
	}
	switch key {
	case "Tab":
		if n > 0 {
			s.focused = (s.focused + 1) % n
		}
	case "Shift+Tab":
		if n > 0 {
			s.focused = (s.focused - 1 + n) % n
		}
	case "Enter":
		if focused.Type == plugin.FieldTextArea {
			break // the front end breaks the line
		}
		if s.focused < n-1 {
			s.focused++
			break
		}
		c.mu.Unlock()
		return c.submitForm(ctx)
	case "Ctrl+Enter":
		c.mu.Unlock()
		return c.submitForm(ctx)
	case "Space":
		if focused.Type == plugin.FieldCheckbox {
			s.values[focused.ID] = strconv.FormatBool(s.values[focused.ID] != "true")
			delete(s.errors, focused.ID)
		}
	case "Escape":
		if len(c.stack) > 1 {
			c.back() // unlocks
			c.refresh(ctx)
			return Outcome{}, nil
		}
	}
	c.mu.Unlock()
	return Outcome{}, nil
}

// submitForm validates the form and runs its Submit action with the values.
func (c *Controller) submitForm(ctx context.Context) (Outcome, error) {
	c.mu.Lock()
	f := c.top()
	s := f.form
	if s == nil {
		c.mu.Unlock()
		return Outcome{}, nil
	}
	s.setErrors(c.validate(s))
	if len(s.errors) > 0 {
		c.mu.Unlock()
		return Outcome{}, nil
	}
	action := f.view.Form.Submit
	action.Do.Values = s.jsonValues()
	entry := Entry{Plugin: f.nav.Plugin}
	c.mu.Unlock()
	return c.run(ctx, entry, action)
}

// form returns the state of the open form. c.mu must be held.
func (c *Controller) form() (*formState, error) {
	if s := c.top().form; s != nil {
		return s, nil
	}
	return nil, fmt.Errorf("core: the current view is not a form")
}

// SetField sets the value of the form field id, as typed: see formState for
// the text of each field type.
func (c *Controller) SetField(id, value string) error {
	c.waitSearches()
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.form()
	if err != nil {
		return err
	}
	if s.index(id) < 0 {
		return fmt.Errorf("core: the form has no field %q", id)
	}
	s.values[id] = value
	delete(s.errors, id)
	return nil
}

// FocusField moves the focus to the form field id.
func (c *Controller) FocusField(id string) error {
	c.waitSearches()
	c.mu.Lock()
	defer c.mu.Unlock()
	s, err := c.form()
	if err != nil {
		return err
	}
	i := s.index(id)
	if i < 0 {
		return fmt.Errorf("core: the form has no field %q", id)
	}
	s.focused = i
	return nil
}

// FormSnapshot is an open form.
type FormSnapshot struct {
	Fields []FieldSnapshot `json:"fields"`
	// Focused is the ID of the focused field.
	Focused string `json:"focused,omitempty"`
	// Submit is the title of the submit action.
	Submit string `json:"submit"`
}

// FieldSnapshot is a form field with its value and error.
type FieldSnapshot struct {
	plugin.Field
	Value string `json:"value"`
	Error string `json:"error,omitempty"`
}

func (s *formState) snapshot(submit string) *FormSnapshot {
	out := &FormSnapshot{Submit: submit, Fields: make([]FieldSnapshot, 0, len(s.fields))}
	for i, fl := range s.fields {
		out.Fields = append(out.Fields, FieldSnapshot{Field: fl, Value: s.values[fl.ID], Error: s.errors[fl.ID]})
		if i == s.focused {
			out.Focused = fl.ID
		}
	}
	return out
}

// argState holds the arguments typed for the selected entry
// (docs/m5-plugin-platform.md §3.2). They are dropped when another entry is
// selected.
type argState struct {
	key    string // the entry they belong to
	values map[string]string
	focus  string // the focused argument, "" for the search field
}

// argsOf returns the arguments typed for the entry with key, or nil.
func (f *frame) argsOf(key string) *argState {
	if f.args != nil && f.args.key == key {
		return f.args
	}
	return nil
}

// argsFor returns the arguments of the entry with key, starting them.
func (f *frame) argsFor(key string) *argState {
	if st := f.argsOf(key); st != nil {
		return st
	}
	f.args = &argState{key: key, values: map[string]string{}}
	return f.args
}

// missingArgument returns the first required argument of e without a value.
func missingArgument(e Entry, st *argState) string {
	for _, a := range e.Arguments {
		if a.Required && (st == nil || strings.TrimSpace(st.values[a.Name]) == "") {
			return a.Name
		}
	}
	return ""
}

// argumentValues adds the argument values of e to an action's values.
func argumentValues(e Entry, st *argState, values map[string]jsontext.Value) map[string]jsontext.Value {
	out := maps.Clone(values)
	if out == nil {
		out = map[string]jsontext.Value{}
	}
	for _, a := range e.Arguments {
		v := ""
		if st != nil {
			v = st.values[a.Name]
		}
		out[a.Name] = stringValue(v)
	}
	return out
}

// argumentKey handles Tab and Shift+Tab, which move between the search field
// and the arguments of the selected entry, and Escape in an argument, which
// goes back to the search field. It reports whether it took the key. c.mu
// must be held.
func (c *Controller) argumentKey(f *frame, key string) bool {
	if key != "Tab" && key != "Shift+Tab" && key != "Escape" {
		return false
	}
	e, ok := itemAt(c.visible(f), f.sel)
	if !ok || len(e.Arguments) == 0 {
		return false
	}
	if key == "Escape" {
		st := f.argsOf(e.Key)
		if st == nil || st.focus == "" {
			return false
		}
		st.focus = ""
		return true
	}
	st := f.argsFor(e.Key)
	// Positions: -1 is the search field, then the arguments.
	i := slices.IndexFunc(e.Arguments, func(a plugin.Argument) bool { return a.Name == st.focus })
	if st.focus == "" {
		i = -1
	}
	n := len(e.Arguments)
	if key == "Tab" {
		i++
	} else {
		i--
	}
	i = (i+1+n+1)%(n+1) - 1
	st.focus = ""
	if i >= 0 {
		st.focus = e.Arguments[i].Name
	}
	return true
}

// selectedArgs returns the selected entry, which must take the argument
// name, and its argument state. c.mu must be held.
func (c *Controller) selectedArgs(name string) (*argState, error) {
	f := c.top()
	e, ok := itemAt(c.visible(f), f.sel)
	if !ok {
		return nil, fmt.Errorf("core: no entry is selected")
	}
	if name != "" && !slices.ContainsFunc(e.Arguments, func(a plugin.Argument) bool { return a.Name == name }) {
		return nil, fmt.Errorf("core: %q takes no argument %q", e.Key, name)
	}
	return f.argsFor(e.Key), nil
}

// SetArgument sets the value of an argument of the selected entry.
func (c *Controller) SetArgument(name, value string) error {
	c.waitSearches()
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.selectedArgs(name)
	if err != nil {
		return err
	}
	st.values[name] = value
	return nil
}

// FocusArgument moves the focus to an argument of the selected entry, or to
// the search field for "".
func (c *Controller) FocusArgument(name string) error {
	c.waitSearches()
	c.mu.Lock()
	defer c.mu.Unlock()
	st, err := c.selectedArgs(name)
	if err != nil {
		return err
	}
	st.focus = name
	return nil
}

// ArgumentsSnapshot is the arguments of the selected entry.
type ArgumentsSnapshot struct {
	Entry  string             `json:"entry"` // key of the entry
	Fields []ArgumentSnapshot `json:"fields"`
	// Focused is the name of the focused argument, "" for the search field.
	Focused string `json:"focused,omitempty"`
}

// ArgumentSnapshot is an argument with its value.
type ArgumentSnapshot struct {
	plugin.Argument
	Value string `json:"value"`
}

func argumentsSnapshot(e Entry, st *argState) *ArgumentsSnapshot {
	if len(e.Arguments) == 0 {
		return nil
	}
	out := &ArgumentsSnapshot{Entry: e.Key}
	for _, a := range e.Arguments {
		v := ""
		if st != nil {
			v = st.values[a.Name]
		}
		out.Fields = append(out.Fields, ArgumentSnapshot{Argument: a, Value: v})
	}
	if st != nil {
		out.Focused = st.focus
	}
	return out
}
