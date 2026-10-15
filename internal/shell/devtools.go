package shell

import (
	"os"

	"github.com/egoist/mygo"
)

// DevToolsEnv turns on MyGo's developer tools (inspector: F12, Ctrl+Shift+I,
// Ctrl+Shift+C) in a release build when set to 1.
const DevToolsEnv = "GOLAUNCHER_DEVTOOLS"

// devToolsVersion is the version Run was started with; dev builds get the
// developer tools.
var devToolsVersion string

// devTools decides whether windows get MyGo's developer tools. MyGo turns
// them on unless the app was built with `mygo build`, which golauncher is
// not; left alone, release builds would open the inspector on F12 and take
// Ctrl+Shift+C, the shortcut of "Copy Path", away from the launcher.
func devTools() mygo.DevTools {
	if devToolsVersion == "dev" || os.Getenv(DevToolsEnv) == "1" {
		return mygo.DevToolsEnabled
	}
	return mygo.DevToolsDisabled
}
