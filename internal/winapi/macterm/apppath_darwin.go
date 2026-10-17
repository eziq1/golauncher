//go:build darwin

package macterm

import (
	"os"

	"github.com/eziq1/golauncher/internal/darwin/ns"
)

// AppPath returns the .app bundle Launch Services knows for bundleID (as
// NSWorkspace URLForApplicationWithBundleIdentifier: finds it), or "" when
// the app is not installed.
func AppPath(bundleID string) string {
	if bundleID == "" {
		return ""
	}
	var p string
	ns.Pool(func() {
		ws := ns.Send(ns.Class("NSWorkspace"), "sharedWorkspace")
		if u := ns.Send(ws, "URLForApplicationWithBundleIdentifier:", ns.String(bundleID)); u != 0 {
			p = ns.GoString(ns.Send(u, "path"))
		}
	})
	if p == "" {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return "" // removed since Launch Services saw it
	}
	return p
}
