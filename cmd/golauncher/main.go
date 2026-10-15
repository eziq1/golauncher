// Command golauncher is the launcher's GUI: a Raycast-style search panel
// summoned with a global hotkey (Alt+Space by default), living in the tray.
//
// Build it without a console window:
//
//	go build -ldflags "-H windowsgui" ./cmd/golauncher
//
// Flags, which a running instance also takes from a second one:
//
//	--show      show the launcher
//	--settings  open the settings window
//	--quit      quit the running instance
package main

import (
	"fmt"
	"os"

	"golauncher/internal/shell"
)

// version is set at build time with -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	if err := shell.Run(shell.Options{Args: os.Args[1:], Version: version}); err != nil {
		fmt.Fprintln(os.Stderr, "golauncher:", err)
		os.Exit(1)
	}
}
