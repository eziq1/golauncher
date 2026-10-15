package shell

import (
	"bytes"
	"image/png"
	"log/slog"
	"testing"
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
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("size = %v", b)
	}
}
