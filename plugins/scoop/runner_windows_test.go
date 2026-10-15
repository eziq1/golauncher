//go:build windows

package scoop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeShim writes a stand-in scoop.ps1 into root\shims, which the runner
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

func TestPowerShellRunner(t *testing.T) {
	if testing.Short() {
		t.Skip("starts PowerShell")
	}
	root := fakeShim(t)
	r := &PowerShell{Root: root}
	out := &output{}
	proc, err := r.Start([]string{"golauncher-test-echo", "it's", "a b", "$(Get-Date)", "*"}, out)
	if err != nil {
		t.Fatal(err)
	}
	code, err := proc.Wait()
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if code != 3 {
		t.Errorf("exit code %d, want 3\n%s", code, text)
	}
	for _, want := range []string{"arg=[golauncher-test-echo]", "arg=[it's]", "arg=[a b]", "arg=[$(Get-Date)]", "arg=[*]",
		"SCOOP=" + root, "ünïcödé 中文"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	proc.Stop() // after the end: no effect
}

func TestPowerShellRunnerStop(t *testing.T) {
	if testing.Short() {
		t.Skip("starts PowerShell")
	}
	r := &PowerShell{Root: fakeShim(t)}
	out := &output{}
	proc, err := r.Start([]string{"golauncher-test-sleep"}, out)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(out.String(), "sleeping") {
		if time.Now().After(deadline) {
			t.Fatalf("the script did not start: %q", out.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	proc.Stop()
	code, _ := proc.Wait()
	if code == 0 || time.Since(start) > 10*time.Second {
		t.Errorf("Stop: exit code %d after %v", code, time.Since(start))
	}
}
