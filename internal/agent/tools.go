package agent

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Tool names.
const (
	ToolListPlugins = "list_plugins"
	ToolSearch      = "search"
	ToolRunCommand  = "run_command"
	ToolGetDetail   = "get_detail"
	ToolRunAction   = "run_action"
)

// Tool is a tool's definition, as MCP's tools/list returns it.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema jsontext.Value `json:"inputSchema"`
	Annotations *Annotations   `json:"annotations,omitempty"`
}

// Annotations are MCP's hints about a tool.
type Annotations struct {
	ReadOnlyHint    bool `json:"readOnlyHint"`
	DestructiveHint bool `json:"destructiveHint"`
	OpenWorldHint   bool `json:"openWorldHint"`
}

// Instructions is the MCP server's instructions: how to use the tools.
const Instructions = `golauncher is the user's keyboard launcher for Windows. Its plugins (apps, files, WinGet and Scoop packages, network tools, system commands, clipboard history when the user allows it, and more) are available through these tools:
1. list_plugins shows the plugins you may use and their commands; search finds items (apps, commands, results of plugins) by words, like typing in the launcher.
2. run_command runs a plugin command (e.g. one that opens a list such as a package search), optionally with arguments and a search query; its items come back with handles.
3. get_detail reads an item's details; run_action runs one of an item's actions (the first is the default). Use only handles returned by these tools; they expire after 10 minutes.
Every item lists its actions with a risk: "read" runs directly; "open" (open an app, a URL, a folder, copy text) usually runs directly; "change" and "destructive" ask the user to confirm in the launcher window first. If the user declines, do not retry or work around it; tell the user instead. Report only what the tool results say was done.`

// Tools returns the definitions of the tools.
func Tools() []Tool {
	ro := &Annotations{ReadOnlyHint: true}
	act := &Annotations{DestructiveHint: true}
	return []Tool{
		{Name: ToolListPlugins, Title: "List plugins", Annotations: ro,
			Description: "List the launcher plugins you may use, with their commands (name, title, whether it opens a list or acts at once, its arguments and risk). Pass plugin to list one plugin only.",
			InputSchema: schema(`{"type":"object","properties":{"plugin":{"type":"string","description":"A plugin ID, e.g. builtin.winget"}},"additionalProperties":false}`)},
		{Name: ToolSearch, Title: "Search the launcher", Annotations: ro,
			Description: "Search the launcher's root search (apps, commands and plugin results), or, with view, the list returned by run_command or run_action. Returns items with handles and their actions.",
			InputSchema: schema(`{"type":"object","properties":{"query":{"type":"string","description":"The words to search for"},"plugin":{"type":"string","description":"Only items of this plugin ID"},"view":{"type":"string","description":"A view handle (v…) to search in instead of the root search"},"limit":{"type":"integer","minimum":1,"maximum":50,"description":"Most items returned (default 20)"}},"additionalProperties":false}`)},
		{Name: ToolRunCommand, Title: "Run a launcher command", Annotations: act,
			Description: "Run a plugin command from list_plugins. A command that opens a list returns its items (filtered by query when given); a command that acts at once asks the user to confirm first.",
			InputSchema: schema(`{"type":"object","properties":{"plugin":{"type":"string","description":"The plugin ID"},"command":{"type":"string","description":"The command name"},"arguments":{"type":"object","additionalProperties":{"type":"string"},"description":"The command's arguments by name"},"query":{"type":"string","description":"Search text typed into the opened list"}},"required":["plugin","command"],"additionalProperties":false}`)},
		{Name: ToolGetDetail, Title: "Read an item's details", Annotations: ro,
			Description: "Read the details (text and metadata) of an item by its handle.",
			InputSchema: schema(`{"type":"object","properties":{"handle":{"type":"string","description":"An item handle (i…)"}},"required":["handle"],"additionalProperties":false}`)},
		{Name: ToolRunAction, Title: "Run an item's action", Annotations: act,
			Description: "Run an action of an item (by its handle; the first action when action is omitted), or submit a form (f… handle) with values. Actions that change something ask the user to confirm first; the result says whether it was done or declined.",
			InputSchema: schema(`{"type":"object","properties":{"handle":{"type":"string","description":"An item handle (i…) or a form handle (f…)"},"action":{"type":"string","description":"The action ID (default: the first action)"},"values":{"type":"object","additionalProperties":{"type":"string"},"description":"Form field values by field ID, or the item's argument values by name (checkbox: \"true\"/\"false\", date: YYYY-MM-DD, dropdown: the option value)"}},"required":["handle"],"additionalProperties":false}`)},
	}
}

func schema(s string) jsontext.Value { return jsontext.Value(s) }

// Result is a tool's result: JSON text, and whether it reports a failure.
type Result struct {
	Text    string
	IsError bool
}

// ErrUnknownTool is returned by Call for a tool that does not exist.
var ErrUnknownTool = errors.New("agent: unknown tool")

// Risk is how much an action can change (docs/ai.md §4.2).
type Risk string

const (
	RiskRead        Risk = "read"
	RiskOpen        Risk = "open"
	RiskChange      Risk = "change"
	RiskDestructive Risk = "destructive"
)

// RiskOf classifies an action.
func RiskOf(a plugin.Action) Risk {
	if a.Style == plugin.StyleDestructive {
		return RiskDestructive
	}
	switch a.Do.Kind {
	case plugin.ActionPush, plugin.ActionShowTask:
		return RiskRead
	case plugin.ActionOpen:
		var o plugin.OpenOptions
		if len(a.Do.Payload) > 0 && json.Unmarshal(a.Do.Payload, &o) == nil && strings.EqualFold(o.Verb, "runas") {
			return RiskChange // runs as administrator
		}
		return RiskOpen
	case plugin.ActionCopyText, plugin.ActionCopyImage, plugin.ActionCopyFiles:
		return RiskOpen
	case plugin.ActionCallback, plugin.ActionRunCommand:
		if a.ReadOnly {
			return RiskRead
		}
	}
	return RiskChange // Paste, RunProcess, callbacks, no-view commands, unknown kinds
}

// Call runs tool name with its JSON arguments.
func (s *Session) Call(ctx context.Context, name string, args jsontext.Value) (Result, error) {
	a := s.a
	if !slices.ContainsFunc(Tools(), func(t Tool) bool { return t.Name == name }) {
		return Result{}, fmt.Errorf("%w %q", ErrUnknownTool, name)
	}
	if len(args) == 0 || string(args) == "null" {
		args = jsontext.Value("{}")
	}
	cfg := a.o.Config.Get()
	if s.Client().external() && !cfg.Agent.External {
		a.record(s, name, "", OutcomeRefused, "external access is off")
		return fail("refused", "AI apps may not use golauncher: the user has not turned on Settings › Plugins › AI › Allow AI apps to use golauncher. Ask the user to turn it on if they want this."), nil
	}
	c := &call{s: s, cfg: cfg, tool: name}
	var res Result
	var err error
	switch name {
	case ToolListPlugins:
		var in struct {
			Plugin string `json:"plugin"`
		}
		if err = decode(args, &in); err == nil {
			c.summary = in.Plugin
			res = c.listPlugins(in.Plugin)
		}
	case ToolSearch:
		var in struct {
			Query  string `json:"query"`
			Plugin string `json:"plugin"`
			View   string `json:"view"`
			Limit  int    `json:"limit"`
		}
		if err = decode(args, &in); err == nil {
			c.summary = quote(truncate(in.Query, 80))
			res = c.search(ctx, truncate(in.Query, maxQuery), in.Plugin, in.View, limitOf(in.Limit))
		}
	case ToolRunCommand:
		var in struct {
			Plugin    string            `json:"plugin"`
			Command   string            `json:"command"`
			Arguments map[string]string `json:"arguments"`
			Query     string            `json:"query"`
		}
		if err = decode(args, &in); err == nil {
			c.summary = in.Plugin + "/" + in.Command
			res = c.runCommand(ctx, in.Plugin, in.Command, in.Arguments, truncate(in.Query, maxQuery))
		}
	case ToolGetDetail:
		var in struct {
			Handle string `json:"handle"`
		}
		if err = decode(args, &in); err == nil {
			res = c.getDetail(in.Handle)
		}
	case ToolRunAction:
		var in struct {
			Handle string            `json:"handle"`
			Action string            `json:"action"`
			Values map[string]string `json:"values"`
		}
		if err = decode(args, &in); err == nil {
			res = c.runAction(ctx, in.Handle, in.Action, in.Values)
		}
	}
	if err != nil {
		res = fail("error", "invalid arguments: "+err.Error())
		c.outcome, c.detail = OutcomeError, err.Error()
	}
	a.record(s, name, c.summary, cmp.Or(c.outcome, OutcomeOK), c.detail)
	return res, nil
}

// decode decodes tool arguments, rejecting unknown ones.
func decode(args jsontext.Value, v any) error {
	return json.Unmarshal(args, v, json.RejectUnknownMembers(true))
}

func quote(s string) string {
	if s == "" {
		return ""
	}
	return "“" + s + "”"
}

// call is one tool call in progress.
type call struct {
	s    *Session
	cfg  config.Config
	tool string

	summary string
	outcome Outcome
	detail  string
}

// fail is an error result.
func fail(status, msg string) Result {
	return Result{Text: marshal(map[string]any{"status": status, "error": msg}), IsError: true}
}

// failf records and returns an error result.
func (c *call) failf(outcome Outcome, format string, args ...any) Result {
	msg := fmt.Sprintf(format, args...)
	c.outcome, c.detail = outcome, msg
	status := "error"
	if outcome == OutcomeRefused {
		status = "refused"
	}
	return fail(status, msg)
}

func ok(v any) Result { return Result{Text: marshal(v)} }

func marshal(v any) string {
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return `{"status":"error","error":"internal error encoding the result"}`
	}
	return string(b)
}

// plugins returns the plugins the client may use, by ID.
func (c *call) plugins() map[string]core.PluginInfo {
	out := map[string]core.PluginInfo{}
	for _, p := range c.s.a.eng.Plugins() {
		if Exposed(p, c.cfg.Agent) {
			out[p.ID] = p
		}
	}
	return out
}

// pluginOf returns an exposed plugin, or a refusal.
func (c *call) pluginOf(id string) (core.PluginInfo, *Result) {
	p, found := c.plugins()[id]
	if !found {
		r := c.failf(OutcomeRefused, "plugin %q is not available to AI (it does not exist, is turned off, or the user did not allow AI to use it); see list_plugins", id)
		return p, &r
	}
	return p, nil
}

type argumentJSON struct {
	Name     string   `json:"name"`
	Type     string   `json:"type,omitempty"`
	Required bool     `json:"required,omitzero"`
	Options  []string `json:"options,omitempty"`
	Hint     string   `json:"hint,omitempty"`
}

func argumentsJSON(args []plugin.Argument) []argumentJSON {
	var out []argumentJSON
	for _, a := range args {
		aj := argumentJSON{Name: a.Name, Type: string(a.Type), Required: a.Required, Hint: truncate(a.Placeholder, maxText)}
		for _, o := range a.Options {
			aj.Options = append(aj.Options, o.Value)
		}
		out = append(out, aj)
	}
	return out
}

type commandJSON struct {
	Name      string         `json:"name"`
	Title     string         `json:"title"`
	Kind      string         `json:"kind"` // "list": opens a list; "action": acts at once
	Risk      Risk           `json:"risk"`
	Arguments []argumentJSON `json:"arguments,omitempty"`
}

type pluginJSON struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Commands    []commandJSON `json:"commands,omitempty"`
}

func (c *call) listPlugins(only string) Result {
	plugins := c.plugins()
	if only != "" {
		if _, r := c.pluginOf(only); r != nil {
			return *r
		}
	}
	out := []pluginJSON{}
	for _, p := range c.s.a.eng.Plugins() {
		if _, ok := plugins[p.ID]; !ok || only != "" && p.ID != only {
			continue
		}
		pj := pluginJSON{ID: p.ID, Name: p.Name, Description: truncate(p.Description, maxText)}
		for _, cmd := range p.Commands {
			e, ok := c.s.a.eng.CommandEntry(p.ID, cmd.Name)
			if !ok || len(e.Actions) == 0 {
				continue
			}
			kind := "list"
			if cmd.Mode == plugin.ModeNoView {
				kind = "action"
			}
			pj.Commands = append(pj.Commands, commandJSON{Name: cmd.Name, Title: cmd.Title, Kind: kind, Risk: RiskOf(e.Actions[0]),
				Arguments: argumentsJSON(e.Arguments)})
		}
		out = append(out, pj)
	}
	return ok(map[string]any{"plugins": out})
}

type actionJSON struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Risk  Risk   `json:"risk"`
}

type itemJSON struct {
	Handle    string         `json:"handle"`
	Title     string         `json:"title"`
	Subtitle  string         `json:"subtitle,omitempty"`
	Info      string         `json:"info,omitempty"`
	Section   string         `json:"section,omitempty"`
	Plugin    string         `json:"plugin"`
	Command   bool           `json:"command,omitzero"`
	Details   bool           `json:"details,omitzero"`
	Arguments []argumentJSON `json:"arguments,omitempty"`
	Actions   []actionJSON   `json:"actions"`
}

// item adds an entry to the session's table and describes it.
func (c *call) item(e core.Entry, section string, nav *core.Nav, query string) itemJSON {
	h := c.s.put(&handle{kind: kindItem, entry: e, nav: nav, query: query})
	it := itemJSON{
		Handle: h, Title: truncate(e.Title, maxTitle), Subtitle: truncate(subtitle(e), maxTitle), Info: truncate(info(e), maxTitle),
		Section: truncate(section, maxTitle), Plugin: e.Plugin, Command: strings.HasPrefix(e.ID, "cmd:"),
		Details:   e.Detail != nil && (e.Detail.Markdown != "" || len(metadataJSON(e.Detail.Metadata)) > 0),
		Arguments: argumentsJSON(e.Arguments), Actions: []actionJSON{},
	}
	if it.Command {
		it.Section = ""
	}
	for _, a := range e.Actions {
		it.Actions = append(it.Actions, actionJSON{ID: a.ID, Title: truncate(a.Title, maxTitle), Risk: RiskOf(a)})
	}
	return it
}

// subtitle is the subtitle of e an AI may see: none when it is sensitive
// (Item.SubtitleSensitive, e.g. a window title naming a document).
func subtitle(e core.Entry) string {
	if e.SubtitleSensitive {
		return ""
	}
	return e.Subtitle
}

// matchVisible scores query against what an AI may see of e, so that a
// search cannot probe a sensitive subtitle: 0 for no match.
func matchVisible(query string, e core.Entry) float64 {
	e.Subtitle = subtitle(e)
	return core.MatchEntry(query, e)
}

// info joins e's Accessory and the texts of its Accessories, e.g.
// "CPU 3.2% · 1.2 GB".
func info(e core.Entry) string {
	parts := make([]string, 0, 1+len(e.Accessories))
	if e.Accessory != "" {
		parts = append(parts, e.Accessory)
	}
	for _, a := range e.Accessories {
		if a.Text != "" {
			parts = append(parts, a.Text)
		}
	}
	return strings.Join(parts, " · ")
}

func (c *call) search(ctx context.Context, query, only, view string, limit int) Result {
	if view != "" {
		h, err := c.s.get(view)
		if err != nil {
			return c.failf(OutcomeError, "%v", err)
		}
		if h.kind != kindView {
			return c.failf(OutcomeError, "%q is not a view handle (v…)", view)
		}
		if _, r := c.pluginOf(h.nav.Plugin); r != nil {
			return *r
		}
		vj, err := c.view(ctx, *h.nav, query, limit)
		if err != nil {
			return c.failf(OutcomeError, "%v", err)
		}
		return ok(map[string]any{"query": query, "view": vj})
	}
	if strings.TrimSpace(query) == "" {
		return c.failf(OutcomeError, "query is required (or a view handle)")
	}
	if only != "" {
		if _, r := c.pluginOf(only); r != nil {
			return *r
		}
	}
	entries, err := c.s.a.eng.Search(ctx, query)
	if err != nil {
		return c.failf(OutcomeError, "%v", err)
	}
	plugins := c.plugins()
	items := []itemJSON{}
	more := 0
	for _, e := range entries {
		if _, ok := plugins[e.Plugin]; !ok || e.Sensitive || only != "" && e.Plugin != only {
			continue
		}
		if e.SubtitleSensitive && e.Score == 0 && matchVisible(query, e) == 0 {
			continue // found by its subtitle only, which the AI may not see
		}
		if len(items) == limit {
			more++
			continue
		}
		items = append(items, c.item(e, e.Group, nil, query))
	}
	res := map[string]any{"query": query, "items": items}
	if more > 0 {
		res["more"] = more
	}
	return ok(res)
}

// Settling of views that load in the background: after rendering, the view
// is rendered again when its plugin invalidates it within settleQuiet, at
// most settleRenders times and until settleMax.
var (
	settleQuiet   = 300 * time.Millisecond
	settleMax     = 3 * time.Second
	settleRenders = 3
)

// render renders a view, waiting for it to settle.
func (c *call) render(ctx context.Context, nav core.Nav) (plugin.View, error) {
	eng := c.s.a.eng
	inv := make(chan struct{}, 1)
	remove := eng.OnInvalidate(func(pluginID, viewID string) {
		if pluginID == nav.Plugin && (viewID == "" || viewID == nav.Request.ID) {
			select {
			case inv <- struct{}{}:
			default:
			}
		}
	})
	defer remove()
	v, err := eng.View(ctx, nav)
	if err != nil {
		return v, err
	}
	deadline := time.NewTimer(settleMax)
	defer deadline.Stop()
	for range settleRenders {
		quiet := time.NewTimer(settleQuiet)
		select {
		case <-inv:
			quiet.Stop()
			if v2, err := eng.View(ctx, nav); err == nil {
				v = v2
			}
			continue
		case <-quiet.C:
		case <-deadline.C:
			quiet.Stop()
		case <-ctx.Done():
			quiet.Stop()
			return v, ctx.Err()
		}
		break
	}
	return v, nil
}

type fieldJSON struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Title    string   `json:"title"`
	Required bool     `json:"required,omitzero"`
	Default  string   `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"`
	Info     string   `json:"info,omitempty"`
}

type formJSON struct {
	Handle string      `json:"handle"`
	Submit string      `json:"submit"`
	Risk   Risk        `json:"risk"`
	Fields []fieldJSON `json:"fields"`
}

type viewJSON struct {
	Handle   string     `json:"handle"`
	Title    string     `json:"title,omitempty"`
	Kind     string     `json:"kind"`
	Text     string     `json:"text,omitempty"`
	Items    []itemJSON `json:"items,omitempty"`
	More     int        `json:"more,omitzero"`
	Empty    string     `json:"empty,omitempty"`
	Form     *formJSON  `json:"form,omitempty"`
	Metadata []metaJSON `json:"metadata,omitempty"`
}

type metaJSON struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// formInfo is a form the client may submit.
type formInfo struct {
	form  plugin.Form
	title string
}

// view renders the view nav (a new instance of it on the first call) with
// query typed in its search field, and describes it.
func (c *call) view(ctx context.Context, nav core.Nav, query string, limit int) (viewJSON, error) {
	if nav.Request.Instance == "" {
		nav.Request.Instance = c.s.a.newInstance()
		c.s.addView(nav)
	}
	req := nav
	req.Request.Query = plugin.Query{Text: query}
	v, err := c.render(ctx, req)
	if err != nil {
		return viewJSON{}, err
	}
	vj := viewJSON{Title: truncate(v.Title, maxTitle), Kind: string(cmp.Or(v.Kind, plugin.ViewList))}
	vj.Handle = c.s.put(&handle{kind: kindView, nav: &nav, query: query})
	if v.Kind == plugin.ViewForm && v.Form != nil {
		vj.Form = c.formJSON(nav, v)
		return vj, nil
	}
	if v.Detail != nil {
		vj.Text = truncate(v.Detail.Markdown, maxMarkdown)
		vj.Metadata = metadataJSON(v.Detail.Metadata)
	}
	sections := core.ViewEntries(nav.Plugin, v)
	for _, s := range sections {
		var entries []core.Entry
		for _, e := range s.Items {
			if e.Sensitive {
				continue
			}
			if v.FilterLocally && strings.TrimSpace(query) != "" {
				if e.Rank = matchVisible(query, e); e.Rank == 0 {
					continue
				}
			}
			entries = append(entries, e)
		}
		if v.FilterLocally && strings.TrimSpace(query) != "" {
			slices.SortStableFunc(entries, func(a, b core.Entry) int { return cmp.Compare(b.Rank, a.Rank) })
		}
		for _, e := range entries {
			if len(vj.Items) == limit {
				vj.More++
				continue
			}
			vj.Items = append(vj.Items, c.item(e, s.Title, &nav, query))
		}
	}
	if len(vj.Items) == 0 && v.Kind != plugin.ViewDetail {
		vj.Empty = truncate(v.EmptyText, maxText)
	}
	return vj, nil
}

// metadataJSON describes the detail rows an AI may see: not the sensitive
// ones (Metadata.Sensitive, e.g. a command line).
func metadataJSON(md []plugin.Metadata) []metaJSON {
	var out []metaJSON
	for _, m := range md {
		if m.Sensitive {
			continue
		}
		out = append(out, metaJSON{Label: truncate(m.Label, maxTitle), Value: truncate(m.Value, maxText)})
	}
	return out
}

func (c *call) formJSON(nav core.Nav, v plugin.View) *formJSON {
	f := &formInfo{form: *v.Form, title: v.Title}
	h := c.s.put(&handle{kind: kindForm, nav: &nav, form: f})
	fj := &formJSON{Handle: h, Submit: v.Form.Submit.Title, Risk: RiskOf(v.Form.Submit), Fields: []fieldJSON{}}
	for _, fl := range v.Form.Fields {
		if fl.Type == plugin.FieldPassword {
			continue // never through an AI
		}
		fd := fieldJSON{ID: fl.ID, Type: string(fl.Type), Title: truncate(fl.Title, maxTitle), Required: fl.Required, Info: truncate(fl.Info, maxText)}
		if len(fl.Default) > 0 {
			var s any
			if json.Unmarshal(fl.Default, &s) == nil && s != nil {
				fd.Default = truncate(fmt.Sprint(s), maxText)
			}
		}
		for _, o := range fl.Options {
			fd.Options = append(fd.Options, o.Value)
		}
		fj.Fields = append(fj.Fields, fd)
	}
	return fj
}

func (c *call) getDetail(id string) Result {
	h, err := c.s.get(id)
	if err != nil {
		return c.failf(OutcomeError, "%v", err)
	}
	if h.kind != kindItem {
		return c.failf(OutcomeError, "%q is not an item handle (i…)", id)
	}
	if _, r := c.pluginOf(h.entry.Plugin); r != nil {
		return *r
	}
	e := h.entry
	c.summary = quote(truncate(e.Title, 80))
	res := map[string]any{"handle": id, "title": truncate(e.Title, maxTitle), "plugin": e.Plugin}
	if s := subtitle(e); s != "" {
		res["subtitle"] = truncate(s, maxTitle)
	}
	if d := e.Detail; d != nil {
		if d.Markdown != "" {
			res["text"] = truncate(d.Markdown, maxMarkdown)
		}
		if md := metadataJSON(d.Metadata); len(md) > 0 {
			res["metadata"] = md
		}
	}
	if _, has := res["text"]; !has && res["metadata"] == nil {
		res["note"] = "this item has no details"
	}
	return ok(res)
}

func (c *call) runCommand(ctx context.Context, pluginID, name string, args map[string]string, query string) Result {
	p, r := c.pluginOf(pluginID)
	if r != nil {
		return *r
	}
	e, found := c.s.a.eng.CommandEntry(pluginID, name)
	if !found || len(e.Actions) == 0 {
		return c.failf(OutcomeError, "plugin %q has no command %q; see list_plugins", pluginID, name)
	}
	action := e.Actions[0]
	if len(e.Arguments) > 0 {
		values, missing := core.ArgumentValues(e, args, action.Do.Values)
		if missing != "" {
			return c.failf(OutcomeError, "the command needs the argument %q", missing)
		}
		action.Do.Values = values
	}
	return c.execute(ctx, target{entry: e, action: action, title: e.Title, plugin: p.Name, query: query, command: true})
}

func (c *call) runAction(ctx context.Context, id, actionID string, values map[string]string) Result {
	h, err := c.s.get(id)
	if err != nil {
		return c.failf(OutcomeError, "%v", err)
	}
	pluginID := h.entry.Plugin
	if h.kind != kindItem {
		pluginID = h.nav.Plugin
	}
	p, r := c.pluginOf(pluginID)
	if r != nil {
		return *r
	}
	switch h.kind {
	case kindView:
		return c.failf(OutcomeError, "%q is a view handle: search in it, or act on its items", id)
	case kindForm:
		if actionID != "" && actionID != h.form.form.Submit.ID && actionID != "submit" {
			return c.failf(OutcomeError, "a form has only the action %q", h.form.form.Submit.ID)
		}
		for fid := range values {
			i := slices.IndexFunc(h.form.form.Fields, func(f plugin.Field) bool { return f.ID == fid })
			if i < 0 || h.form.form.Fields[i].Type == plugin.FieldPassword {
				return c.failf(OutcomeError, "the form has no field %q", fid)
			}
		}
		vals, errs := c.s.a.eng.FormValues(&h.form.form, values)
		if len(errs) > 0 {
			c.outcome, c.detail = OutcomeError, "invalid form values"
			return Result{Text: marshal(map[string]any{"status": "error", "error": "some form values are invalid", "fields": errs}), IsError: true}
		}
		a := h.form.form.Submit
		a.Do.Values = vals
		return c.execute(ctx, target{entry: core.Entry{Plugin: h.nav.Plugin}, action: a, title: a.Title, item: h.form.title, plugin: p.Name})
	}
	e := h.entry
	a, found := core.FindAction(e, actionID)
	if !found {
		var ids []string
		for _, x := range e.Actions {
			ids = append(ids, x.ID)
		}
		return c.failf(OutcomeError, "the item has no action %q; its actions: %s", actionID, strings.Join(ids, ", "))
	}
	if len(e.Arguments) > 0 {
		vals, missing := core.ArgumentValues(e, values, a.Do.Values)
		if missing != "" && a.ID == e.Actions[0].ID {
			return c.failf(OutcomeError, "the action needs the argument %q (pass it in values)", missing)
		}
		if missing == "" {
			a.Do.Values = vals
		}
	}
	if k := a.Do.Kind; (k == plugin.ActionCallback || k == plugin.ActionRunCommand) && a.Do.Query == "" {
		a.Do.Query = h.query
	}
	t := target{entry: e, action: a, title: a.Title, item: e.Title, plugin: p.Name}
	if strings.HasPrefix(e.ID, "cmd:") {
		t.title, t.item, t.command = e.Title, "", true
	}
	return c.execute(ctx, t)
}

// target is an action about to run.
type target struct {
	entry  core.Entry
	action plugin.Action
	// title, item and plugin name it for the user: the action's (or the
	// command's) title, the item's title and the plugin's name.
	title, item, plugin string
	query               string // typed into the view the action opens
	command             bool
}

// sessionKey identifies "the same action" for Allow for this session: the
// plugin, the action and, for callbacks, its handler.
func (t target) sessionKey() string {
	k := t.entry.Plugin + "\x00" + t.action.ID + "\x00" + string(t.action.Do.Kind)
	if kind := t.action.Do.Kind; kind == plugin.ActionCallback || kind == plugin.ActionRunCommand {
		k += "\x00" + t.action.Do.Target
	}
	return k
}

// execute applies the policy to an action and runs it.
func (c *call) execute(ctx context.Context, t target) Result {
	a := c.s.a
	risk := RiskOf(t.action)
	if t.item == t.title {
		t.item = "" // "Lock" "Lock": the item names the action
	}
	c.summary = t.title
	if t.item != "" {
		c.summary += " " + quote(t.item)
	}
	if t.plugin != "" {
		c.summary += " (" + t.plugin + ")"
	}
	ask := false
	switch risk {
	case RiskOpen:
		ask = c.cfg.Agent.AskOpen && !c.s.allowedFor(t.sessionKey())
	case RiskChange:
		ask = !c.s.allowedFor(t.sessionKey())
	case RiskDestructive:
		ask = true
	}
	if t.action.AskAlways && risk != RiskRead {
		ask = true
	}
	if ask {
		a.mu.Lock()
		confirm := a.confirm
		a.mu.Unlock()
		d := core.DecisionDeny
		if confirm != nil {
			cl := c.s.Client()
			d = confirm(ctx, core.ExternalRequest{
				Client: a.DisplayName(cl), Action: t.title, Item: t.item, Plugin: t.plugin, Message: t.action.ConfirmMessage,
				Destructive: risk == RiskDestructive, EndsSession: t.action.EndsSession,
				AllowSession: risk != RiskDestructive && !t.action.AskAlways, InPlace: cl.Chat,
			})
		}
		switch d {
		case core.DecisionDeny:
			c.outcome = OutcomeDeclined
			return Result{IsError: true, Text: marshal(map[string]any{"status": "declined", "action": t.title, "item": t.item,
				"error": "The user declined this action (or did not answer in time). Do not retry it or work around it unless the user asks."})}
		case core.DecisionAllowSession:
			c.s.allow(t.sessionKey())
		}
	}
	out, err := a.eng.Run(ctx, t.entry, t.action, false)
	if err != nil {
		return c.failf(OutcomeError, "%s failed: %v", t.title, err)
	}
	if len(out.FieldErrors) > 0 {
		c.outcome, c.detail = OutcomeError, "invalid form values"
		return Result{IsError: true, Text: marshal(map[string]any{"status": "error", "error": "the plugin rejected the form values", "fields": out.FieldErrors})}
	}
	res := map[string]any{"status": "done", "action": t.title}
	if t.command {
		res = map[string]any{"status": "done", "title": t.title}
	}
	if t.item != "" {
		res["item"] = t.item
	}
	if out.Toast != nil {
		msg := out.Toast.Title
		if out.Toast.Message != "" {
			msg += ": " + out.Toast.Message
		}
		res["message"] = truncate(msg, maxText)
		if out.Toast.Style == plugin.ToastFailure {
			res["status"] = "failed"
			c.outcome, c.detail = OutcomeError, msg
		}
	}
	if out.Push != nil {
		if _, exposed := c.plugins()[out.Push.Plugin]; exposed {
			vj, err := c.view(ctx, *out.Push, t.query, DefaultLimit)
			if err != nil {
				return c.failf(OutcomeError, "%v", err)
			}
			res["view"] = vj
		} else {
			res["note"] = "it opened a view of a plugin AI may not use"
		}
	}
	return ok(res)
}
