// Package actions is the list of AI actions on the selected text and on the
// files handed to the root search (R103, docs/ai-actions.md): the AI
// plugin's built-in actions, merged with what the user changed and the
// actions the user added. The AI plugin builds its rows from the merged
// list; the settings window edits it. The list is stored in the AI plugin's
// preferences under Pref, as JSON (Stored).
//
// Only what the user changed of a built-in action is stored (its title,
// prompt, whether it is on, its place), so built-in actions that were not
// changed follow new versions, and built-in actions added later show up.
package actions

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strconv"
	"strings"
)

// Pref is the name of the AI plugin's preference holding the list. It is
// not declared in the manifest: the settings window draws its own section.
const Pref = "actions"

// Target is what an action applies to.
type Target string

const (
	// TargetSelection is the selected text (the default).
	TargetSelection Target = "selection"
	// TargetFiles is the files handed to the root search (R97).
	TargetFiles Target = "files"
	// TargetBoth is both.
	TargetBoth Target = "both"
)

// Mode is how an action shows its answer.
type Mode string

const (
	// ModeChat opens an AI chat, where the user can follow up (the default).
	ModeChat Mode = "chat"
	// ModeResult shows only the answer, ready to paste (docs/ai-actions.md §4).
	ModeResult Mode = "result"
)

// DefaultIcon is the icon of an action that names none.
const DefaultIcon = "sparkles"

// Icons are the built-in icons an action can use (internal/ui's icon set).
var Icons = []string{
	"sparkles", "languages", "text", "message-square", "code", "file", "search",
	"globe", "hash", "check", "info", "wrench", "terminal", "clipboard", "activity",
}

// Placeholders are the placeholders a prompt can use (docs/ai-actions.md §3).
var Placeholders = []string{"{selection}", "{lang}", "{input}", "{app}", "{date}"}

// Action is an AI action.
type Action struct {
	// ID is a built-in action's name ("translate") or a custom action's
	// "custom-<n>"; it is the row's ID.
	ID string `json:"id"`
	// Title is the row's title. A built-in action's may use {lang}.
	Title string `json:"title"`
	// Prompt is sent to the model, with its placeholders replaced.
	Prompt string `json:"prompt"`
	// Targets is what the action applies to ("": TargetSelection).
	Targets Target `json:"targets,omitempty"`
	// Result is how the answer shows ("": ModeChat).
	Result Mode `json:"result,omitempty"`
	// Keywords are extra words the row matches, separated by commas.
	Keywords string `json:"keywords,omitempty"`
	// Icon is the name of a built-in icon ("": DefaultIcon).
	Icon string `json:"icon,omitempty"`
	// MinLength hides the action for a selection shorter than this many
	// characters (0: no minimum).
	MinLength int `json:"minLength,omitzero"`
	// Disabled hides the action.
	Disabled bool `json:"disabled,omitzero"`

	// Builtin marks one of the AI plugin's actions (not stored).
	Builtin bool `json:"-"`
	// Name is a built-in action's name in a chat's title ("Translate"),
	// while its title is not changed; "" uses the title.
	Name string `json:"-"`
}

// OnSelection reports whether the action applies to the selected text.
func (a Action) OnSelection() bool { return a.Targets != TargetFiles }

// OnFiles reports whether the action applies to files.
func (a Action) OnFiles() bool { return a.Targets == TargetFiles || a.Targets == TargetBoth }

// ResultMode reports whether the action shows only the answer.
func (a Action) ResultMode() bool { return a.Result == ModeResult }

// IconName is the action's icon.
func (a Action) IconName() string {
	if a.Icon == "" || !slices.Contains(Icons, a.Icon) {
		return DefaultIcon
	}
	return a.Icon
}

// KeywordList splits the keywords.
func (a Action) KeywordList() []string {
	var out []string
	for k := range strings.SplitSeq(a.Keywords, ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// Override is what the user changed of a built-in action ("" and false: not
// changed).
type Override struct {
	Title    string `json:"title,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	Disabled bool   `json:"disabled,omitzero"`
}

// Stored is the preference's value.
type Stored struct {
	// Order is the IDs of the actions in the user's order; nil while the
	// order is the default one (the built-in actions, then the custom ones).
	Order []string `json:"order,omitempty"`
	// Builtin are the changes to built-in actions, by ID.
	Builtin map[string]Override `json:"builtin,omitempty"`
	// Custom are the user's actions.
	Custom []Action `json:"custom,omitempty"`
}

// Decode reads the preference's value; a missing or malformed value is the
// default list (ok is false when it was malformed).
func Decode(raw jsontext.Value) (st Stored, ok bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return Stored{}, true
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return Stored{}, false
	}
	return st, true
}

// Encode is the preference's value of st.
func Encode(st Stored) jsontext.Value {
	b, err := json.Marshal(st, json.Deterministic(true))
	if err != nil {
		return jsontext.Value("{}")
	}
	return b
}

// Merge is the list: the built-in actions (defaults, in their default
// order) with the user's changes, and the custom actions, in the user's
// order. A built-in action the order does not name (added in a later
// version) comes after the built-in action that precedes it by default.
func Merge(defaults []Action, st Stored) []Action {
	byID := map[string]Action{}
	for _, d := range defaults {
		a := d
		a.Builtin = true
		if o, ok := st.Builtin[d.ID]; ok {
			if t := strings.TrimSpace(o.Title); t != "" && t != d.Title {
				a.Title, a.Name = t, ""
			}
			if strings.TrimSpace(o.Prompt) != "" {
				a.Prompt = o.Prompt
			}
			a.Disabled = o.Disabled
		}
		byID[a.ID] = a
	}
	var custom []string
	for _, c := range st.Custom {
		if _, dup := byID[c.ID]; dup || c.ID == "" {
			continue
		}
		c.Builtin, c.Name = false, ""
		byID[c.ID] = c
		custom = append(custom, c.ID)
	}
	var ids []string
	for _, id := range st.Order {
		if _, ok := byID[id]; ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	pos := 0
	for _, d := range defaults {
		if i := slices.Index(ids, d.ID); i >= 0 {
			pos = i + 1
			continue
		}
		ids = slices.Insert(ids, pos, d.ID)
		pos++
	}
	for _, id := range custom {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	out := make([]Action, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

// Diff is what to store for list, a merged list the user changed: the
// changes to the built-in actions (defaults), the custom actions and the
// order when it is not the default one.
func Diff(defaults []Action, list []Action) Stored {
	var st Stored
	def := map[string]Action{}
	for _, d := range defaults {
		def[d.ID] = d
	}
	var ids []string
	for _, a := range list {
		ids = append(ids, a.ID)
		d, builtin := def[a.ID]
		if !builtin {
			c := a
			c.Builtin, c.Name = false, ""
			st.Custom = append(st.Custom, c)
			continue
		}
		var o Override
		if t := strings.TrimSpace(a.Title); t != "" && t != d.Title {
			o.Title = t
		}
		if strings.TrimSpace(a.Prompt) != "" && a.Prompt != d.Prompt {
			o.Prompt = a.Prompt
		}
		o.Disabled = a.Disabled
		if o != (Override{}) {
			if st.Builtin == nil {
				st.Builtin = map[string]Override{}
			}
			st.Builtin[a.ID] = o
		}
	}
	if !slices.Equal(ids, defaultOrder(defaults, st.Custom)) {
		st.Order = ids
	}
	return st
}

// defaultOrder is the order of the built-in actions followed by custom.
func defaultOrder(defaults, custom []Action) []string {
	var ids []string
	for _, d := range defaults {
		ids = append(ids, d.ID)
	}
	for _, c := range custom {
		ids = append(ids, c.ID)
	}
	return ids
}

// Restore puts the built-in actions of list back as they come (their
// defaults, in their default order, first); the custom actions stay, in
// their order, after them.
func Restore(defaults []Action, list []Action) []Action {
	out := Merge(defaults, Stored{})
	for _, a := range list {
		if !a.Builtin {
			out = append(out, a)
		}
	}
	return out
}

// Move moves the action at i by delta places (-1 up, 1 down) and reports
// whether it moved.
func Move(list []Action, i, delta int) bool {
	j := i + delta
	if i < 0 || i >= len(list) || j < 0 || j >= len(list) {
		return false
	}
	list[i], list[j] = list[j], list[i]
	return true
}

// NewID is an ID for a new custom action of list.
func NewID(list []Action) string {
	n := 0
	for _, a := range list {
		if s, ok := strings.CutPrefix(a.ID, "custom-"); ok {
			if k, err := strconv.Atoi(s); err == nil && k > n {
				n = k
			}
		}
	}
	return "custom-" + strconv.Itoa(n+1)
}

// Problem is what is wrong with an action being edited.
type Problem string

const (
	NoProblem Problem = ""
	NoTitle   Problem = "noTitle"
	NoPrompt  Problem = "noPrompt"
)

// Check reports what is wrong with a: a title and a prompt are needed.
func Check(a Action) Problem {
	switch {
	case strings.TrimSpace(a.Title) == "":
		return NoTitle
	case strings.TrimSpace(a.Prompt) == "":
		return NoPrompt
	}
	return NoProblem
}

// Vars are the values of the placeholders.
type Vars struct {
	Selection string // {selection}
	Lang      string // {lang}: the target language, in English
	Input     string // {input}: what the user typed for the action
	App       string // {app}: the app the text was selected in
	Date      string // {date}: today, as 2006-01-02
}

// Expand replaces the placeholders of s with v, in one pass: text put in
// is not scanned again.
func Expand(s string, v Vars) string {
	return strings.NewReplacer(
		"{selection}", v.Selection,
		"{lang}", v.Lang,
		"{input}", v.Input,
		"{app}", v.App,
		"{date}", v.Date,
	).Replace(s)
}

// UsesSelection reports whether a prompt places the selected text itself.
func UsesSelection(prompt string) bool { return strings.Contains(prompt, "{selection}") }

// UsesInput reports whether a prompt asks for {input}.
func UsesInput(prompt string) bool { return strings.Contains(prompt, "{input}") }

// Provider is implemented by the AI plugin: its built-in actions, with
// their titles and keywords in the current UI language. The settings window
// merges them with the stored preference.
type Provider interface {
	BuiltinActions() []Action
}
