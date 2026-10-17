package claudecode

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// outLine is a line of the CLI's stream-json output. Fields whose shape may
// vary are kept raw, so that an unexpected shape loses that field only.
type outLine struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype"`
	SessionID string          `json:"session_id"`
	Model     string          `json:"model"`
	Event     *streamEvent    `json:"event"`
	Message   *message        `json:"message"`
	Error     json.RawMessage `json:"error"`

	IsError        bool                       `json:"is_error"`
	Result         json.RawMessage            `json:"result"`
	Errors         json.RawMessage            `json:"errors"`
	Usage          *usage                     `json:"usage"`
	ModelUsage     map[string]json.RawMessage `json:"modelUsage"`
	TerminalReason string                     `json:"terminal_reason"`
	RateLimitInfo  *struct {
		Status string `json:"status"`
	} `json:"rate_limit_info"`

	// A control request of the CLI's (control_request).
	RequestID string `json:"request_id"`
	Request   *struct {
		Subtype  string `json:"subtype"`
		ToolName string `json:"tool_name"`
	} `json:"request"`
}

// streamEvent is the API streaming event inside a stream_event line.
type streamEvent struct {
	Type  string `json:"type"`
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Message *struct {
		ID string `json:"id"`
	} `json:"message"`
}

// message is the message of an assistant or user line.
type message struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

// block is a content block of an assistant or user message: text, a tool
// call (tool_use: ID, Name, Input) or a tool's result (tool_result:
// ToolUseID, Content, IsError).
type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// isToolUse reports whether b is a tool call.
func (b *block) isToolUse() bool { return b.Type == "tool_use" || b.Type == "server_tool_use" }

// toolEvent is the EventTool of a tool call block.
func (b *block) toolEvent() backend.Event {
	return backend.Event{Kind: backend.EventTool, Tool: b.Name, ToolID: b.ID, Input: compactJSON(b.Input)}
}

// resultText is the text of a tool_result block: its content is a string or
// a list of blocks, whose texts are joined.
func (b *block) resultText() string {
	if s := rawString(b.Content); s != "" {
		return s
	}
	var parts []block
	if json.Unmarshal(b.Content, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n")
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

// usage is a result's token usage.
type usage struct {
	InputTokens              int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

// syntheticModel is the model of messages the CLI makes up itself, such as
// error messages.
const syntheticModel = "<synthetic>"

// parser turns the output lines of one turn into events.
type parser struct {
	emit  func(backend.Event)
	reply func([]byte) error // writes to the CLI's input (nil: cannot)

	sawJSON     bool // a JSON line arrived
	sawInit     bool // the turn's init line arrived
	emitted     bool // text or a tool step was emitted
	model       string
	streamed    map[string]bool // messages that arrived as stream events
	streamedAny bool
	errCode     string // the error code of a synthetic assistant message
	errText     string // its text
	rateLimited bool   // a rate limit event said the request was not allowed
}

func newParser(emit func(backend.Event)) *parser {
	return &parser{emit: emit, streamed: map[string]bool{}}
}

// handle parses one line. It returns the result line that ends the turn, or
// garbage when the line is not JSON at all.
func (s *parser) handle(raw []byte) (res *outLine, garbage bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, false
	}
	if !json.Valid(raw) {
		return nil, true
	}
	s.sawJSON = true
	var l outLine
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, false // JSON of an unexpected shape: skipped
	}
	switch l.Type {
	case "system":
		if l.Subtype == "init" {
			s.sawInit = true
			if l.Model != "" {
				s.model = l.Model
			}
		}
	case "rate_limit_event":
		if l.RateLimitInfo != nil && l.RateLimitInfo.Status != "" && l.RateLimitInfo.Status != "allowed" && l.RateLimitInfo.Status != "allowed_warning" {
			s.rateLimited = true
		}
	case "stream_event":
		s.streamEvent(l.Event)
	case "assistant":
		s.assistant(&l)
	case "user":
		s.user(&l)
	case "control_request":
		s.controlRequest(&l)
	case "result":
		return &l, false
	}
	return nil, false
}

// controlRequest answers a control request of the CLI's. The only one
// expected is a permission request (can_use_tool), which a CLI without
// --permission-prompts might send; every tool the model may use is
// pre-approved, so it is denied, as --permission-prompts none would.
func (s *parser) controlRequest(l *outLine) {
	if l.Request == nil || l.RequestID == "" || s.reply == nil {
		return
	}
	resp := map[string]any{"subtype": "error", "request_id": l.RequestID, "error": "not supported by golauncher"}
	if l.Request.Subtype == "can_use_tool" {
		resp = map[string]any{"subtype": "success", "request_id": l.RequestID,
			"response": map[string]any{"behavior": "deny", "message": "golauncher allows only the tools it pre-approved"}}
	}
	if b, err := jsonLine(map[string]any{"type": "control_response", "response": resp}); err == nil {
		_ = s.reply(b)
	}
}

func (s *parser) streamEvent(e *streamEvent) {
	if e == nil {
		return
	}
	switch e.Type {
	case "message_start":
		s.streamedAny = true
		if e.Message != nil && e.Message.ID != "" {
			s.streamed[e.Message.ID] = true
		}
	case "content_block_start":
		if b := e.ContentBlock; b != nil && (b.Type == "tool_use" || b.Type == "server_tool_use") && b.Name != "" {
			s.emitted = true
			s.emit(backend.Event{Kind: backend.EventTool, Tool: b.Name, ToolID: b.ID})
		}
	case "content_block_delta":
		if d := e.Delta; d != nil && d.Type == "text_delta" && d.Text != "" {
			s.emitted = true
			s.emit(backend.Event{Kind: backend.EventText, Text: d.Text})
		}
	}
}

// assistant handles a complete assistant message. Its text already arrived
// as stream events, unless the CLI made the message up (an error message);
// its tool calls did too, but without their arguments, which are reported
// now with the call's ID.
func (s *parser) assistant(l *outLine) {
	m := l.Message
	if m == nil {
		return
	}
	code := rawString(l.Error)
	var blocks []block
	_ = json.Unmarshal(m.Content, &blocks)
	if m.ID != "" && s.streamed[m.ID] || m.ID == "" && s.streamedAny && m.Model != syntheticModel && code == "" {
		for _, b := range blocks {
			if b.isToolUse() && b.ID != "" && b.Name != "" && len(b.Input) > 0 {
				s.emitted = true
				s.emit(b.toolEvent())
			}
		}
		return
	}
	var text strings.Builder
	for _, b := range blocks {
		switch {
		case b.Type == "text":
			text.WriteString(b.Text)
		case b.isToolUse():
			if b.Name != "" && code == "" {
				s.emitted = true
				s.emit(b.toolEvent())
			}
		}
	}
	if code != "" {
		// An error: the result reports it, with the code kept to classify it.
		s.errCode, s.errText = code, text.String()
		return
	}
	if text.Len() > 0 {
		s.emitted = true
		s.emit(backend.Event{Kind: backend.EventText, Text: text.String()})
	}
}

// user handles a user line: the results of the model's tool calls. Other
// user lines (such as "[Request interrupted by user]") are ignored.
func (s *parser) user(l *outLine) {
	if l.Message == nil {
		return
	}
	var blocks []block
	if json.Unmarshal(l.Message.Content, &blocks) != nil {
		return // a plain string: not a tool result
	}
	for _, b := range blocks {
		if b.Type != "tool_result" || b.ToolUseID == "" {
			continue
		}
		s.emitted = true
		s.emit(backend.Event{Kind: backend.EventToolResult, ToolID: b.ToolUseID, Result: b.resultText(), IsError: b.IsError})
	}
}

// resultText is the text of a result: its result, else its errors.
func resultText(l *outLine) string {
	if t := rawString(l.Result); t != "" {
		return t
	}
	var errs []json.RawMessage
	if json.Unmarshal(l.Errors, &errs) == nil {
		var parts []string
		for _, e := range errs {
			if t := rawString(e); t != "" {
				parts = append(parts, t)
			} else if len(e) > 0 && string(e) != "null" {
				parts = append(parts, string(e))
			}
		}
		return strings.Join(parts, "\n")
	}
	return rawString(l.Errors)
}

// usageOf returns the usage of a result.
func (s *parser) usageOf(l *outLine) *backend.Usage {
	u := &backend.Usage{Model: s.model}
	if l.Usage != nil {
		u.InputTokens = l.Usage.InputTokens + l.Usage.CacheCreationInputTokens + l.Usage.CacheReadInputTokens
		u.OutputTokens = l.Usage.OutputTokens
	}
	if u.Model == "" {
		best := -1
		for name, raw := range l.ModelUsage {
			var mu struct {
				OutputTokens int `json:"outputTokens"`
			}
			_ = json.Unmarshal(raw, &mu)
			if mu.OutputTokens > best || (mu.OutputTokens == best && name < u.Model) {
				best, u.Model = mu.OutputTokens, name
			}
		}
	}
	return u
}

// classify returns the kind of an error from the CLI's error code and text.
func classify(code, text string, rateLimited bool) backend.ErrorKind {
	t := strings.ToLower(text)
	switch {
	case code == "authentication_failed", strings.Contains(t, "/login"), strings.Contains(t, "not logged in"), strings.Contains(t, "invalid api key"):
		return backend.ErrNotLoggedIn
	case code == "rate_limit", code == "billing_error", rateLimited, strings.Contains(t, "usage limit"), strings.Contains(t, "limit reached"):
		return backend.ErrQuota
	}
	return backend.ErrFailed
}

// rawString returns raw as a string if it is a JSON string.
func rawString(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// lostSessionText is the CLI's error when the session to resume is unknown.
const lostSessionText = "No conversation found with session ID"

// sessionInUse reports whether text is the CLI's error for a session ID
// that exists already (matched loosely: "… is already in use").
func sessionInUse(text string) bool {
	t := strings.ToLower(text)
	return strings.Contains(t, "already") && (strings.Contains(t, "in use") || strings.Contains(t, "exists"))
}

// decoder is the agentrt.Decoder of a turn: the parser, and the result line
// that ended the turn.
type decoder struct {
	p   *parser
	res *outLine
}

// Line implements agentrt.Decoder.
func (d *decoder) Line(raw []byte) (end, garbage bool) {
	res, garbage := d.p.handle(raw)
	if res != nil {
		d.res = res
		return true, false
	}
	return false, garbage
}

// Progress implements agentrt.Decoder. The session ID of init lines is the
// one golauncher chose (--session-id), so it is not reported.
func (d *decoder) Progress() agentrt.Progress {
	return agentrt.Progress{Output: d.p.sawJSON, Started: d.p.sawInit, Emitted: d.p.emitted}
}

// Outcome implements agentrt.Decoder: it maps the result line that ended
// the turn.
func (d *decoder) Outcome(cancelled bool) agentrt.Outcome {
	st, res := d.p, d.res
	text := resultText(res)
	if res.TerminalReason == "aborted_streaming" || cancelled && res.Subtype == "error_during_execution" {
		return agentrt.Outcome{Kind: agentrt.Cancelled}
	}
	if strings.Contains(text, lostSessionText) {
		return agentrt.Outcome{Kind: agentrt.LostSession}
	}
	if !res.IsError && st.errCode == "" {
		if !st.emitted && text != "" {
			st.emitted = true
			st.emit(backend.Event{Kind: backend.EventText, Text: text})
		}
		return agentrt.Outcome{Kind: agentrt.Done, Usage: st.usageOf(res)}
	}
	if sessionInUse(text) {
		return agentrt.Outcome{Kind: agentrt.SessionInUse}
	}
	if text == "" {
		text = st.errText
	}
	if text == "" {
		text = res.Subtype
	}
	return agentrt.Outcome{Kind: agentrt.Failed, Err: &backend.Error{Kind: classify(st.errCode, text, st.rateLimited), Message: text}}
}

// userLine is the stream-json input line of a user message. A message that
// starts with "/" gets a leading space: the CLI would take it for a slash
// command.
func userLine(text string) any {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	return struct {
		Type    string `json:"type"`
		Message msg    `json:"message"`
	}{"user", msg{"user", promptText(text)}}
}

// promptText is text as sent to the CLI.
func promptText(text string) string {
	if strings.HasPrefix(text, "/") {
		return " " + text
	}
	return text
}

// interruptLine is the control request that stops the running answer.
func interruptLine(id string) any {
	type req struct {
		Subtype string `json:"subtype"`
	}
	return struct {
		Type      string `json:"type"`
		RequestID string `json:"request_id"`
		Request   req    `json:"request"`
	}{"control_request", id, req{"interrupt"}}
}
