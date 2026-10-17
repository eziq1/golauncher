package acp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/eziq1/golauncher/internal/ai/agentrt"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// Draining the history a loaded session replays: the output must be quiet
// for drainQuiet, and draining stops after drainMax in any case.
var (
	drainQuiet = 300 * time.Millisecond
	drainMax   = 5 * time.Second
)

// message is a JSON-RPC 2.0 message: a request (Method and ID), a
// notification (Method, no ID) or a response (ID, Result or Error).
type message struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      jsontext.Value `json:"id,omitzero"`
	Method  string         `json:"method,omitempty"`
	Params  jsontext.Value `json:"params,omitzero"`
	Result  jsontext.Value `json:"result,omitzero"`
	Error   *rpcError      `json:"error,omitempty"`
}

// hasID reports whether m carries an ID (a request or a response).
func (m *message) hasID() bool { return len(m.ID) > 0 && string(m.ID) != "null" }

// rpcError is a JSON-RPC error.
type rpcError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    jsontext.Value `json:"data,omitzero"`
}

func itoa(n int) string { return strconv.Itoa(n) }

// Method not found: the answer to every request of the agent but
// session/request_permission.
const methodNotFound = -32601

// line is v as one line of input.
func line(v any) []byte {
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		panic(err) // only golauncher's own values are marshalled
	}
	return append(b, '\n')
}

func request(id int64, method string, params any) []byte {
	return line(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", id, method, params})
}

func notification(method string, params any) []byte {
	return line(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", method, params})
}

func response(id jsontext.Value, result any) []byte {
	return line(struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      jsontext.Value `json:"id"`
		Result  any            `json:"result"`
	}{"2.0", id, result})
}

func errorResponse(id jsontext.Value, code int, msg string) []byte {
	return line(struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      jsontext.Value `json:"id"`
		Error   rpcError       `json:"error"`
	}{"2.0", id, rpcError{Code: code, Message: msg}})
}

// parse parses a line of the agent's output; ok is false when it is not a
// JSON-RPC 2.0 message.
func parse(raw []byte) (m message, ok bool) {
	if json.Unmarshal(raw, &m) != nil || m.JSONRPC != "2.0" {
		return message{}, false
	}
	return m, true
}

// The parameters golauncher sends.
type (
	initializeParams struct {
		ProtocolVersion    int                `json:"protocolVersion"`
		ClientCapabilities clientCapabilities `json:"clientCapabilities"`
		ClientInfo         implementation     `json:"clientInfo"`
	}
	clientCapabilities struct {
		FS       fsCapabilities `json:"fs"`
		Terminal bool           `json:"terminal"`
	}
	fsCapabilities struct {
		ReadTextFile  bool `json:"readTextFile"`
		WriteTextFile bool `json:"writeTextFile"`
	}
	implementation struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	sessionParams struct {
		SessionID  string      `json:"sessionId,omitempty"`
		CWD        string      `json:"cwd"`
		MCPServers []mcpServer `json:"mcpServers"`
	}
	mcpServer struct {
		Name    string   `json:"name"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Env     []envVar `json:"env"`
	}
	envVar struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	promptParams struct {
		SessionID string        `json:"sessionId"`
		Prompt    []promptBlock `json:"prompt"`
	}
	// promptBlock is a block of a prompt: text ({"type":"text","text":…})
	// or an image ({"type":"image","mimeType":…,"data":<base64>}, R91).
	promptBlock struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		MimeType string `json:"mimeType,omitempty"`
		Data     string `json:"data,omitempty"`
	}
	sessionIDParams struct {
		SessionID string `json:"sessionId"`
	}
)

// textBlock is a text block of a prompt.
func textBlock(text string) promptBlock { return promptBlock{Type: "text", Text: text} }

// MarshalJSON marshals a text block as {"type":"text","text":…} (also with
// an empty text) and an image block without a text.
func (b promptBlock) MarshalJSON() ([]byte, error) {
	if b.Type == "image" {
		return json.Marshal(struct {
			Type     string `json:"type"`
			MimeType string `json:"mimeType"`
			Data     string `json:"data"`
		}{b.Type, b.MimeType, b.Data})
	}
	return json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{b.Type, b.Text})
}

// The results golauncher reads.
type (
	initializeResult struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession        bool               `json:"loadSession"`
			PromptCapabilities promptCapabilities `json:"promptCapabilities"`
		} `json:"agentCapabilities"`
	}
	// promptCapabilities are the content blocks the agent takes in a prompt
	// besides text (ACP: image, audio, embedded resources).
	promptCapabilities struct {
		Image           bool `json:"image"`
		Audio           bool `json:"audio"`
		EmbeddedContext bool `json:"embeddedContext"`
	}
	sessionResult struct {
		SessionID string `json:"sessionId"`
		Models    *struct {
			CurrentModelID string `json:"currentModelId"`
		} `json:"models"`
	}
)

// mcpServers is the session's mcpServers: golauncher's tool server, if any.
func mcpServers(s *agentrt.MCPServer) []mcpServer {
	if s == nil {
		return []mcpServer{}
	}
	env := make([]envVar, 0, len(s.Env))
	for _, k := range slices.Sorted(maps.Keys(s.Env)) {
		env = append(env, envVar{k, s.Env[k]})
	}
	args := s.Args
	if args == nil {
		args = []string{}
	}
	return []mcpServer{{Name: s.Name, Command: s.Command, Args: args, Env: env}}
}

// codec is the protocol state of one agent process.
type codec struct {
	cfg *Config
	l   agentrt.Launch

	nextID    int64
	sessionID string
	model     string // the agent's current model, for Usage
	lost      bool   // a resume was asked for, a new session started
	system    string // the system prompt, sent with the first prompt
	promptID  int64  // the running turn's session/prompt request
	// caps are the agent's promptCapabilities (initialize).
	caps promptCapabilities
}

var (
	_ agentrt.AttachmentCodec = (*codec)(nil)
	_ agentrt.Codec           = (*codec)(nil)
	_ agentrt.Handshaker      = (*codec)(nil)
	_ agentrt.ResumeLoser     = (*codec)(nil)
)

// ResumeLost implements agentrt.ResumeLoser.
func (c *codec) ResumeLost() bool { return c.lost }

// Handshake implements agentrt.Handshaker: initialize, then session/load or
// session/new. It returns the session ID.
func (c *codec) Handshake(ctx context.Context, conn agentrt.Conn) (string, error) {
	res, rerr, err := c.call(ctx, conn, "initialize", initializeParams{
		ProtocolVersion:    ProtocolVersion,
		ClientCapabilities: clientCapabilities{},
		ClientInfo:         implementation{Name: c.cfg.ClientName, Version: c.cfg.ClientVersion},
	})
	if err != nil {
		return "", err
	}
	if rerr != nil {
		return "", rpcErr(rerr)
	}
	var init initializeResult
	if err := json.Unmarshal(res, &init); err != nil {
		return "", fmt.Errorf("%s: invalid initialize result: %w", c.cfg.Info.Name, err)
	}
	if init.ProtocolVersion != ProtocolVersion {
		return "", &backend.Error{Kind: backend.ErrFailed, Message: fmt.Sprintf(
			"%s speaks version %d of the Agent Client Protocol; golauncher speaks version %d", c.cfg.Info.Name, init.ProtocolVersion, ProtocolVersion)}
	}
	c.caps = init.AgentCapabilities.PromptCapabilities
	servers := mcpServers(c.l.Tools)
	if c.l.Resume && c.l.SessionID != "" {
		if !init.AgentCapabilities.LoadSession {
			c.lost = true
		} else {
			res, rerr, err := c.call(ctx, conn, "session/load", sessionParams{SessionID: c.l.SessionID, CWD: c.l.Dir, MCPServers: servers})
			if err != nil {
				return "", err
			}
			if rerr == nil {
				var sr sessionResult
				_ = json.Unmarshal(res, &sr)
				if sr.Models != nil {
					c.model = sr.Models.CurrentModelID
				}
				c.sessionID = c.l.SessionID
				if err := c.drain(ctx, conn); err != nil {
					return "", err
				}
				return c.sessionID, nil
			}
			if k := classifyRPC(rerr); k == backend.ErrNotLoggedIn || k == backend.ErrQuota {
				return "", rpcErr(rerr)
			}
			c.lost = true // e.g. the agent deleted the session: start a new one
		}
	}
	res, rerr, err = c.call(ctx, conn, "session/new", sessionParams{CWD: c.l.Dir, MCPServers: servers})
	if err != nil {
		return "", err
	}
	if rerr != nil {
		return "", rpcErr(rerr)
	}
	var sr sessionResult
	if err := json.Unmarshal(res, &sr); err != nil || sr.SessionID == "" {
		return "", fmt.Errorf("%s: session/new returned no session ID", c.cfg.Info.Name)
	}
	if sr.Models != nil {
		c.model = sr.Models.CurrentModelID
	}
	c.sessionID = sr.SessionID
	c.system = c.l.SystemPrompt
	return c.sessionID, nil
}

// call sends a request and waits for its response. Notifications that
// arrive meanwhile are dropped (session/load's history replay), requests of
// the agent are answered with an error.
func (c *codec) call(ctx context.Context, conn agentrt.Conn, method string, params any) (jsontext.Value, *rpcError, error) {
	c.nextID++
	id := c.nextID
	if err := conn.Write(request(id, method, params)); err != nil {
		return nil, nil, err
	}
	want := strconv.FormatInt(id, 10)
	for {
		raw, err := conn.Read(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %s: %w", c.cfg.Info.Name, method, err)
		}
		m, ok := parse(raw)
		if !ok {
			continue
		}
		switch {
		case m.Method != "":
			if err := c.refuse(conn, m); err != nil {
				return nil, nil, err
			}
		case string(bytes.TrimSpace(m.ID)) == want:
			if m.Error != nil {
				return nil, m.Error, nil
			}
			return m.Result, nil, nil
		}
	}
}

// refuse answers a request of the agent outside a turn (or one golauncher
// does not support) with an error; notifications are ignored.
func (c *codec) refuse(conn agentrt.Conn, m message) error {
	if !m.hasID() {
		return nil
	}
	return conn.Write(errorResponse(m.ID, methodNotFound, "Method not found: "+m.Method))
}

// drain reads and drops what the agent writes after session/load (its
// history replay, which Gemini CLI partly sends after the response) until
// the output is quiet.
func (c *codec) drain(ctx context.Context, conn agentrt.Conn) error {
	deadline := time.Now().Add(drainMax)
	for {
		wait := min(drainQuiet, time.Until(deadline))
		if wait <= 0 {
			return nil
		}
		rctx, cancel := context.WithTimeout(ctx, wait)
		raw, err := conn.Read(rctx)
		cancel()
		if err != nil {
			if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				return nil // quiet
			}
			return fmt.Errorf("%s: session/load: %w", c.cfg.Info.Name, err)
		}
		if m, ok := parse(raw); ok && m.Method != "" {
			if err := c.refuse(conn, m); err != nil {
				return err
			}
		}
	}
}

// Message implements agentrt.Codec: a session/prompt request.
func (c *codec) Message(text string) ([]byte, error) {
	if c.sessionID == "" {
		return nil, errors.New("acp: no session")
	}
	var blocks []promptBlock
	if c.system != "" {
		blocks = append(blocks, textBlock(c.system))
		c.system = ""
	}
	blocks = append(blocks, textBlock(text))
	if !c.cfg.Commands && looksLikeCommand(blocks[0].Text) {
		blocks[0].Text = "\u200b" + blocks[0].Text
	}
	return c.prompt(blocks), nil
}

// prompt is the session/prompt request of a turn with blocks.
func (c *codec) prompt(blocks []promptBlock) []byte {
	c.nextID++
	c.promptID = c.nextID
	return request(c.promptID, "session/prompt", promptParams{SessionID: c.sessionID, Prompt: blocks})
}

// MessageWith implements agentrt.AttachmentCodec: session/prompt with the
// system prompt still to be sent, then a block per attachment in order (an
// image block, which needs the agent's image prompt capability; a text file
// as a backend.FileBlock text block), then the message's text. Unless
// Config.Commands is set, a text block that looks like a command gets the
// leading zero width space (as in Message).
func (c *codec) MessageWith(text string, atts []backend.Attachment) ([]byte, error) {
	if c.sessionID == "" {
		return nil, errors.New("acp: no session")
	}
	var blocks []promptBlock
	if c.system != "" {
		blocks = append(blocks, textBlock(c.system))
	}
	for _, a := range atts {
		switch a.Kind {
		case backend.AttachImage:
			if !c.caps.Image {
				return nil, backend.AttachmentError(c.cfg.Info.Name, "does not accept images")
			}
			data, err := c.readAttachment(a)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, promptBlock{Type: "image", MimeType: a.MediaType, Data: base64.StdEncoding.EncodeToString(data)})
		case backend.AttachText:
			data, err := c.readAttachment(a)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, textBlock(backend.FileBlock(a.Name, string(data))))
		default:
			return nil, backend.AttachmentError(c.cfg.Info.Name, "%s attachments cannot be sent", a.Kind)
		}
	}
	blocks = append(blocks, textBlock(text))
	if !c.cfg.Commands {
		for i := range blocks {
			if blocks[i].Type == "text" && looksLikeCommand(blocks[i].Text) {
				blocks[i].Text = "\u200b" + blocks[i].Text
			}
		}
	}
	c.system = ""
	return c.prompt(blocks), nil
}

// PromptWith implements agentrt.AttachmentCodec (there is no per-turn mode).
func (c *codec) PromptWith(string, []backend.Attachment) ([]byte, error) {
	return nil, fmt.Errorf("%s: no per-turn mode", c.cfg.Info.Name)
}

// readAttachment reads the file of a; an error does not name the file
// (errors are logged, docs/ai.md \u00a79).
func (c *codec) readAttachment(a backend.Attachment) ([]byte, error) {
	data, err := os.ReadFile(a.Path)
	if err != nil {
		if pe, ok := errors.AsType[*fs.PathError](err); ok {
			err = pe.Err
		}
		return nil, backend.AttachmentError(c.cfg.Info.Name, "reading an attachment: %v", err)
	}
	return data, nil
}

// looksLikeCommand reports whether an agent may take text for a slash
// command (Gemini CLI: the trimmed text starts with "/" or "$").
func looksLikeCommand(text string) bool {
	t := strings.TrimLeftFunc(text, func(r rune) bool { return unicode.IsSpace(r) || r == '\ufeff' })
	return strings.HasPrefix(t, "/") || strings.HasPrefix(t, "$")
}

// Prompt implements agentrt.Codec (there is no per-turn mode).
func (*codec) Prompt(string) []byte { return nil }

// Interrupt implements agentrt.Codec: the session/cancel notification.
func (c *codec) Interrupt() []byte {
	return notification("session/cancel", sessionIDParams{SessionID: c.sessionID})
}

// NewDecoder implements agentrt.Codec.
func (c *codec) NewDecoder(io agentrt.TurnIO) agentrt.Decoder {
	return &decoder{c: c, io: io, promptID: strconv.FormatInt(c.promptID, 10), tools: map[string]*toolState{}}
}
