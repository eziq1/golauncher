package codex

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// event is a line of `codex exec --json`: thread.started (ThreadID),
// turn.started, item.started / item.updated / item.completed (Item),
// turn.completed (Usage), turn.failed (Error) and error (Message).
type event struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
	Item     *item  `json:"item"`
	Usage    *usage `json:"usage"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
	Message string `json:"message"`
}

// item is a thread item. Fields whose shape may vary are kept raw, so that
// an unexpected shape loses that field only.
type item struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	// agent_message, reasoning
	Text string `json:"text"`
	// mcp_tool_call
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    *struct {
		Content           json.RawMessage `json:"content"`
		StructuredContent json.RawMessage `json:"structured_content"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	// web_search
	Query  string `json:"query"`
	Action *struct {
		Query   string   `json:"query"`
		Queries []string `json:"queries"`
	} `json:"action"`
	// command_execution
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	ExitCode         *int   `json:"exit_code"`
	// file_change
	Changes json.RawMessage `json:"changes"`
	// error
	Message string `json:"message"`
}

// usage is a turn's token usage. Cached input tokens are part of the input
// tokens, reasoning tokens part of the output tokens (OpenAI's counting).
type usage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
}

// Tool names of the steps of Codex's own tools (golauncher's MCP tools are
// "mcp__<server>__<tool>", as with Claude Code). Shell and FileChange never
// happen with the isolation flags; they are shown if they do.
const (
	toolWebSearch  = "WebSearch"
	toolShell      = "Shell"
	toolFileChange = "FileChange"
)

// decoder turns the events of one turn into backend events.
type decoder struct {
	emit func(backend.Event)

	output, started, emitted bool
	sessionID                string

	texts      map[string]string // agent message text emitted, by item ID
	lastText   string            // the item whose text was emitted last
	endsInLine bool              // the text emitted so far ends with a line break
	tools      map[string]string // the Input reported of each tool step, by item ID
	results    map[string]bool   // tool steps whose result was reported

	end     *event
	lastErr string // the last error that was not a retry notice
}

func newDecoder(emit func(backend.Event)) *decoder {
	return &decoder{emit: emit, texts: map[string]string{}, tools: map[string]string{}, results: map[string]bool{}}
}

// Line implements agentrt.Decoder.
func (d *decoder) Line(raw []byte) (end, garbage bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false, false
	}
	if !json.Valid(raw) {
		return false, true
	}
	d.output = true
	var ev event
	if json.Unmarshal(raw, &ev) != nil {
		return false, false // JSON of an unexpected shape: skipped
	}
	switch ev.Type {
	case "thread.started":
		if ev.ThreadID != "" {
			d.sessionID = ev.ThreadID
			d.started = true
		}
	case "item.started", "item.updated", "item.completed":
		if ev.Item != nil {
			d.item(ev.Item, ev.Type == "item.completed")
		}
	case "error":
		if ev.Message != "" && !transient(ev.Message) {
			d.lastErr = ev.Message
		}
	case "turn.completed", "turn.failed":
		d.end = &ev
		return true, false
	}
	return false, false
}

// transient reports whether an error event is only a notice that Codex
// retries ("Reconnecting... 2/5 (…)", "…; retrying 1/5 in 188ms…").
func transient(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "retrying") || strings.HasPrefix(m, "reconnecting")
}

// item handles a thread item's event; done is set for item.completed.
func (d *decoder) item(it *item, done bool) {
	switch it.Type {
	case "agent_message":
		d.text(it)
	case "mcp_tool_call":
		d.tool(it.ID, "mcp__"+it.Server+"__"+it.Tool, argsJSON(it.Arguments))
		if done || it.Status == "completed" || it.Status == "failed" {
			res, isErr := mcpResult(it)
			d.result(it.ID, res, isErr)
		}
	case "web_search":
		d.tool(it.ID, toolWebSearch, queryJSON(it))
		if done {
			d.result(it.ID, "", it.Status == "failed")
		}
	case "command_execution":
		in := ""
		if it.Command != "" {
			in = marshal(map[string]string{"command": it.Command})
		}
		d.tool(it.ID, toolShell, in)
		if done {
			failed := it.Status == "failed" || it.Status == "declined" || it.ExitCode != nil && *it.ExitCode != 0
			d.result(it.ID, it.AggregatedOutput, failed)
		}
	case "file_change":
		in := ""
		if c := compactJSON(it.Changes); c != "" {
			in = `{"changes":` + c + `}`
		}
		d.tool(it.ID, toolFileChange, in)
		if done {
			d.result(it.ID, "", it.Status != "completed")
		}
	case "error":
		// A notice such as a fallback to another transport; the turn goes on.
		if it.Message != "" {
			d.lastErr = it.Message
		}
	}
	// reasoning, todo_list and others are not shown.
}

// text emits what is new in an agent message: item.updated may carry the
// text so far, item.completed carries all of it. Messages are separated by
// a blank line.
func (d *decoder) text(it *item) {
	if it.Text == "" {
		return
	}
	prev, seen := d.texts[it.ID]
	var add string
	switch {
	case !seen:
		add = it.Text
		if d.lastText != "" && !d.endsInLine {
			add = "\n\n" + add
		}
	case strings.HasPrefix(it.Text, prev):
		add = it.Text[len(prev):]
	default:
		return // changed, not grown: what was shown stays
	}
	d.texts[it.ID] = it.Text
	if add == "" {
		return
	}
	d.lastText = it.ID
	d.endsInLine = strings.HasSuffix(add, "\n")
	d.emitted = true
	d.emit(backend.Event{Kind: backend.EventText, Text: add})
}

// tool reports a tool step: once when it starts, and again with the same ID
// when its input becomes known.
func (d *decoder) tool(id, name, input string) {
	prev, seen := d.tools[id]
	if seen && (input == "" || input == prev) {
		return
	}
	d.tools[id] = input
	d.emitted = true
	d.emit(backend.Event{Kind: backend.EventTool, Tool: name, ToolID: id, Input: input})
}

// result reports a tool step's result, once.
func (d *decoder) result(id, text string, isErr bool) {
	if d.results[id] {
		return
	}
	d.results[id] = true
	d.emitted = true
	d.emit(backend.Event{Kind: backend.EventToolResult, ToolID: id, Result: text, IsError: isErr})
}

// mcpResult is the text of an MCP tool call's result: the texts of its
// content blocks, else its structured content, else the error message.
func mcpResult(it *item) (string, bool) {
	isErr := it.Status == "failed" || it.Error != nil
	if it.Error != nil && it.Error.Message != "" {
		return it.Error.Message, true
	}
	if it.Result == nil {
		return "", isErr
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(it.Result.Content, &blocks) == nil {
		var texts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				texts = append(texts, b.Text)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n"), isErr
		}
	}
	return compactJSON(it.Result.StructuredContent), isErr
}

// argsJSON is the JSON text of a tool call's arguments, which Codex reports
// as an object (or as a string holding one).
func argsJSON(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if json.Valid([]byte(s)) {
			return compactJSON(json.RawMessage(s))
		}
		return ""
	}
	return compactJSON(raw)
}

// queryJSON is the input of a web search step, {"query": …}, "" while the
// query is not known.
func queryJSON(it *item) string {
	q := it.Query
	if q == "" && it.Action != nil {
		q = it.Action.Query
		if q == "" && len(it.Action.Queries) > 0 {
			q = strings.Join(it.Action.Queries, " | ")
		}
	}
	if q == "" {
		return ""
	}
	return marshal(map[string]string{"query": q})
}

// marshal is v as compact JSON without HTML escapes.
func marshal(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return ""
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// compactJSON returns raw as compact JSON text, "" when it is missing or
// not JSON.
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return ""
	}
	return buf.String()
}

// Progress implements agentrt.Decoder.
func (d *decoder) Progress() agentrt.Progress {
	return agentrt.Progress{Output: d.output, Started: d.started, Emitted: d.emitted, SessionID: d.sessionID}
}

// Outcome implements agentrt.Decoder: it maps turn.completed or turn.failed.
func (d *decoder) Outcome(cancelled bool) agentrt.Outcome {
	ev := d.end
	if ev.Type == "turn.completed" {
		u := &backend.Usage{}
		if ev.Usage != nil {
			u.InputTokens = ev.Usage.InputTokens
			u.OutputTokens = ev.Usage.OutputTokens
		}
		return agentrt.Outcome{Kind: agentrt.Done, Usage: u}
	}
	if cancelled {
		return agentrt.Outcome{Kind: agentrt.Cancelled}
	}
	msg := ""
	if ev.Error != nil {
		msg = ev.Error.Message
	}
	if msg == "" {
		msg = d.lastErr
	}
	if msg == "" {
		msg = "Codex could not answer"
	}
	if lostSession(msg) {
		return agentrt.Outcome{Kind: agentrt.LostSession}
	}
	return agentrt.Outcome{Kind: agentrt.Failed, Err: &backend.Error{Kind: classify(msg), Message: msg}}
}

// lostSession reports whether text is Codex's error for a thread to resume
// that it does not have ("thread/resume failed: no rollout found for thread
// id …").
func lostSession(text string) bool {
	t := strings.ToLower(text)
	return strings.Contains(t, "no rollout found") || strings.Contains(t, "not materialized")
}

// status429 matches the HTTP status 429 (not digits inside a request ID).
var status429 = regexp.MustCompile(`\b429\b`)

// classify returns the kind of a Codex error from its text:
//
//   - not signed in: "401 Unauthorized", "Not logged in", a token that could
//     not be refreshed ("… log out and sign in again");
//   - the usage limit: "You've hit your usage limit …", 429, rate limits,
//     quota;
//   - too old: "… requires a newer version of Codex";
//   - anything else, such as a model the ChatGPT account cannot use ("The
//     '…' model is not supported when using Codex with a ChatGPT account"),
//     failed with the CLI's text.
func classify(text string) backend.ErrorKind {
	t := strings.ToLower(text)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(t, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("401 unauthorized", "unauthorized", "not logged in", "codex login", "sign in again", "log in again",
		"authentication", "access token could not be refreshed"):
		return backend.ErrNotLoggedIn
	case has("usage limit", "rate limit", "too many requests", "quota") || status429.MatchString(t):
		return backend.ErrQuota
	case has("newer version of codex", "requires a newer version", "please upgrade codex", "update codex"):
		return backend.ErrVersion
	}
	return backend.ErrFailed
}
