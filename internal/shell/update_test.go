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
		env    string
		winget bool
		want   string
	}{
		{"", false, ""}, // the update service decides: installed, portable or dev
		{"", true, update.DisabledWinget},
		{"1", false, update.DisabledEnv},
		{"1", true, update.DisabledEnv},
		{"0", true, update.DisabledWinget},
	} {
		if got := updateDisabled(env(tc.env), tc.winget); got != tc.want {
			t.Errorf("updateDisabled(%q, %v) = %q, want %q", tc.env, tc.winget, got, tc.want)
		}
	}
}

func TestWingetCopyDoesNotUpdate(t *testing.T) {
	// Even when it looks like the installed copy, a winget copy leaves
	// updates to winget.
	s := update.NewService(update.Options{Dir: t.TempDir(), Current: "0.8.0", InstallDir: t.TempDir(), Disabled: updateDisabled(func(string) string { return "" }, true)})
	if s.CanInstall() || s.Status().Disabled != update.DisabledWinget {
		t.Fatalf("status = %+v", s.Status())
	}
}
