// Package agent is golauncher's AI tool layer (docs/ai.md §4): a handful of
// generic tools — list_plugins, search, run_command, get_detail and
// run_action — through which an AI client uses the launcher's plugins. They
// run in the launcher's process through core, the same code paths as the
// launcher window and glctl, with risk levels and a policy that asks the user
// (core.Controller.ConfirmExternal) before anything changes.
//
// Clients reach the tools over MCP (mcp.go): `golauncher mcp` (bridge.go) is
// a stdio MCP server for Claude Desktop and Claude Code that forwards to the
// running launcher over a named pipe only the current user can open
// (serve.go). The launcher's own AI chat uses the same bridge with a token
// that identifies it.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/i18n"
)

// Environment variables of `golauncher mcp` set by the launcher for its own
// AI chat: the process token and the chat's session ID.
const (
	EnvToken   = "GOLAUNCHER_AGENT_TOKEN"
	EnvSession = "GOLAUNCHER_AGENT_SESSION"
)

// ServerName is the MCP server's name; Claude Code calls the tools
// mcp__golauncher__<tool>.
const ServerName = "golauncher"

// HandleTTL is how long an item handle stays valid.
var HandleTTL = 10 * time.Minute

// Limits of tool results.
const (
	DefaultLimit  = 20
	MaxLimit      = 50
	maxTitle      = 200
	maxText       = 300
	maxMarkdown   = 4000
	maxQuery      = 500
	maxLogSummary = 160
)

// processToken is the token of this launcher process.
var processToken = sync.OnceValue(func() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
})

// ProcessToken returns the random token of this process, which the launcher
// passes to the bridge it configures for its own AI chat (EnvToken).
func ProcessToken() string { return processToken() }

// Confirmer asks the user to confirm an action (core.Controller.ConfirmExternal).
type Confirmer func(ctx context.Context, req core.ExternalRequest) core.Decision

// Options configure an Agent.
type Options struct {
	Engine *core.Engine
	Config *config.Store
	// Confirm asks the user; nil denies everything that needs asking.
	Confirm Confirmer
	// Activity records the tool calls (nil: none).
	Activity *Activity
	// Token identifies the launcher's own AI chat ("" accepts none).
	Token string
	// Version is reported as the MCP server's version.
	Version string
	Log     *slog.Logger
	// Now is the clock of handles and the activity (default: the engine's).
	Now func() time.Time
}

// Agent runs the tools. It is safe for concurrent use.
type Agent struct {
	o   Options
	eng *core.Engine
	loc *i18n.Localizer
	log *slog.Logger

	mu        sync.Mutex
	confirm   Confirmer
	sessions  map[string]*Session // chat sessions by chat ID
	instances int                 // view instances opened
	clients   int                 // external sessions, for their keys
}

// New returns an agent over o.Engine.
func New(o Options) *Agent {
	if o.Now == nil {
		o.Now = o.Engine.Now
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Agent{o: o, eng: o.Engine, loc: o.Engine.Core(), log: log.With("component", "agent"), confirm: o.Confirm,
		sessions: map[string]*Session{}}
}

// SetConfirmer replaces the function that asks the user.
func (a *Agent) SetConfirmer(c Confirmer) {
	a.mu.Lock()
	a.confirm = c
	a.mu.Unlock()
}

// Activity returns the activity log, or nil.
func (a *Agent) Activity() *Activity { return a.o.Activity }

// Token returns the token that identifies the launcher's own chat.
func (a *Agent) Token() string { return a.o.Token }

// Client describes who uses the tools.
type Client struct {
	// Name and Version are the MCP clientInfo (e.g. "claude-ai", "claude-code").
	Name    string
	Version string
	// Chat is set for the launcher's own AI chat, whose ChatSession is the
	// chat's ID.
	Chat        bool
	ChatSession string
}

// external reports whether the client is an AI app outside the launcher,
// which needs Settings › AI › "Allow AI apps to use golauncher".
func (c Client) external() bool { return !c.Chat }

// DisplayName is how the client is named to the user ("Claude Desktop").
func (a *Agent) DisplayName(c Client) string {
	switch {
	case c.Chat:
		return a.loc.T("agent.client.chat")
	case c.Name == "claude-ai" || c.Name == "Claude Desktop" || c.Name == "claude-desktop":
		return a.loc.T("agent.client.desktop")
	case c.Name == "claude-code":
		return a.loc.T("agent.client.code")
	case c.Name != "":
		return c.Name
	}
	return a.loc.T("agent.client.unknown")
}

// Session is what one client has: its handles and the actions the user
// allowed for the session. A chat keeps its session across reconnections
// (each Claude Code process starts a new bridge); an external client has one
// per connection.
type Session struct {
	a      *Agent
	key    string
	client Client

	mu      sync.Mutex
	handles map[string]*handle
	next    int
	allowed map[string]bool // action keys allowed for the session
	views   []core.Nav      // view instances opened, closed with the session
}

// Session returns the session of client c: the chat's own, or a new one.
// Release it when the connection ends.
func (a *Agent) Session(c Client) *Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c.Chat && c.ChatSession != "" {
		if s, ok := a.sessions[c.ChatSession]; ok {
			s.mu.Lock()
			s.client = c
			s.mu.Unlock()
			return s
		}
		s := a.newSession("chat:"+c.ChatSession, c)
		a.sessions[c.ChatSession] = s
		return s
	}
	a.clients++
	return a.newSession("client:"+strconv.Itoa(a.clients), c)
}

func (a *Agent) newSession(key string, c Client) *Session {
	return &Session{a: a, key: key, client: c, handles: map[string]*handle{}, allowed: map[string]bool{}}
}

// Release ends an external client's session: its views close. A chat's
// session stays (the chat may reconnect), its views are closed.
func (s *Session) Release() {
	s.mu.Lock()
	views := s.views
	s.views = nil
	s.mu.Unlock()
	if len(views) > 0 {
		s.a.eng.CloseViews(views)
	}
}

// Client returns the session's client.
func (s *Session) Client() Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

// setClient records the client's MCP clientInfo.
func (s *Session) setClient(name, version string) {
	s.mu.Lock()
	s.client.Name, s.client.Version = name, version
	s.mu.Unlock()
}

// newInstance returns a view instance ID for the tool layer's views.
func (a *Agent) newInstance() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.instances++
	return "ai" + strconv.Itoa(a.instances)
}

// now is the agent's clock.
func (a *Agent) now() time.Time { return a.o.Now() }

// record adds a tool call to the activity log.
func (a *Agent) record(s *Session, tool, summary string, outcome Outcome, detail string) {
	if a.o.Activity == nil {
		return
	}
	c := s.Client()
	a.o.Activity.Add(Entry{
		Time:    a.now(),
		Client:  a.DisplayName(c),
		Tool:    tool,
		Summary: truncate(summary, maxLogSummary),
		Outcome: outcome,
		Detail:  truncate(detail, maxLogSummary),
	})
}

// handleKinds of the session table.
type handleKind int

const (
	kindItem handleKind = iota
	kindView
	kindForm
)

// handle is an entry of the session's table: something the client got from
// a tool and may act on.
type handle struct {
	kind    handleKind
	entry   core.Entry // kindItem
	nav     *core.Nav  // the view it came from (nil: the root search); the view itself for kindView and kindForm
	query   string     // the search text the view was rendered with
	form    *formInfo  // kindForm
	expires time.Time
}

// put adds h to the table and returns its ID: "i1", "v2", "f3".
func (s *Session) put(h *handle) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.a.now()
	for id, old := range s.handles {
		if now.After(old.expires) {
			delete(s.handles, id)
		}
	}
	s.next++
	prefix := map[handleKind]string{kindItem: "i", kindView: "v", kindForm: "f"}[h.kind]
	id := prefix + strconv.Itoa(s.next)
	h.expires = now.Add(HandleTTL)
	s.handles[id] = h
	return id
}

// get returns a handle that has not expired.
func (s *Session) get(id string) (*handle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.handles[id]
	if !ok {
		return nil, fmt.Errorf("unknown handle %q: use a handle from a recent search, run_command or run_action result", id)
	}
	if s.a.now().After(h.expires) {
		delete(s.handles, id)
		return nil, fmt.Errorf("handle %q has expired (handles last %v): search again", id, HandleTTL)
	}
	return h, nil
}

// addView remembers a view instance opened for the session.
func (s *Session) addView(nav core.Nav) {
	s.mu.Lock()
	s.views = append(s.views, nav)
	s.mu.Unlock()
}

// allowedFor reports whether the user allowed an action for the session.
func (s *Session) allowedFor(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allowed[key]
}

func (s *Session) allow(key string) {
	s.mu.Lock()
	s.allowed[key] = true
	s.mu.Unlock()
}

// truncate shortens s to at most n runes.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// limitOf returns the item limit asked for.
func limitOf(n int) int {
	if n <= 0 {
		return DefaultLimit
	}
	return min(n, MaxLimit)
}
