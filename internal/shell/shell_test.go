package shell

import (
	"bytes"
	"image/png"
	"log/slog"
	"testing"

	"github.com/eziq1/golauncher/internal/core"
)

func TestParseArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, actionNone},
		{[]string{"--show"}, actionShow},
		{[]string{"--settings"}, actionSettings},
		{[]string{"-quit"}, actionQuit},
		{[]string{"--unknown", "--settings"}, actionSettings},
		{[]string{"--settings", "--quit"}, actionQuit},
	} {
		if got := parseArgs(tc.args); got != tc.want {
			t.Errorf("parseArgs(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestAccelerator(t *testing.T) {
	defer func(v bool) { core.MacKeys = v }(core.MacKeys)
	core.MacKeys = false
	for in, want := range map[string]string{
		"Alt+Space":        "Alt+Space",
		"shift+ctrl+v":     "Ctrl+Shift+V",
		"Win+Alt+K":        "Alt+Super+K",
		"Ctrl+Shift+Enter": "Ctrl+Shift+Enter",
	} {
		if got := accelerator(in); got != want {
			t.Errorf("accelerator(%q) = %q, want %q", in, got, want)
		}
	}
	// On macOS "Ctrl" is ⌘ and "Control" the Control key.
	core.MacKeys = true
	for in, want := range map[string]string{
		"Alt+Space":         "Alt+Space",
		"shift+ctrl+v":      "Cmd+Shift+V",
		"Win+Alt+K":         "Cmd+Alt+K",
		"Control+Space":     "Ctrl+Space",
		"Ctrl+Control+Up":   "Cmd+Ctrl+Up",
		"CmdOrCtrl+Shift+P": "Cmd+Shift+P",
	} {
		if got := accelerator(in); got != want {
			t.Errorf("mac: accelerator(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetLevel(t *testing.T) {
	var v slog.LevelVar
	for in, want := range map[string]slog.Level{"debug": slog.LevelDebug, "warn": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo} {
		setLevel(&v, in)
		if v.Level() != want {
			t.Errorf("setLevel(%q) = %v", in, v.Level())
		}
	}
}

func TestTrayIcon(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(trayIcon()))
	if err != nil {
		t.Fatal(err)
	}
	w, h := 32, 32
	if trayIconTemplate {
		w, h = 44, 36 // 22×18 points, the menu bar's height
	}
	if b := img.Bounds(); b.Dx() != w || b.Dy() != h {
		t.Fatalf("size = %v", b)
	}
	if !trayIconTemplate {
		return
	}
	// A template image: black, with only its alpha drawing the shape.
	opaque := 0
	for y := range h {
		for x := range w {
			r, g, b, a := img.At(x, y).RGBA()
			if a > 0 && (r != 0 || g != 0 || b != 0) {
				t.Fatalf("pixel %d,%d is not black", x, y)
			}
			if a == 0xffff {
				opaque++
			}
		}
	}
	if opaque < w*h/10 {
		t.Fatalf("only %d opaque pixels", opaque)
	}
}
