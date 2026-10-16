package claudecode

import (
	"encoding/json"
	"strings"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
)

// launcherToolsPattern is the --allowedTools entry that pre-approves the
// tools of golauncher's MCP server (and nothing else); the CLI names them
// mcp__<server>__<tool>. The launcher asks the user itself before any tool
// changes something (docs/ai.md §5).
var launcherToolsPattern = "mcp__" + agentrt.ToolServerName + "__*"

// webTools are Claude Code's own tools for searching and fetching web pages.
var webTools = []string{"WebSearch", "WebFetch"}

// toolFlags returns the flags that choose the model's tools: no local tool
// of Claude Code's, the web tools when web is set, and golauncher's MCP
// server when mcpConfig (its configuration file) is set. Only these tools
// are pre-approved; with --permission-prompts none everything else is
// denied.
func toolFlags(web bool, mcpConfig string) []string {
	var builtin, allowed []string
	if mcpConfig != "" {
		allowed = append(allowed, launcherToolsPattern)
	}
	if web {
		builtin = webTools
		allowed = append(allowed, webTools...)
	}
	a := []string{"--tools", strings.Join(builtin, ",")}
	if mcpConfig != "" {
		a = append(a, "--mcp-config", mcpConfig)
	}
	if len(allowed) > 0 {
		a = append(a, "--allowedTools", strings.Join(allowed, ","))
	}
	return a
}

// mcpServer is a server entry of the CLI's MCP configuration.
type mcpServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// toolConfig returns the CLI's MCP configuration for a session: only
// golauncher's server s.
func toolConfig(s agentrt.MCPServer) ([]byte, error) {
	cfg := map[string]map[string]mcpServer{
		"mcpServers": {s.Name: {Type: "stdio", Command: s.Command, Args: s.Args, Env: s.Env}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}
