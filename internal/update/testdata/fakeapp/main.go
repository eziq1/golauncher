// Command fakeapp stands in for golauncher.exe in the update helper's
// tests. Built with -ldflags "-X main.mode=...", it behaves as a new
// version that is healthy (writes the health marker), crashes at start, or
// hangs without ever being ready.
package main

import (
	"os"
	"time"
)

var mode = "healthy"

func main() {
	switch mode {
	case "crash":
		os.Exit(3)
	case "hang":
		time.Sleep(time.Minute)
	default:
		if marker := os.Getenv("GOLAUNCHER_UPDATE_HEALTH"); marker != "" {
			_ = os.WriteFile(marker, []byte("ok\n"), 0o644)
		}
	}
}
