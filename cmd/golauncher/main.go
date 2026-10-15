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
//
// A copy that is not installed offers to install itself for the current
// user, or to upgrade or downgrade the installed version
// (docs/self-install.md). These flags are for this copy only:
//
//	--portable   run without installing
//	--uninstall  uninstall golauncher (Apps & features runs this)
//
// GOLAUNCHER_NO_INSTALL=1 turns the installer off, as do builds without a
// version (version "dev").
package main

import (
	"fmt"
	"os"

	"github.com/eziq1/golauncher/internal/shell"
)

// version is set at build time with -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	if err := shell.Run(shell.Options{Args: os.Args[1:], Version: version}); err != nil {
		fmt.Fprintln(os.Stderr, "golauncher:", err)
		os.Exit(1)
	}
}
