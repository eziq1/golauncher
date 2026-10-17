package agent

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/pipe"
)

// BridgeOptions configure Bridge.
type BridgeOptions struct {
	// Pipe is the launcher's pipe (default pipe.AgentName()).
	Pipe string
	// Dial connects to the pipe (default pipe.Dial).
	Dial func(ctx context.Context, name string) (net.Conn, error)
	// Start starts the launcher in the background when it is not running;
	// nil does not start it.
	Start func() error
	// Wait is how long to wait for a launcher that was started (default 30s).
	Wait time.Duration
	// Token, Session and Agent are sent in the hello (EnvToken, EnvSession,
	// EnvAgent).
	Token, Session, Agent string
	// Version is reported when the launcher cannot be reached.
	Version string
	Log     *slog.Logger
}

// Bridge is `golauncher mcp`: a stdio MCP server that forwards everything to
// the running launcher over its pipe, starting the launcher first when it is
// not running (docs/ai.md §4.3). When the launcher cannot be reached, it
// still answers MCP itself: initialize and tools/list work, and every tool
// call returns an error that says why.
func Bridge(ctx context.Context, stdin io.Reader, stdout io.Writer, o BridgeOptions) error {
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	conn, err := connect(ctx, o)
	if err != nil {
		log.Warn("bridge: launcher unavailable", "err", err)
		return serveMCP(ctx, stdin, stdout, unavailable{err}, mcpOptions{Version: o.Version, Log: log})
	}
	defer conn.Close()
	b, _ := json.Marshal(hello{Golauncher: helloKind, Token: o.Token, Session: o.Session, Agent: o.Agent})
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return err
	}
	done := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, stdin)
		done <- err // the client went away
	}()
	go func() {
		_, err := io.Copy(stdout, conn)
		if err == nil {
			err = errors.New("agent: golauncher closed the connection")
		}
		done <- err // the launcher quit
	}()
	select {
	case err = <-done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	return err
}

// connect connects to the launcher's pipe, starting the launcher when no
// pipe exists.
func connect(ctx context.Context, o BridgeOptions) (net.Conn, error) {
	dial := o.Dial
	if dial == nil {
		dial = pipe.Dial
	}
	name := o.Pipe
	if name == "" {
		n, err := pipe.AgentName()
		if err != nil {
			return nil, err
		}
		name = n
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, err := dial(dctx, name)
	cancel()
	if err == nil || !errors.Is(err, pipe.ErrNotFound) {
		return conn, err
	}
	if o.Start == nil {
		return nil, fmt.Errorf("golauncher is not running: %w", err)
	}
	if err := o.Start(); err != nil {
		return nil, fmt.Errorf("golauncher is not running and could not be started: %w", err)
	}
	deadline := time.Now().Add(cmp.Or(o.Wait, 30*time.Second))
	for {
		dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		conn, err = dial(dctx, name)
		cancel()
		if err == nil || !errors.Is(err, pipe.ErrNotFound) {
			return conn, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("golauncher was started but did not get ready in time: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// unavailable answers MCP when the launcher cannot be reached.
type unavailable struct{ err error }

func (unavailable) initialize(string, string) {}
func (unavailable) instructions() string      { return Instructions }
func (unavailable) tools() []Tool             { return Tools() }
func (u unavailable) call(_ context.Context, name string, _ jsontext.Value) (Result, error) {
	for _, t := range Tools() {
		if t.Name == name {
			return fail("error", "golauncher is unavailable: "+u.err.Error()), nil
		}
	}
	return Result{}, fmt.Errorf("%w %q", ErrUnknownTool, name)
}
