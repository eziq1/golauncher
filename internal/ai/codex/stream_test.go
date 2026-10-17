package codex

import (
	"fmt"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// decode feeds lines to a decoder and returns the events, whether a line
// ended the turn, and the decoder.
func decode(lines ...string) ([]backend.Event, bool, *decoder) {
	var evs []backend.Event
	d := newDecoder(func(ev backend.Event) { evs = append(evs, ev) })
	for _, l := range lines {
		if end, _ := d.Line([]byte(l)); end {
			return evs, true, d
		}
	}
	return evs, false, d
}

func TestDecodeSteps(t *testing.T) {
	evs, end, d := decode(
		`{"type":"thread.started","thread_id":"0190aaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"**Planning**"}}`,
		`{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"dir C:\\","aggregated_output":"","exit_code":null,"status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"dir C:\\","aggregated_output":"denied","exit_code":1,"status":"failed"}}`,
		`{"type":"item.completed","item":{"id":"item_2","type":"file_change","changes":[{"path":"C:\\x.txt","kind":"add"}],"status":"failed"}}`,
		`{"type":"item.started","item":{"id":"item_3","type":"mcp_tool_call","server":"golauncher","tool":"get_detail","arguments":"{\"handle\": \"h2\"}","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"item_3","type":"mcp_tool_call","server":"golauncher","tool":"get_detail","arguments":"{\"handle\": \"h2\"}","result":{"content":[],"structured_content":{"title":"T"}},"status":"completed"}}`,
		`{"type":"item.updated","item":{"id":"item_4","type":"todo_list","items":[{"text":"a","completed":false}]}}`,
		`{"type":"item.completed","item":{"id":"item_5","type":"agent_message","text":"Done"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1200,"cached_input_tokens":1000,"output_tokens":30,"reasoning_output_tokens":20}}`,
	)
	if !end {
		t.Fatal("no end")
	}
	want := []backend.Event{
		{Kind: backend.EventTool, Tool: "Shell", ToolID: "item_1", Input: `{"command":"dir C:\\"}`},
		{Kind: backend.EventToolResult, ToolID: "item_1", Result: "denied", IsError: true},
		{Kind: backend.EventTool, Tool: "FileChange", ToolID: "item_2", Input: `{"changes":[{"path":"C:\\x.txt","kind":"add"}]}`},
		{Kind: backend.EventToolResult, ToolID: "item_2", IsError: true},
		{Kind: backend.EventTool, Tool: "mcp__golauncher__get_detail", ToolID: "item_3", Input: `{"handle":"h2"}`},
		{Kind: backend.EventToolResult, ToolID: "item_3", Result: `{"title":"T"}`},
		{Kind: backend.EventText, Text: "Done"},
	}
	if fmt.Sprint(evs) != fmt.Sprint(want) {
		t.Errorf("events\n %+v\nwant\n %+v", evs, want)
	}
	p := d.Progress()
	if !p.Output || !p.Started || !p.Emitted || p.SessionID != "0190aaaa-bbbb-7ccc-8ddd-eeeeeeeeeeee" {
		t.Errorf("progress %+v", p)
	}
	o := d.Outcome(false)
	if o.Kind != agentrt.Done || o.Usage == nil || o.Usage.InputTokens != 1200 || o.Usage.OutputTokens != 30 {
		t.Errorf("outcome %+v", o)
	}
}

func TestDecodeText(t *testing.T) {
	// Growing text, a change that is not growth (kept as shown), a second
	// message after a line break.
	evs, _, _ := decode(
		`{"type":"item.started","item":{"id":"a","type":"agent_message","text":""}}`,
		`{"type":"item.updated","item":{"id":"a","type":"agent_message","text":"Hel"}}`,
		`{"type":"item.updated","item":{"id":"a","type":"agent_message","text":"Hello\n"}}`,
		`{"type":"item.updated","item":{"id":"a","type":"agent_message","text":"Changed"}}`,
		`{"type":"item.completed","item":{"id":"a","type":"agent_message","text":"Hello\n"}}`,
		`{"type":"item.completed","item":{"id":"b","type":"agent_message","text":"World"}}`,
		`not json at all`,
		`{"type":"item.completed","item":{"id":"c","type":"agent_message","text":"!"}}`,
	)
	var text strings.Builder
	for _, ev := range evs {
		text.WriteString(ev.Text)
	}
	if text.String() != "Hello\nWorld\n\n!" {
		t.Errorf("text %q", text.String())
	}
	if _, garbage := newDecoder(func(backend.Event) {}).Line([]byte("WARN not json")); !garbage {
		t.Error("a line that is not JSON is not garbage")
	}
}

func TestDecodeFailures(t *testing.T) {
	for _, tt := range []struct {
		lines     []string
		cancelled bool
		kind      agentrt.OutcomeKind
		errKind   backend.ErrorKind
		msg       string
	}{
		{lines: []string{
			`{"type":"error","message":"Reconnecting... 1/5 (unexpected status 401 Unauthorized: Missing bearer (fake))"}`,
			`{"type":"error","message":"unexpected status 401 Unauthorized: Missing bearer (fake)"}`,
			`{"type":"turn.failed","error":{"message":"unexpected status 401 Unauthorized: Missing bearer (fake)"}}`,
		}, kind: agentrt.Failed, errKind: backend.ErrNotLoggedIn, msg: "unexpected status 401 Unauthorized: Missing bearer (fake)"},
		{lines: []string{
			`{"type":"error","message":"stream error; retrying 1/5 in 188ms…"}`,
			`{"type":"error","message":"The 'fake' model is not supported when using Codex with a ChatGPT account."}`,
			`{"type":"turn.failed","error":{}}`,
		}, kind: agentrt.Failed, errKind: backend.ErrFailed, msg: "The 'fake' model is not supported when using Codex with a ChatGPT account."},
		{lines: []string{`{"type":"turn.failed","error":{"message":"exceeded retry limit, last status: 429 Too Many Requests"}}`},
			kind: agentrt.Failed, errKind: backend.ErrQuota, msg: "exceeded retry limit, last status: 429 Too Many Requests"},
		{lines: []string{`{"type":"turn.failed","error":{"message":"thread/resume failed: no rollout found for thread id x"}}`},
			kind: agentrt.LostSession},
		{lines: []string{`{"type":"turn.failed","error":{"message":"interrupted"}}`}, cancelled: true, kind: agentrt.Cancelled},
	} {
		_, end, d := decode(tt.lines...)
		if !end {
			t.Fatalf("%q: no end", tt.lines)
		}
		o := d.Outcome(tt.cancelled)
		if o.Kind != tt.kind {
			t.Errorf("%q: outcome %+v", tt.lines, o)
			continue
		}
		if tt.kind == agentrt.Failed {
			if e, ok := o.Err.(*backend.Error); !ok || e.Kind != tt.errKind || e.Message != tt.msg {
				t.Errorf("%q: error %v", tt.lines, o.Err)
			}
		}
	}
}

func TestClassify(t *testing.T) {
	for text, want := range map[string]backend.ErrorKind{
		"Not logged in": backend.ErrNotLoggedIn,
		"Your access token could not be refreshed. Please log out and sign in again.": backend.ErrNotLoggedIn,
		"You've hit your usage limit. Try again in 3 days.":                           backend.ErrQuota,
		"stream error: rate limit exceeded":                                           backend.ErrQuota,
		"The 'x' model requires a newer version of Codex.":                            backend.ErrVersion,
		"The 'x' model is not supported when using Codex with a ChatGPT account.":     backend.ErrFailed,
		"unexpected status 500, request id: req_0429abc, cf-ray: 9429aa-SEA":          backend.ErrFailed,
		"Codex (ChatGPT) exited with code 3":                                          backend.ErrFailed,
	} {
		if got := classify(text); got != want {
			t.Errorf("classify(%q) = %s, want %s", text, got, want)
		}
	}
}

func TestExitOutcome(t *testing.T) {
	o := adapter{}.ExitOutcome("Error: thread/resume: thread/resume failed: no rollout found for thread id 00000000-0000-4000-8000-000000000000 (code -32600)")
	if o.Kind != agentrt.LostSession {
		t.Errorf("lost: %+v", o)
	}
	o = adapter{}.ExitOutcome("ERROR codex_core: something\nError: unexpected status 401 Unauthorized")
	if o.Kind != agentrt.Failed || backend.KindOf(o.Err) != backend.ErrNotLoggedIn {
		t.Errorf("auth: %+v", o)
	}
}

func TestCodec(t *testing.T) {
	c := adapter{}.NewCodec(agentrt.Launch{})
	if _, err := c.Message("x"); err == nil {
		t.Error("Message works")
	}
	if string(c.Prompt("/help\nme")) != "/help\nme" || c.Interrupt() != nil {
		t.Error("Prompt or Interrupt")
	}
}
