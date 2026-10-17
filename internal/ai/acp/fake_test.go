package acp

import (
	"bufio"
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// The test binary is also the fake ACP agent of these tests: with
// ACP_FAKE=1 it speaks ACP on stdin and stdout instead of running the tests.
//
// It logs every line it reads to $ACP_FAKE_LOG and keeps the sessions it
// created in $ACP_FAKE_LOG.sessions. Its environment shapes it:
//
//	ACP_FAKE_PROTO=<n>     initialize reports protocol version n (default 1)
//	ACP_FAKE_NOLOAD=1      no loadSession capability
//	ACP_FAKE_LOADFAIL=1    session/load fails
//	ACP_FAKE_NEWERR=auth   session/new fails: authentication required
//	ACP_FAKE_NEWERR=quota  session/new fails with 429
//
// The text of a prompt's last block picks what a turn does: "tool", "web",
// "perm", "slow", "fs", "crash", "garbage" (once), "quota"; anything else is
// echoed ("echo: <text>").
func fakeAgent() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "--version" {
		fmt.Println("fake-acp 1.0.0")
		return
	}
	f := &fake{log: os.Getenv("ACP_FAKE_LOG"), in: make(chan message, 100)}
	go f.read()
	for {
		m, ok := <-f.in
		if !ok {
			return
		}
		f.handle(m)
	}
}

type fake struct {
	log     string
	in      chan message
	mu      sync.Mutex
	session string
	n       int
}

// read queues the lines of stdin, logging each.
func (f *fake) read() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if lf, err := os.OpenFile(f.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(lf, line)
			lf.Close()
		}
		var m message
		if json.Unmarshal([]byte(line), &m) == nil {
			f.in <- m
		}
	}
	close(f.in)
}

func (f *fake) send(v any) {
	b, _ := json.Marshal(v)
	f.mu.Lock()
	os.Stdout.Write(append(b, '\n'))
	f.mu.Unlock()
}

func (f *fake) result(id jsontext.Value, v any) {
	f.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": v})
}

func (f *fake) fail(id jsontext.Value, code int, msg string) {
	f.send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func (f *fake) update(u map[string]any) {
	f.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": f.session, "update": u}})
}

func (f *fake) chunk(kind, text string) {
	f.update(map[string]any{"sessionUpdate": kind, "content": map[string]any{"type": "text", "text": text}})
}

func textContent(s string) []any {
	return []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": s}}}
}

func (f *fake) sessions() []string {
	b, _ := os.ReadFile(f.log + ".sessions")
	return strings.Fields(string(b))
}

func (f *fake) handle(m message) {
	switch m.Method {
	case "initialize":
		proto := 1
		fmt.Sscan(os.Getenv("ACP_FAKE_PROTO"), &proto)
		f.result(m.ID, map[string]any{"protocolVersion": proto,
			"agentCapabilities": map[string]any{"loadSession": os.Getenv("ACP_FAKE_NOLOAD") != "1"},
			"authMethods":       []any{map[string]any{"id": "fake-login", "name": "Log in"}}})
	case "session/new":
		switch os.Getenv("ACP_FAKE_NEWERR") {
		case "auth":
			f.fail(m.ID, -32000, "Authentication required.")
			return
		case "quota":
			f.fail(m.ID, 429, "Rate limit exceeded. Try again later.")
			return
		}
		f.n++
		f.session = fmt.Sprintf("sess-%d-%d", os.Getpid(), f.n)
		if sf, err := os.OpenFile(f.log+".sessions", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(sf, f.session)
			sf.Close()
		}
		f.result(m.ID, map[string]any{"sessionId": f.session, "models": map[string]any{"currentModelId": "fake-model"}})
		f.update(map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []any{}})
	case "session/load":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		json.Unmarshal(m.Params, &p)
		if os.Getenv("ACP_FAKE_LOADFAIL") == "1" || !slices.Contains(f.sessions(), p.SessionID) {
			f.fail(m.ID, -32603, "Invalid session identifier "+p.SessionID)
			return
		}
		f.session = p.SessionID
		// The history: partly before the response, partly after it.
		f.chunk("user_message_chunk", "OLD QUESTION")
		f.chunk("agent_thought_chunk", "OLD THOUGHT")
		f.chunk("agent_message_chunk", "OLD ANSWER")
		f.result(m.ID, map[string]any{"models": map[string]any{"currentModelId": "fake-model-loaded"}})
		time.Sleep(100 * time.Millisecond)
		f.chunk("agent_message_chunk", "OLD LATE ANSWER")
		time.Sleep(100 * time.Millisecond)
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "old-1", "status": "completed", "title": "old tool", "kind": "other", "content": textContent("old result")})
		f.update(map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []any{}})
	case "session/prompt":
		f.prompt(m)
	}
}

// cancelled reports whether session/cancel arrived (without waiting).
func (f *fake) cancelled() bool {
	for {
		select {
		case m, ok := <-f.in:
			if !ok {
				os.Exit(0)
			}
			if m.Method == "session/cancel" {
				return true
			}
		default:
			return false
		}
	}
}

// await waits for the response with the ID id.
func (f *fake) await(id string) (message, bool) {
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-f.in:
			if !ok {
				os.Exit(0)
			}
			if m.Method == "" && string(m.ID) == id {
				return m, true
			}
		case <-timeout:
			return message{}, false
		}
	}
}

func (f *fake) prompt(m message) {
	var p struct {
		Prompt []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	json.Unmarshal(m.Params, &p)
	text := ""
	if n := len(p.Prompt); n > 0 {
		text = p.Prompt[n-1].Text
	}
	end := func(reason string) { f.result(m.ID, map[string]any{"stopReason": reason}) }
	switch text {
	case "tool":
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "status": "in_progress",
			"title": `{"query": "Visual Studio Code"}`, "kind": "other", "content": []any{}})
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed",
			"content": textContent(`{"items":[{"handle":"i1"}]}`)})
		f.chunk("agent_message_chunk", "1")
		end("end_turn")
	case "web":
		f.update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "w1", "status": "in_progress",
			"title": `Searching the web for: "weather"`, "kind": "search"})
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "w1", "status": "failed",
			"content": textContent("no network")})
		f.chunk("agent_message_chunk", "done")
		end("end_turn")
	case "perm":
		f.send(map[string]any{"jsonrpc": "2.0", "id": "perm-1", "method": "session/request_permission", "params": map[string]any{
			"sessionId": f.session,
			"options": []any{
				map[string]any{"optionId": "proceed_always", "name": "Always Allow", "kind": "allow_always"},
				map[string]any{"optionId": "proceed_once", "name": "Allow", "kind": "allow_once"},
				map[string]any{"optionId": "cancel", "name": "Reject", "kind": "reject_once"},
			},
			"toolCall": map[string]any{"toolCallId": "p1", "status": "pending", "title": "Fetching content from: https://example.com/",
				"kind": "fetch", "content": []any{}, "locations": []any{}},
		}})
		r, ok := f.await(`"perm-1"`)
		if !ok {
			end("end_turn")
			return
		}
		var res struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		json.Unmarshal(r.Result, &res)
		f.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "p1", "status": "completed", "content": textContent("page")})
		f.chunk("agent_message_chunk", "outcome: "+strings.TrimSpace(res.Outcome.Outcome+" "+res.Outcome.OptionID))
		for range 10 {
			time.Sleep(30 * time.Millisecond)
			if f.cancelled() {
				end("cancelled")
				return
			}
		}
		end("end_turn")
	case "slow":
		for range 500 {
			f.chunk("agent_message_chunk", "x")
			time.Sleep(20 * time.Millisecond)
			if f.cancelled() {
				end("cancelled")
				return
			}
		}
		end("end_turn")
	case "fs":
		f.send(map[string]any{"jsonrpc": "2.0", "id": 77, "method": "fs/read_text_file", "params": map[string]any{"sessionId": f.session, "path": `C:\example.txt`}})
		r, _ := f.await("77")
		code := 0
		if r.Error != nil {
			code = r.Error.Code
		}
		f.chunk("agent_message_chunk", fmt.Sprintf("fs error %d", code))
		end("end_turn")
	case "crash":
		f.chunk("agent_message_chunk", "partial")
		time.Sleep(50 * time.Millisecond)
		fmt.Fprintln(os.Stderr, "boom: fake crash")
		os.Exit(3)
	case "garbage":
		marker := f.log + ".garbage"
		if _, err := os.Stat(marker); err != nil {
			os.WriteFile(marker, nil, 0o644)
			f.mu.Lock()
			fmt.Println("this is not JSON")
			f.mu.Unlock()
			return
		}
		f.chunk("agent_message_chunk", "echo: "+text)
		end("end_turn")
	case "quota":
		f.fail(m.ID, 429, "Rate limit exceeded. Try again later.")
	default:
		f.chunk("agent_message_chunk", "echo: ")
		f.chunk("agent_thought_chunk", "thinking")
		f.chunk("agent_message_chunk", strings.TrimPrefix(text, "\u200b"))
		end("end_turn")
	}
}

// received returns the messages the fake read, in order.
func received(log string) []message {
	b, _ := os.ReadFile(log)
	var out []message
	for l := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		var m message
		if json.Unmarshal([]byte(strings.TrimSpace(l)), &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// methods returns the methods (or "response") of the messages the fake read.
func methods(log string) []string {
	var out []string
	for _, m := range received(log) {
		out = append(out, cmp.Or(m.Method, "response"))
	}
	return out
}
