package everything

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

// TestStressTyping simulates typing in the launcher: every keystroke starts
// a search and cancels the previous one.
func TestStressTyping(t *testing.T) {
	if testing.Short() || os.Getenv("GOLAUNCHER_EVERYTHING_TESTS") != "1" || !Running() {
		t.Skip("needs a running Everything")
	}
	words := []string{"n", "no", "not", "note", "notep", "notepa", "notepad", "r", "re", "rep", "repo", "report", "中", "中文"}
	var wg sync.WaitGroup
	for round := range 20 {
		var cancel context.CancelFunc = func() {}
		for _, w := range words {
			cancel()
			var ctx context.Context
			ctx, cancel = context.WithTimeout(t.Context(), 2*time.Second)
			wg.Go(func() {
				Search(ctx, Query{Text: w, Max: 100})
			})
			time.Sleep(time.Duration(round%5) * time.Millisecond)
		}
		cancel()
	}
	wg.Wait()
}
