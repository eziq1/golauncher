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
// (`claude -p --resume <id>`). An older CLI that lacks some of the flags
// (found with `claude --help`) runs with older equivalents or without the
// features that need them, never with less isolation (compat.go,
// docs/ai.md §22).
package claudecode

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
		HelpArgs:     []string{"--help"},
		ParseHelp:    parseHelp,
		Fit:          fit,
		Attachments:  backend.AllAttachments(),
	}
}

// noAutoMemory turns Claude Code's auto memory off; --safe-mode does that
// too, but it cannot be used with golauncher's MCP server (nor with a CLI
// older than --safe-mode).
const noAutoMemory = "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1"

// Command implements agentrt.Adapter. Every flag keeps the CLI away from the
// user's own setup and from this computer: no settings or hooks, no MCP
// servers but golauncher's (l.MCPConfig, when set), no slash commands, no
// local tools, no permission prompts (toolFlags pre-approves the tools the
// model may use). A CLI that lacks some of these flags (l.Flags, l.Version)
// is run as decide says: with an older equivalent, without the feature that
// needs the flag, or not at all (compat.go, docs/ai.md §22).
func (adapter) Command(l agentrt.Launch) (agentrt.Command, error) {
	p := decide(support{version: l.Version, flags: l.Flags})
	if p.refused != nil {
		return agentrt.Command{}, p.refused
	}
	a := []string{l.Path, "-p"}
	if streamInput(l, p) {
		a = append(a, "--input-format", "stream-json")
	}
	a = append(a, "--output-format", "stream-json")
	if p.partial {
		a = append(a, "--include-partial-messages")
	}
	a = append(a, "--verbose")
	if l.Resume {
		a = append(a, "--resume", l.SessionID)
	} else {
		a = append(a, "--session-id", l.SessionID)
	}
	a = append(a, "--setting-sources", "")
	mcpConfig := l.MCPConfig
	if !p.launcher {
		mcpConfig = ""
	}
	safe := mcpConfig == "" && p.safeMode
	if safe {
		// --safe-mode also drops the servers of --mcp-config (verified with
		// 2.1.296), so with golauncher's tools isolation comes from the other
		// flags and from the environment (no auto memory); see docs/ai.md §16.
		a = append(a, "--safe-mode")
	}
	if p.strictMCP {
		a = append(a, "--strict-mcp-config")
	}
	if p.slash {
		a = append(a, "--disable-slash-commands")
	}
	a = append(a, toolFlags(l.WebSearch && p.web, mcpConfig)...)
	switch {
	case p.prompts:
		a = append(a, "--permission-prompts", "none")
	case p.dontAsk:
		a = append(a, "--permission-mode", "dontAsk")
	}
	if sp := l.SystemPrompt; sp != "" && p.appendSP {
		if proc.IsBatch(l.Path) {
			// cmd.exe ends the command line at a line break.
			sp = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(sp)
		}
		a = append(a, "--append-system-prompt", sp)
	}
	if l.Model != "" && p.model {
		a = append(a, "--model", l.Model)
	}
	cmd := agentrt.Command{Argv: a}
	if l.Tools != nil || !safe {
		cmd.Env = []string{noAutoMemory}
	}
	return cmd, nil
}

// streamInput reports whether the process reads stream-json input
// (--input-format stream-json): a persistent process, and a per-turn process
// whose message has attachments (R91: content blocks need it; without them
// the per-turn prompt stays plain text), when the CLI has the flag.
func streamInput(l agentrt.Launch, p plan) bool {
	return p.persistent && (l.Persistent || len(l.Attachments) > 0)
}

// MCPConfig implements agentrt.Adapter: Claude Code reads the tool server
// from an --mcp-config file.
func (adapter) MCPConfig(s agentrt.MCPServer) ([]byte, error) { return toolConfig(s) }

// NewCodec implements agentrt.Adapter. A CLI without --append-system-prompt
// gets the system prompt with the first message of a new session instead.
func (adapter) NewCodec(l agentrt.Launch) agentrt.Codec {
	p := decide(support{version: l.Version, flags: l.Flags})
	c := &codec{streamPrompt: !l.Persistent && streamInput(l, p)}
	if l.SystemPrompt != "" && !l.Resume && !p.appendSP {
		c.fold = l.SystemPrompt
	}
	return c
}

// ExitOutcome implements agentrt.Adapter. A flag the CLI does not know
// ("error: unknown option '--x'", which the probe should have prevented) is
// reported as a version too old for golauncher.
func (adapter) ExitOutcome(text string) agentrt.Outcome {
	if sessionInUse(text) {
		return agentrt.Outcome{Kind: agentrt.SessionInUse, Err: &backend.Error{Kind: classify("", text, false), Message: text}}
	}
	if unknownOption(text) != "" {
		return agentrt.Outcome{Kind: agentrt.Failed, Err: &backend.Error{Kind: backend.ErrVersion, Message: text}}
	}
	return agentrt.Outcome{Kind: agentrt.Failed, Err: &backend.Error{Kind: classify("", text, false), Message: text}}
}

// interruptSeq numbers interrupt requests.
var interruptSeq atomic.Int64

// codec is Claude Code's stream-json protocol for one process. Its only
// state is the system prompt still to be sent with the first message (a CLI
// without --append-system-prompt).
type codec struct {
	mu   sync.Mutex
	fold string
	// streamPrompt: a per-turn process that reads its message as stream-json
	// (it has attachments and the CLI has --input-format, streamInput).
	streamPrompt bool
}

var _ agentrt.AttachmentCodec = (*codec)(nil)

// first returns text with the system prompt still to be sent, if any.
func (c *codec) first(text string) string {
	c.mu.Lock()
	sp := c.fold
	c.fold = ""
	c.mu.Unlock()
	if sp == "" {
		return text
	}
	return foldSystemPrompt(sp, text)
}

// foldSystemPrompt is a first message that carries the system prompt.
func foldSystemPrompt(sp, text string) string {
	return "<golauncher-instructions>\n" + sp + "\n</golauncher-instructions>\n\n" + text
}

// Message implements agentrt.Codec.
func (c *codec) Message(text string) ([]byte, error) { return jsonLine(userLine(c.first(text))) }

// Prompt implements agentrt.Codec: a per-turn process reads the question
// from stdin.
func (c *codec) Prompt(text string) []byte { return []byte(promptText(c.first(text))) }

// MessageWith implements agentrt.AttachmentCodec: a user line whose content
// is a list of blocks, in the order of atts (images as base64 image blocks,
// text files as backend.FileBlock text blocks), then the message's text.
func (c *codec) MessageWith(text string, atts []backend.Attachment) ([]byte, error) {
	blocks := make([]inBlock, 0, len(atts)+1)
	for _, a := range atts {
		data, err := readAttachment(a)
		if err != nil {
			return nil, err
		}
		switch a.Kind {
		case backend.AttachImage:
			blocks = append(blocks, inBlock{Type: "image", Source: &imageSource{Type: "base64", MediaType: a.MediaType,
				Data: base64.StdEncoding.EncodeToString(data)}})
		case backend.AttachText:
			blocks = append(blocks, inBlock{Type: "text", Text: backend.FileBlock(a.Name, string(data))})
		default:
			return nil, backend.AttachmentError("Claude Code", "%s attachments cannot be sent", a.Kind)
		}
	}
	if t := promptText(c.first(text)); t != "" {
		blocks = append(blocks, inBlock{Type: "text", Text: t})
	}
	return jsonLine(userBlocksLine(blocks))
}

// PromptWith implements agentrt.AttachmentCodec. With --input-format
// stream-json (streamPrompt) the per-turn process reads the line of
// MessageWith; an older CLI without it reads plain text, which carries the
// text files inline but cannot carry images.
func (c *codec) PromptWith(text string, atts []backend.Attachment) ([]byte, error) {
	if c.streamPrompt {
		return c.MessageWith(text, atts)
	}
	if len(backend.Images(atts)) > 0 {
		return nil, backend.AttachmentError("Claude Code", "this version cannot take images (it lacks %s)", flagInputFormat)
	}
	inlined, err := backend.InlineText(text, atts)
	if err != nil {
		return nil, err
	}
	return []byte(promptText(c.first(inlined))), nil
}

// readAttachment reads the file of a; an error does not name the file
// (docs/ai.md §9: errors are logged).
func readAttachment(a backend.Attachment) ([]byte, error) {
	data, err := os.ReadFile(a.Path)
	if err != nil {
		if pe, ok := errors.AsType[*fs.PathError](err); ok {
			err = pe.Err
		}
		return nil, backend.AttachmentError("Claude Code", "reading an attachment: %v", err)
	}
	return data, nil
}

// Interrupt implements agentrt.Codec: the control request that stops the
// answer.
func (*codec) Interrupt() []byte {
	b, _ := jsonLine(interruptLine("golauncher-interrupt-" + strconv.FormatInt(interruptSeq.Add(1), 10)))
	return b
}

// NewDecoder implements agentrt.Codec.
func (*codec) NewDecoder(io agentrt.TurnIO) agentrt.Decoder {
	p := newParser(io.Emit)
	p.reply = io.Reply
	return &decoder{p: p}
}

// jsonLine is v as one line of input.
func jsonLine(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
