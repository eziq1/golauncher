// Package claudecode is the AI backend that chats through the user's
// installed Claude Code CLI (docs/ai.md §6.1). It is the Claude Code adapter
// of the agent runtime (internal/ai/agentrt, docs/ai.md §20): the runtime
// runs the processes, this package knows Claude Code's command line and its
// stream-json protocol.
//
// A conversation keeps one `claude -p` process running that reads user
// messages and writes the answers as stream-json, so the subscription the
// user signed in with is used and no API key is needed. The process runs
// isolated from the user's own Claude Code setup (no settings, hooks, MCP
// servers, slash commands or local tools) in a Job Object without a console
// window. The only tools the model may get are web search and golauncher's
// own MCP server (Options.Tools), through which it uses the launcher's
// plugins.
//
// When the CLI's version is outside the tested range, or the persistent
// process misbehaves, a conversation falls back to one process per turn
// (`claude -p --resume <id>`).
package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/internal/winapi/proc"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// ID is the backend's ID in the AI plugin's "Backend" preference.
const ID = "claude-code"

// The CLI versions the stream-json protocol was verified with. Versions
// below MinVersion are refused; versions with another major version than
// TestedMajor work in the more careful per-turn mode.
const (
	MinVersion  = "2.1.0"
	TestedMajor = 2
)

// Defaults for Options.
const (
	DefaultMaxProcesses = agentrt.DefaultMaxProcesses
	DefaultGracePeriod  = agentrt.DefaultGracePeriod
)

// envDrop are variables that Claude Code sets for programs it starts; the
// launcher has them when it was started from inside Claude Code, and the CLI
// must not see them.
var envDrop = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT"}

// Options configure a Backend (see agentrt.Options).
type Options = agentrt.Options

// ToolServer is how the CLI starts golauncher's MCP server (stdio).
type ToolServer = agentrt.ToolServer

// Backend is the Claude Code backend: the agent runtime with the Claude Code
// adapter. It is safe for concurrent use.
type Backend struct {
	*agentrt.Runtime
	o Options
}

// New returns a backend configured by o.
func New(o Options) *Backend {
	return &Backend{Runtime: agentrt.New(adapter{}, o), o: o}
}

// adapter is Claude Code's agentrt.Adapter.
type adapter struct{}

var _ agentrt.Adapter = adapter{}

// Info implements agentrt.Adapter.
func (adapter) Info() agentrt.Info {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".local", "bin", "claude.exe"))
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		paths = append(paths, filepath.Join(appData, "npm", "claude.cmd"))
	}
	return agentrt.Info{
		ID:           "claudecode",
		Name:         "Claude Code",
		Commands:     []string{"claude"},
		InstallPaths: paths,
		VersionArgs:  []string{"--version"},
		MinVersion:   MinVersion,
		TestedMajor:  TestedMajor,
		EnvDrop:      envDrop,
	}
}

// noAutoMemory turns Claude Code's auto memory off; --safe-mode does that
// too, but it cannot be used with golauncher's MCP server.
const noAutoMemory = "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"

// Command implements agentrt.Adapter. Every flag keeps the CLI away from the
// user's own setup and from this computer: no settings or hooks, no MCP
// servers but golauncher's (l.MCPConfig, when set), no slash commands, no
// local tools, no permission prompts (toolFlags pre-approves the tools the
// model may use).
func (adapter) Command(l agentrt.Launch) (agentrt.Command, error) {
	a := []string{l.Path, "-p"}
	if l.Persistent {
		a = append(a, "--input-format", "stream-json")
	}
	a = append(a, "--output-format", "stream-json", "--include-partial-messages", "--verbose")
	if l.Resume {
		a = append(a, "--resume", l.SessionID)
	} else {
		a = append(a, "--session-id", l.SessionID)
	}
	a = append(a, "--setting-sources", "")
	if l.MCPConfig == "" {
		// --safe-mode also drops the servers of --mcp-config (verified with
		// 2.1.296), so with golauncher's tools isolation comes from the other
		// flags and from the environment (no auto memory); see docs/ai.md §16.
		a = append(a, "--safe-mode")
	}
	a = append(a, "--strict-mcp-config", "--disable-slash-commands")
	a = append(a, toolFlags(l.WebSearch, l.MCPConfig)...)
	a = append(a, "--permission-prompts", "none")
	if sp := l.SystemPrompt; sp != "" {
		if proc.IsBatch(l.Path) {
			// cmd.exe ends the command line at a line break.
			sp = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(sp)
		}
		a = append(a, "--append-system-prompt", sp)
	}
	if l.Model != "" {
		a = append(a, "--model", l.Model)
	}
	cmd := agentrt.Command{Argv: a}
	if l.Tools != nil {
		cmd.Env = []string{noAutoMemory}
	}
	return cmd, nil
}

// MCPConfig implements agentrt.Adapter: Claude Code reads the tool server
// from an --mcp-config file.
func (adapter) MCPConfig(s agentrt.MCPServer) ([]byte, error) { return toolConfig(s) }

// NewCodec implements agentrt.Adapter.
func (adapter) NewCodec(agentrt.Launch) agentrt.Codec { return codec{} }

// ExitOutcome implements agentrt.Adapter.
func (adapter) ExitOutcome(text string) agentrt.Outcome {
	if sessionInUse(text) {
		return agentrt.Outcome{Kind: agentrt.SessionInUse, Err: &backend.Error{Kind: classify("", text, false), Message: text}}
	}
	return agentrt.Outcome{Kind: agentrt.Failed, Err: &backend.Error{Kind: classify("", text, false), Message: text}}
}

// interruptSeq numbers interrupt requests.
var interruptSeq atomic.Int64

// codec is Claude Code's stream-json protocol; it keeps no state between
// turns.
type codec struct{}

// Message implements agentrt.Codec.
func (codec) Message(text string) ([]byte, error) { return jsonLine(userLine(text)) }

// Prompt implements agentrt.Codec: a per-turn process reads the question
// from stdin.
func (codec) Prompt(text string) []byte { return []byte(promptText(text)) }

// Interrupt implements agentrt.Codec: the control request that stops the
// answer.
func (codec) Interrupt() []byte {
	b, _ := jsonLine(interruptLine("golauncher-interrupt-" + strconv.FormatInt(interruptSeq.Add(1), 10)))
	return b
}

// NewDecoder implements agentrt.Codec.
func (codec) NewDecoder(io agentrt.TurnIO) agentrt.Decoder {
	return &decoder{p: newParser(io.Emit)}
}

// jsonLine is v as one line of input.
func jsonLine(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
