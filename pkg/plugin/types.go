package plugin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// Payload is opaque plugin data carried through actions and views.
type Payload = jsontext.Value

// MustPayload marshals v into a Payload and panics on failure. Use it for
// values that are known to be serializable.
func MustPayload(v any) Payload {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// Manifest describes a plugin. Any user-visible string may be written as
// "%key%", which the host resolves from the plugin's message catalogs.
type Manifest struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	Description   string       `json:"description,omitempty"`
	Version       string       `json:"version"`
	Icon          Icon         `json:"icon,omitzero"`
	DefaultLocale string       `json:"defaultLocale,omitempty"`
	Permissions   []Permission `json:"permissions,omitempty"`
	Commands      []Command    `json:"commands,omitempty"`
	Preferences   []Preference `json:"preferences,omitempty"`
}

// Permission names a group of Host APIs a plugin may use.
type Permission string

const (
	PermClipboard Permission = "clipboard"
	PermStorage   Permission = "storage"
	PermNetwork   Permission = "network"
	PermExec      Permission = "exec"
)

// CommandMode tells the host what running a command does.
type CommandMode string

const (
	// ModeView opens a view rendered by ViewProvider with the command name as view ID.
	ModeView CommandMode = "view"
	// ModeNoView runs the command through ActionHandler with ActionRunCommand.
	ModeNoView CommandMode = "no-view"
)

// Command is an entry point listed in the root search.
type Command struct {
	Name          string      `json:"name"`
	Title         string      `json:"title"`
	Subtitle      string      `json:"subtitle,omitempty"`
	Mode          CommandMode `json:"mode"`
	Icon          Icon        `json:"icon,omitzero"`
	Keywords      []string    `json:"keywords,omitempty"`
	DefaultHotkey string      `json:"defaultHotkey,omitempty"`
	// Destructive commands ask for confirmation before running.
	Destructive bool `json:"destructive,omitzero"`
	// Arguments are typed inline next to the search field (at most 3).
	Arguments []Argument `json:"arguments,omitempty"`
}

// ArgumentType is the input used for an Argument.
type ArgumentType string

const (
	ArgText     ArgumentType = "text"
	ArgPassword ArgumentType = "password"
	ArgDropdown ArgumentType = "dropdown"
)

// Argument is an inline input of a command or item. Values reach the plugin
// in ViewRequest.Arguments or ActionDo.Values.
type Argument struct {
	Name        string       `json:"name"`
	Type        ArgumentType `json:"type"`
	Placeholder string       `json:"placeholder"`
	Required    bool         `json:"required,omitzero"`
	Options     []Option     `json:"options,omitempty"` // ArgDropdown
}

// PrefType is the editor used for a preference in the settings window.
type PrefType string

const (
	PrefText       PrefType = "text"
	PrefPassword   PrefType = "password"
	PrefNumber     PrefType = "number"
	PrefBool       PrefType = "bool"
	PrefSelect     PrefType = "select"
	PrefStringList PrefType = "stringList"
	PrefPath       PrefType = "path"
	PrefHotkey     PrefType = "hotkey"
	// PrefButton is a button that runs Preference.Action (e.g. "Sign in").
	PrefButton PrefType = "button"
)

// Preference is a user setting declared by a plugin.
type Preference struct {
	Name        string         `json:"name"`
	Type        PrefType       `json:"type"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Default     jsontext.Value `json:"default,omitempty"`
	Options     []Option       `json:"options,omitempty"`
	// DynamicOptions asks the plugin for the options of a PrefSelect through
	// PreferenceOptionsProvider when the settings page opens.
	DynamicOptions bool `json:"dynamicOptions,omitzero"`
	// Group puts preferences with the same group under one heading.
	Group string `json:"group,omitempty"`
	// VisibleWhen shows the preference only when another one has a value.
	VisibleWhen *Condition `json:"visibleWhen,omitempty"`
	// Action is run when a PrefButton is clicked (usually ActionCallback).
	Action *ActionDo `json:"action,omitempty"`
}

// Condition compares the value of another preference.
type Condition struct {
	Pref   string         `json:"pref"`
	Equals jsontext.Value `json:"equals"`
}

// Option is a choice of a PrefSelect preference.
type Option struct {
	Value string `json:"value"`
	Title string `json:"title"`
}

// Icon references an image. Exactly one field should be set.
type Icon struct {
	// Name is a built-in icon name, e.g. "calculator", "clipboard", "app".
	Name string `json:"name,omitempty"`
	// Path is an image file on disk (PNG or SVG).
	Path string `json:"path,omitempty"`
	// Tint colors a built-in icon by meaning: TintSuccess, TintDanger,
	// TintAccent or TintMuted ("" for the text color).
	Tint IconTint `json:"tint,omitempty"`
}

// IconTint is the color of a built-in icon.
type IconTint string

const (
	TintSuccess IconTint = "success"
	TintDanger  IconTint = "danger"
	TintAccent  IconTint = "accent"
	TintMuted   IconTint = "muted"
)

// Item is a row in a list.
type Item struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Icon     Icon     `json:"icon,omitzero"`
	Keywords []string `json:"keywords,omitempty"`
	// Accessory is short text shown at the right edge (e.g. "Application").
	Accessory string `json:"accessory,omitempty"`
	// Group is the section the item is shown under in the root search.
	Group string `json:"group,omitempty"`
	// Score in (0, 1] marks the item as already matched; 0 lets the host match it.
	Score float64 `json:"score,omitzero"`
	// Actions; the first one is the primary action (Enter), the second the
	// secondary one (Ctrl+Enter).
	Actions []Action `json:"actions,omitempty"`
	Detail  *Detail  `json:"detail,omitempty"`
	// Arguments are typed inline before the primary action runs.
	Arguments []Argument `json:"arguments,omitempty"`
	// Sensitive marks an item whose content must never leave the launcher,
	// such as a clip copied from a password manager: it is shown to the user
	// but never returned to an AI using the launcher's plugins (docs/ai.md
	// §4.2).
	Sensitive bool `json:"sensitive,omitzero"`
}

// Detail is a preview shown next to the list.
type Detail struct {
	Markdown string     `json:"markdown,omitempty"`
	Image    string     `json:"image,omitempty"` // path to an image file
	Metadata []Metadata `json:"metadata,omitempty"`
}

// Metadata is a label/value row in a Detail.
type Metadata struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ActionStyle changes how an action is presented.
type ActionStyle string

const (
	StyleDefault ActionStyle = ""
	// StyleDestructive actions are shown in red and ask for confirmation.
	StyleDestructive ActionStyle = "destructive"
)

// Action is something the user can do with an item.
type Action struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Shortcut string      `json:"shortcut,omitempty"` // e.g. "Ctrl+Shift+C"
	Style    ActionStyle `json:"style,omitempty"`
	// EndsSession marks a destructive action that ends the Windows session
	// (shut down, restart, sign out): its confirmation warns that the
	// running background tasks will be interrupted.
	EndsSession bool `json:"endsSession,omitzero"`
	// ReadOnly marks an ActionCallback (or ActionRunCommand) that only
	// reads: it changes nothing on the computer or in the plugin's data
	// (e.g. "Refresh", "Show details"). An AI using the launcher's plugins
	// (docs/ai.md §4.2) runs such actions without asking the user; other
	// callbacks always ask first. Destructive actions are never read-only.
	ReadOnly bool     `json:"readOnly,omitzero"`
	Do       ActionDo `json:"do"`
}

// ActionKind is what the host does when an action runs.
type ActionKind string

const (
	ActionOpen       ActionKind = "Open"       // Target: path, URL, "shell:AppsFolder\<AUMID>"; Payload: OpenOptions
	ActionCopyText   ActionKind = "CopyText"   // Target: text
	ActionCopyImage  ActionKind = "CopyImage"  // Target: PNG file path
	ActionCopyFiles  ActionKind = "CopyFiles"  // Payload: []string
	ActionPaste      ActionKind = "Paste"      // Target: text pasted into the previous window, or Payload: PasteContent
	ActionPush       ActionKind = "Push"       // Target: view ID; Payload: passed to ViewRequest
	ActionRunCommand ActionKind = "RunCommand" // Target: command name of the same plugin
	ActionCallback   ActionKind = "Callback"   // Target: handler name; Payload: handler data
	// ActionRunProcess queues a background task (Host.RunProcess) without a
	// callback. Payload: RunProcessPayload. Needs PermExec.
	ActionRunProcess ActionKind = "RunProcess"
	// ActionShowTask opens Background Tasks at the task with ID Target, or
	// the list of all tasks when Target is empty.
	ActionShowTask ActionKind = "ShowTask"
)

// ActionDo is a serializable description of an action. The host executes
// built-in kinds itself, which keeps them observable and side-effect free in
// tests (see glctl --exec).
type ActionDo struct {
	Kind    ActionKind `json:"kind"`
	Target  string     `json:"target,omitempty"`
	Payload Payload    `json:"payload,omitempty"`
	// Values holds the form field values (form submit) or the argument
	// values (items with arguments), filled in by the host.
	Values map[string]jsontext.Value `json:"values,omitempty"`
	// Query is the search text of the view the action ran from, filled in
	// by the host for ActionCallback and ActionRunCommand: a view can use
	// its search field as an input (a chat's message) and clear it with
	// Result.ClearText.
	Query string `json:"query,omitempty"`
}

// OpenOptions is the Payload of ActionOpen.
type OpenOptions struct {
	Args   string `json:"args,omitempty"`
	Dir    string `json:"dir,omitempty"`
	Verb   string `json:"verb,omitempty"`  // "open" (default) or "runas"
	Reveal bool   `json:"reveal,omitzero"` // select the file in Explorer instead of opening it
}

// PasteContent is the Payload of ActionPaste for non-text content. When
// Target is set it takes precedence and is pasted as text.
type PasteContent struct {
	ImagePath string   `json:"imagePath,omitempty"` // PNG file
	Files     []string `json:"files,omitempty"`
}

// ViewKind selects the layout of a view.
type ViewKind string

const (
	ViewList ViewKind = "list"
	// ViewDetail shows View.Detail across the whole window (Markdown, then
	// its metadata), scrolled to its end as it grows, as a chat or a log.
	// Its items are not listed: the first one supplies the actions (Enter,
	// Ctrl+K and their shortcuts), and the search field stays an input
	// whose text reaches them in ActionDo.Query.
	ViewDetail ViewKind = "detail"
	ViewForm   ViewKind = "form"
)

// View is the content of a pushed view.
type View struct {
	Kind        ViewKind  `json:"kind"`
	Title       string    `json:"title,omitempty"`
	Placeholder string    `json:"placeholder,omitempty"`
	Sections    []Section `json:"sections,omitempty"`
	// FilterLocally asks the host to fuzzy-filter the items with the search
	// text instead of calling View again on every keystroke.
	FilterLocally bool `json:"filterLocally,omitzero"`
	// ShowDetail shows the selected item's Detail next to the list.
	ShowDetail bool    `json:"showDetail,omitzero"`
	Detail     *Detail `json:"detail,omitempty"`
	// EmptyText is shown when there are no items.
	EmptyText string `json:"emptyText,omitempty"`
	// Form is the content of a ViewForm.
	Form *Form `json:"form,omitempty"`
	// Escape, when set, runs on Escape instead of what Escape does by
	// default (clear the search field, then go back), e.g. "Stop" while an
	// answer is being written. Drop it when Escape should go back again.
	Escape *Action `json:"escape,omitempty"`
}

// Form is an input form. The host keeps the field values and passes them in
// ActionDo.Values when Submit runs.
type Form struct {
	Fields []Field `json:"fields"`
	Submit Action  `json:"submit"`
}

// FieldType is the input of a form Field.
type FieldType string

const (
	FieldText     FieldType = "text"
	FieldPassword FieldType = "password"
	FieldTextArea FieldType = "textarea"
	FieldNumber   FieldType = "number"
	FieldCheckbox FieldType = "checkbox"
	FieldDropdown FieldType = "dropdown"
	FieldDate     FieldType = "date" // value "2006-01-02"
	FieldFile     FieldType = "file" // value: a path
)

// Field is one input of a Form.
type Field struct {
	ID          string         `json:"id"`
	Type        FieldType      `json:"type"`
	Title       string         `json:"title"`
	Placeholder string         `json:"placeholder,omitempty"`
	Info        string         `json:"info,omitempty"`
	Default     jsontext.Value `json:"default,omitempty"`
	Options     []Option       `json:"options,omitempty"` // FieldDropdown
	Required    bool           `json:"required,omitzero"`
}

// Section is a titled group of items.
type Section struct {
	Title string `json:"title,omitempty"`
	Items []Item `json:"items"`
}

// Result is returned by ActionHandler.
type Result struct {
	Push      *ViewRequest `json:"push,omitempty"`
	Toast     *Toast       `json:"toast,omitempty"`
	Hide      bool         `json:"hide,omitzero"`
	PopToRoot bool         `json:"popToRoot,omitzero"`
	// Refresh re-runs the current view (e.g. after deleting an item).
	Refresh bool `json:"refresh,omitzero"`
	// FieldErrors keeps a form open and shows these messages by field ID.
	FieldErrors map[string]string `json:"fieldErrors,omitempty"`
	// ClearText empties the search field of the current view (e.g. after
	// sending the message typed in it) and pulls the view again.
	ClearText bool `json:"clearText,omitzero"`
	// Preferences stores values of the plugin's own preferences, as if the
	// user set them in Settings (e.g. an "Enable" button of a first-use
	// prompt). Password preferences cannot be set this way.
	Preferences map[string]jsontext.Value `json:"preferences,omitempty"`
	// Do is an action the host carries out after the handler returned, e.g.
	// ActionShowTask for a task the handler started, or ActionOpen. Only
	// the host's own kinds are allowed (not Callback or RunCommand). Its
	// outcome is merged: a Push or Hide of Do applies, Toast is kept.
	Do *ActionDo `json:"do,omitempty"`
}

// ToastStyle is the look of a Toast.
type ToastStyle string

const (
	ToastSuccess ToastStyle = "success"
	ToastFailure ToastStyle = "failure"
	ToastInfo    ToastStyle = "info"
)

// Toast is a short message shown at the bottom of the window.
type Toast struct {
	Style   ToastStyle `json:"style"`
	Title   string     `json:"title"`
	Message string     `json:"message,omitempty"`
	// Silent asks for no sound when the toast is shown as a Windows
	// notification (Host.Toast while the launcher is hidden).
	Silent bool `json:"silent,omitzero"`
}
