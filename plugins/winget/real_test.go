package winget

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestRealWinget reads the winget of this machine and logs counts and
// timings (GOLAUNCHER_WINGET_TESTS=1 go test -run RealWinget -v). It only
// reads: the index, pinning.db, `winget source export`, `winget list` and
// `winget show`. No operation starts (the fake host's task runner is fake,
// and the test fails if a task was created).
func TestRealWinget(t *testing.T) {
	if os.Getenv("GOLAUNCHER_WINGET_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_WINGET_TESTS=1 to read the winget of this machine")
	}
	if FindWinget(os.Getenv) == "" {
		t.Skip("winget is not installed")
	}
	p := NewWithOptions(Options{Sync: true})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	t.Logf("Init (source export, read the index, winget list, pins): %v", time.Since(begin))

	begin = time.Now()
	srcs := p.loadSources()
	t.Logf("winget source export: %d sources, %v", len(srcs), time.Since(begin))

	ix, err := p.index.Get(t.Context())
	if ix.PackageCount() == 0 {
		t.Fatalf("no packages in the index: %v", err)
	}
	for _, s := range srcs {
		if !s.Indexed() {
			continue
		}
		begin = time.Now()
		c, err := p.readCatalog(s)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("index of %s: %d packages, read in %v", s.Name, len(c.Packages), time.Since(begin))
	}

	queries := []string{"git", "python", "vs code", "7zip", "node", "微信", "x"}
	const rounds = 20
	begin = time.Now()
	for range rounds {
		for _, q := range queries {
			ix.Search(t.Context(), q, defaultMax)
		}
	}
	t.Logf("search latency: %v per query (average of %d queries)", time.Since(begin)/time.Duration(rounds*len(queries)), rounds*len(queries))
	if found := ix.Search(t.Context(), "Git.Git", 1); len(found) == 0 || found[0].ID != "Git.Git" {
		t.Errorf("search Git.Git: %+v", found)
	}

	begin = time.Now()
	p.refreshInstalled(false)
	st := p.Installed()
	updates, local, unparsed := 0, 0, 0
	for _, in := range st.Items {
		switch {
		case in.Available != "" && in.Source != "":
			updates++
		case in.Source == "":
			local++
		}
		if in.Name == "" || in.Version == "" {
			unparsed++
		}
	}
	t.Logf("winget list: %d packages (%d with updates, %d without a source), %v; error %q", len(st.Items), updates, local, time.Since(begin), st.Err)
	if st.Err != "" || len(st.Items) == 0 || unparsed > 0 {
		t.Errorf("winget list: error %q, %d items, %d rows without name or version", st.Err, len(st.Items), unparsed)
	}
	p.mu.Lock()
	pins := len(p.pins)
	p.mu.Unlock()
	t.Logf("pins: %d", pins)

	begin = time.Now()
	d, err := p.details(t.Context(), showRequest{ID: "Git.Git", Source: "winget"}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("winget show: %d fields, %v", len(d.Fields), time.Since(begin))

	if n := len(h.Tasks.List()); n != 0 || len(h.Runner.Commands()) != 0 {
		t.Fatalf("%d tasks were started", n)
	}
}

// heapMB is the live heap after a full collection, in MB.
func heapMB() float64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1 << 20)
}

// TestRealIdleHeap logs the heap the index of this machine's winget keeps
// (go test -run RealIdleHeap -v): none after Init and a window shown, until
// a search reads it, and none again once it is dropped. It runs no winget
// command (the sources default to "winget").
func TestRealIdleHeap(t *testing.T) {
	if FindWinget(os.Getenv) == "" || testing.Short() {
		t.Skip("winget is not installed")
	}
	o := Options{Run: func(context.Context, []string) ([]byte, error) { return nil, errors.New("not run") },
		OpenPins: func() (*sql.DB, error) { return nil, fs.ErrNotExist }}
	p := NewWithOptions(o)
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := heapMB()
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	h.Bus.PublishSync(plugin.EventWindowShown)
	idle := heapMB() - base
	begin := time.Now()
	n := p.Index().PackageCount()
	took := time.Since(begin)
	loaded := heapMB() - base
	p.index.Drop()
	dropped := heapMB() - base
	t.Logf("heap above the start: %.1f MB after Init, %.1f MB with the index (%d packages, read in %v), %.1f MB after it is dropped",
		idle, loaded, n, took, dropped)
	if loaded-idle < 1 {
		t.Errorf("the index took only %.1f MB", loaded-idle)
	}
}
