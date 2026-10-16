package ai

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"unicode/utf8"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Tool steps: the calls the model makes to golauncher's tools (docs/ai.md
// §4.1) and to the web tools, each shown as one line of the answer.

// launcherToolPrefix starts the names of golauncher's MCP tools as the
// backend reports them, e.g. "mcp__golauncher__search".
const launcherToolPrefix = "mcp__golauncher__"

// Limits of what is kept of a step and shown of it.
const (
	maxStepInput  = 2 << 10 // bytes of a step's input kept
	maxStepResult = 8 << 10 // bytes of a step's result kept
	maxStepText   = 200     // runes of a text (error, title, query) shown
)

// addStep records a tool event of the answer being written: a call is
// updated by its ID (its input may come later), a call without an ID is
// added, and a result completes its call. p.mu must be held.
func (ch *chat) addStep(ev backend.Event) {
	i := -1
	if ev.ToolID != "" {
		for j := range ch.steps {
			if ch.steps[j].ID == ev.ToolID {
				i = j
			}
		}
	}
	switch ev.Kind {
	case backend.EventTool:
		if i < 0 {
			ch.steps = append(ch.steps, ToolStep{ID: ev.ToolID})
			i = len(ch.steps) - 1
		}
		s := &ch.steps[i]
		if ev.Tool != "" {
			s.Name = ev.Tool
		}
		if ev.Input != "" {
			s.Input = capText(ev.Input, maxStepInput)
		}
	case backend.EventToolResult:
		if i < 0 {
			return // a result of no known call
		}
		s := &ch.steps[i]
		s.Result, s.Error, s.Done = capResult(ev.Result), ev.IsError, true
	}
}

// stepInput is what the step lines use of a tool's arguments.
type stepInput struct {
	Query   string `json:"query"`
	Plugin  string `json:"plugin"`
	Command string `json:"command"`
	Handle  string `json:"handle"`
	Action  string `json:"action"`
}

// stepResult is what the step lines use of a result of golauncher's tools:
// a JSON object, with status "error", "refused" or "declined" when the call
// failed.
type stepResult struct {
	Status  string           `json:"status"`
	Error   string           `json:"error"`
	Action  string           `json:"action"`
	Item    string           `json:"item"`
	Title   string           `json:"title"`
	Message string           `json:"message"`
	Query   string           `json:"query"`
	Items   []jsontext.Value `json:"items"`
	Plugins []jsontext.Value `json:"plugins"`
	More    int              `json:"more"`
	View    *stepView        `json:"view"`
}

// stepView is the view a command or action opened.
type stepView struct {
	Title string           `json:"title"`
	Items []jsontext.Value `json:"items"`
	More  int              `json:"more"`
}

// failed reports whether the result says the call failed.
func (r *stepResult) failed() bool {
	switch r.Status {
	case "error", "refused", "declined":
		return true
	}
	return false
}

// stepLine describes a step in one line (Markdown, shown in italics).
func (p *Plugin) stepLine(s ToolStep) string {
	tool, ok := strings.CutPrefix(s.Name, launcherToolPrefix)
	if !ok {
		return p.toolLine(s.Name)
	}
	var in stepInput
	if s.Input != "" {
		_ = json.Unmarshal([]byte(s.Input), &in)
	}
	var res stepResult
	parsed := s.Done && json.Unmarshal([]byte(s.Result), &res) == nil
	if s.Done && (s.Error || parsed && res.failed()) {
		return p.failedStepLine(tool, in, res, parsed, s.Result)
	}
	if !parsed {
		res = stepResult{} // pending, or a result of an unexpected shape
	}
	l := p.h.I18n()
	switch tool {
	case "search":
		line := l.T("step.searchAll")
		if q := stepText(in.Query); q != "" {
			line = l.T("step.search", "query", q)
		}
		switch {
		case !s.Done:
			return line + "…"
		case res.Items != nil:
			return line + " → " + l.N("step.results", len(res.Items)+res.More)
		}
		return line
	case "list_plugins":
		line := l.T("step.listPlugins")
		switch {
		case !s.Done:
			return line + "…"
		case res.Plugins != nil:
			return line + " → " + l.N("step.plugins", len(res.Plugins)+res.More)
		}
		return line
	case "run_command":
		if !s.Done {
			return l.T("step.runCommandPending", "command", stepText(firstOf(in.Command, in.Plugin)))
		}
		title := stepText(firstOf(res.Title, in.Command))
		if res.View != nil {
			return l.T("step.runCommand", "title", title) + " → " + l.N("step.items", len(res.View.Items)+res.View.More)
		}
		if title == "" {
			return l.T("step.doneGeneric")
		}
		return "✅ " + title
	case "get_detail":
		if !s.Done {
			return l.T("step.getDetailPending")
		}
		if title := stepText(res.Title); title != "" {
			return l.T("step.getDetail", "title", title)
		}
		return l.T("step.getDetailDone")
	case "run_action":
		if !s.Done {
			return l.T("step.runActionPending")
		}
		action, item := stepText(firstOf(res.Action, in.Action)), stepText(res.Item)
		switch {
		case action == "":
			return l.T("step.doneGeneric")
		case item == "":
			return "✅ " + action
		}
		return l.T("step.actionDone", "action", action, "item", item)
	}
	return "🔧 " + stepText(tool)
}

// failedStepLine describes a call of golauncher's tools that failed: the
// user declined it, the launcher refused it, or it failed.
func (p *Plugin) failedStepLine(tool string, in stepInput, res stepResult, parsed bool, raw string) string {
	l := p.h.I18n()
	msg := res.Error
	if !parsed {
		msg = raw
	}
	msg = stepText(msg)
	switch res.Status {
	case "declined":
		action, item := stepText(firstOf(res.Action, in.Action)), stepText(res.Item)
		switch {
		case action == "":
			return l.T("step.declinedPlain")
		case item == "":
			return l.T("step.declinedAction", "action", action)
		}
		return l.T("step.declined", "action", action, "item", item)
	case "refused":
		if msg == "" {
			return l.T("step.refused")
		}
		return "⛔ " + msg
	}
	if msg == "" {
		return l.T("step.failed", "tool", stepText(tool))
	}
	return l.T("step.error", "tool", stepText(tool), "error", msg)
}

// toolLine describes a call of a tool that is not one of golauncher's
// (also the tools of older records).
func (p *Plugin) toolLine(tool string) string {
	l := p.h.I18n()
	switch tool {
	case "WebSearch":
		return l.T("tool.webSearch")
	case "WebFetch":
		return l.T("tool.webFetch")
	}
	return "🔧 " + stepText(tool)
}

// firstOf returns the first of values that is not empty.
func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// mdEscaper escapes the characters that would change an italic Markdown
// line.
var mdEscaper = strings.NewReplacer(`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "<", `\<`, "~", `\~`, "|", `\|`)

// stepText prepares a value of a tool's arguments or result for a step line:
// one line, at most maxStepText runes, Markdown escaped.
func stepText(s string) string {
	return mdEscaper.Replace(shorten(strings.Join(strings.Fields(s), " "), maxStepText))
}

// shorten cuts s to n runes, ending it with "…" when cut.
func shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// capText cuts s to at most n bytes, at a rune boundary.
func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// capResult caps a step's result to maxStepResult bytes. A longer JSON
// object is reduced to what the step lines use, so that they stay right.
func capResult(s string) string {
	if len(s) <= maxStepResult {
		return s
	}
	var obj map[string]any
	if json.Unmarshal([]byte(s), &obj) == nil {
		if b, err := json.Marshal(summarize(obj)); err == nil && len(b) <= maxStepResult {
			return string(b)
		}
	}
	return capText(s, maxStepResult)
}

// summaryKeys are the text fields of a result the step lines use.
var summaryKeys = []string{"status", "error", "action", "item", "title", "message", "query"}

// summarize returns the part of a result object the step lines use: its
// text fields, shortened, and its lists reduced to a count ("more").
func summarize(obj map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range summaryKeys {
		if s, ok := obj[k].(string); ok {
			out[k] = shorten(s, maxStepText)
		}
	}
	more, _ := obj["more"].(float64)
	for _, k := range []string{"items", "plugins"} {
		if a, ok := obj[k].([]any); ok {
			out[k] = []any{}
			more += float64(len(a))
		}
	}
	if more > 0 {
		out["more"] = int(more)
	}
	if v, ok := obj["view"].(map[string]any); ok {
		out["view"] = summarize(v)
	}
	return out
}
