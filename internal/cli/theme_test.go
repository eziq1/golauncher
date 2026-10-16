package cli

import (
	"strings"
	"testing"
)

// TestThemeGolden covers glctl theme with the built-in themes and the
// theme files in testdata/themes, never the user's folder.
func TestThemeGolden(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
	}{
		{"theme_list", []string{"theme", "list"}, 0},
		{"theme_show_nord", []string{"theme", "show", "nord"}, 0},
		{"theme_export_paper", []string{"theme", "export", "paper"}, 0},
		{"theme_validate_good", []string{"theme", "validate", "testdata/themes/midnight-teal.json"}, 0},
		{"theme_validate_warnings", []string{"theme", "validate", "testdata/themes/faint.json"}, 0},
		{"theme_validate_broken", []string{"theme", "validate", "testdata/themes/broken.json"}, 1},
		{"theme_validate_text", []string{"theme", "validate", "--format", "text", "testdata/themes/faint.json"}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, code := glctl(t, tt.args...)
			if code != tt.code {
				t.Fatalf("exit code %d, want %d; output:\n%s", code, tt.code, out)
			}
			golden(t, tt.name, out)
		})
	}
	if _, code := glctl(t, "theme", "show", "nope"); code != 1 {
		t.Errorf("show of a missing theme: exit %d", code)
	}
	if _, code := glctl(t, "theme"); code != 2 {
		t.Errorf("no subcommand: exit %d", code)
	}
	// A folder of themes adds them, and a theme there may extend another.
	out, code := glctl(t, "theme", "list", "--themes", "testdata/themes", "--format", "text")
	if code != 0 || !strings.Contains(out, "midnight-teal") || !strings.Contains(out, "problem: broken") {
		t.Errorf("list with themes: %d\n%s", code, out)
	}
}
