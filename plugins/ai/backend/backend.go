// Package backend is the contract between the builtin.ai plugin and where its
// answers come from (docs/ai.md §6): an agent CLI driven by the agent
// runtime (internal/ai/agentrt; Claude Code is internal/ai/claudecode,
// docs/ai.md §20) or a fake for tests and glctl.
package backend

import (
	"context"
	"errors"
	"fmt"
)

// Backend is where the answers come from (docs/ai.md §6). The launcher's
// backend runs the user's Claude Code CLI (internal/ai/claudecode); tests
// and glctl use a FakeBackend. Implementations must be safe for concurrent
// use.
type Backend interface {
	// Name is the backend's display name, e.g. "Claude Code". It is a
	// product name and not translated.
	Name() string
	// Check reports whether the backend can be used: a *Error of kind
	// ErrNotInstalled, ErrNotLoggedIn or ErrVersion when it cannot. It may
	// take a moment (it may run the CLI once), so call it in the background.
	Check(ctx context.Context) (Status, error)
	// Open returns a conversation for a session. Starting it may be lazy: a
	// process can be started by the first Send.
	Open(ctx context.Context, o OpenOptions) (Conversation, error)
}

// Status describes a usable backend.
type Status struct {
	// Version is the backend's version, e.g. "2.1.296".
	Version string
	// Untested is set when the version is outside the range the backend was
	// verified with; it still works, more carefully (one process per turn
	// for Claude Code).
	Untested bool
	// Reduced are the features turned off or degraded because the CLI is
	// older than what golauncher was built for (Feature* codes); the plugin
	// says so and offers the update. Isolation is never reduced: what the
	// CLI cannot do safely is turned off instead.
	Reduced []string
}

// The features a Status can report as reduced.
const (
	// FeatureWebSearch: the model cannot search or fetch web pages.
	FeatureWebSearch = "web-search"
	// FeatureLauncherTools: the model cannot use the launcher's plugins.
	FeatureLauncherTools = "launcher-tools"
	// FeatureStreaming: answers arrive whole instead of as they are written.
	FeatureStreaming = "streaming"
	// FeatureModel: the model preference is ignored (the CLI's default).
	FeatureModel = "model"
)

// Reduces reports whether s reports feature as reduced.
func (s Status) Reduces(feature string) bool {
	for _, f := range s.Reduced {
		if f == feature {
			return true
		}
	}
	return false
}

// OpenOptions describe the session of a conversation.
type OpenOptions struct {
	// SessionID identifies the session (a UUID). Resume continues a session
	// that already has messages; otherwise a new one starts with this ID.
	SessionID string
	Resume    bool
	// Model is the model name or alias ("" for the backend's default).
	Model string
	// WebSearch allows the model to search and fetch web pages (never any
	// tool that touches this computer).
	WebSearch bool
	// LauncherTools lets the model use the launcher's plugins through
	// golauncher's MCP tools (list_plugins, search, run_command, get_detail,
	// run_action; docs/ai.md §4). It only has an effect when the backend is
	// configured with a tool server.
	LauncherTools bool
	// SystemPrompt is appended to the backend's system prompt.
	SystemPrompt string
}

// Conversation is one chat session with a backend.
type Conversation interface {
	// Send sends a user message and returns the events of the answer. The
	// channel is closed after the EventDone or EventError that ends the
	// turn. Only one turn runs at a time: Send fails while one is running.
	// Cancelling ctx stops the answer like Cancel.
	Send(ctx context.Context, text string) (<-chan Event, error)
	// Cancel stops the answer being written; its turn ends with an EventDone
	// marked Cancelled. It does nothing when no answer is running.
	Cancel()
	// Release frees what the conversation holds while idle (Claude Code's
	// process) without ending it: the next Send resumes the session.
	Release()
	// Close ends the conversation and frees everything.
	Close() error
}

// EventKind is the kind of an Event.
type EventKind string

const (
	// EventText is a piece of the answer's text (Event.Text).
	EventText EventKind = "text"
	// EventTool reports that the model uses a tool (Event.Tool, e.g.
	// "WebSearch" or "mcp__golauncher__search"), shown as a step line.
	// Event.ToolID identifies the call when the backend knows it; the
	// arguments (Event.Input) may arrive in a later EventTool with the same
	// ToolID, so consumers update the step by ToolID.
	EventTool EventKind = "tool"
	// EventToolResult reports the result of a tool call (Event.ToolID,
	// Event.Result, Event.IsError).
	EventToolResult EventKind = "tool-result"
	// EventPermission asks the user to allow a tool call (Event.Permission):
	// the agent waits until Permission.Respond is called. ACP agents send it
	// (session/request_permission, docs/ai.md §21); Claude Code and Codex
	// never do (their tools are pre-approved and golauncher's own tool layer
	// asks). A consumer that cannot ask must respond with "" (reject).
	EventPermission EventKind = "permission"
	// EventNotice tells the user something about the conversation itself;
	// Event.Text is one of the Notice codes (e.g. NoticeNewSession), which the
	// consumer words.
	EventNotice EventKind = "notice"
	// EventDone ends a turn (Event.Usage, Event.Cancelled).
	EventDone EventKind = "done"
	// EventError ends a turn that failed (Event.Err, usually a *Error).
	EventError EventKind = "error"
)

// Event is something that happened while an answer is written.
type Event struct {
	Kind EventKind
	Text string
	Tool string
	// ToolID is the id of the tool call, for EventTool and EventToolResult
	// (may be "" for EventTool when the backend has none).
	ToolID string
	// Input is the JSON text of the tool call's arguments (EventTool; "" when
	// not known yet).
	Input string
	// Result is the text of the tool's result and IsError tells whether the
	// call failed (EventToolResult).
	Result  string
	IsError bool
	// Usage is set on EventDone when the backend reports it.
	Usage *Usage
	// Cancelled is set on EventDone when the answer was stopped.
	Cancelled bool
	Err       error
	// Permission is the request of an EventPermission.
	Permission *PermissionRequest
}

// NoticeNewSession (EventNotice): the backend could not continue the
// conversation it had (the agent cannot load sessions, or no longer has
// this one), so this answer starts a new one without the earlier messages.
const NoticeNewSession = "new-session"

// PermissionRequest is an agent asking to run a tool call (EventPermission).
type PermissionRequest struct {
	// ToolID identifies the call (as in EventTool); Tool and Input describe
	// it, Title is the agent's own description of it.
	ToolID, Tool, Input, Title string
	// Kind is the agent's kind of the tool (ACP's tool kinds: "read", "edit",
	// "delete", "move", "search", "execute", "think", "fetch", "other"; ""
	// when unknown).
	Kind string
	// Options are the answers the agent offers (allow once, always, reject…).
	Options []PermissionOption
	// Respond answers with the ID of one of Options; "" rejects (also when the
	// turn was stopped). Only the first call counts.
	Respond func(optionID string)
	// Done, when set, is closed once the request was answered by anyone
	// (the user, the timeout, a stop): a dialog still asking can close.
	Done <-chan struct{}
}

// Destructive reports whether the request's kind changes or removes things
// or runs programs, so that it is never allowed for the rest of a session.
func (r *PermissionRequest) Destructive() bool {
	switch r.Kind {
	case "edit", "delete", "move", "execute":
		return true
	}
	return false
}

// Option returns the ID of the first option of kind ("" when none).
func (r *PermissionRequest) Option(kind string) string {
	for _, o := range r.Options {
		if o.Kind == kind {
			return o.ID
		}
	}
	return ""
}

// PermissionAsk is a permission request (EventPermission) as the user is
// asked about it, in the launcher's confirmation dialog (docs/ai.md §5).
type PermissionAsk struct {
	// Client names who asks, e.g. "Gemini CLI (AI Chat)".
	Client string
	// Action is what the agent wants to do (its own description), Tool the
	// tool's name, Message an extra line ("" for none).
	Action, Tool, Message string
	// Destructive asks with the danger style; AllowSession offers "Allow for
	// this session" (only when the agent offers "always" and the request is
	// not destructive).
	Destructive, AllowSession bool
}

// PermissionDecision is the user's answer to a PermissionAsk.
type PermissionDecision int

const (
	// PermissionDeny: declined, or no answer in time.
	PermissionDeny PermissionDecision = iota
	// PermissionAllow: this once.
	PermissionAllow
	// PermissionAllowSession: this and the same again in the session.
	PermissionAllowSession
)

// PermissionAsker asks the user about a permission request and waits for
// the answer (deny when ctx ends). The host provides it; it also records the
// request in the AI activity log.
type PermissionAsker func(ctx context.Context, a PermissionAsk) PermissionDecision

// Forgetter is implemented by backends that keep something about a
// session (e.g. the CLI's own session ID): Forget drops it when the chat is
// deleted.
type Forgetter interface {
	Forget(sessionID string)
}

// PermissionOption is an answer to a PermissionRequest.
type PermissionOption struct {
	ID    string
	Title string
	// Kind is "allow_once", "allow_always", "reject_once" or "reject_always"
	// (ACP's option kinds).
	Kind string
}

// Usage is what a turn consumed.
type Usage struct {
	InputTokens  int
	OutputTokens int
	// Model is the model that answered, e.g. "claude-sonnet-5-5".
	Model string
}

// ErrorKind classifies backend errors so the plugin can explain them.
type ErrorKind string

const (
	// ErrNotInstalled: the CLI is not installed (or not found).
	ErrNotInstalled ErrorKind = "not-installed"
	// ErrNotLoggedIn: the CLI is not signed in.
	ErrNotLoggedIn ErrorKind = "not-logged-in"
	// ErrQuota: the subscription's usage limit was reached; Message is the
	// CLI's own text.
	ErrQuota ErrorKind = "quota"
	// ErrVersion: the CLI is too old.
	ErrVersion ErrorKind = "version"
	// ErrUnavailable: the backend chosen in the preferences does not exist
	// in this version; Message is its ID.
	ErrUnavailable ErrorKind = "unavailable"
	// ErrFailed: anything else; Message is the CLI's text.
	ErrFailed ErrorKind = "failed"
)

// Error is a backend error. Message is the backend's own text (shown as is,
// e.g. the CLI's message about the usage limit).
type Error struct {
	Kind    ErrorKind
	Message string
	// Version is the version found, for ErrVersion.
	Version string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return string(e.Kind)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

// KindOf returns the kind of a backend error, ErrFailed for other errors.
func KindOf(err error) ErrorKind {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Kind
	}
	return ErrFailed
}
