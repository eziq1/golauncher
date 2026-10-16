package agent_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/agent"
	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/winapi/pipe"
)

// mcpClient is an MCP client in the test: it writes lines and reads the
// answers.
type mcpClient struct {
	t  *testing.T
	w  io.Writer
	r  *bufio.Reader
	id int
}

func newClient(t *testing.T, w io.Writer, r io.Reader) *mcpClient {
	return &mcpClient{t: t, w: w, r: bufio.NewReader(r)}
}

func (c *mcpClient) send(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.w, line+"\n"); err != nil {
		c.t.Fatal(err)
	}
}

// response reads the next message.
type response struct {
	ID     any            `json:"id"`
	Result map[string]any `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *mcpClient) read() response {
	c.t.Helper()
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	var r response
	if err := json.Unmarshal(line, &r); err != nil {
		c.t.Fatalf("not JSON: %s", line)
	}
	return r
}

// request sends a request and returns its response.
func (c *mcpClient) request(method string, params any) response {
	c.t.Helper()
	c.id++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	c.send(string(b))
	return c.read()
}

func (c *mcpClient) initialize(version string) response {
	return c.request("initialize", map[string]any{"protocolVersion": version, "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "claude-code", "version": "2.1.0"}})
}

// toolText is the text of a tools/call result, and whether it is an error.
func toolText(t *testing.T, r response) (string, bool) {
	t.Helper()
	if r.Error != nil {
		t.Fatalf("error %+v", r.Error)
	}
	content := r.Result["content"].([]any)
	return content[0].(map[string]any)["text"].(string), r.Result["isError"] == true
}

// serve runs the agent's MCP server for client c on pipes.
func serve(t *testing.T, w *world, c agent.Client) *mcpClient {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- w.ag.ServeMCP(context.Background(), inR, outW, c, false); outW.Close() }()
	t.Cleanup(func() {
		inW.Close()
		if err := <-done; err != nil {
			t.Errorf("ServeMCP: %v", err)
		}
	})
	return newClient(t, inW, outR)
}

func TestMCPServer(t *testing.T) {
	w := newWorld(t, nil)
	c := serve(t, w, agent.Client{Chat: true, ChatSession: "m1"})

	// Before initialize.
	if r := c.request("tools/list", nil); r.Error == nil || r.Error.Code != -32002 {
		t.Errorf("tools/list before initialize: %+v", r)
	}
	if r := c.request("ping", nil); r.Error != nil {
		t.Errorf("ping: %+v", r.Error)
	}
	r := c.initialize("2025-06-18")
	if r.Result["protocolVersion"] != "2025-06-18" || r.Result["serverInfo"].(map[string]any)["name"] != "golauncher" ||
		!strings.Contains(r.Result["instructions"].(string), "run_action") || r.Result["capabilities"].(map[string]any)["tools"] == nil {
		t.Errorf("initialize: %+v", r.Result)
	}
	// An unknown version gets the newest.
	if r := c.initialize("1999-01-01"); r.Result["protocolVersion"] != agent.ProtocolVersions[0] {
		t.Errorf("negotiated %v", r.Result["protocolVersion"])
	}
	c.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`) // no answer

	r = c.request("tools/list", map[string]any{})
	var names []string
	for _, tool := range r.Result["tools"].([]any) {
		m := tool.(map[string]any)
		names = append(names, m["name"].(string))
		if m["inputSchema"].(map[string]any)["type"] != "object" || m["description"] == "" {
			t.Errorf("tool %v", m)
		}
	}
	if strings.Join(names, ",") != "list_plugins,search,run_command,get_detail,run_action" {
		t.Errorf("tools %q", names)
	}

	text, isErr := toolText(t, c.request("tools/call", map[string]any{"name": "search", "arguments": map[string]any{"query": "Visual Studio Code"}}))
	if isErr || !strings.Contains(text, `"handle":"i1"`) || !strings.Contains(text, "Visual Studio Code") {
		t.Errorf("search: %s", text)
	}
	text, isErr = toolText(t, c.request("tools/call", map[string]any{"name": "run_action", "arguments": map[string]any{"handle": "i1", "action": "runas"}}))
	if !isErr || !strings.Contains(text, `"status":"declined"`) {
		t.Errorf("declined: %s", text)
	}
	text, isErr = toolText(t, c.request("tools/call", map[string]any{"name": "get_detail", "arguments": map[string]any{}}))
	if !isErr || !strings.Contains(text, "unknown handle") {
		t.Errorf("no handle: %s", text)
	}

	// Errors.
	for _, tc := range []struct {
		line string
		code int
	}{
		{`{not json`, -32700},
		{`{"jsonrpc":"2.0","id":90,"method":"resources/list"}`, -32601},
		{`{"jsonrpc":"2.0","id":91,"method":"tools/call","params":{"name":"format_disk","arguments":{}}}`, -32602},
		{`{"jsonrpc":"2.0","id":92,"method":"tools/call","params":{"arguments":{}}}`, -32602},
		{`{"jsonrpc":"2.0","id":93,"method":"tools/call","params":{"name":"search","arguments":[1]}}`, -32602},
		{`{"id":94,"method":"ping"}`, -32600},
	} {
		c.send(tc.line)
		if r := c.read(); r.Error == nil || r.Error.Code != tc.code {
			t.Errorf("%s: %+v", tc.line, r)
		}
	}
	if e := w.ag.Activity().Entries(); len(e) != 3 || e[0].Client != "Claude (AI Chat)" {
		t.Errorf("activity %+v", e)
	}
}

func TestMCPExternalClientName(t *testing.T) {
	w := newWorld(t, func(c *config.Config) { c.Agent.External = true })
	c := serve(t, w, agent.Client{})
	c.initialize("2025-03-26")
	toolText(t, c.request("tools/call", map[string]any{"name": "list_plugins", "arguments": map[string]any{}}))
	if e := w.ag.Activity().Entries(); len(e) != 1 || e[0].Client != "Claude Code" {
		t.Errorf("activity %+v", e)
	}
}

// pipeName is a pipe of its own for a test.
func pipeName() string { return `\\.\pipe\golauncher-test-` + rand.Text() }

// bridge runs agent.Bridge on pipes and returns its client.
func bridge(t *testing.T, o agent.BridgeOptions) (*mcpClient, <-chan error) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- agent.Bridge(t.Context(), inR, outW, o); outW.Close() }()
	t.Cleanup(func() { inW.Close() })
	return newClient(t, inW, outR), done
}

// TestPipe serves the agent on a real pipe and talks to it through the
// bridge: the chat's token makes the calls the chat's; without it they are
// an AI app's, refused while external access is off.
func TestPipe(t *testing.T) {
	w := newWorld(t, nil)
	name := pipeName()
	ln, err := pipe.Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- w.ag.Serve(ctx, ln) }()
	defer func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	}()

	chat, _ := bridge(t, agent.BridgeOptions{Pipe: name, Token: "tok", Session: "chat-7"})
	chat.initialize("2025-06-18")
	if text, isErr := toolText(t, chat.request("tools/call", map[string]any{"name": "list_plugins", "arguments": map[string]any{}})); isErr {
		t.Errorf("chat refused: %s", text)
	}
	ext, _ := bridge(t, agent.BridgeOptions{Pipe: name, Token: "wrong"})
	ext.initialize("2025-06-18")
	if text, isErr := toolText(t, ext.request("tools/call", map[string]any{"name": "list_plugins", "arguments": map[string]any{}})); !isErr || !strings.Contains(text, "refused") {
		t.Errorf("external with access off: %s", text)
	}
	e := w.ag.Activity().Entries()
	if len(e) != 2 || e[0].Outcome != agent.OutcomeRefused || e[0].Client != "Claude Code" || e[1].Client != "Claude (AI Chat)" {
		t.Errorf("activity %+v", e)
	}

	// A connection that never says hello is dropped.
	defer agent.SetHelloTimeout(100 * time.Millisecond)()
	conn, err := pipe.Dial(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Error("read from a connection without hello")
	}
}

// TestBridgeStartsLauncher starts the launcher when its pipe is missing
// and waits until it listens.
func TestBridgeStartsLauncher(t *testing.T) {
	w := newWorld(t, nil)
	name := pipeName()
	var started atomic.Int32
	start := func() error {
		started.Add(1)
		go func() {
			time.Sleep(300 * time.Millisecond) // the launcher starting up
			ln, err := pipe.Listen(name)
			if err != nil {
				t.Error(err)
				return
			}
			go w.ag.Serve(t.Context(), ln)
		}()
		return nil
	}
	c, _ := bridge(t, agent.BridgeOptions{Pipe: name, Start: start, Wait: 10 * time.Second, Token: "tok", Session: "s"})
	r := c.initialize("2025-06-18")
	if r.Result["serverInfo"] == nil {
		t.Fatalf("initialize: %+v", r)
	}
	if text, isErr := toolText(t, c.request("tools/call", map[string]any{"name": "search", "arguments": map[string]any{"query": "Visual Studio Code"}})); isErr {
		t.Errorf("search: %s", text)
	}
	if n := started.Load(); n != 1 {
		t.Errorf("started %d times", n)
	}
}

// TestBridgeUnavailable answers MCP itself when the launcher cannot start.
func TestBridgeUnavailable(t *testing.T) {
	c, _ := bridge(t, agent.BridgeOptions{Pipe: pipeName(), Start: func() error { return errors.New("no launcher here") }})
	r := c.initialize("2025-06-18")
	if r.Result["instructions"] == nil {
		t.Fatalf("initialize: %+v", r)
	}
	if r := c.request("tools/list", nil); len(r.Result["tools"].([]any)) != 5 {
		t.Errorf("tools/list: %+v", r)
	}
	text, isErr := toolText(t, c.request("tools/call", map[string]any{"name": "search", "arguments": map[string]any{"query": "x"}}))
	if !isErr || !strings.Contains(text, "could not be started") || !strings.Contains(text, "no launcher here") {
		t.Errorf("call: %s", text)
	}
	// Without a way to start it.
	c, _ = bridge(t, agent.BridgeOptions{Pipe: pipeName()})
	c.initialize("2025-06-18")
	if text, _ := toolText(t, c.request("tools/call", map[string]any{"name": "search", "arguments": map[string]any{"query": "x"}})); !strings.Contains(text, "not running") {
		t.Errorf("call: %s", text)
	}
}

// TestBridgeRelaysHello checks the hello line the bridge sends first.
func TestBridgeRelaysHello(t *testing.T) {
	server, client := net.Pipe()
	dial := func(context.Context, string) (net.Conn, error) { return client, nil }
	c, _ := bridge(t, agent.BridgeOptions{Pipe: "x", Dial: dial, Token: "t0k", Session: "abc"})
	br := bufio.NewReader(server)
	line, err := br.ReadString('\n')
	if err != nil || line != `{"golauncher":"hello","token":"t0k","session":"abc"}`+"\n" {
		t.Fatalf("hello %q %v", line, err)
	}
	go func() { fmt.Fprintln(server, `{"jsonrpc":"2.0","id":1,"result":{}}`) }()
	c.send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	got, _ := br.ReadString('\n')
	if !strings.Contains(got, `"ping"`) {
		t.Errorf("relayed %q", got)
	}
	if r := c.read(); r.Result == nil {
		t.Errorf("relayed back %+v", r)
	}
	server.Close()
}
