package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
)

// The MCP server (docs/ai.md §4.3): JSON-RPC 2.0, one message per line, over
// stdio (golauncher mcp) or the launcher's pipe. It answers initialize (with
// protocol version negotiation), ping, tools/list and tools/call, and
// ignores notifications.

// ProtocolVersions are the MCP versions understood, newest first.
var ProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
	codeNotInitialized = -32002
)

// maxLine is the longest message accepted.
const maxLine = 4 << 20

// mcpHandler is what an MCP connection serves.
type mcpHandler interface {
	// initialize records the client's clientInfo.
	initialize(name, version string)
	instructions() string
	tools() []Tool
	call(ctx context.Context, name string, args jsontext.Value) (Result, error)
}

// ClientInfo is MCP's clientInfo / serverInfo.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type rpcMessage struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      jsontext.Value `json:"id,omitzero"`
	Method  string         `json:"method,omitempty"`
	Params  jsontext.Value `json:"params,omitzero"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      jsontext.Value `json:"id"`
	Result  any            `json:"result,omitempty"`
	Error   *rpcError      `json:"error,omitempty"`
}

// mcpConn is one MCP connection.
type mcpConn struct {
	h       mcpHandler
	w       io.Writer
	log     *slog.Logger
	version string // the server's version
	serial  bool

	wmu sync.Mutex

	mu          sync.Mutex
	initialized bool
	inflight    map[string]context.CancelFunc
	wg          sync.WaitGroup
}

// mcpOptions configure serveMCP.
type mcpOptions struct {
	Version string // the server's version
	Log     *slog.Logger
	// Serial handles one request at a time, in order (glctl: reproducible
	// output).
	Serial bool
}

// serveMCP serves h on r/w until r ends (the requests in flight finish) or
// ctx is done (they are cancelled). Requests run concurrently: a tool call
// may wait for the user's confirmation.
func serveMCP(ctx context.Context, r io.Reader, w io.Writer, h mcpHandler, o mcpOptions) error {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c := &mcpConn{h: h, w: w, log: log, version: o.Version, serial: o.Serial, inflight: map[string]context.CancelFunc{}}
	defer c.wg.Wait()
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := readLine(br)
		if len(bytes.TrimSpace(line)) > 0 {
			c.dispatch(ctx, line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

// readLine reads one line without its line ending.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > maxLine {
			return nil, fmt.Errorf("mcp: message longer than %d bytes", maxLine)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(buf, "\r\n"), err
	}
}

func (c *mcpConn) write(v any) {
	b, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		c.log.Error("mcp: encoding a response", "err", err)
		return
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		c.log.Debug("mcp: writing", "err", err)
	}
}

func (c *mcpConn) reply(id jsontext.Value, result any) {
	c.write(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (c *mcpConn) fail(id jsontext.Value, code int, msg string) {
	if len(id) == 0 {
		id = jsontext.Value("null")
	}
	c.write(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (c *mcpConn) dispatch(ctx context.Context, line []byte) {
	var m rpcMessage
	if err := json.Unmarshal(line, &m); err != nil {
		c.fail(nil, codeParse, "parse error: "+err.Error())
		return
	}
	if m.JSONRPC != "2.0" || m.Method == "" {
		if m.Method == "" && len(m.ID) > 0 {
			return // a response to a request we never send: ignored
		}
		c.fail(m.ID, codeInvalidRequest, "invalid request")
		return
	}
	if len(m.ID) == 0 {
		c.notification(m)
		return
	}
	key := string(m.ID)
	rctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.inflight[key] = cancel
	c.mu.Unlock()
	run := func() {
		defer func() {
			cancel()
			c.mu.Lock()
			delete(c.inflight, key)
			c.mu.Unlock()
			if r := recover(); r != nil {
				c.log.Error("mcp: request panicked", "method", m.Method, "panic", r, "stack", string(debug.Stack()))
				c.fail(m.ID, codeInternal, fmt.Sprintf("internal error: %v", r))
			}
		}()
		c.request(rctx, m)
	}
	if c.serial {
		run()
		return
	}
	c.wg.Go(run)
}

// notification handles a notification: notifications/cancelled stops a
// request; the others (notifications/initialized, …) need nothing.
func (c *mcpConn) notification(m rpcMessage) {
	if m.Method != "notifications/cancelled" {
		return
	}
	var p struct {
		RequestID jsontext.Value `json:"requestId"`
	}
	if json.Unmarshal(m.Params, &p) == nil {
		c.mu.Lock()
		cancel := c.inflight[string(p.RequestID)]
		c.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
}

func (c *mcpConn) request(ctx context.Context, m rpcMessage) {
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string     `json:"protocolVersion"`
			ClientInfo      ClientInfo `json:"clientInfo"`
		}
		if err := json.Unmarshal(cmpParams(m.Params), &p); err != nil {
			c.fail(m.ID, codeInvalidParams, "invalid params: "+err.Error())
			return
		}
		version := ProtocolVersions[0]
		if slices.Contains(ProtocolVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		c.h.initialize(p.ClientInfo.Name, p.ClientInfo.Version)
		c.mu.Lock()
		c.initialized = true
		c.mu.Unlock()
		c.reply(m.ID, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      ClientInfo{Name: ServerName, Version: c.version},
			"instructions":    c.h.instructions(),
		})
	case "ping":
		c.reply(m.ID, struct{}{})
	case "tools/list":
		if !c.ready(m) {
			return
		}
		c.reply(m.ID, map[string]any{"tools": c.h.tools()})
	case "tools/call":
		if !c.ready(m) {
			return
		}
		var p struct {
			Name      string         `json:"name"`
			Arguments jsontext.Value `json:"arguments,omitzero"`
		}
		if err := json.Unmarshal(cmpParams(m.Params), &p); err != nil || p.Name == "" {
			c.fail(m.ID, codeInvalidParams, "invalid params: want a tool name and arguments")
			return
		}
		if len(p.Arguments) > 0 && p.Arguments.Kind() != '{' && string(p.Arguments) != "null" {
			c.fail(m.ID, codeInvalidParams, "invalid params: arguments must be an object")
			return
		}
		res, err := c.h.call(ctx, p.Name, p.Arguments)
		if errors.Is(err, ErrUnknownTool) {
			c.fail(m.ID, codeInvalidParams, "unknown tool: "+p.Name)
			return
		}
		if err != nil {
			c.fail(m.ID, codeInternal, err.Error())
			return
		}
		out := map[string]any{"content": []map[string]string{{"type": "text", "text": res.Text}}, "isError": res.IsError}
		c.reply(m.ID, out)
	default:
		c.fail(m.ID, codeMethodNotFound, "method not found: "+m.Method)
	}
}

// ready reports whether the client initialized, answering an error if not.
func (c *mcpConn) ready(m rpcMessage) bool {
	c.mu.Lock()
	ok := c.initialized
	c.mu.Unlock()
	if !ok {
		c.fail(m.ID, codeNotInitialized, "not initialized: send initialize first")
	}
	return ok
}

func cmpParams(p jsontext.Value) jsontext.Value {
	if len(p) == 0 || string(p) == "null" {
		return jsontext.Value("{}")
	}
	return p
}

// sessionHandler serves a Session over MCP.
type sessionHandler struct{ s *Session }

func (h sessionHandler) initialize(name, version string) { h.s.setClient(name, version) }
func (h sessionHandler) instructions() string            { return Instructions }
func (h sessionHandler) tools() []Tool                   { return Tools() }
func (h sessionHandler) call(ctx context.Context, name string, args jsontext.Value) (Result, error) {
	return h.s.Call(ctx, name, args)
}

// ServeMCP serves the tools for client c over r/w (one MCP connection) until
// r ends or ctx is done. serial handles one request at a time (glctl).
func (a *Agent) ServeMCP(ctx context.Context, r io.Reader, w io.Writer, c Client, serial bool) error {
	s := a.Session(c)
	defer s.Release()
	return serveMCP(ctx, r, w, sessionHandler{s}, mcpOptions{Version: a.o.Version, Log: a.log, Serial: serial})
}
