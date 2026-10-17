package config

import (
	"sync"
	"testing"
	"time"
)

// TestNotifyInCommitOrder is review finding 2.2: two concurrent Updates
// commit A then B; the listeners must end on B, the stored configuration,
// whatever the interleaving of their notifications. The first listener
// blocks while it is told about A, so B commits and notifies meanwhile.
func TestNotifyInCommitOrder(t *testing.T) {
	s := NewMemory(Default())
	entered := make(chan struct{})
	release := make(chan struct{})
	var (
		mu   sync.Mutex
		seen []string // what the second listener was told, in order
	)
	first := true
	s.OnChange(func(c Config) {
		if c.Hotkey == "A" && first {
			first = false
			close(entered)
			<-release
		}
	})
	s.OnChange(func(c Config) {
		mu.Lock()
		seen = append(seen, c.Hotkey)
		mu.Unlock()
	})

	doneA := make(chan error)
	go func() { doneA <- s.Update(func(c *Config) { c.Hotkey = "A" }) }()
	<-entered // A is committed; its notification is under way
	doneB := make(chan error, 1)
	go func() { doneB <- s.Update(func(c *Config) { c.Hotkey = "B" }) }()
	// Give B the time to commit and, before the fix, to notify ahead of A.
	eventually(t, func() bool { return s.Get().Hotkey == "B" })
	time.Sleep(50 * time.Millisecond)
	close(release)
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-doneB:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the second Update did not return")
	}
	if got := s.Get().Hotkey; got != "B" {
		t.Fatalf("stored hotkey = %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 || seen[len(seen)-1] != "B" {
		t.Fatalf("listener was told %q, last must be the stored B", seen)
	}
	for i := 1; i < len(seen); i++ {
		if seen[i-1] == "B" && seen[i] == "A" {
			t.Fatalf("listener was told %q: A after B", seen)
		}
	}
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestNotifyNestedUpdate: a listener may call Get and Update; the nested
// change is delivered after the current one, without deadlock.
func TestNotifyNestedUpdate(t *testing.T) {
	s := NewMemory(Default())
	var seen []string
	s.OnChange(func(c Config) {
		seen = append(seen, c.Hotkey)
		if c.Hotkey == "A" {
			if s.Get().Hotkey != "A" {
				t.Error("Get in a listener")
			}
			if err := s.Update(func(c *Config) { c.Hotkey = "B" }); err != nil {
				t.Error(err)
			}
		}
	})
	done := make(chan error)
	go func() { done <- s.Update(func(c *Config) { c.Hotkey = "A" }) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: Update from a listener")
	}
	if len(seen) != 2 || seen[0] != "A" || seen[1] != "B" || s.Get().Hotkey != "B" {
		t.Fatalf("seen %q, stored %q", seen, s.Get().Hotkey)
	}
}
