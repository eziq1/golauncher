package acp

import (
	"bytes"
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"sync"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// MCPTool is the tool name DefaultTool gives a call whose title is a JSON
// object: Gemini CLI titles the calls of MCP tools with their arguments and
// does not tell which server's tool it is.
const MCPTool = "mcp"

// DefaultTool names a tool call for the tool steps, from what the agent
// reports (agents differ; Gemini CLI 0.34 reports titles and kinds only):
//
//   - a title that is a JSON object is an MCP tool's arguments: Tool MCPTool,
//     Input that object (the server and tool are not known);
//   - kind "search" titled `Searching the web for: "q"`: Tool "WebSearch",
//     Input {"query":"q"};
//   - kind "fetch": Tool "WebFetch", Input {"url":…} when the title is
//     "Fetching content from: <url>";
//   - otherwise Tool is the title (or the kind).
//
// The call's raw input, when the agent reports one, is the Input.
func DefaultTool(c ToolCall) (tool, input string) {
	if raw := bytes.TrimSpace(c.RawInput); len(raw) > 0 && string(raw) != "null" {
		input = compact(raw)
	}
	title := strings.TrimSpace(c.Title)
	if strings.HasPrefix(title, "{") {
		var obj map[string]jsontext.Value
		if json.Unmarshal([]byte(title), &obj) == nil {
			return MCPTool, cmp.Or(input, compact([]byte(title)))
		}
	}
	switch c.Kind {
	case "search":
		if q, ok := strings.CutPrefix(title, `Searching the web for: "`); ok {
			return "WebSearch", cmp.Or(input, jsonObject("query", strings.TrimSuffix(q, `"`)))
		}
	case "fetch":
		if u, ok := strings.CutPrefix(title, "Fetching content from: "); ok {
			return "WebFetch", cmp.Or(input, jsonObject("url", strings.TrimSpace(u)))
		}
		return "WebFetch", input
	}
	return cmp.Or(title, c.Kind, "tool"), input
}

// jsonObject is {"key":"value"}.
func jsonObject(key, value string) string {
	b, _ := json.Marshal(map[string]string{key: value})
	return string(b)
}

// compact is raw JSON without insignificant space ("" when invalid).
func compact(raw []byte) string {
	v := jsontext.Value(bytes.Clone(raw))
	if v.Compact() != nil {
		return ""
	}
	return string(v)
}

// toolState is a tool call of the turn.
type toolState struct {
	call        ToolCall
	tool, input string // as last reported
	reported    bool
}

// decoder is the agentrt.Decoder of one turn.
type decoder struct {
	c        *codec
	io       agentrt.TurnIO
	promptID string
	tools    map[string]*toolState
	pr       agentrt.Progress
	res      *message // the response that ended the turn
}

// update is a session/update notification.
type update struct {
	SessionID string `json:"sessionId"`
	Update    struct {
		SessionUpdate string         `json:"sessionUpdate"`
		Content       jsontext.Value `json:"content"`
		toolFields    `json:",inline"`
	} `json:"update"`
}

// toolFields are the fields of a tool call (tool_call, tool_call_update,
// session/request_permission's toolCall); absent ones are nil.
type toolFields struct {
	ToolCallID string         `json:"toolCallId"`
	Title      *string        `json:"title"`
	Kind       *string        `json:"kind"`
	Status     *string        `json:"status"`
	RawInput   jsontext.Value `json:"rawInput"`
	RawOutput  jsontext.Value `json:"rawOutput"`
}

// contentBlock is a text (or other) content block.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// toolContent is an item of a tool call's content.
type toolContent struct {
	Type    string       `json:"type"`
	Content contentBlock `json:"content"`
	Path    string       `json:"path"`
}

// permissionParams are session/request_permission's parameters.
type permissionParams struct {
	SessionID string `json:"sessionId"`
	Options   []struct {
		OptionID string `json:"optionId"`
		Name     string `json:"name"`
		Kind     string `json:"kind"`
	} `json:"options"`
	ToolCall struct {
		toolFields `json:",inline"`
		Content    jsontext.Value `json:"content"`
	} `json:"toolCall"`
}

// Line implements agentrt.Decoder.
func (d *decoder) Line(raw []byte) (end, garbage bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false, false
	}
	m, ok := parse(raw)
	if !ok {
		return false, true
	}
	d.pr.Output = true
	switch {
	case m.Method != "" && m.hasID():
		d.request(m)
	case m.Method == "session/update":
		d.update(m.Params)
	case m.Method != "":
		// Other notifications (e.g. extensions' "_…" methods) are ignored.
	case string(bytes.TrimSpace(m.ID)) == d.promptID:
		d.res = &m
		return true, false
	}
	return false, false
}

// update handles a session/update notification. The agent's thoughts, the
// replayed user messages, plans, modes and commands are not shown.
func (d *decoder) update(params jsontext.Value) {
	var u update
	if json.Unmarshal(params, &u) != nil || u.SessionID != d.c.sessionID {
		return
	}
	switch u.Update.SessionUpdate {
	case "agent_message_chunk":
		var b contentBlock
		if json.Unmarshal(u.Update.Content, &b) == nil && b.Type == "text" && b.Text != "" {
			d.pr.Emitted = true
			d.io.Emit(backend.Event{Kind: backend.EventText, Text: b.Text})
		}
	case "tool_call", "tool_call_update":
		if u.Update.ToolCallID == "" {
			return
		}
		st := d.tool(u.Update.ToolCallID, u.Update.toolFields, u.Update.Content)
		d.report(st)
	}
}

// tool records what f and content tell about a tool call.
func (d *decoder) tool(id string, f toolFields, content jsontext.Value) *toolState {
	st := d.tools[id]
	if st == nil {
		st = &toolState{call: ToolCall{ID: id}}
		d.tools[id] = st
	}
	c := &st.call
	if f.Title != nil {
		c.Title = *f.Title
	}
	if f.Kind != nil {
		c.Kind = *f.Kind
	}
	if f.Status != nil {
		c.Status = *f.Status
	}
	if len(f.RawInput) > 0 {
		c.RawInput = bytes.Clone(f.RawInput)
	}
	if text, ok := contentText(content); ok {
		c.Result = text
	} else if len(f.RawOutput) > 0 && c.Result == "" {
		c.Result = compact(f.RawOutput)
	}
	return st
}

// contentText joins the text of a tool call's content; ok is false when
// there is none.
func contentText(raw jsontext.Value) (string, bool) {
	var items []toolContent
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return "", false
	}
	var parts []string
	for _, it := range items {
		switch {
		case it.Type == "content" && it.Content.Type == "text":
			parts = append(parts, it.Content.Text)
		case it.Type == "diff" && it.Path != "":
			parts = append(parts, "diff: "+it.Path)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n"), true
}

// report emits the tool call's step when its name or input changed, and its
// result once it ended.
func (d *decoder) report(st *toolState) {
	c := &st.call
	ended := !c.Done && (c.Status == "completed" || c.Status == "failed")
	if ended {
		c.Done, c.Failed = true, c.Status == "failed"
	}
	tool, input := d.c.cfg.Tool(*c)
	if !st.reported || tool != st.tool || input != st.input {
		st.tool, st.input, st.reported = tool, input, true
		d.pr.Emitted = true
		d.io.Emit(backend.Event{Kind: backend.EventTool, ToolID: c.ID, Tool: tool, Input: input})
	}
	if ended {
		result := c.Result
		if f := d.c.cfg.ToolResult; f != nil {
			result = f(*c)
		}
		d.io.Emit(backend.Event{Kind: backend.EventToolResult, ToolID: c.ID, Result: result, IsError: c.Failed})
	}
}

// request handles a request of the agent: session/request_permission
// becomes an EventPermission, every other method (fs/*, terminal/*, …) is
// refused.
func (d *decoder) request(m message) {
	if m.Method != "session/request_permission" {
		_ = d.io.Reply(errorResponse(m.ID, methodNotFound, "Method not found: "+m.Method))
		return
	}
	var p permissionParams
	if json.Unmarshal(m.Params, &p) != nil {
		_ = d.io.Reply(errorResponse(m.ID, -32602, "Invalid params"))
		return
	}
	id := bytes.Clone(m.ID)
	var options []backend.PermissionOption
	for _, o := range p.Options {
		options = append(options, backend.PermissionOption{ID: o.OptionID, Title: o.Name, Kind: o.Kind})
	}
	req := &backend.PermissionRequest{Options: options}
	if tc := p.ToolCall; tc.ToolCallID != "" {
		st := d.tool(tc.ToolCallID, tc.toolFields, tc.Content)
		d.report(st)
		req.ToolID, req.Tool, req.Input, req.Title, req.Kind = tc.ToolCallID, st.tool, st.input, st.call.Title, st.call.Kind
	}
	reply := d.io.Reply
	var once sync.Once
	req.Respond = func(optionID string) {
		once.Do(func() { _ = reply(response(id, permissionOutcome(options, optionID))) })
	}
	d.io.Emit(backend.Event{Kind: backend.EventPermission, Permission: req})
}

// permissionOutcome is the answer to a permission request: the option
// chosen; "" (or an option that was not offered) is the first reject_once
// option, else cancelled.
func permissionOutcome(options []backend.PermissionOption, optionID string) any {
	type selected struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId,omitempty"`
	}
	valid := false
	for _, o := range options {
		valid = valid || optionID != "" && o.ID == optionID
	}
	if !valid {
		optionID = ""
		for _, o := range options {
			if o.Kind == "reject_once" {
				optionID = o.ID
				break
			}
		}
	}
	if optionID == "" {
		return map[string]any{"outcome": selected{Outcome: "cancelled"}}
	}
	return map[string]any{"outcome": selected{Outcome: "selected", OptionID: optionID}}
}

// promptResult is session/prompt's result.
type promptResult struct {
	StopReason string `json:"stopReason"`
	Usage      *struct {
		InputTokens  int `json:"inputTokens"`
		OutputTokens int `json:"outputTokens"`
	} `json:"usage"`
}

// Outcome implements agentrt.Decoder.
func (d *decoder) Outcome(cancelled bool) agentrt.Outcome {
	if d.res == nil {
		return agentrt.Outcome{Kind: agentrt.Failed}
	}
	if e := d.res.Error; e != nil {
		switch {
		case cancelled:
			return agentrt.Outcome{Kind: agentrt.Cancelled}
		case lostSession(e):
			return agentrt.Outcome{Kind: agentrt.LostSession, Err: rpcErr(e)}
		}
		return agentrt.Outcome{Kind: agentrt.Failed, Err: rpcErr(e)}
	}
	var r promptResult
	_ = json.Unmarshal(d.res.Result, &r)
	switch r.StopReason {
	case "cancelled":
		return agentrt.Outcome{Kind: agentrt.Cancelled}
	case "refusal":
		return agentrt.Outcome{Kind: agentrt.Failed, Err: &backend.Error{Kind: backend.ErrFailed, Message: d.c.cfg.Info.Name + " refused to answer"}}
	}
	// end_turn, max_tokens, max_turn_requests: what was written is the answer.
	u := &backend.Usage{Model: d.c.model}
	if r.Usage != nil {
		u.InputTokens, u.OutputTokens = r.Usage.InputTokens, r.Usage.OutputTokens
	}
	return agentrt.Outcome{Kind: agentrt.Done, Usage: u}
}

// Progress implements agentrt.Decoder. The session exists since the
// handshake.
func (d *decoder) Progress() agentrt.Progress {
	p := d.pr
	p.Started = true
	return p
}
