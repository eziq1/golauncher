package shell

import (
	"testing"

	"github.com/eziq1/golauncher/internal/update"
)

func TestUpdateDisabled(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == update.EnvNoUpdate {
				return v
			}
			return ""
		}
	}
	for _, tc := range []struct {
		env       string
		managedBy string
		want      string
	}{
		{"", "", ""}, // the update service decides: installed, portable or dev
		{"", update.DisabledWinget, update.DisabledWinget},
		{"", update.DisabledHomebrew, update.DisabledHomebrew},
		{"1", "", update.DisabledEnv},
		{"1", update.DisabledWinget, update.DisabledEnv},
		{"1", update.DisabledHomebrew, update.DisabledEnv},
		{"0", update.DisabledWinget, update.DisabledWinget},
	} {
		if got := updateDisabled(env(tc.env), tc.managedBy); got != tc.want {
			t.Errorf("updateDisabled(%q, %q) = %q, want %q", tc.env, tc.managedBy, got, tc.want)
		}
	}
}

func TestManagedCopyDoesNotUpdate(t *testing.T) {
	// Even when it looks like the installed copy, a copy that winget or
	// Homebrew installed leaves updates to them.
	for _, m := range []string{update.DisabledWinget, update.DisabledHomebrew} {
		s := update.NewService(update.Options{Dir: t.TempDir(), Current: "0.8.0", InstallDir: t.TempDir(), Disabled: updateDisabled(func(string) string { return "" }, m)})
		if s.CanInstall() || s.Status().Disabled != m {
			t.Fatalf("%s: status = %+v", m, s.Status())
		}
	}
}
