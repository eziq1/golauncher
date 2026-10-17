package agent

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json/v2"
	"errors"
	"net"
	"sync"
	"time"
)

// The launcher serves the tools on a named pipe only the current user can
// open (internal/winapi/pipe). Every connection starts with one hello line
// from the bridge, then speaks MCP:
//
//	{"golauncher":"hello","token":"…","session":"…","agent":"…"}
//
// A token equal to the launcher's process token marks the launcher's own AI
// chat, whose session ID names the chat; other connections are AI apps,
// which need Settings › AI › "Allow AI apps to use golauncher".

// hello is the first line of a pipe connection.
type hello struct {
	Golauncher string `json:"golauncher"`
	Token      string `json:"token,omitempty"`
	Session    string `json:"session,omitempty"`
	// Agent names the agent CLI of the launcher's chat (EnvAgent).
	Agent string `json:"agent,omitempty"`
}

const helloKind = "hello"

// helloTimeout bounds the wait for the hello line.
var helloTimeout = 10 * time.Second

// Serve accepts connections on ln until ctx is done or ln fails, serving MCP
// on each.
func (a *Agent) Serve(ctx context.Context, ln net.Listener) error {
	stop := context.AfterFunc(ctx, func() { ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		wg.Go(func() {
			defer conn.Close()
			if err := a.serveConn(ctx, conn); err != nil {
				a.log.Debug("agent connection", "err", err)
			}
		})
	}
}

// serveConn reads the hello line and serves MCP on conn.
func (a *Agent) serveConn(ctx context.Context, conn net.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	context.AfterFunc(ctx, func() { conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(helloTimeout))
	br := bufio.NewReader(conn)
	line, err := readLine(br)
	if err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Time{})
	var h hello
	if err := json.Unmarshal(line, &h); err != nil || h.Golauncher != helloKind {
		return errors.New("agent: the connection did not start with a hello")
	}
	c := Client{}
	if tok := a.o.Token; tok != "" && subtle.ConstantTimeCompare([]byte(h.Token), []byte(tok)) == 1 {
		c.Chat, c.ChatSession, c.ChatAgent = true, h.Session, h.Agent
	}
	a.log.Info("ai client connected", "chat", c.Chat)
	defer a.log.Info("ai client disconnected", "chat", c.Chat)
	return a.ServeMCP(ctx, br, conn, c, false)
}
