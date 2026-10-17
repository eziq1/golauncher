// Command fakeclaude imitates the Claude Code CLI's stream-json protocol for
// the tests of internal/ai/claudecode.
//
// --help prints the script's helpFile (default: every flag the backend
// uses), and any long option it does not list is rejected with commander's
// "error: unknown option '--x'", so that older versions can be imitated.
// Without --include-partial-messages no stream events are written.
//
// Environment:
//
//	FAKECLAUDE_SCRIPT  JSON file with process-level behavior (script below)
//	FAKECLAUDE_LOG     file that gets a JSON line per start (argv) and per
//	                   input line (stdin)
//	FAKECLAUDE_STATE   directory with a file per known session ID, so that
//	                   --resume works across processes
//
// What a turn does depends on the user's text: "slow" writes many deltas
// with pauses, "crash" exits 3 after a delta, "fail" ends with an error
// result, "auth" and "quota" imitate a missing login and the usage limit,
// "tool" uses WebSearch, "launcher <query>" calls the search tool of the
// golauncher MCP server from --mcp-config with <query> (as an MCP client,
// logging the tools it listed and the server's _meta echo) before the
// answer; anything else is echoed as "echo: <text>".
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// script is the process-level behavior.
type script struct {
	// Version is printed by --version (default "2.1.296 (Claude Code)").
	Version string `json:"version"`
	// CrashAtStart makes the persistent mode exit 2 at once.
	CrashAtStart bool `json:"crashAtStart"`
	// Garbage makes the persistent mode answer with a line that is not JSON.
	Garbage bool `json:"garbage"`
	// InUseAsResult reports a --session-id of a known session as a result
	// line instead of on stderr.
	InUseAsResult bool `json:"inUseAsResult"`
	// HelpFile is printed by --help (default: defaultHelp); it also lists
	// the options the fake accepts.
	HelpFile string `json:"helpFile"`
	// HelpFails makes --help exit 1 without output.
	HelpFails bool `json:"helpFails"`
	// Rejects are long options rejected although --help lists them (a
	// probe that went wrong).
	Rejects []string `json:"rejects"`
	// AskPermission makes the "perm" turn send a can_use_tool control
	// request and report the answer.
	AskPermission bool `json:"askPermission"`
}

var (
	sc      script
	outMu   sync.Mutex
	session string
	model   = "claude-fake-1"
	partial bool // --include-partial-messages
	// responses are the control responses to the fake's own requests.
	responses = make(chan map[string]any, 4)
)

func main() {
	if f := os.Getenv("FAKECLAUDE_SCRIPT"); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			_ = json.Unmarshal(b, &sc)
		}
	}
	args := os.Args[1:]
	if slices.Contains(args, "--version") {
		fmt.Println(versionText())
		return
	}
	if slices.Contains(args, "--help") {
		logEntry(map[string]any{"help": true})
		if sc.HelpFails {
			os.Exit(1)
		}
		fmt.Print(helpText())
		return
	}
	logEntry(map[string]any{"argv": args})
	rejectUnknown(args)
	partial = slices.Contains(args, "--include-partial-messages")
	persistent := false
	resume := false
	for i, a := range args {
		switch a {
		case "--input-format":
			persistent = i+1 < len(args) && args[i+1] == "stream-json"
		case "--resume":
			resume = true
			session = args[i+1]
		case "--session-id":
			session = args[i+1]
		case "--model":
			model = args[i+1]
		}
	}
	if session == "" {
		fail("no session")
	}
	known := knownSession(session)
	if !resume && known {
		if sc.InUseAsResult {
			emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true,
				"errors": []string{"Session ID " + session + " is already in use."}, "session_id": session})
			os.Exit(1)
		}
		fail("Error: Session ID " + session + " is already in use.")
	}
	if persistent {
		runPersistent(resume && !known)
		return
	}
	b, _ := io.ReadAll(os.Stdin)
	logEntry(map[string]any{"stdin": string(b)})
	if resume && !known {
		lostSession()
	}
	if !turn(strings.TrimPrefix(string(b), " "), nil) {
		os.Exit(1)
	}
}

// versionText is what --version prints.
func versionText() string {
	if sc.Version != "" {
		return sc.Version
	}
	return "2.1.296 (Claude Code)"
}

// runPersistent answers stream-json user messages until stdin closes.
func runPersistent(lost bool) {
	if sc.CrashAtStart {
		fail("fake: crashed at start")
	}
	msgs := make(chan map[string]any, 16)
	controls := make(chan map[string]any, 16)
	go func() {
		s := bufio.NewScanner(os.Stdin)
		s.Buffer(nil, 64<<20) // a message with images is one long line
		for s.Scan() {
			logEntry(map[string]any{"stdin": s.Text()})
			var m map[string]any
			if json.Unmarshal(s.Bytes(), &m) != nil {
				continue
			}
			if m["type"] == "control_request" {
				controls <- m
			} else if m["type"] == "control_response" {
				responses <- m
			} else {
				msgs <- m
			}
		}
		close(msgs)
	}()
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				return
			}
			if lost {
				lostSession()
			}
			if sc.Garbage {
				fmt.Println("Something that is not JSON at all")
				continue
			}
			msg, _ := m["message"].(map[string]any)
			turn(messageText(msg["content"]), controls)
		case c := <-controls:
			controlResponse(c)
		}
	}
}

// messageText is the text of a user message's content: a string, or a list
// of blocks (a message with attachments), whose last text block that is not
// an attached file is the text, followed by " [images: N, files: M]". Each
// image block must have a media type and base64 data; the log gets the
// counts and the decoded sizes.
func messageText(content any) string {
	if s, ok := content.(string); ok {
		return strings.TrimPrefix(s, " ")
	}
	blocks, _ := content.([]any)
	text := ""
	images, files := 0, 0
	var sizes []int
	var types []string
	for _, raw := range blocks {
		b, _ := raw.(map[string]any)
		switch b["type"] {
		case "image":
			src, _ := b["source"].(map[string]any)
			mt, _ := src["media_type"].(string)
			data, _ := src["data"].(string)
			dec, err := base64.StdEncoding.DecodeString(data)
			if src["type"] != "base64" || mt == "" || err != nil || len(dec) == 0 {
				return "bad image block"
			}
			images++
			sizes = append(sizes, len(dec))
			types = append(types, mt)
		case "text":
			t, _ := b["text"].(string)
			if strings.HasPrefix(t, "<attached-file") {
				files++
				continue
			}
			text = strings.TrimPrefix(t, " ")
		}
	}
	logEntry(map[string]any{"images": images, "imageBytes": sizes, "imageTypes": types, "files": files})
	return fmt.Sprintf("%s [images: %d, files: %d]", text, images, files)
}

// askPermission sends a can_use_tool control request, as a CLI without
// --permission-prompts none might, and returns the behavior answered.
func askPermission() string {
	emit(map[string]any{"type": "control_request", "request_id": "perm-1",
		"request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "input": map[string]any{"command": "ls"}}})
	select {
	case m := <-responses:
		resp, _ := m["response"].(map[string]any)
		inner, _ := resp["response"].(map[string]any)
		b, _ := inner["behavior"].(string)
		logEntry(map[string]any{"permission": b, "requestId": resp["request_id"]})
		return b
	case <-time.After(5 * time.Second):
		return "no answer"
	}
}

// lostSession reports a --resume of an unknown session and exits.
func lostSession() {
	emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true,
		"errors": []string{"No conversation found with session ID: " + session}, "session_id": session})
	os.Exit(1)
}

// turn answers text; controls (persistent mode) may interrupt it. It reports
// whether the turn succeeded.
func turn(text string, controls <-chan map[string]any) bool {
	remember(session)
	emit(map[string]any{"type": "system", "subtype": "init", "session_id": session, "model": model, "tools": []string{},
		"mcp_servers": []string{}, "claude_code_version": strings.Fields(versionText())[0], "apiKeySource": "none"})
	emit(map[string]any{"type": "system", "subtype": "status", "status": "requesting", "session_id": session})
	switch text {
	case "fail":
		return errorResult("Something went wrong (fake)", "")
	case "auth":
		return errorResult("Not logged in · Please run /login", "authentication_failed")
	case "quota":
		emit(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "rejected"}})
		return errorResult("Claude AI usage limit reached|1700000000", "rate_limit")
	}
	emit(map[string]any{"type": "rate_limit_event", "rate_limit_info": map[string]any{"status": "allowed"}})
	if text == "perm" && sc.AskPermission {
		text = "perm: " + askPermission()
	}
	if q, ok := strings.CutPrefix(text, "launcher"); ok {
		launcherSearch(strings.TrimSpace(q))
	}
	id := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	stream(map[string]any{"type": "message_start", "message": map[string]any{"id": id, "model": model}})
	if text == "tool" {
		stream(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "name": "WebSearch"}})
		stream(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": "{}"}})
		stream(map[string]any{"type": "content_block_stop", "index": 0})
	}
	stream(map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "text", "text": ""}})
	stream(map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "thinking_delta", "thinking": "hmm"}})
	var chunks []string
	delay := time.Duration(0)
	switch text {
	case "slow":
		for range 100 {
			chunks = append(chunks, "x ")
		}
		delay = 50 * time.Millisecond
	case "crash":
		stream(map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "text_delta", "text": "partial"}})
		fmt.Fprintln(os.Stderr, "fake crash: something broke")
		os.Exit(3)
	default:
		reply := "echo: " + text
		for len(reply) > 4 {
			chunks = append(chunks, reply[:4])
			reply = reply[4:]
		}
		chunks = append(chunks, reply)
	}
	var full strings.Builder
	for _, ch := range chunks {
		if controls != nil {
			select {
			case c := <-controls:
				controlResponse(c)
				emit(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": "[Request interrupted by user]"}}}})
				emit(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "terminal_reason": "aborted_streaming", "session_id": session})
				return true
			default:
			}
		}
		time.Sleep(delay)
		full.WriteString(ch)
		stream(map[string]any{"type": "content_block_delta", "index": 1, "delta": map[string]any{"type": "text_delta", "text": ch}})
	}
	stream(map[string]any{"type": "content_block_stop", "index": 1})
	stream(map[string]any{"type": "message_delta", "usage": map[string]any{"output_tokens": 4}})
	stream(map[string]any{"type": "message_stop"})
	emit(map[string]any{"type": "assistant", "session_id": session, "message": map[string]any{"id": id, "model": model, "role": "assistant",
		"content": []map[string]any{{"type": "text", "text": full.String()}}}})
	emit(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": full.String(), "session_id": session,
		"stop_reason": "end_turn", "terminal_reason": "completed", "num_turns": 1, "permission_denials": []string{},
		"usage":      map[string]any{"input_tokens": 2, "cache_creation_input_tokens": 10, "cache_read_input_tokens": 100, "output_tokens": 4},
		"modelUsage": map[string]any{model: map[string]any{"inputTokens": 2, "outputTokens": 4}}})
	return true
}

// launcherSearch imitates the model calling golauncher's search tool for q,
// as Claude Code does: the streamed tool_use block, the complete assistant
// message with the call's input, the call itself to the server from
// --mcp-config, and the user line with its result.
func launcherSearch(q string) {
	toolID := fmt.Sprintf("toolu_%d", time.Now().UnixNano())
	msgID := fmt.Sprintf("msg_tool_%d", time.Now().UnixNano())
	name := "mcp__golauncher__search"
	input := map[string]any{"query": q}
	inputJSON, _ := json.Marshal(input)
	stream(map[string]any{"type": "message_start", "message": map[string]any{"id": msgID, "model": model}})
	stream(map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": toolID, "name": name, "input": map[string]any{}}})
	stream(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(inputJSON)}})
	stream(map[string]any{"type": "content_block_stop", "index": 0})
	stream(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 9}})
	stream(map[string]any{"type": "message_stop"})
	emit(map[string]any{"type": "assistant", "session_id": session, "message": map[string]any{"id": msgID, "model": model, "role": "assistant",
		"content": []map[string]any{{"type": "tool_use", "id": toolID, "name": name, "input": input}}}})

	content, isError := callTool("search", input)
	emit(map[string]any{"type": "user", "session_id": session, "message": map[string]any{"role": "user",
		"content": []map[string]any{{"type": "tool_result", "tool_use_id": toolID, "content": content, "is_error": isError}}}})
}

// callTool calls a tool of the "golauncher" server in the --mcp-config file
// like an MCP client and returns the result's content and isError. A
// failure is returned as an error result.
func callTool(tool string, args map[string]any) (any, bool) {
	content, isError, err := mcpCall(tool, args)
	if err != nil {
		logEntry(map[string]any{"mcpError": err.Error()})
		return []map[string]any{{"type": "text", "text": "fake MCP failure: " + err.Error()}}, true
	}
	return content, isError
}

// mcpConfig is the part of the --mcp-config file the fake uses.
type mcpConfig struct {
	MCPServers map[string]struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	} `json:"mcpServers"`
}

func mcpCall(tool string, args map[string]any) (any, bool, error) {
	path := ""
	argv := os.Args[1:]
	for i, a := range argv {
		if a == "--mcp-config" && i+1 < len(argv) {
			path = argv[i+1]
		}
	}
	if path == "" {
		return nil, false, fmt.Errorf("no --mcp-config")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	var cfg mcpConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, false, err
	}
	srv, ok := cfg.MCPServers["golauncher"]
	if !ok || srv.Type != "stdio" {
		return nil, false, fmt.Errorf("no stdio golauncher server")
	}
	cmd := exec.Command(srv.Command, srv.Args...)
	hide(cmd)
	cmd.Env = os.Environ()
	for k, v := range srv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, false, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	defer func() {
		in.Close()
		_ = cmd.Wait()
	}()
	r := bufio.NewScanner(out)
	r.Buffer(nil, 1<<20)
	send := func(v map[string]any) error {
		v["jsonrpc"] = "2.0"
		b, _ := json.Marshal(v)
		_, err := in.Write(append(b, '\n'))
		return err
	}
	call := func(id int, method string, params any) (json.RawMessage, error) {
		if err := send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
			return nil, err
		}
		if !r.Scan() {
			return nil, fmt.Errorf("%s: no answer", method)
		}
		var resp struct {
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(r.Bytes(), &resp); err != nil {
			return nil, err
		}
		if len(resp.Error) > 0 && string(resp.Error) != "null" {
			return nil, fmt.Errorf("%s: %s", method, resp.Error)
		}
		return resp.Result, nil
	}
	if _, err := call(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "claude-code", "version": "2.1.296"}}); err != nil {
		return nil, false, err
	}
	if err := send(map[string]any{"method": "notifications/initialized"}); err != nil {
		return nil, false, err
	}
	listRaw, err := call(2, "tools/list", map[string]any{})
	if err != nil {
		return nil, false, err
	}
	var list struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	_ = json.Unmarshal(listRaw, &list)
	var names []string
	for _, t := range list.Tools {
		names = append(names, t.Name)
	}
	resRaw, err := call(3, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return nil, false, err
	}
	var res struct {
		Content json.RawMessage `json:"content"`
		IsError bool            `json:"isError"`
		Meta    json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(resRaw, &res); err != nil {
		return nil, false, err
	}
	logEntry(map[string]any{"mcpTools": names, "mcpMeta": res.Meta})
	return res.Content, res.IsError, nil
}

// errorResult ends a turn with a synthetic assistant error and an error
// result.
func errorResult(text, code string) bool {
	if code != "" {
		emit(map[string]any{"type": "assistant", "session_id": session, "error": code,
			"message": map[string]any{"id": "synthetic", "model": "<synthetic>", "role": "assistant",
				"content": []map[string]any{{"type": "text", "text": text}}}})
	}
	emit(map[string]any{"type": "result", "subtype": "success", "is_error": true, "result": text, "session_id": session})
	return false
}

func controlResponse(c map[string]any) {
	emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": c["request_id"], "response": map[string]any{}}})
}

func stream(ev map[string]any) {
	if !partial {
		return
	}
	emit(map[string]any{"type": "stream_event", "event": ev, "session_id": session})
}

func emit(v any) {
	b, _ := json.Marshal(v)
	outMu.Lock()
	defer outMu.Unlock()
	os.Stdout.Write(append(b, '\n'))
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

func knownSession(id string) bool {
	dir := os.Getenv("FAKECLAUDE_STATE")
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, id))
	return err == nil
}

func remember(id string) {
	if dir := os.Getenv("FAKECLAUDE_STATE"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, id), nil, 0o644)
	}
}

func logEntry(e map[string]any) {
	f := os.Getenv("FAKECLAUDE_LOG")
	if f == "" {
		return
	}
	e["pid"] = os.Getpid()
	b, _ := json.Marshal(e)
	lf, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer lf.Close()
	lf.Write(append(b, '\n'))
}
