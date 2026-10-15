// Package control is a local test channel into a running launcher. The GUI
// serves it on a unix socket in the data directory, and `glctl attach`
// drives the real window through it for end-to-end tests.
//
// The protocol is one JSON request per line, answered by one JSON response
// per line.
package control

import (
	"bufio"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/eziq1/golauncher/internal/core"
)

// Window is the launcher window as seen by the control channel.
type Window interface {
	Show()
	Hide()
	Visible() bool
}

// Request is a control command.
type Request struct {
	// Cmd is one of "show", "hide", "type", "key", "select", "snapshot".
	Cmd   string `json:"cmd"`
	Text  string `json:"text,omitempty"`
	Key   string `json:"key,omitempty"`
	Index int    `json:"index,omitzero"`
}

// Response answers a Request.
type Response struct {
	OK       bool           `json:"ok"`
	Error    string         `json:"error,omitempty"`
	Visible  bool           `json:"visible"`
	Outcome  *core.Outcome  `json:"outcome,omitempty"`
	Snapshot *core.Snapshot `json:"snapshot,omitempty"`
}

// Serve accepts connections on the unix socket at path until ctx is done.
func Serve(ctx context.Context, path string, ctrl *core.Controller, win Window) error {
	os.Remove(path) // a stale socket from a crashed instance
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	context.AfterFunc(ctx, func() { ln.Close() })
	defer os.Remove(path)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveConn(ctx, conn, ctrl, win)
	}
}

func serveConn(ctx context.Context, conn net.Conn, ctrl *core.Controller, win Window) {
	defer conn.Close()
	dec := jsontext.NewDecoder(conn)
	for {
		var req Request
		if err := json.UnmarshalDecode(dec, &req); err != nil {
			return
		}
		resp := handle(ctx, req, ctrl, win)
		if err := json.MarshalWrite(conn, resp); err != nil {
			return
		}
		if _, err := conn.Write([]byte("\n")); err != nil {
			return
		}
	}
}

func handle(ctx context.Context, req Request, ctrl *core.Controller, win Window) Response {
	var resp Response
	switch req.Cmd {
	case "show":
		win.Show()
	case "hide":
		win.Hide()
	case "type":
		ctrl.SetText(ctx, req.Text)
	case "key":
		out, err := ctrl.Key(ctx, req.Key)
		if err != nil {
			resp.Error = err.Error()
		}
		if out.Hide {
			win.Hide()
		}
		resp.Outcome = &out
	case "select":
		ctrl.Select(req.Index)
	case "snapshot":
	default:
		resp.Error = fmt.Sprintf("unknown command %q", req.Cmd)
	}
	ctrl.Wait()
	snap := ctrl.Snapshot()
	resp.Snapshot = &snap
	resp.Visible = win.Visible()
	resp.OK = resp.Error == ""
	return resp
}

// Client talks to a running launcher.
type Client struct {
	conn net.Conn
	r    *bufio.Reader
}

// Dial connects to the control socket at path.
func Dial(path string) (*Client, error) {
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("control: is golauncher running with GOLAUNCHER_CONTROL=1? %w", err)
	}
	return &Client{conn: conn, r: bufio.NewReader(conn)}, nil
}

// Do sends a request and waits for the response.
func (c *Client) Do(req Request) (Response, error) {
	c.conn.SetDeadline(time.Now().Add(10 * time.Second))
	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := c.conn.Write(append(b, '\n')); err != nil {
		return Response{}, err
	}
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, err
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }
