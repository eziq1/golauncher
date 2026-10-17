// Package macterm knows the terminal apps and code editors of macOS: their
// bundle identifiers, how to find the installed ones (Launch Services), and
// the command lines that open a terminal window or tab in a folder, run a
// command in it, or open folders and files in an editor or Quick Look.
//
// Building a command line is pure and the same on every platform, so it is
// unit-tested anywhere; only AppPath (the Launch Services lookup) and Run
// (which starts programs) touch the system. The builtin.terminal and
// builtin.files plugins use it on macOS, and share the user's default
// terminal through SetPreferred and Preferred.
package macterm

import (
	"slices"
	"sync/atomic"
)

// Driver is how a terminal is opened.
type Driver int

const (
	// DriverTerminal is Terminal.app: AppleScript (do script).
	DriverTerminal Driver = iota
	// DriverITerm is iTerm2: AppleScript (create window, write text).
	DriverITerm
	// DriverGhostty is Ghostty: open -na with --working-directory and -e.
	DriverGhostty
	// DriverWezTerm is WezTerm: its CLI (wezterm start, wezterm cli spawn).
	DriverWezTerm
	// DriverAlacritty is Alacritty: open -na with --working-directory and -e.
	DriverAlacritty
	// DriverKitty is kitty: open -na with --directory and the program.
	DriverKitty
	// DriverWarp is Warp: its warp://action URLs (no commands).
	DriverWarp
)

// Terminal is a terminal app.
type Terminal struct {
	// ID is stable and stored in preferences: "terminal", "iterm2", …
	ID string
	// Name is the product name, which is not translated.
	Name     string
	BundleID string
	Driver   Driver
	// Tabs reports whether a new tab can be opened (else only windows).
	Tabs bool
	// Commands reports whether a command can be run in the new window.
	Commands bool
}

// Terminals are the terminals known, in the order they are listed.
var Terminals = []Terminal{
	{ID: "terminal", Name: "Terminal", BundleID: "com.apple.Terminal", Driver: DriverTerminal, Tabs: true, Commands: true},
	{ID: "iterm2", Name: "iTerm2", BundleID: "com.googlecode.iterm2", Driver: DriverITerm, Tabs: true, Commands: true},
	{ID: "ghostty", Name: "Ghostty", BundleID: "com.mitchellh.ghostty", Driver: DriverGhostty, Commands: true},
	{ID: "wezterm", Name: "WezTerm", BundleID: "com.github.wez.wezterm", Driver: DriverWezTerm, Tabs: true, Commands: true},
	{ID: "alacritty", Name: "Alacritty", BundleID: "org.alacritty", Driver: DriverAlacritty, Commands: true},
	{ID: "kitty", Name: "kitty", BundleID: "net.kovidgoyal.kitty", Driver: DriverKitty, Commands: true},
	{ID: "warp", Name: "Warp", BundleID: "dev.warp.Warp-Stable", Driver: DriverWarp, Tabs: true},
}

// Installed is a terminal found on this Mac.
type Installed struct {
	Terminal
	// Path is the .app bundle.
	Path string
}

// Detect returns the installed terminals in the order of Terminals;
// appPath returns the bundle of a bundle identifier, "" when none.
func Detect(appPath func(bundleID string) string) []Installed {
	var out []Installed
	for _, t := range Terminals {
		if p := appPath(t.BundleID); p != "" {
			out = append(out, Installed{Terminal: t, Path: p})
		}
	}
	return out
}

// autoOrder is the order Pick tries terminals in when none was chosen:
// someone who installed another terminal most likely prefers it to the
// one that comes with macOS.
var autoOrder = []string{"iterm2", "ghostty", "wezterm", "kitty", "alacritty", "warp", "terminal"}

// Pick returns the terminal to use: id when it is installed, else the
// automatic choice (Auto). ok is false when none is installed.
func Pick(installed []Installed, id string) (Installed, bool) {
	if i := slices.IndexFunc(installed, func(t Installed) bool { return t.ID == id }); id != "" && i >= 0 {
		return installed[i], true
	}
	return Auto(installed)
}

// Auto returns the terminal used when the user chose none: the first
// installed of iTerm2, Ghostty, WezTerm, kitty, Alacritty, Warp, and
// Terminal last.
func Auto(installed []Installed) (Installed, bool) {
	for _, id := range autoOrder {
		if i := slices.IndexFunc(installed, func(t Installed) bool { return t.ID == id }); i >= 0 {
			return installed[i], true
		}
	}
	if len(installed) > 0 {
		return installed[0], true
	}
	return Installed{}, false
}

// preferred is the default terminal chosen in builtin.terminal's settings.
var preferred atomic.Value // string

// SetPreferred records the ID of the terminal chosen as the default ("" for
// the automatic choice), for the plugins that open terminals.
func SetPreferred(id string) { preferred.Store(id) }

// Preferred returns the ID recorded by SetPreferred, "" when none.
func Preferred() string {
	s, _ := preferred.Load().(string)
	return s
}

// Editor is a code editor that opens folders and files.
type Editor struct {
	ID       string // "vscode", "cursor", "zed"
	Name     string // the product name, not translated
	BundleID string
}

// Editors are the editors known, in the order they are listed.
var Editors = []Editor{
	{ID: "vscode", Name: "VS Code", BundleID: "com.microsoft.VSCode"},
	{ID: "cursor", Name: "Cursor", BundleID: "com.todesktop.230313mzl4w4u92"},
	{ID: "zed", Name: "Zed", BundleID: "dev.zed.Zed"},
}

// InstalledEditors returns the editors appPath finds, in the order of
// Editors.
func InstalledEditors(appPath func(bundleID string) string) []Editor {
	var out []Editor
	for _, e := range Editors {
		if appPath(e.BundleID) != "" {
			out = append(out, e)
		}
	}
	return out
}

// EditorByID returns the known editor id.
func EditorByID(id string) (Editor, bool) {
	i := slices.IndexFunc(Editors, func(e Editor) bool { return e.ID == id })
	if i < 0 {
		return Editor{}, false
	}
	return Editors[i], true
}
