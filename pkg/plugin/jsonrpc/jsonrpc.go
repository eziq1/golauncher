// Package jsonrpc is a small JSON-RPC 2.0 connection over a byte stream with
// NDJSON framing: every message is one JSON object on one line. It is used
// on both ends of the external plugin protocol (docs/plugin-protocol.md):
// by the host (internal/extproc) and by Go plugins (pkg/plugin/rpcserve).
//
// Both sides may send requests and notifications. Incoming requests are
// handled on their own goroutines, so a handler may itself call the other
// side; incoming notifications are handled in order on the reading
// goroutine and must not block. A panicking handler produces an error
// response instead of crashing the process.
package jsonrpc

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
	"strconv"
	"sync"
)

// Standard and protocol error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	// CodePermissionDenied: the plugin called a host API its manifest does
	// not declare a permission for.
	CodePermissionDenied = -32001
	// CodeUnavailable: the plugin cannot run on this system (its Init
	// returned plugin.ErrUnavailable); the message is the reason.
	CodeUnavailable = -32002
	// CodeRequestCancelled answers a request cancelled with $/cancelRequest.
	CodeRequestCancelled = -32800
)

// MethodCancel is the notification that cancels an outstanding request.
const MethodCancel = "$/cancelRequest"

// Error is a JSON-RPC error object. Handlers may return one to choose the
// code; other errors become CodeInternalError.
type Error struct {
	Code    int64          `json:"code"`
	Message string         `json:"message"`
	Data    jsontext.Value `json:"data,omitzero"`
}

// Error returns the message, which plugins and the host write for people;
// use errors.AsType to inspect the code.
func (e *Error) Error() string { return e.Message }

// Errorf returns an *Error with code and a formatted message.
func Errorf(code int64, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ErrClosed is returned by Call when the connection closed before the
// response arrived.
var ErrClosed = errors.New("jsonrpc: connection closed")

// Handler serves an incoming request or notification. For notifications
// (notify true) the result is ignored. params may be nil.
type Handler func(ctx context.Context, method string, params jsontext.Value, notify bool) (any, error)

// message is any incoming JSON-RPC message.
type message struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      jsontext.Value `json:"id,omitzero"`
	Method  string         `json:"method,omitempty"`
	Params  jsontext.Value `json:"params,omitzero"`
	Result  jsontext.Value `json:"result,omitzero"`
	Error   *Error         `json:"error,omitempty"`
}

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type response struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      jsontext.Value `json:"id"`
	Result  jsontext.Value `json:"result,omitzero"`
	Error   *Error         `json:"error,omitempty"`
}

// CancelParams are the parameters of $/cancelRequest.
type CancelParams struct {
	ID jsontext.Value `json:"id"`
}

// Conn is a JSON-RPC connection. Create it with NewConn and start reading
// with Run.
type Conn struct {
	r       io.Reader
	w       io.Writer
	handler Handler
	log     *slog.Logger

	wmu sync.Mutex // serializes writes

	mu       sync.Mutex
	nextID   int64
	pending  map[string]chan *message
	incoming map[string]context.CancelFunc
	closed   bool

	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

// NewConn returns a connection reading from r and writing to w. log
// receives warnings about stray output (nil discards them).
func NewConn(r io.Reader, w io.Writer, h Handler, log *slog.Logger) *Conn {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{
		r: r, w: w, handler: h, log: log,
		pending:  map[string]chan *message{},
		incoming: map[string]context.CancelFunc{},
		done:     make(chan struct{}),
		ctx:      ctx, cancel: cancel,
	}
}

// MaxLine is the longest accepted message line.
const MaxLine = 64 << 20

// Run reads and dispatches messages until the input ends or fails. It then
// fails all outstanding calls with ErrClosed and cancels the contexts of
// incoming requests. It returns nil at end of input.
func (c *Conn) Run() error {
	defer c.shutdown()
	br := bufio.NewReaderSize(c.r, 64<<10)
	for {
		line, err := readLine(br)
		if len(line) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// readLine reads one line without its line ending.
func readLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > MaxLine {
			return nil, fmt.Errorf("jsonrpc: line longer than %d bytes", MaxLine)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return bytes.TrimRight(buf, "\r\n"), err
	}
}

// Done is closed when Run has returned.
func (c *Conn) Done() <-chan struct{} { return c.done }

func (c *Conn) shutdown() {
	c.mu.Lock()
	c.closed = true
	pending := c.pending
	c.pending = map[string]chan *message{}
	c.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
	c.cancel()
	close(c.done)
}

func (c *Conn) dispatch(line []byte) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return
	}
	var m message
	if trimmed[0] != '{' || json.Unmarshal(trimmed, &m) != nil {
		c.log.Warn("non-protocol output ignored", "line", truncate(string(line), 500))
		return
	}
	switch {
	case m.Method != "" && m.ID == nil:
		c.handleNotification(&m)
	case m.Method != "":
		key := idKey(m.ID)
		ctx, cancel := context.WithCancel(c.ctx)
		c.mu.Lock()
		c.incoming[key] = cancel
		c.mu.Unlock()
		go c.handleRequest(ctx, cancel, &m)
	default:
		key := idKey(m.ID)
		c.mu.Lock()
		ch, ok := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ok {
			ch <- &m
		}
	}
}

func idKey(id jsontext.Value) string {
	var n int64
	if json.Unmarshal(id, &n) == nil {
		return strconv.FormatInt(n, 10)
	}
	var s string
	if json.Unmarshal(id, &s) == nil {
		return s
	}
	return string(id)
}

func (c *Conn) handleNotification(m *message) {
	if m.Method == MethodCancel {
		var p CancelParams
		if json.Unmarshal(m.Params, &p) == nil {
			c.mu.Lock()
			cancel := c.incoming[idKey(p.ID)]
			c.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
		return
	}
	_, _ = c.call(c.ctx, m, true)
}

func (c *Conn) handleRequest(ctx context.Context, cancel context.CancelFunc, m *message) {
	defer func() {
		cancel()
		c.mu.Lock()
		delete(c.incoming, idKey(m.ID))
		c.mu.Unlock()
	}()
	res, err := c.call(ctx, m, false)
	resp := response{JSONRPC: "2.0", ID: m.ID}
	if err == nil {
		resp.Result, err = json.Marshal(res)
	}
	if err != nil {
		resp.Result = nil
		if e, ok := errors.AsType[*Error](err); ok {
			resp.Error = e
		} else if ctx.Err() != nil && c.ctx.Err() == nil {
			resp.Error = &Error{Code: CodeRequestCancelled, Message: err.Error()}
		} else {
			resp.Error = &Error{Code: CodeInternalError, Message: err.Error()}
		}
	}
	if err := c.write(resp); err != nil {
		c.log.Debug("jsonrpc: writing response", "method", m.Method, "err", err)
	}
}

// call runs the handler with panics turned into errors.
func (c *Conn) call(ctx context.Context, m *message, notify bool) (res any, err error) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("jsonrpc: handler panicked", "method", m.Method, "panic", r, "stack", string(debug.Stack()))
			err = &Error{Code: CodeInternalError, Message: fmt.Sprintf("panic in %s: %v", m.Method, r)}
		}
	}()
	if c.handler == nil {
		return nil, Errorf(CodeMethodNotFound, "method not found: %s", m.Method)
	}
	return c.handler(ctx, m.Method, m.Params, notify)
}

func (c *Conn) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(b)
	return err
}

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	return c.write(notification{JSONRPC: "2.0", Method: method, Params: params})
}

// Call sends a request and decodes the result into result (which may be
// nil). When ctx ends first, it sends $/cancelRequest and returns ctx's
// error. A JSON-RPC error response is returned as *Error.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	ch := make(chan *message, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.nextID++
	id := c.nextID
	key := strconv.FormatInt(id, 10)
	c.pending[key] = ch
	c.mu.Unlock()
	if err := c.write(request{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return fmt.Errorf("jsonrpc: sending %s: %w", method, err)
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if result != nil && len(m.Result) > 0 {
			if err := json.Unmarshal(m.Result, result); err != nil {
				return fmt.Errorf("jsonrpc: decoding result of %s: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		_, stillPending := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if stillPending {
			_ = c.Notify(MethodCancel, CancelParams{ID: jsontext.Value(key)})
		}
		return ctx.Err()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
