package builtin

import (
	"maps"
	"os"

	"github.com/eziq1/golauncher/internal/agent"
	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/internal/ai/claudecode"
	"github.com/eziq1/golauncher/internal/ai/codex"
	"github.com/eziq1/golauncher/internal/ai/gemini"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai"
)

// geminiModels are the choices of Gemini CLI's "Model" preference (model
// names: not translated); the empty default is Gemini CLI's automatic choice.
var geminiModels = []plugin.Option{
	{Value: "gemini-2.5-pro", Title: "Gemini 2.5 Pro"},
	{Value: "gemini-2.5-flash", Title: "Gemini 2.5 Flash"},
	{Value: "gemini-2.5-flash-lite", Title: "Gemini 2.5 Flash-Lite"},
}

// aiBackends is the registry of the AI chat's backends: the choices of the
// AI plugin's "Backend" preference, by ID, the first being the default
// (R71, R72, docs/ai.md §20, §21). Each runs an agent CLI through the agent
// runtime (internal/ai/agentrt) with its adapter. Constructing one starts
// nothing.
func aiBackends() []ai.BackendOption {
	return []ai.BackendOption{
		{ID: claudecode.ID, Backend: claudecode.New(claudecode.Options{Tools: toolServer("")})},
		// Codex's models depend on the account and the CLI version: only its
		// default is offered (Models non-nil gives it its own preference).
		{ID: codex.ID, Backend: codex.New(codex.Options{Tools: toolServer("Codex")}), Models: []plugin.Option{}},
		{ID: gemini.ID, Backend: gemini.New(gemini.Options{Tools: toolServer("Gemini CLI")}), Models: geminiModels},
	}
}

// toolServer is the MCP server the AI chat gives the agent CLI: this
// program's `golauncher mcp` bridge with the process token, which marks the
// calls as the chat's (docs/ai.md §4.3), and the name of the agent CLI for
// the confirmations ("" for Claude Code, whose calls keep their name). nil
// when the program path is unknown.
func toolServer(agentName string) *agentrt.ToolServer {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	env := map[string]string{agent.EnvToken: agent.ProcessToken()}
	if agentName != "" {
		env = maps.Clone(env)
		env[agent.EnvAgent] = agentName
	}
	return &agentrt.ToolServer{
		Command:    exe,
		Args:       []string{"mcp"},
		Env:        env,
		SessionEnv: agent.EnvSession,
	}
}
