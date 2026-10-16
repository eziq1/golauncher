// Package agentrt is the agent runtime (docs/ai.md §19, §20): what every
// AI backend that drives an agent CLI needs, whichever CLI it is. It
// implements backend.Backend on top of an Adapter that knows one CLI's
// command line and protocol (internal/ai/claudecode is the first).
//
// The runtime owns:
//
//   - finding the CLI (configured path, PATH, usual install locations) and
//     the version gate (too old: refused; another major version: works, in
//     the more careful per-turn mode);
//   - process supervision: every process runs in a Job Object without a
//     console window; a conversation's process starts with its first
//     message; it keeps running between turns (persistent mode) or runs once
//     per turn, and falls back from persistent to per turn when the CLI
//     misbehaves; at most Options.MaxProcesses persistent processes are kept
//     (least recently used idle ones are released first); a process that
//     crashes is restarted for the next turn; stop interrupts the answer
//     (persistent) or kills the process (per turn);
//   - the idle release hook: Conversation.Release ends an idle process, the
//     next Send resumes the session (the plugin's timers decide when, R59);
//   - an isolated working directory (%LOCALAPPDATA%\golauncher\ai) and an
//     environment without the variables a parent agent set;
//   - golauncher's MCP tool server for the conversation (token and session
//     environment, written to a 0600 file the adapter asks for, removed when
//     the conversation closes);
//   - the mapping from chat IDs to the CLI's session IDs, for CLIs that
//     assign their own (SessionMap);
//   - error classification at the process level (not installed, too old,
//     exit without an answer) and the logging policy: lengths, durations and
//     error kinds only, never message content (docs/ai.md §9).
//
// # How other CLIs map onto Adapter (phase 2, not implemented)
//
// ACP (Agent Client Protocol; e.g. `gemini --acp`, or Zed's claude-code-acp
// and codex-acp): Info.PerTurnOnly is false and there is no per-turn mode
// (Info.NoPerTurn), so the fallback is a restarted persistent process.
// Command returns the agent's command line. MCPConfig returns nil: the tool
// server (Launch.Tools) goes into session/new's mcpServers instead. The Codec
// is a Handshaker: Handshake sends initialize, then session/load (when
// Launch.Resume and the agent has the loadSession capability) or
// session/new, and returns the session ID the agent assigned
// (Info.AssignsSessionID, kept in the SessionMap). Message is a session/prompt
// request; the Decoder turns session/update notifications into events
// (agent_message_chunk → EventText, tool_call → EventTool, tool_call_update →
// EventToolResult), answers session/request_permission requests through
// TurnIO.Reply after the user decided (EventPermission), and reports end at
// the response to the session/prompt request (its stopReason "cancelled" →
// Cancelled). Interrupt is the session/cancel notification. JSON-RPC errors
// map to Outcome errors (auth → ErrNotLoggedIn and so on).
//
// Codex CLI (`codex exec --json`, `codex exec resume <id>`): Info.PerTurnOnly
// and Info.AssignsSessionID. Command returns `codex exec --json` with
// `--sandbox read-only`, `-c mcp_servers.golauncher.*` from Launch.Tools
// (MCPConfig returns nil), `resume <Launch.SessionID>` when resuming, and
// reads the prompt from stdin (Codec.Prompt). The Decoder reports the
// thread.started event's ID as Progress.SessionID, item events as text and
// tool steps, and end at turn.completed (usage) or turn.failed.
package agentrt

import (
	"context"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Adapter is one agent CLI: how to find it, how to start it, and how to
// speak its protocol. Its methods must be safe for concurrent use; per-process
// state lives in the Codec.
type Adapter interface {
	// Info describes the CLI. It is called when the runtime needs it and
	// should be cheap.
	Info() Info
	// Command is the command line of a process, and the variables added to
	// its environment.
	Command(l Launch) (Command, error)
	// MCPConfig returns the contents of the file that hands golauncher's tool
	// server to the CLI (the runtime writes it to a 0600 file in the working
	// directory and passes its path in Launch.MCPConfig), or nil when the
	// adapter passes the server otherwise (from Launch.Tools).
	MCPConfig(s MCPServer) ([]byte, error)
	// NewCodec returns the protocol state of one process.
	NewCodec(l Launch) Codec
	// ExitOutcome classifies a process that ended before it ended the turn;
	// text is the end of its stderr (or "<Name> exited with code N"). It
	// returns Failed with an error, or SessionInUse.
	ExitOutcome(text string) Outcome
}

// Info describes an agent CLI.
type Info struct {
	// ID names the backend in logs and internal error texts, e.g.
	// "claudecode".
	ID string
	// Name is the display name, e.g. "Claude Code" (a product name: not
	// translated).
	Name string
	// Commands are looked up on PATH, in order (e.g. "claude").
	Commands []string
	// InstallPaths are the usual install locations, tried after PATH.
	InstallPaths []string
	// VersionArgs print the version, e.g. ["--version"].
	VersionArgs []string
	// ParseVersion extracts x.y.z from their output (nil: ParseVersion).
	ParseVersion func(out string) string
	// MinVersion is the oldest version accepted; TestedMajor the major
	// version the protocol was verified with (another one works in per-turn
	// mode, or with NoPerTurn as usual but marked untested).
	MinVersion  string
	TestedMajor int
	// EnvDrop are variables removed from the CLI's environment (case
	// insensitive), e.g. those a parent agent sets for its children.
	EnvDrop []string
	// WorkDir is the subdirectory of the default working directory for this
	// CLI ("" for the directory itself).
	WorkDir string
	// PerTurnOnly: the CLI has no persistent mode (one process per turn).
	PerTurnOnly bool
	// NoPerTurn: the CLI has no per-turn mode; a misbehaving persistent
	// process is restarted instead of falling back.
	NoPerTurn bool
	// AssignsSessionID: the CLI chooses the session ID (reported through
	// Progress.SessionID or Handshaker), which the runtime maps to the chat's
	// ID; otherwise the chat's ID is the CLI's session ID.
	AssignsSessionID bool
}

// Launch is what a process is started for.
type Launch struct {
	// Path is the CLI found; Dir its working directory.
	Path, Dir string
	// Persistent: the process answers several turns (else one).
	Persistent bool
	// SessionID is the CLI's session ID: the chat's ID, or the one the CLI
	// assigned earlier ("" for a new session of a CLI that assigns them).
	// Resume continues it; otherwise a new session starts.
	SessionID string
	Resume    bool
	// Model ("" for the CLI's default), WebSearch and SystemPrompt come from
	// backend.OpenOptions.
	Model        string
	WebSearch    bool
	SystemPrompt string
	// Tools is golauncher's MCP tool server, nil when the model gets no
	// launcher tools; MCPConfig the path of the file written from
	// Adapter.MCPConfig ("" when none).
	Tools     *MCPServer
	MCPConfig string
	// Prompt is the message of a per-turn process (for CLIs that take it as
	// an argument; it is also written with Codec.Prompt).
	Prompt string
}

// Command is how a process is started.
type Command struct {
	Argv []string
	// Env are NAME=value entries added to (or replacing in) the environment.
	Env []string
}

// Codec speaks the CLI's protocol with one process.
type Codec interface {
	// Message is the input that sends a user message to a persistent process.
	Message(text string) ([]byte, error)
	// Prompt is what a per-turn process reads on stdin before it is closed
	// (nil: nothing).
	Prompt(text string) []byte
	// Interrupt is the input that stops the answer of a persistent process;
	// nil kills the process instead.
	Interrupt() []byte
	// NewDecoder returns the decoder of one turn's output.
	NewDecoder(io TurnIO) Decoder
}

// Handshaker is implemented by Codecs whose persistent process needs
// requests before the first message (ACP: initialize, session/new or
// session/load). Handshake returns the session ID the CLI assigned, if any.
// Claude Code needs none.
type Handshaker interface {
	Handshake(ctx context.Context, c Conn) (sessionID string, err error)
}

// Conn is a process's input and output during a handshake.
type Conn interface {
	// Write writes to the process's input.
	Write(b []byte) error
	// Read returns the next line of output; an error once the process ended
	// or ctx is done.
	Read(ctx context.Context) ([]byte, error)
}

// TurnIO is what a Decoder may do during a turn.
type TurnIO struct {
	// Emit delivers an event to the conversation's reader (text, tool
	// steps, permission requests). The final event is the runtime's.
	Emit func(backend.Event)
	// Reply writes to the process's input, e.g. the answer to a permission
	// request.
	Reply func(b []byte) error
}

// Decoder turns one turn's output lines into events.
type Decoder interface {
	// Line handles one line. end reports that it ended the turn (Outcome
	// tells how); garbage that it is not the protocol at all (a persistent
	// process then falls back to per-turn mode).
	Line(line []byte) (end, garbage bool)
	// Outcome is how the turn ended, after Line reported end. cancelled
	// tells whether the user asked to stop. It may still emit events (e.g. a
	// result's text that was not streamed).
	Outcome(cancelled bool) Outcome
	// Progress is what the output showed so far.
	Progress() Progress
}

// Progress is what a turn's output showed so far.
type Progress struct {
	// Output: a line of the protocol arrived.
	Output bool
	// Started: the CLI confirmed the session, which later processes resume.
	Started bool
	// Emitted: text or tool steps reached the user (a retry would repeat
	// them).
	Emitted bool
	// SessionID is the session ID the CLI reported ("" when none).
	SessionID string
}

// OutcomeKind is how a turn ended.
type OutcomeKind int

const (
	// Done: answered (Outcome.Usage).
	Done OutcomeKind = iota
	// Cancelled: stopped by the user.
	Cancelled
	// Failed: Outcome.Err (usually a *backend.Error).
	Failed
	// LostSession: the CLI does not know the session to resume; the runtime
	// starts a new one once.
	LostSession
	// SessionInUse: the session to start exists already; the runtime resumes
	// it once.
	SessionInUse
)

// Outcome is how a turn ended.
type Outcome struct {
	Kind  OutcomeKind
	Usage *backend.Usage
	Err   error
}

// MCPServer is golauncher's MCP tool server as one conversation's CLI starts
// it (stdio).
type MCPServer struct {
	// Name is the server's name in the CLI's configuration (ToolServerName).
	Name    string
	Command string
	// Args is never nil.
	Args []string
	// Env has ToolServer.Env and the conversation's session variable.
	Env map[string]string
}
