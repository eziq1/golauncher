// Command glctl drives the golauncher engine without a GUI, for agents and
// tests. Run "glctl help" for usage.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/eziq1/golauncher/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Main(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
