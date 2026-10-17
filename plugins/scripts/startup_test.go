package scripts

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestSlowDirectoryDoesNotBlockInit: a scripts directory that does not
// answer (a disconnected network drive) must not hold up Init, which the
// engine calls without a timeout (code review 3.2). A root search during the
// scan returns what is known, and the plugin asks for it again when the scan
// is done.
func TestSlowDirectoryDoesNotBlockInit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.ps1"), []byte("# @raycast.title Slow Script\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewWithOptions(Options{Directories: []string{dir}, System: fakeSystem(nil)})
	release := make(chan struct{})
	blocked := make(chan struct{}, 1)
	p.readDir = func(name string) ([]os.DirEntry, error) {
		select {
		case blocked <- struct{}{}:
		default:
		}
		<-release
		return os.ReadDir(name)
	}
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	released := false
	unblock := func() {
		if !released {
			released = true
			close(release)
		}
	}
	t.Cleanup(func() { unblock(); p.Close() })

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- p.Init(t.Context(), h) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		unblock()
		<-done
		t.Fatalf("Init blocked on the directory scan for %v", time.Since(start))
	}

	// The scan is under way: the search sees nothing yet but is remembered.
	<-blocked
	items, err := p.RootItems(t.Context(), plugin.Query{Text: "slow"})
	if err != nil || len(items) != 0 {
		t.Fatalf("items during the scan = %+v, %v", items, err)
	}
	unblock()
	eventually(t, "the scan", func() bool { return len(p.Scripts()) == 1 })
	eventually(t, "Invalidate after the scan", func() bool { return slices.Equal(h.Invalidated(), []string{""}) })
	items, _ = p.RootItems(t.Context(), plugin.Query{Text: "slow"})
	if len(items) != 1 || items[0].Title != "Slow Script" {
		t.Errorf("items after the scan = %+v", items)
	}
}

// TestBackgroundScanWithoutSearch: when no search came in during the
// initial scan, finishing it does not ask the host to refresh.
func TestBackgroundScanWithoutSearch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.ps1"), []byte("# @raycast.title A\n"), 0o644)
	p := NewWithOptions(Options{Directories: []string{dir}, System: fakeSystem(nil)})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	eventually(t, "the scan", p.loaded.Load)
	if len(p.Scripts()) != 1 {
		t.Errorf("scripts = %+v", p.Scripts())
	}
	if inv := h.Invalidated(); len(inv) != 0 {
		t.Errorf("invalidated without a search: %q", inv)
	}
}
