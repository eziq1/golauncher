// Command glctl drives the golauncher engine without a GUI, for agents and
// tests. Run "glctl help" for usage.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/eziq1/golauncher/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	cli.Version = version
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Main(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
