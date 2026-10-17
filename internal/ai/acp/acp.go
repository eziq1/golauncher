// Package acp is the agent runtime's adapter for agents that speak the Agent
// Client Protocol (ACP, https://agentclientprotocol.com): JSON-RPC 2.0 over
// the agent's stdin and stdout, one JSON object per line (docs/ai.md §20,
// §21). It is generic: internal/ai/gemini configures it for `gemini --acp`,
// and Custom for any other ACP agent the user names.
//
// The client (golauncher) advertises no capabilities: no file system
// (fs/read_text_file, fs/write_text_file) and no terminal, so an agent can
// neither read nor write files nor run commands through golauncher; requests
// for them, and for any other client method, are answered with JSON-RPC error
// -32601. What the agent may do on its own is the agent's configuration (see
// internal/ai/gemini for Gemini CLI's).
//
// # Message flow
//
// A conversation keeps one agent process (Info.NoPerTurn: there is no
// per-turn mode; a process that misbehaves is restarted). The runtime calls
// Codec.Handshake once per process:
//
//	→ initialize {protocolVersion: 1, clientCapabilities: {fs: {readTextFile: false, writeTextFile: false}, terminal: false}, clientInfo}
//	← {protocolVersion: 1, agentCapabilities: {loadSession: true, …}}
//	→ session/load {sessionId, cwd, mcpServers}     (resuming, when the agent can load sessions)
//	← session/update … (the history, replayed; read and dropped until the output is quiet)
//	→ session/new {cwd, mcpServers}                  (otherwise, or when the load failed)
//	← {sessionId, models: {currentModelId}}
//
// The session ID the agent assigned is kept in the runtime's session map
// (Info.AssignsSessionID). When a resume was asked for but a new session
// started (no loadSession capability, or the load failed), ResumeLost reports
// it and the runtime tells the user (backend.NoticeNewSession).
//
// Each turn is one session/prompt request; its response ends the turn
// ({stopReason: "end_turn"|"cancelled"|…}). Meanwhile the agent streams
// session/update notifications (agent_message_chunk → EventText; tool_call
// and tool_call_update → EventTool and EventToolResult) and may send
// session/request_permission requests (→ EventPermission, answered through
// TurnIO.Reply). Stopping an answer is the session/cancel notification; the
// runtime first rejects the pending permission requests, as ACP asks.
//
// ACP has no system prompt: Launch.SystemPrompt is sent as a leading text
// block of the first prompt of a new session (not of a loaded one, whose
// history has it already).
//
// Unless Config.Commands is set, a message that starts with "/" or "$" is
// sent with a leading zero width space, so the agent does not run it as one of
// its own slash commands (Gemini CLI's /memory, /init, /extensions and
// /restore change files or install software).
package acp

import (
	"cmp"
	"path/filepath"
	"strings"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// ProtocolVersion is the ACP version golauncher speaks.
const ProtocolVersion = 1

// Config describes an ACP agent.
type Config struct {
	// Info describes the agent's CLI; NewAdapter sets NoPerTurn and
	// AssignsSessionID (and clears PerTurnOnly).
	Info agentrt.Info
	// Args returns what follows the executable (Launch.Path) on the command
	// line, and the agent's environment variables and files (agentrt.Command);
	// nil starts the executable without arguments.
	Args func(l agentrt.Launch) (agentrt.Command, error)
	// ClientName and ClientVersion are sent in initialize's clientInfo
	// ("golauncher" and "dev" when empty).
	ClientName, ClientVersion string
	// Tool names a tool call for the tool steps (backend.Event.Tool and
	// Input); nil is DefaultTool.
	Tool func(c ToolCall) (tool, input string)
	// ToolResult is the result text shown of a call that ended
	// (backend.Event.Result); nil is c.Result.
	ToolResult func(c ToolCall) string
	// Commands lets messages that start with "/" or "$" reach the agent as
	// they are (its slash commands).
	Commands bool
}

// ToolCall is what the agent told about a tool call so far.
type ToolCall struct {
	// ID is the agent's toolCallId; Title, Kind (ACP's tool kinds: "read",
	// "edit", "search", "fetch", "execute", "other", …) and Status
	// ("pending", "in_progress", "completed", "failed") as the agent last
	// reported them.
	ID, Title, Kind, Status string
	// RawInput is the call's arguments, when the agent reports them (Gemini
	// CLI 0.34 does not).
	RawInput []byte
	// Done reports that the call ended (completed or failed); Result is the
	// text of its content then.
	Done, Failed bool
	Result       string
}

// NewAdapter returns the runtime adapter of the agent c describes.
func NewAdapter(c Config) agentrt.Adapter {
	c.Info.NoPerTurn, c.Info.AssignsSessionID, c.Info.PerTurnOnly = true, true, false
	c.ClientName = cmp.Or(c.ClientName, "golauncher")
	c.ClientVersion = cmp.Or(c.ClientVersion, "dev")
	if c.Tool == nil {
		c.Tool = DefaultTool
	}
	return &adapter{c: c}
}

// Custom is the configuration of an ACP agent the user names: command (a
// path, or a name looked up on PATH) started with args. Its version is read
// with --version when it prints one (agents that print none are taken as
// 0.0.0, untested).
func Custom(name, command string, args []string) Config {
	info := agentrt.Info{
		ID: "acp", Name: name,
		VersionArgs: []string{"--version"},
		ParseVersion: func(out string) string {
			return cmp.Or(agentrt.ParseVersion(out), "0.0.0")
		},
		TestedMajor: -1,
		WorkDir:     "acp-" + safeName(name),
	}
	if filepath.IsAbs(command) {
		info.InstallPaths = []string{command}
	} else {
		info.Commands = []string{command}
	}
	args = append([]string(nil), args...)
	return Config{Info: info, Args: func(agentrt.Launch) (agentrt.Command, error) {
		return agentrt.Command{Argv: append([]string(nil), args...)}, nil
	}}
}

// safeName keeps the letters, digits, '-' and '_' of a name.
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, s)
	return cmp.Or(s, "agent")
}

// adapter is an ACP agent's agentrt.Adapter.
type adapter struct{ c Config }

// Info implements agentrt.Adapter.
func (a *adapter) Info() agentrt.Info { return a.c.Info }

// Command implements agentrt.Adapter.
func (a *adapter) Command(l agentrt.Launch) (agentrt.Command, error) {
	var cmd agentrt.Command
	if a.c.Args != nil {
		var err error
		if cmd, err = a.c.Args(l); err != nil {
			return agentrt.Command{}, err
		}
	}
	cmd.Argv = append([]string{l.Path}, cmd.Argv...)
	return cmd, nil
}

// MCPConfig implements agentrt.Adapter: the tool server goes into
// session/new's mcpServers.
func (*adapter) MCPConfig(agentrt.MCPServer) ([]byte, error) { return nil, nil }

// NewCodec implements agentrt.Adapter.
func (a *adapter) NewCodec(l agentrt.Launch) agentrt.Codec { return &codec{cfg: &a.c, l: l} }

// ExitOutcome implements agentrt.Adapter: the end of the agent's stderr,
// classified by its words.
func (*adapter) ExitOutcome(text string) agentrt.Outcome {
	return agentrt.Outcome{Kind: agentrt.Failed, Err: &backend.Error{Kind: classifyText(text), Message: text}}
}

// classifyText classifies an error text of an agent by its words.
func classifyText(text string) backend.ErrorKind {
	t := strings.ToLower(text)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(t, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("rate limit", "quota", "resource_exhausted", "resource exhausted", "429", "usage limit"):
		return backend.ErrQuota
	case has("authentication required", "authentication failed", "not logged in", "not signed in",
		"please log in", "please login", "/auth", "api key is missing", "api key not", "unauthenticated", "invalid api key"):
		return backend.ErrNotLoggedIn
	}
	return backend.ErrFailed
}

// classifyRPC classifies a JSON-RPC error of the agent: -32000 is ACP's
// "authentication required" (Gemini CLI uses it for every sign-in problem),
// 429 Gemini CLI's rate limit.
func classifyRPC(e *rpcError) backend.ErrorKind {
	if e.Code == 429 {
		return backend.ErrQuota
	}
	if k := classifyText(e.Message); k != backend.ErrFailed {
		return k
	}
	// Signed in, but the service refuses this client (Gemini CLI 0.34 with a
	// Google account of Gemini Code Assist for individuals: "This client is
	// no longer supported …"): signing in again would not help.
	if strings.Contains(strings.ToLower(e.Message), "no longer supported") {
		return backend.ErrFailed
	}
	if e.Code == -32000 {
		return backend.ErrNotLoggedIn
	}
	return backend.ErrFailed
}

// rpcErr is e as a backend error.
func rpcErr(e *rpcError) *backend.Error {
	return &backend.Error{Kind: classifyRPC(e), Message: cmp.Or(strings.TrimSpace(e.Message), "error "+itoa(e.Code))}
}

// lostSession reports whether e says the agent does not know the session.
func lostSession(e *rpcError) bool {
	return strings.Contains(strings.ToLower(e.Message), "session not found")
}
