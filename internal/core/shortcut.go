package core

import (
	"runtime"
	"slices"
	"strings"
)

// MacKeys selects the macOS spelling of shortcuts. It is set on macOS
// (tests change it). Shortcuts are written with "Ctrl" for the primary
// modifier everywhere: the Ctrl key on Windows, ⌘ on macOS, where "Control"
// names the Control key (⌃) and "Cmd", "Win" and "Super" are ⌘ too
// (docs/macos-port.md §5.1). Manifests and plugins keep writing "Ctrl+K".
var MacKeys = runtime.GOOS == "darwin"

// NormalizeShortcut makes shortcut strings comparable: "ctrl + shift+c"
// becomes "Ctrl+Shift+C"; aliases such as "CmdOrCtrl", "Mod", "Option" and
// "Esc" become the names above.
func NormalizeShortcut(s string) string {
	parts := strings.Split(s, "+")
	var mods []string
	key := ""
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch strings.ToLower(p) {
		case "ctrl", "cmdorctrl", "commandorcontrol", "mod":
			mods = append(mods, "Ctrl")
		case "control":
			mods = append(mods, cmpMac("Control", "Ctrl"))
		case "alt", "option":
			mods = append(mods, "Alt")
		case "shift":
			mods = append(mods, "Shift")
		case "win", "super", "meta", "cmd", "command":
			mods = append(mods, cmpMac("Ctrl", "Win"))
		case "esc":
			key = "Escape"
		case "return":
			key = "Enter"
		default:
			if len(p) == 1 {
				p = strings.ToUpper(p)
			} else if p != "" {
				p = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
			}
			key = p
		}
	}
	order := map[string]int{"Ctrl": 0, "Alt": 1, "Shift": 2, "Win": 3, "Control": 3}
	slices.SortFunc(mods, func(a, b string) int { return order[a] - order[b] })
	mods = slices.Compact(mods)
	return strings.Join(append(mods, key), "+")
}

// normalizeShortcut is NormalizeShortcut.
func normalizeShortcut(s string) string { return NormalizeShortcut(s) }

// cmpMac returns mac with MacKeys, else other.
func cmpMac(mac, other string) string {
	if MacKeys {
		return mac
	}
	return other
}
