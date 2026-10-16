//go:build windows

package proc

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// collect runs o and returns its output lines and exit code.
func collect(t *testing.T, o Options) (string, int) {
	t.Helper()
	var mu sync.Mutex
	var b strings.Builder
	w := &LineWriter{OnLine: func(s string) { mu.Lock(); b.WriteString(s + "\n"); mu.Unlock() }}
	o.Output = w
	p, err := Start(o)
	if err != nil {
		t.Fatal(err)
	}
	code, err := p.Wait()
	if err != nil {
		t.Fatal(err)
	}
	w.Flush()
	return b.String(), code
}

func TestBatchArguments(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "args test.cmd")
	os.WriteFile(script, []byte("@echo off\r\necho [%1] [%2] [%3]\r\nexit /b 5\r\n"), 0o644)
	out, code := collect(t, Options{Argv: []string{script, "hello world & more", "", `a"b`}, Dir: dir})
	if code != 5 || out != "[\"hello world & more\"] [\"\"] [\"a\"\"b\"]\n" {
		t.Errorf("code %d, output %q", code, out)
	}
	if got := BatchCommandLine(`C:\Windows\cmd.exe`, `C:\s\a.bat`, []string{"x y"}); got != `C:\Windows\cmd.exe /d /s /c ""C:\s\a.bat" "x y""` {
		t.Errorf("BatchCommandLine = %s", got)
	}
}

func TestEnvAndDir(t *testing.T) {
	dir := t.TempDir()
	out, _ := collect(t, Options{Argv: []string{"cmd", "/d", "/c", "echo %PROC_TEST% & cd"}, Dir: dir, Env: Environ("PROC_TEST=42")})
	if !strings.Contains(out, "42") || !strings.Contains(strings.ToLower(out), strings.ToLower(dir)) {
		t.Errorf("output %q", out)
	}
}

// TestStdinAndStderr writes to the program's input through the pipe and
// receives its stderr apart from stdout.
func TestStdinAndStderr(t *testing.T) {
	var mu sync.Mutex
	var out, errOut strings.Builder
	p, err := Start(Options{
		Argv:      []string{"cmd", "/d", "/q", "/c", "set /p LINE= & call echo got %LINE% & echo oops 1>&2"},
		Output:    &LineWriter{OnLine: func(s string) { mu.Lock(); out.WriteString(s + "\n"); mu.Unlock() }},
		Stderr:    &LineWriter{OnLine: func(s string) { mu.Lock(); errOut.WriteString(s + "\n"); mu.Unlock() }},
		StdinPipe: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stdin().Write([]byte("hello\r\n")); err != nil {
		t.Fatal(err)
	}
	p.Stdin().Close()
	if code, err := p.Wait(); code != 0 || err != nil {
		t.Fatalf("code %d, err %v", code, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(out.String(), "got hello") || strings.Contains(out.String(), "oops") || !strings.Contains(errOut.String(), "oops") {
		t.Errorf("stdout %q, stderr %q", out.String(), errOut.String())
	}
	q, err := Start(Options{Argv: []string{"cmd", "/d", "/c", "exit 0"}})
	if err != nil {
		t.Fatal(err)
	}
	if q.Stdin() != nil {
		t.Error("Stdin without StdinPipe")
	}
	q.Wait()
}

// TestStopEndsTree stops cmd.exe while its child ping runs: the job object
// ends both, so the output pipe closes at once instead of after WaitDelay.
func TestStopEndsTree(t *testing.T) {
	var mu sync.Mutex
	var b strings.Builder
	w := &LineWriter{OnLine: func(s string) { mu.Lock(); b.WriteString(s + "\n"); mu.Unlock() }}
	p, err := Start(Options{Argv: []string{"cmd", "/d", "/c", "echo started & ping -n 60 127.0.0.1 >nul & echo end"},
		Output: w, WaitDelay: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		started := strings.Contains(b.String(), "started")
		mu.Unlock()
		if started {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no output")
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	p.Stop()
	code, err := p.Wait()
	if d := time.Since(start); d > 10*time.Second || code == 0 || err != nil {
		t.Errorf("Stop: %v, code %d, err %v", d, code, err)
	}
	p.Stop() // after the end: no effect
}
