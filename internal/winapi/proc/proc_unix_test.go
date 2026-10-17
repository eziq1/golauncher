//go:build !windows

package proc

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStopEndsTree stops sh while its child sleep runs: killing the
// process group ends both, so the output pipe (which sleep inherited)
// closes at once instead of after WaitDelay.
func TestStopEndsTree(t *testing.T) {
	var mu sync.Mutex
	var b strings.Builder
	w := &LineWriter{OnLine: func(s string) { mu.Lock(); b.WriteString(s + "\n"); mu.Unlock() }}
	p, err := Start(Options{Argv: []string{"/bin/sh", "-c", "echo started; sleep 60; echo end"},
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

// TestEnvCase checks that variable names are case-sensitive on Unix.
func TestEnvCase(t *testing.T) {
	got := MergeEnv([]string{"Path=a", "PATH=b"}, []string{"PATH=c"})
	if strings.Join(got, ",") != "Path=a,PATH=c" {
		t.Errorf("MergeEnv = %q", got)
	}
	if Getenv(got, "path") != "" || Getenv(got, "Path") != "a" {
		t.Error("Getenv")
	}
}
