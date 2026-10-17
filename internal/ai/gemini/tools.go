package gemini

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/internal/ai/acp"
	"github.com/eziq1/golauncher/internal/ai/agentrt"
)

// launcherToolPrefix starts the tool names of golauncher's MCP server as the
// AI plugin shows them (Claude Code's naming, mcp__<server>__<tool>).
const launcherToolPrefix = "mcp__" + agentrt.ToolServerName + "__"

// launcherTools are the tools of golauncher's MCP server (internal/agent).
var launcherTools = []string{"list_plugins", "search", "run_command", "get_detail", "run_action"}

// tool names a tool call for the tool steps. Gemini CLI 0.34 titles a call of
// an MCP tool with its JSON arguments only and does not tell which tool it
// is; the only MCP server it may use is golauncher's, whose tool can be told
// by the arguments' names (internal/agent's input schemas):
//
//	command → run_command; action or values → run_action;
//	query, view or limit → search; none, or plugin only → list_plugins;
//	handle only → get_detail or run_action (with its default action): known
//	from the result (run_action's has a status of done, declined or failed),
//	until then the generic acp.MCPTool.
//
// A call that failed names its tool in Gemini CLI's error text.
func tool(c acp.ToolCall) (string, string) {
	name, input := acp.DefaultTool(c)
	if name != acp.MCPTool {
		return name, input
	}
	if t, _, ok := mcpError(c); ok {
		return launcherToolPrefix + t, input
	}
	var args map[string]jsontext.Value
	if json.Unmarshal([]byte(input), &args) != nil {
		return name, input
	}
	has := func(k string) bool { _, ok := args[k]; return ok }
	t := ""
	switch {
	case has("command"):
		t = "run_command"
	case has("action"), has("values"):
		t = "run_action"
	case has("query"), has("view"), has("limit"):
		t = "search"
	case len(args) == 0, len(args) == 1 && has("plugin"):
		t = "list_plugins"
	case len(args) == 1 && has("handle") && c.Done:
		var res struct {
			Status *string `json:"status"`
		}
		switch err := json.Unmarshal([]byte(c.Result), &res); {
		case err == nil && res.Status == nil:
			t = "get_detail"
		case err == nil && (*res.Status == "done" || *res.Status == "declined" || *res.Status == "failed"):
			t = "run_action"
		}
	}
	if t == "" {
		return name, input
	}
	return launcherToolPrefix + t, input
}

// toolResult is the result shown of a call: for a failed call of one of
// golauncher's tools, the tool's own result rather than Gemini CLI's error
// text around it.
func toolResult(c acp.ToolCall) string {
	if _, text, ok := mcpError(c); ok && text != "" {
		return text
	}
	return c.Result
}

// mcpError parses Gemini CLI's text of a failed MCP call ("MCP tool 'search'
// reported tool error for function call: {…} with response: [{…}]") into the
// tool's name and the text of its result.
func mcpError(c acp.ToolCall) (tool, text string, ok bool) {
	if !c.Failed {
		return "", "", false
	}
	rest, ok := strings.CutPrefix(c.Result, "MCP tool '")
	if !ok {
		return "", "", false
	}
	tool, rest, ok = strings.Cut(rest, "' reported tool error")
	if !ok || !slices.Contains(launcherTools, tool) {
		return "", "", false
	}
	if _, resp, ok := strings.Cut(rest, " with response: "); ok {
		var parts []struct {
			FunctionResponse struct {
				Response struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"response"`
			} `json:"functionResponse"`
		}
		if json.Unmarshal([]byte(resp), &parts) == nil {
			var texts []string
			for _, p := range parts {
				for _, ct := range p.FunctionResponse.Response.Content {
					if ct.Type == "text" {
						texts = append(texts, ct.Text)
					}
				}
			}
			text = strings.Join(texts, "\n")
		}
	}
	return tool, text, true
}
