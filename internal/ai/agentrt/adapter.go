// Package agentrt is the agent runtime (docs/ai.md §19, §20): what every
// AI backend that drives an agent CLI needs, whichever CLI it is. It
// implements backend.Backend on top of an Adapter that knows one CLI's
// command line and protocol (internal/ai/claudecode is the first).
//
// The runtime owns:
//
//   - finding the CLI (configured path, PATH, usual install locations) and
//     the version gate (too old: refused; another major version: works, in
//     the more careful per-turn mode), and the capability probe: the
//     CLI's flags from its help, once per binary, from which the adapter
//     decides how to run an older version (Info.HelpArgs, Info.Fit,
//     docs/ai.md §22);
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
//     not signed in with Info.LoginArgs, exit without an answer) and the
//     logging policy: lengths, durations and error kinds only, never message
//     content (docs/ai.md §9);
//   - the permission bridge: an agent's permission request (EventPermission)
//     is answered at most once, and rejected when the user does not answer
//     within PermissionTimeout, stops the answer, or the turn ends;
//   - choosing among several installations (Info.Candidates: the highest
//     acceptable version), and telling the user when a session could not be
//     resumed (EventNotice).
//
// The adapters: Claude Code (internal/ai/claudecode, stream-json, persistent
// with a per-turn fallback); the generic ACP adapter (internal/ai/acp,
// persistent only: a Handshaker sends initialize and session/new or
// session/load, Message is session/prompt, Interrupt session/cancel, the
// Decoder maps session/update and session/request_permission), used by
// Gemini CLI (internal/ai/gemini); Codex (internal/ai/codex, `codex exec
// --json` per turn, `resume <id>`). See docs/ai.md §20 and §21.
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
	// Candidates, when set, replaces Commands and InstallPaths: it returns
	// the CLIs found, in order of preference. With more than one, Check uses
	// the one with the highest version. err explains why none can be used
	// (e.g. only a launcher script that needs another runtime was found); it
	// is reported when there is no candidate (as ErrNotInstalled unless it
	// is a *backend.Error).
	Candidates func() (paths []string, err error)
	// Discover, when set, lists every installation the search finds, in
	// order, with how it was found, including the launcher scripts it skips
	// (the settings show them, R84). It must be the same search as
	// Candidates. Without it the runtime lists Candidates, or the matches of
	// Commands on PATH and the InstallPaths that exist.
	Discover func() []Found
	// ResolvePath, when set, maps a configured CLI path to the CLI to run
	// (Codex: an npm launcher to the native binary of its installation).
	ResolvePath func(path string) string
	// PathEnv, when set, returns variables the CLI at path needs, merged into
	// the environment of each of its processes (the checks too), e.g. a PATH
	// on which a script finds its interpreter.
	PathEnv func(path string) []string
	// LoginArgs, when set, are run by Check after the version check: a
	// non-zero exit code means that the CLI is not signed in
	// (ErrNotLoggedIn, with the command's output as the message).
	LoginArgs []string
	// HelpArgs, when set, make Check probe the CLI's flags once per binary
	// (path, version, size and modification time; cached in memory and in
	// the working directory): they print its help, which ParseHelp turns
	// into Flags (nil: ParseHelpFlags). A probe that fails or finds nothing
	// leaves Launch.Flags nil (docs/ai.md §22).
	HelpArgs  []string
	ParseHelp func(out string) Flags
	// Fit, when set, decides from the version and the probed flags (nil when
	// unknown) how the CLI is run: per turn only, features turned off. An
	// error (usually ErrVersion: something it cannot do without is missing)
	// makes Check refuse the CLI. Command must apply the same decision.
	Fit func(version string, flags Flags) (Fit, error)
}

// Launch is what a process is started for.
type Launch struct {
	// Path is the CLI found; Dir its working directory.
	Path, Dir string
	// Version is the CLI's version as Check found it ("" when unknown, e.g.
	// for Runtime.Command), for flags that depend on it.
	Version string
	// Flags are the CLI's flags as Check probed them (Info.HelpArgs); nil
	// when unknown (no probe, it failed, or Runtime.Command): the adapter
	// then goes by Version, or assumes the flags it was tested with.
	Flags Flags
	// ChatID is the chat's ID (backend.OpenOptions.SessionID).
	ChatID string
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
	// Files are written into the working directory (Launch.Dir) before the
	// process starts, by name (a base name, 0600), e.g. a settings file the
	// CLI reads; Runtime.Command, which only previews, writes nothing.
	Files map[string][]byte
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

// ResumeLoser is implemented by Codecs whose Handshake may start a new
// session although Launch.Resume asked to continue one (the agent cannot
// load sessions, or it no longer has this one). ResumeLost reports that it
// did, after Handshake; the runtime then tells the user with an EventNotice
// (backend.NoticeNewSession) at the start of the turn.
type ResumeLoser interface {
	ResumeLost() bool
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
	//
	// A permission request (EventPermission) is bridged by the runtime: its
	// Respond is called at most once, with "" (reject) when the user does not
	// answer within PermissionTimeout, when the user stops the answer (before
	// Interrupt is sent) or when the turn ends; Permission.Done is closed
	// once it was answered. The decoder's Respond writes the answer (through
	// Reply) and maps "" to the agent's reject option.
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
