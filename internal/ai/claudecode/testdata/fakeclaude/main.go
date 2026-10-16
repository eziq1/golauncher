// Command fakeclaude imitates the Claude Code CLI's stream-json protocol for
// the tests of internal/ai/claudecode.
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
// "tool" uses WebSearch; anything else is echoed as "echo: <text>".
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
}

var (
	sc      script
	outMu   sync.Mutex
	session string
	model   = "claude-fake-1"
)

func main() {
	if f := os.Getenv("FAKECLAUDE_SCRIPT"); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			_ = json.Unmarshal(b, &sc)
		}
	}
	args := os.Args[1:]
	if slices.Contains(args, "--version") {
		v := sc.Version
		if v == "" {
			v = "2.1.296 (Claude Code)"
		}
		fmt.Println(v)
		return
	}
	logEntry(map[string]any{"argv": args})
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

// runPersistent answers stream-json user messages until stdin closes.
func runPersistent(lost bool) {
	if sc.CrashAtStart {
		fail("fake: crashed at start")
	}
	msgs := make(chan map[string]any, 16)
	controls := make(chan map[string]any, 16)
	go func() {
		s := bufio.NewScanner(os.Stdin)
		s.Buffer(nil, 1<<20)
		for s.Scan() {
			logEntry(map[string]any{"stdin": s.Text()})
			var m map[string]any
			if json.Unmarshal(s.Bytes(), &m) != nil {
				continue
			}
			if m["type"] == "control_request" {
				controls <- m
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
			text, _ := msg["content"].(string)
			turn(strings.TrimPrefix(text, " "), controls)
		case c := <-controls:
			controlResponse(c)
		}
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
		"mcp_servers": []string{}, "claude_code_version": "2.1.296", "apiKeySource": "none"})
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
