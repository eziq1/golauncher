// Command fakecodex imitates the Codex CLI (`codex exec --json`) for the
// tests of internal/ai/codex. Every event it writes is made up.
//
// Environment:
//
//	FAKECODEX_VERSION    printed by --version (default "codex-cli 0.162.0-alpha.1";
//	                     a version.txt next to the executable wins)
//	FAKECODEX_LOGGED_OUT "1": `login status` says "Not logged in" and exits 1
//	FAKECODEX_LOG        file that gets a JSON line per exec start (argv) and
//	                     per prompt read (stdin)
//	FAKECODEX_STATE      directory with a file per known thread ID, so that
//	                     `exec resume <id>` works across processes
//
// What a turn does depends on the prompt: "multi" answers with two messages
// (the first one growing in item.updated events), "tool <query>" calls
// golauncher's search tool, "toolfail" a tool call that fails, "web" a web
// search, "auth", "quota" and "newer" fail the turn like a missing sign-in,
// the usage limit and a too old CLI, "retry" reports retries first, "crash"
// exits after a message without ending the turn, "slow" writes a long
// answer slowly, "garbage" writes a line that is not JSON first; anything
// else is echoed as "echo: <prompt>".
package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func main() {
	args := os.Args[1:]
	switch {
	case slices.Contains(args, "--version"):
		v := os.Getenv("FAKECODEX_VERSION")
		if exe, err := os.Executable(); err == nil {
			// A copy with a version.txt next to it has that version.
			if b, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "version.txt")); err == nil {
				v = strings.TrimSpace(string(b))
			}
		}
		if v == "" {
			v = "codex-cli 0.162.0-alpha.1"
		}
		fmt.Println(v)
		return
	case len(args) >= 2 && args[0] == "login" && args[1] == "status":
		if os.Getenv("FAKECODEX_LOGGED_OUT") == "1" {
			fmt.Println("Not logged in")
			os.Exit(1)
		}
		fmt.Println("Logged in using ChatGPT")
		return
	case len(args) == 0 || args[0] != "exec":
		fmt.Fprintln(os.Stderr, "fakecodex: unexpected arguments", args)
		os.Exit(2)
	}
	logEntry(map[string]any{"argv": args})
	if args[len(args)-1] != "-" {
		fail("fakecodex: the prompt is not read from stdin")
	}
	images := checkImages(args)
	thread := ""
	if i := slices.Index(args, "resume"); i >= 0 {
		j := i + 1
		for j < len(args) && strings.HasPrefix(args[j], "--image=") {
			j++
		}
		if j >= len(args)-1 {
			fail("fakecodex: resume without an ID")
		}
		thread = args[j]
		if !known(thread) {
			fail("Error: thread/resume: thread/resume failed: no rollout found for thread id " + thread + " (code -32600)")
		}
	} else {
		thread = newID()
	}
	b, _ := io.ReadAll(os.Stdin)
	prompt := string(b)
	logEntry(map[string]any{"stdin": prompt})

	emit(map[string]any{"type": "thread.started", "thread_id": thread})
	remember(thread)
	emit(map[string]any{"type": "turn.started"})
	switch {
	case prompt == "multi":
		emit(itemEv("item.started", map[string]any{"id": "item_0", "type": "agent_message", "text": ""}))
		emit(itemEv("item.updated", map[string]any{"id": "item_0", "type": "agent_message", "text": "Let me"}))
		emit(itemEv("item.updated", map[string]any{"id": "item_0", "type": "agent_message", "text": "Let me think."}))
		emit(itemEv("item.completed", map[string]any{"id": "item_0", "type": "agent_message", "text": "Let me think."}))
		emit(itemEv("item.completed", map[string]any{"id": "item_1", "type": "reasoning", "text": "**Thinking**"}))
		emit(itemEv("item.completed", map[string]any{"id": "item_2", "type": "agent_message", "text": "Done."}))
	case strings.HasPrefix(prompt, "tool "):
		q := strings.TrimPrefix(prompt, "tool ")
		call := map[string]any{"id": "item_0", "type": "mcp_tool_call", "server": "golauncher", "tool": "search",
			"arguments": map[string]any{"query": q}, "result": nil, "error": nil, "status": "in_progress"}
		emit(itemEv("item.started", call))
		call["status"] = "completed"
		call["result"] = map[string]any{"content": []any{map[string]any{"type": "text", "text": `{"items":[{"handle":"h1","title":"Example"}]}`}},
			"structured_content": nil}
		emit(itemEv("item.completed", call))
		emit(itemEv("item.completed", map[string]any{"id": "item_1", "type": "agent_message", "text": "1"}))
	case prompt == "toolfail":
		call := map[string]any{"id": "item_0", "type": "mcp_tool_call", "server": "golauncher", "tool": "run_action",
			"arguments": map[string]any{"handle": "h1"}, "result": nil, "error": nil, "status": "in_progress"}
		emit(itemEv("item.started", call))
		call["status"] = "failed"
		call["error"] = map[string]any{"message": "tool call failed (fake)"}
		emit(itemEv("item.completed", call))
		emit(itemEv("item.completed", map[string]any{"id": "item_1", "type": "agent_message", "text": "It failed."}))
	case prompt == "web":
		emit(itemEv("item.started", map[string]any{"id": "ws_1", "type": "web_search", "query": ""}))
		emit(itemEv("item.completed", map[string]any{"id": "ws_1", "type": "web_search", "query": "weather today",
			"action": map[string]any{"type": "search", "query": "weather today"}}))
		emit(itemEv("item.completed", map[string]any{"id": "item_1", "type": "agent_message", "text": "Sunny."}))
	case prompt == "auth":
		failTurn("unexpected status 401 Unauthorized: Missing bearer or basic authentication in header (fake)")
	case prompt == "quota":
		failTurn("You've hit your usage limit (fake). Try again later.")
	case prompt == "newer":
		failTurn("The 'fake-model' model requires a newer version of Codex. Please upgrade to the latest app or CLI and try again.")
	case prompt == "retry":
		emit(map[string]any{"type": "error", "message": "Reconnecting... 1/5 (stream disconnected before completion)"})
		emit(itemEv("item.completed", map[string]any{"id": "item_0", "type": "error", "message": "Falling back from WebSockets to HTTPS transport (fake)"}))
		emit(map[string]any{"type": "error", "message": "stream error: fake; retrying 2/5 in 10ms…"})
		emit(itemEv("item.completed", map[string]any{"id": "item_1", "type": "agent_message", "text": "made it"}))
	case prompt == "crash":
		emit(itemEv("item.completed", map[string]any{"id": "item_0", "type": "agent_message", "text": "partial"}))
		fmt.Fprintln(os.Stderr, "fake crash: something broke")
		os.Exit(3)
	case prompt == "slow":
		text := ""
		emit(itemEv("item.started", map[string]any{"id": "item_0", "type": "agent_message", "text": ""}))
		for range 200 {
			text += "x"
			emit(itemEv("item.updated", map[string]any{"id": "item_0", "type": "agent_message", "text": text}))
			time.Sleep(50 * time.Millisecond)
		}
		emit(itemEv("item.completed", map[string]any{"id": "item_0", "type": "agent_message", "text": text}))
	case prompt == "garbage":
		fmt.Println("2026-01-01T00:00:00Z WARN fake: this is not JSON")
		emit(itemEv("item.completed", map[string]any{"id": "item_0", "type": "agent_message", "text": "still fine"}))
	default:
		text := "echo: " + prompt
		if images > 0 {
			text += fmt.Sprintf(" [images: %d]", images)
		}
		emit(itemEv("item.completed", map[string]any{"id": "item_0", "type": "agent_message", "text": text}))
	}
	emit(map[string]any{"type": "turn.completed", "usage": map[string]any{"input_tokens": 100, "cached_input_tokens": 40,
		"cache_write_input_tokens": 0, "output_tokens": 7, "reasoning_output_tokens": 3}})
}

// checkImages returns the number of --image=<file> arguments, which must
// come right after "exec" or right after "resume" (before the thread ID)
// and name files that exist; the log gets their sizes.
func checkImages(args []string) int {
	var sizes []int64
	for i, a := range args {
		path, ok := strings.CutPrefix(a, "--image=")
		if !ok {
			if a == "-i" || a == "--image" {
				fail("fakecodex: --image without '='")
			}
			continue
		}
		j := i - 1
		for j >= 0 && strings.HasPrefix(args[j], "--image=") {
			j--
		}
		if j < 0 || args[j] != "exec" && args[j] != "resume" {
			fail("fakecodex: --image in the wrong place")
		}
		fi, err := os.Stat(path)
		if err != nil {
			fail("fakecodex: image: " + err.Error())
		}
		sizes = append(sizes, fi.Size())
	}
	if len(sizes) > 0 {
		logEntry(map[string]any{"imageSizes": sizes})
	}
	return len(sizes)
}

func itemEv(typ string, it map[string]any) map[string]any {
	return map[string]any{"type": typ, "item": it}
}

// failTurn ends the turn as failed with msg, like Codex: an error event, then
// turn.failed, exit code 1.
func failTurn(msg string) {
	emit(map[string]any{"type": "error", "message": msg})
	emit(map[string]any{"type": "turn.failed", "error": map[string]any{"message": msg}})
	os.Exit(1)
}

func emit(v any) {
	b, _ := json.Marshal(v)
	os.Stdout.Write(append(b, '\n'))
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

// newID returns a made-up thread ID (a random UUID).
func newID() string {
	var u [16]byte
	rand.Read(u[:])
	u[6], u[8] = u[6]&0x0f|0x70, u[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:])
}

func statePath(id string) string {
	dir := os.Getenv("FAKECODEX_STATE")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, id)
}

func known(id string) bool {
	p := statePath(id)
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

func remember(id string) {
	if p := statePath(id); p != "" {
		os.WriteFile(p, nil, 0o644)
	}
}

func logEntry(v map[string]any) {
	f := os.Getenv("FAKECODEX_LOG")
	if f == "" {
		return
	}
	v["pid"] = os.Getpid()
	b, _ := json.Marshal(v)
	file, err := os.OpenFile(f, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	file.Write(append(b, '\n'))
}
