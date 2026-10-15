package ui

import (
	"slices"
	"strings"

	mui "github.com/egoist/mygo/ui"
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

// KeyString spells a key press as a shortcut, e.g. "Ctrl+Shift+C". It
// returns "" for keys without a name.
func KeyString(mods mui.Modifiers, key mui.Key) string {
	name, ok := keyNames[key]
	if !ok {
		return ""
	}
	var parts []string
	if mods&mui.Ctrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if mods&mui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if mods&mui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	if mods&mui.Super != 0 {
		parts = append(parts, "Win")
	}
	return strings.Join(append(parts, name), "+")
}

// NormalizeShortcut makes shortcut strings comparable, as the engine does:
// "ctrl + shift+c" becomes "Ctrl+Shift+C".
func NormalizeShortcut(s string) string {
	var mods []string
	key := ""
	for p := range strings.SplitSeq(s, "+") {
		p = strings.TrimSpace(p)
		switch strings.ToLower(p) {
		case "ctrl", "control", "cmdorctrl", "commandorcontrol":
			mods = append(mods, "Ctrl")
		case "alt", "option":
			mods = append(mods, "Alt")
		case "shift":
			mods = append(mods, "Shift")
		case "win", "super", "meta", "cmd", "command":
			mods = append(mods, "Win")
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
	order := map[string]int{"Ctrl": 0, "Alt": 1, "Shift": 2, "Win": 3}
	slices.SortFunc(mods, func(a, b string) int { return order[a] - order[b] })
	mods = slices.Compact(mods)
	return strings.Join(append(mods, key), "+")
}

// keyCaps splits a shortcut into the key caps shown for it.
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
	return caps
}
