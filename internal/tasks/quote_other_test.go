//go:build !windows

package tasks

import (
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func TestCommandLineUnix(t *testing.T) {
	for _, tt := range []struct {
		argv []string
		want string
	}{
		{[]string{"echo", "hi there"}, `echo "hi there"`},
		{[]string{"echo", ""}, `echo ""`},
		{[]string{"rm", "a;b", "*.txt"}, `rm "a;b" "*.txt"`},
		{[]string{"echo", "$HOME"}, `echo '$HOME'`},
		{[]string{"echo", "it's \"x\""}, `echo 'it'\''s "x"'`},
		{[]string{"/bin/ls", `C:\x`}, `/bin/ls 'C:\x'`},
	} {
		if got := CommandLine(plugin.ProcessSpec{Argv: tt.argv}); got != tt.want {
			t.Errorf("CommandLine(%q) = %s, want %s", tt.argv, got, tt.want)
		}
	}
}
