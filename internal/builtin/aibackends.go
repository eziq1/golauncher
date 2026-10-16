package builtin

import (
	"os"

	"github.com/eziq1/golauncher/internal/agent"
	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/internal/ai/claudecode"
	"github.com/eziq1/golauncher/plugins/ai"
)

// aiBackends is the registry of the AI chat's backends: the choices of the
// AI plugin's "Backend" preference, by ID, the first being the default
// (R71, docs/ai.md §20). Each runs an agent CLI through the agent runtime
// (internal/ai/agentrt) with its adapter; phase 2 adds ACP agents and Codex
// here. Constructing one starts nothing.
func aiBackends() []ai.BackendOption {
	tools := toolServer()
	return []ai.BackendOption{
		{ID: claudecode.ID, Backend: claudecode.New(claudecode.Options{Tools: tools})},
	}
}

// toolServer is the MCP server the AI chat gives the agent CLI: this
// program's `golauncher mcp` bridge with the process token, which marks the
// calls as the chat's (docs/ai.md §4.3). nil when the program path is
// unknown.
func toolServer() *agentrt.ToolServer {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	return &agentrt.ToolServer{
		Command:    exe,
		Args:       []string{"mcp"},
		Env:        map[string]string{agent.EnvToken: agent.ProcessToken()},
		SessionEnv: agent.EnvSession,
	}
}
