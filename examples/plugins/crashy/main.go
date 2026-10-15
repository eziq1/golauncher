// Command crashy is a misbehaving external plugin used by the tests of
// internal/extproc. Its callbacks (ActionCallback targets) panic, exit,
// hang, call host APIs it has no permission for, push renders and so on.
//
// Flags select start-up misbehavior:
//
//	-mode exit         exit before answering initialize
//	-mode badproto     answer initialize with protocol 99
//	-mode unavailable  report the plugin unavailable from Init
//	-mode noise        print non-protocol lines to stdout first
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/pkg/plugin/rpcserve"
)

func main() {
	mode := flag.String("mode", "", "start-up misbehavior")
	flag.Parse()
	switch *mode {
	case "exit":
		os.Exit(3)
	case "badproto":
		badProtocol()
		return
	case "noise":
		fmt.Fprintln(os.Stdout, "this is not JSON")
		fmt.Fprintln(os.Stdout, "{not json either")
	}
	if err := rpcserve.Serve(&crashy{mode: *mode}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// badProtocol answers initialize with an unsupported protocol version.
func badProtocol() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":1,"result":{"protocol":99,"capabilities":{}}}`)
	}
}

type crashy struct {
	mode string
	h    plugin.Host
}

func (c *crashy) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.crashy"} }

func (c *crashy) Init(_ context.Context, h plugin.Host) error {
	if c.mode == "unavailable" {
		return fmt.Errorf("needs something that is missing: %w", plugin.ErrUnavailable)
	}
	c.h = h
	fmt.Fprintln(os.Stderr, "crashy started")
	return nil
}

func (c *crashy) Close() error { return nil }

func (c *crashy) View(ctx context.Context, req plugin.ViewRequest) (plugin.View, error) {
	return plugin.View{Kind: plugin.ViewList, Title: req.ID}, nil
}

func (c *crashy) HandleAction(ctx context.Context, a plugin.ActionDo) (plugin.Result, error) {
	ok := plugin.Result{Toast: &plugin.Toast{Style: plugin.ToastSuccess, Title: a.Target}}
	switch a.Target {
	case "ok":
		return ok, nil
	case "panic":
		panic("crashy panics")
	case "go-panic":
		c.h.Go(func() { panic("crashy panics in Host.Go") })
		return ok, nil
	case "exit":
		os.Exit(2)
	case "hang":
		select {} // ignores ctx
	case "slow":
		// Waits for cancellation and reports it through Invalidate.
		select {
		case <-ctx.Done():
			c.h.Invalidate("cancelled")
			return plugin.Result{}, ctx.Err()
		case <-time.After(30 * time.Second):
			return ok, nil
		}
	case "storage":
		// crashy declares no permissions, so this must be denied.
		if err := c.h.Storage().Set("k", []byte("v")); err != nil {
			return plugin.Result{}, err
		}
		return ok, nil
	case "clipboard":
		if err := c.h.Clipboard().WriteText("x"); err != nil {
			return plugin.Result{}, err
		}
		return ok, nil
	case "render":
		c.h.Render("v1", plugin.View{Kind: plugin.ViewList, Title: "rendered"})
		return ok, nil
	case "invalidate":
		c.h.Invalidate("list")
		return ok, nil
	case "error":
		return plugin.Result{}, errors.New("crashy failed")
	}
	return plugin.Result{}, fmt.Errorf("unknown callback %q", a.Target)
}
