// Package claudecode is the AI backend that chats through the user's
// installed Claude Code CLI (docs/ai.md §6.1). A conversation keeps one
// `claude -p` process running that reads user messages and writes the
// answers as stream-json, so the subscription the user signed in with is
// used and no API key is needed. The process runs isolated from the user's
// own Claude Code setup (no settings, hooks, MCP servers, slash commands or
// local tools) in a Job Object without a console window. The only tools the
// model may get are web search and golauncher's own MCP server (Options.Tools),
// through which it uses the launcher's plugins.
//
// When the CLI's version is outside the tested range, or the persistent
// process misbehaves, a conversation falls back to one process per turn
// (`claude -p --resume <id>`).
package claudecode

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/proc"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// The CLI versions the stream-json protocol was verified with. Versions
// below MinVersion are refused; versions with another major version than
// TestedMajor work in the more careful per-turn mode.
const (
	MinVersion  = "2.1.0"
	TestedMajor = 2
)

// Defaults for Options.
const (
	DefaultMaxProcesses = 2
	DefaultGracePeriod  = 2 * time.Second
)

// interruptTimeout is how long an interrupted answer may take to end before
// the process is killed.
const interruptTimeout = 5 * time.Second

// checkTimeout caps `claude --version`.
const checkTimeout = 30 * time.Second

// envDrop are variables that Claude Code sets for programs it starts; the
// launcher has them when it was started from inside Claude Code, and the CLI
// must not see them.
var envDrop = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT"}

// Options configure a Backend.
type Options struct {
	// Path is the CLI to run (the "Claude Code path" preference); empty
	// looks it up on PATH and in the usual install locations.
	Path string
	// Dir is the working directory of the CLI; empty is
	// %LOCALAPPDATA%\golauncher\ai. It is created when missing.
	Dir string
	// Log receives lengths, durations and errors, never message content.
	Log *slog.Logger
	// PerTurn forces one process per turn.
	PerTurn bool
	// MaxProcesses caps the persistent processes kept across conversations
	// (DefaultMaxProcesses when 0). A busy one is never stopped, so the cap
	// can be exceeded for a while.
	MaxProcesses int
	// Env are NAME=value entries added to (or replacing in) the CLI's
	// environment.
	Env []string
	// GracePeriod is how long a released process may take to exit before it
	// is killed (DefaultGracePeriod when 0).
	GracePeriod time.Duration
	// Tools is golauncher's MCP tool server (docs/ai.md §4.3). With it, a
	// conversation opened with OpenOptions.LauncherTools lets the model use
	// the launcher's plugins through that server's tools, and no others.
	Tools *ToolServer
}

// ToolServer is how the CLI starts golauncher's MCP server (stdio).
type ToolServer struct {
	// Command and Args start the server, e.g. golauncher.exe mcp.
	Command string
	Args    []string
	// Env are variables set for the server (e.g. the pipe's token).
	Env map[string]string
	// SessionEnv, when set, is the name of a variable that receives the
	// conversation's session ID, e.g. "GOLAUNCHER_AGENT_SESSION", so the
	// server can tie its tool calls to the conversation.
	SessionEnv string
}

// Backend is the Claude Code backend. It is safe for concurrent use.
type Backend struct {
	o   Options
	log *slog.Logger

	mu     sync.Mutex
	info   *cliInfo
	convs  map[*conversation]bool
	live   []*conversation // with a persistent process, least recently used first
	closed bool
}

// cliInfo is what Check found.
type cliInfo struct {
	path     string
	version  string
	untested bool
}

// New returns a backend configured by o.
func New(o Options) *Backend {
	if o.MaxProcesses <= 0 {
		o.MaxProcesses = DefaultMaxProcesses
	}
	if o.GracePeriod <= 0 {
		o.GracePeriod = DefaultGracePeriod
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Backend{o: o, log: log.With("backend", "claudecode"), convs: map[*conversation]bool{}}
}

// Name implements backend.Backend.
func (b *Backend) Name() string { return "Claude Code" }

// Check implements backend.Backend: it finds the CLI and runs
// `claude --version`.
func (b *Backend) Check(ctx context.Context) (backend.Status, error) {
	path, err := b.find()
	if err != nil {
		return backend.Status{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	out, code, err := b.runVersion(ctx, path)
	if err != nil {
		return backend.Status{}, &backend.Error{Kind: backend.ErrFailed, Message: err.Error()}
	}
	ver := parseVersion(out)
	if code != 0 || ver == "" {
		msg := strings.TrimSpace(out)
		if msg == "" {
			msg = fmt.Sprintf("claude --version: exit code %d", code)
		}
		return backend.Status{}, &backend.Error{Kind: backend.ErrFailed, Message: msg}
	}
	if compareVersions(ver, MinVersion) < 0 {
		return backend.Status{}, &backend.Error{Kind: backend.ErrVersion, Version: ver}
	}
	info := &cliInfo{path: path, version: ver, untested: majorOf(ver) != TestedMajor}
	b.mu.Lock()
	b.info = info
	b.mu.Unlock()
	b.log.Info("claude code found", "version", ver, "untested", info.untested)
	return backend.Status{Version: ver, Untested: info.untested}, nil
}

// resolve returns what the last Check found, running it when needed.
func (b *Backend) resolve(ctx context.Context) (*cliInfo, error) {
	b.mu.Lock()
	info := b.info
	b.mu.Unlock()
	if info != nil {
		return info, nil
	}
	if _, err := b.Check(ctx); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.info, nil
}

// Open implements backend.Backend. The process is started by the first Send.
func (b *Backend) Open(ctx context.Context, o backend.OpenOptions) (backend.Conversation, error) {
	if o.SessionID == "" {
		return nil, errors.New("claudecode: no session ID")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, errors.New("claudecode: backend closed")
	}
	c := &conversation{b: b, oo: o, closedCh: make(chan struct{})}
	b.convs[c] = true
	return c, nil
}

// Close ends every conversation and kills their processes.
func (b *Backend) Close() error {
	b.mu.Lock()
	b.closed = true
	convs := make([]*conversation, 0, len(b.convs))
	for c := range b.convs {
		convs = append(convs, c)
	}
	b.mu.Unlock()
	for _, c := range convs {
		c.Close()
	}
	return nil
}

// forget removes a closed conversation.
func (b *Backend) forget(c *conversation) {
	b.mu.Lock()
	delete(b.convs, c)
	b.live = slices.DeleteFunc(b.live, func(x *conversation) bool { return x == c })
	b.mu.Unlock()
}

// acquire records that c started a persistent process and returns the idle
// conversations whose processes must be released to stay within
// MaxProcesses, least recently used first.
func (b *Backend) acquire(c *conversation) []*conversation {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.live = slices.DeleteFunc(b.live, func(x *conversation) bool { return x == c })
	b.live = append(b.live, c)
	var victims []*conversation
	excess := len(b.live) - b.o.MaxProcesses
	for _, x := range b.live {
		if excess <= 0 {
			break
		}
		if x != c && !x.busy.Load() {
			victims = append(victims, x)
			excess--
		}
	}
	return victims
}

// touch marks c as the most recently used, if it has a process.
func (b *Backend) touch(c *conversation) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i := slices.Index(b.live, c); i >= 0 {
		b.live = append(slices.Delete(b.live, i, i+1), c)
	}
}

// drop records that c no longer has a persistent process.
func (b *Backend) drop(c *conversation) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.live = slices.DeleteFunc(b.live, func(x *conversation) bool { return x == c })
}

// liveCount is the number of conversations with a persistent process.
func (b *Backend) liveCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.live)
}

// find locates the CLI.
func (b *Backend) find() (string, error) {
	if b.o.Path != "" {
		if fi, err := os.Stat(b.o.Path); err == nil && !fi.IsDir() {
			return b.o.Path, nil
		}
		return "", &backend.Error{Kind: backend.ErrNotInstalled, Message: b.o.Path}
	}
	if p, err := exec.LookPath("claude"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, nil
		}
		return p, nil
	}
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "claude.exe"))
	}
	if appData := os.Getenv("APPDATA"); appData != "" {
		candidates = append(candidates, filepath.Join(appData, "npm", "claude.cmd"))
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", &backend.Error{Kind: backend.ErrNotInstalled}
}

// dir returns the CLI's working directory, creating it.
func (b *Backend) dir() (string, error) {
	d := b.o.Dir
	if d == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		d = filepath.Join(cache, "golauncher", "ai")
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

// env returns the CLI's environment.
func (b *Backend) env() []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(envDrop, func(d string) bool { return strings.EqualFold(k, d) })
	})
	return proc.MergeEnv(env, b.o.Env)
}

// runVersion runs `claude --version` and returns its output and exit code.
func (b *Backend) runVersion(ctx context.Context, path string) (string, int, error) {
	dir, err := b.dir()
	if err != nil {
		return "", 0, err
	}
	out := &tail{max: 4096}
	p, err := proc.Start(proc.Options{Argv: []string{path, "--version"}, Dir: dir, Env: b.env(), Output: out})
	if err != nil {
		return "", 0, err
	}
	stop := context.AfterFunc(ctx, p.Stop)
	defer stop()
	code, err := p.Wait()
	if err != nil {
		return "", 0, err
	}
	if ctx.Err() != nil {
		return "", 0, ctx.Err()
	}
	return out.String(), code, nil
}

// parseVersion returns the first x.y.z in out, e.g. "2.1.296" from
// "2.1.296 (Claude Code)".
func parseVersion(out string) string {
	for f := range strings.FieldsSeq(out) {
		f = strings.TrimPrefix(f, "v")
		parts := strings.Split(f, ".")
		if len(parts) < 3 {
			continue
		}
		ok := true
		for _, p := range parts[:3] {
			if _, err := strconv.Atoi(p); err != nil {
				ok = false
			}
		}
		if ok {
			return strings.Join(parts[:3], ".")
		}
	}
	return ""
}

// versionParts returns the numbers of a x.y.z version.
func versionParts(v string) [3]int {
	var n [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		n[i], _ = strconv.Atoi(p)
	}
	return n
}

// compareVersions compares two x.y.z versions like strings.Compare.
func compareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// majorOf returns the major version.
func majorOf(v string) int { return versionParts(v)[0] }
