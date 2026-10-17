package ui

import (
	"slices"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/core"
)

// keyNames names the keys that shortcuts use, as written in manifests and
// the configuration ("Ctrl+Shift+C", "Alt+Space").
var keyNames = map[mui.Key]string{
	mui.KeyEnter: "Enter", mui.KeyEscape: "Escape", mui.KeyBackspace: "Backspace",
	mui.KeyTab: "Tab", mui.KeySpace: "Space", mui.KeyDelete: "Delete", mui.KeyInsert: "Insert",
	mui.KeyHome: "Home", mui.KeyEnd: "End", mui.KeyPageUp: "PageUp", mui.KeyPageDown: "PageDown",
	mui.KeyLeft: "Left", mui.KeyRight: "Right", mui.KeyUp: "Up", mui.KeyDown: "Down",
	mui.KeyF1: "F1", mui.KeyF2: "F2", mui.KeyF3: "F3", mui.KeyF4: "F4", mui.KeyF5: "F5", mui.KeyF6: "F6",
	mui.KeyF7: "F7", mui.KeyF8: "F8", mui.KeyF9: "F9", mui.KeyF10: "F10", mui.KeyF11: "F11", mui.KeyF12: "F12",
	mui.KeyMinus: "-", mui.KeyEqual: "=", mui.KeyComma: ",", mui.KeyPeriod: ".", mui.KeySlash: "/",
	mui.KeySemicolon: ";", mui.KeyQuote: "'", mui.KeyBracketLeft: "[", mui.KeyBracketRight: "]",
	mui.KeyBackslash: `\`, mui.KeyBackquote: "`",
}

func init() {
	for k := mui.KeyA; k <= mui.KeyZ; k++ {
		keyNames[k] = string(rune('A' + int(k-mui.KeyA)))
	}
	for k := mui.Key0; k <= mui.Key9; k++ {
		keyNames[k] = string(rune('0' + int(k-mui.Key0)))
	}
}

// Primary is the modifier of the primary shortcuts, those written "Ctrl+…":
// the Ctrl key, and ⌘ on macOS (core.MacKeys).
func Primary() mui.Modifiers {
	if core.MacKeys {
		return mui.Super
	}
	return mui.Ctrl
}

// KeyString spells a key press as a shortcut, e.g. "Ctrl+Shift+C". It
// returns "" for keys without a name. On macOS ⌘ is spelled "Ctrl" and the
// Control key "Control" (core.MacKeys).
func KeyString(mods mui.Modifiers, key mui.Key) string {
	name, ok := keyNames[key]
	if !ok {
		return ""
	}
	primary, other, otherName := mui.Ctrl, mui.Super, "Win"
	if core.MacKeys {
		primary, other, otherName = mui.Super, mui.Ctrl, "Control"
	}
	var parts []string
	if mods&primary != 0 {
		parts = append(parts, "Ctrl")
	}
	if mods&mui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if mods&mui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	if mods&other != 0 {
		parts = append(parts, otherName)
	}
	return strings.Join(append(parts, name), "+")
}

// NormalizeShortcut makes shortcut strings comparable, as the engine does:
// "ctrl + shift+c" becomes "Ctrl+Shift+C".
func NormalizeShortcut(s string) string { return core.NormalizeShortcut(s) }

// macModifiers are the symbols of the modifiers on macOS, in Apple's order
// (⌃⌥⇧⌘).
var macModifiers = []struct{ name, symbol string }{
	{"Control", "⌃"}, {"Alt", "⌥"}, {"Shift", "⇧"}, {"Ctrl", "⌘"},
}

// keyCaps splits a shortcut into the key caps shown for it. On macOS the
// modifiers are shown as symbols, in Apple's order.
func keyCaps(shortcut string) []string {
	if shortcut == "" {
		return nil
	}
	caps := strings.Split(NormalizeShortcut(shortcut), "+")
	for i, k := range caps {
		switch k {
		case "Enter":
			caps[i] = "↵"
		case "Up":
			caps[i] = "↑"
		case "Down":
			caps[i] = "↓"
		case "Left":
			caps[i] = "←"
		case "Right":
			caps[i] = "→"
		}
	}
	if core.MacKeys {
		key := caps[len(caps)-1]
		var out []string
		for _, m := range macModifiers {
			if slices.Contains(caps[:len(caps)-1], m.name) {
				out = append(out, m.symbol)
			}
		}
		caps = append(out, key)
	}
	return caps
}
