// Command fakemcp imitates golauncher's MCP server (stdio, one JSON-RPC
// message per line) for the tests of internal/ai/claudecode: it answers
// initialize, tools/list and tools/call. A call answers with a canned search
// result for arguments.query, or with an error result when the query is
// "fail". The result's _meta echoes what the server saw: its arguments, the
// GOLAUNCHER_* variables of its environment, the client's name and whether
// the client sent notifications/initialized.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

var (
	client      string
	initialized bool
)

func main() {
	s := bufio.NewScanner(os.Stdin)
	s.Buffer(nil, 1<<20)
	for s.Scan() {
		var r request
		if json.Unmarshal(s.Bytes(), &r) != nil {
			continue
		}
		if len(r.ID) == 0 {
			if r.Method == "notifications/initialized" {
				initialized = true
			}
			continue
		}
		reply(r.ID, answer(r))
	}
}

func answer(r request) any {
	switch r.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			ClientInfo      struct {
				Name string `json:"name"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(r.Params, &p)
		client = p.ClientInfo.Name
		return map[string]any{"protocolVersion": p.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{"name": "fakemcp", "version": "0"}}
	case "tools/list":
		var tools []map[string]any
		for _, n := range []string{"list_plugins", "search", "run_command", "get_detail", "run_action"} {
			tools = append(tools, map[string]any{"name": n, "description": "fake " + n, "inputSchema": map[string]any{"type": "object"}})
		}
		return map[string]any{"tools": tools}
	case "tools/call":
		var p struct {
			Name      string `json:"name"`
			Arguments struct {
				Query string `json:"query"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(r.Params, &p)
		meta := map[string]any{"args": os.Args[1:], "env": golauncherEnv(), "client": client, "initialized": initialized, "tool": p.Name}
		if p.Arguments.Query == "fail" {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": `{"status":"error","error":"fake failure"}`}},
				"isError": true, "_meta": meta}
		}
		res, _ := json.Marshal(map[string]any{"query": p.Arguments.Query,
			"items": []map[string]any{{"handle": "i1", "title": "Item for " + p.Arguments.Query, "plugin": "builtin.fake"}}})
		return map[string]any{"content": []map[string]any{{"type": "text", "text": string(res)}}, "isError": false, "_meta": meta}
	}
	return nil
}

func golauncherEnv() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(k), "GOLAUNCHER_") {
			env[k] = v
		}
	}
	return env
}

func reply(id json.RawMessage, result any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	os.Stdout.Write(append(b, '\n'))
}
