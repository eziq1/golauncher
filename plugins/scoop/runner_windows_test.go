//go:build windows

package scoop

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/winapi/proc"
)

// fakeShim writes a stand-in scoop.ps1 into root\shims, which the script
// puts first on PATH. The real Scoop is never run: the arguments used here
// are not Scoop commands, so even a real scoop would only print an error.
func fakeShim(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	shims := filepath.Join(root, "shims")
	if err := os.MkdirAll(shims, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "\xef\xbb\xbf" + `if ($args[0] -eq 'golauncher-test-sleep') { Write-Output 'sleeping'; Start-Sleep -Seconds 60; exit 0 }
if ($args[0] -eq 'golauncher-test-ok') { Write-Output 'fine'; exit 0 }
foreach ($a in $args) { Write-Output "arg=[$a]" }
Write-Output "SCOOP=$env:SCOOP"
Write-Output 'ünïcödé 中文'
exit 3
`
	if err := os.WriteFile(filepath.Join(shims, "scoop.ps1"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// lines collects output lines.
type lines struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lines) add(s string) {
	l.mu.Lock()
	l.b.WriteString(s + "\n")
	l.mu.Unlock()
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// runCommand starts the process the plugin would hand to the host for
// steps, as the host's task runner does.
func runCommand(t *testing.T, root string, steps [][]string) (*proc.Process, *lines) {
	t.Helper()
	p := &Plugin{root: root}
	cmd := p.command(steps)
	out := &lines{}
	w := &proc.LineWriter{OnLine: out.add}
	pr, err := proc.Start(proc.Options{Argv: cmd.Argv, Dir: cmd.Dir, Env: proc.Environ(cmd.Env...), Output: w})
	if err != nil {
		t.Fatal(err)
	}
	return pr, out
}

func TestPowerShellCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("starts PowerShell")
	}
	root := fakeShim(t)
	pr, out := runCommand(t, root, [][]string{
		{"golauncher-test-ok"},
		{"golauncher-test-echo", "it's", "a b", "$(Get-Date)", "*"},
		{"golauncher-test-ok"}, // not run: the step before failed
	})
	code, err := pr.Wait()
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if code != 3 {
		t.Errorf("exit code %d, want 3\n%s", code, text)
	}
	for _, want := range []string{"> scoop golauncher-test-ok\nfine", "arg=[golauncher-test-echo]", "arg=[it's]", "arg=[a b]",
		"arg=[$(Get-Date)]", "arg=[*]", "SCOOP=" + root, "ünïcödé 中文"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "fine") != 1 {
		t.Errorf("the step after the failure ran:\n%s", text)
	}
	pr.Stop() // after the end: no effect
}

func TestPowerShellCommandStop(t *testing.T) {
	if testing.Short() {
		t.Skip("starts PowerShell")
	}
	pr, out := runCommand(t, fakeShim(t), [][]string{{"golauncher-test-sleep"}})
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(out.String(), "sleeping") {
		if time.Now().After(deadline) {
			t.Fatalf("the script did not start: %q", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	pr.Stop()
	code, _ := pr.Wait()
	if code == 0 || time.Since(start) > 10*time.Second {
		t.Errorf("Stop: exit code %d after %v", code, time.Since(start))
	}
}
