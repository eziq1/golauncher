package terminal

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestRealMachine reads this computer's Windows Terminal settings, the
// fragments and the WSL registry, and runs `wsl --list --verbose` (which
// only reads). It never opens, starts or stops anything: any other command
// fails the test. Opt in with GOLAUNCHER_TERMINAL_TESTS=1.
func TestRealMachine(t *testing.T) {
	if os.Getenv("GOLAUNCHER_TERMINAL_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_TERMINAL_TESTS=1 to read this computer's terminal settings and WSL distributions")
	}
	readOnly := func(ctx context.Context, argv ...string) ([]byte, error) {
		if !slices.Equal(argv, []string{wslExe, "--list", "--verbose"}) {
			t.Errorf("refused to run %q", argv)
			return nil, errors.ErrUnsupported
		}
		out, err := runHidden(ctx, argv...)
		t.Logf("wsl --list --verbose (err %v):\n%s", err, DecodeOutput(out))
		return out, err
	}
	p := NewWithOptions(Options{Run: readOnly, Sync: true})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		if errors.Is(err, plugin.ErrUnavailable) {
			t.Skipf("unavailable here: %v", err)
		}
		t.Fatal(err)
	}
	defer p.Close()
	s := p.snapshot()
	t.Logf("Windows Terminal: %v, wt = %s, settings = %s", s.hasWT, s.wt, s.term.Settings)
	for _, e := range s.term.Problems {
		t.Logf("fragment problem: %v", e)
	}
	for _, pr := range s.term.Profiles {
		t.Logf("profile %-40q source=%-32s default=%-5v icon=%q cmd=%q", pr.Name, pr.Source, pr.Default, pr.Icon, pr.Commandline)
	}
	t.Logf("WSL: %d distributions, default %q", len(s.reg.Distros), s.reg.DefaultName())
	for _, d := range s.reg.Distros {
		t.Logf("distro %q v%d %s state=%+v", d.Name, d.Version, d.BasePath, s.states[d.Name])
	}
	items, err := p.RootItems(t.Context(), plugin.Query{Context: &plugin.Context{Folder: `C:\Windows`}})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		var acts []string
		for _, a := range it.Actions {
			acts = append(acts, a.ID)
		}
		t.Logf("item %-48s %-60q [%s]", it.ID, it.Subtitle, strings.Join(acts, " "))
	}
	if s.term.Settings != "" && len(s.term.Profiles) == 0 {
		t.Error("settings.json found but no profile read")
	}
}
