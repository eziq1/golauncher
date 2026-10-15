package clock

import (
	"os"
	"runtime"
	"runtime/pprof"
	"testing"
)

// maxIndexHeap bounds the live heap of the loaded city index
// (docs/world-clock.md §10).
const maxIndexHeap = 7 << 20

// liveHeap returns the live heap after a full collection.
func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// TestIndexMemory: the loaded index keeps at most maxIndexHeap live. Set
// CLOCK_HEAP_PROFILE to a file name to write a heap profile.
func TestIndexMemory(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector changes allocation sizes")
	}
	before := liveHeap()
	idx, err := loadIndex()
	if err != nil {
		t.Fatal(err)
	}
	after := liveHeap()
	if f := os.Getenv("CLOCK_HEAP_PROFILE"); f != "" {
		out, err := os.Create(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.Lookup("heap").WriteTo(out, 0); err != nil {
			t.Fatal(err)
		}
		out.Close()
	}
	runtime.KeepAlive(idx)
	live := int64(after) - int64(before)
	t.Logf("index: %.2f MB live", float64(live)/(1<<20))
	if live > maxIndexHeap {
		t.Errorf("index keeps %d bytes live, want at most %d", live, maxIndexHeap)
	}
}
