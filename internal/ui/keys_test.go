package ui

import (
	"os"
	"slices"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/core"
)

// TestMain runs the tests with the Windows spelling of shortcuts, which
// their simulated key presses (mui.Ctrl) are written for; TestKeysMac
// covers macOS.
func TestMain(m *testing.M) {
	core.MacKeys = false
	os.Exit(m.Run())
}

// withMacKeys runs fn with core.MacKeys set to mac.
func withMacKeys(t *testing.T, mac bool, fn func()) {
	t.Helper()
	defer func(v bool) { core.MacKeys = v }(core.MacKeys)
	core.MacKeys = mac
	fn()
}

func TestKeysWindows(t *testing.T) {
	withMacKeys(t, false, func() {
		if Primary() != mui.Ctrl {
			t.Error("Primary is not Ctrl")
		}
		for _, tt := range []struct {
			mods mui.Modifiers
			key  mui.Key
			want string
		}{
			{mui.Ctrl | mui.Shift, mui.KeyC, "Ctrl+Shift+C"},
			{mui.Super | mui.Alt, mui.KeyK, "Alt+Win+K"},
			{0, mui.KeyEnter, "Enter"},
		} {
			if got := KeyString(tt.mods, tt.key); got != tt.want {
				t.Errorf("KeyString(%v, %v) = %q, want %q", tt.mods, tt.key, got, tt.want)
			}
		}
		for in, want := range map[string]string{
			"ctrl + shift+c": "Ctrl+Shift+C", "Control+N": "Ctrl+N", "cmd+k": "Win+K",
			"Mod+Enter": "Ctrl+Enter", "option+esc": "Alt+Escape",
		} {
			if got := NormalizeShortcut(in); got != want {
				t.Errorf("NormalizeShortcut(%q) = %q, want %q", in, got, want)
			}
		}
		if got := keyCaps("Ctrl+Shift+Enter"); !slices.Equal(got, []string{"Ctrl", "Shift", "↵"}) {
			t.Errorf("keyCaps = %q", got)
		}
	})
}

func TestKeysMac(t *testing.T) {
	withMacKeys(t, true, func() {
		if Primary() != mui.Super {
			t.Error("Primary is not ⌘")
		}
		for _, tt := range []struct {
			mods mui.Modifiers
			key  mui.Key
			want string
		}{
			{mui.Super | mui.Shift, mui.KeyC, "Ctrl+Shift+C"}, // ⌘⇧C
			{mui.Ctrl, mui.KeyN, "Control+N"},                 // ⌃N
			{mui.Ctrl | mui.Super | mui.Alt, mui.KeyK, "Ctrl+Alt+Control+K"},
		} {
			if got := KeyString(tt.mods, tt.key); got != tt.want {
				t.Errorf("KeyString(%v, %v) = %q, want %q", tt.mods, tt.key, got, tt.want)
			}
		}
		for in, want := range map[string]string{
			"ctrl+shift+c": "Ctrl+Shift+C", "Cmd+K": "Ctrl+K", "Win+K": "Ctrl+K",
			"control+n": "Control+N", "Control+Ctrl+Up": "Ctrl+Control+Up", "CmdOrCtrl+,": "Ctrl+,",
		} {
			if got := NormalizeShortcut(in); got != want {
				t.Errorf("NormalizeShortcut(%q) = %q, want %q", in, got, want)
			}
		}
		// A key press matches the shortcut written for it.
		if KeyString(mui.Super|mui.Shift, mui.KeyF) != NormalizeShortcut("Ctrl+Shift+F") {
			t.Error("⌘⇧F does not match Ctrl+Shift+F")
		}
		for in, want := range map[string][]string{
			"Ctrl+Shift+Enter":   {"⇧", "⌘", "↵"},
			"Ctrl+Alt+Control+K": {"⌃", "⌥", "⌘", "K"},
			"Escape":             {"Escape"},
		} {
			if got := keyCaps(in); !slices.Equal(got, want) {
				t.Errorf("keyCaps(%q) = %q, want %q", in, got, want)
			}
		}
	})
}
