package scoop

import (
	"os"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
)

// TestRealScoop reads the Scoop of this machine, if there is one, and logs
// how long indexing and searching take (go test -run RealScoop -v). It only
// reads: no Scoop command is run (the runner is fake).
func TestRealScoop(t *testing.T) {
	root, _ := DetectRoot("", os.Getenv)
	if root == "" || testing.Short() {
		t.Skip("Scoop is not installed")
	}
	runner := &FakeRunner{}
	cache := t.TempDir()
	start := func() (*Plugin, time.Duration) {
		p := NewWithOptions(Options{Root: root, Global: DetectGlobal(os.Getenv), NoDetect: true, Runner: runner, Sync: true})
		h, err := hostfake.New(p, cache)
		if err != nil {
			t.Fatal(err)
		}
		begin := time.Now()
		if err := p.Init(t.Context(), h); err != nil {
			t.Fatal(err)
		}
		took := time.Since(begin)
		t.Cleanup(func() { p.Close() })
		return p, took
	}
	cold, coldTook := start()
	ix := cold.Index()
	outdatedApps := 0
	for i := range ix.Apps {
		if _, ok := ix.Outdated(&ix.Apps[i]); ok {
			outdatedApps++
		}
	}
	t.Logf("root %s: %d buckets, %d manifests, %d installed apps (%d with updates)",
		root, len(ix.Buckets), ix.PackageCount(), len(ix.Apps), outdatedApps)
	t.Logf("index without cache (parse all manifests, read apps, write cache): %v", coldTook)
	if st, err := os.Stat(cachePath(cold.h.CacheDir())); err == nil {
		t.Logf("cache file: %.1f MB", float64(st.Size())/(1<<20))
	}

	begin := time.Now()
	if _, err := loadCache(cold.h.CacheDir(), root); err != nil {
		t.Fatal(err)
	}
	t.Logf("cache load (decode only): %v", time.Since(begin))
	_, warmTook := start()
	t.Logf("index from cache (decode, check stamps, read apps): %v", warmTook)

	begin = time.Now()
	serial := 0
	for _, b := range ix.Buckets {
		rb, _ := readBucket(b.Name, b.Dir, nil)
		serial += len(rb.Packages)
	}
	t.Logf("parsing all %d manifests on one goroutine: %v", serial, time.Since(begin))

	queries := []string{"git", "python", "vs code", "7z", "node", "微信", "x"}
	const rounds = 20
	begin = time.Now()
	for range rounds {
		for _, q := range queries {
			ix.Search(t.Context(), q, defaultMax)
		}
	}
	t.Logf("search latency: %v per query (average of %d queries)", time.Since(begin)/time.Duration(rounds*len(queries)), rounds*len(queries))

	begin = time.Now()
	r := computeReclaim(t.Context(), root, ix.Apps)
	t.Logf("cleanup estimate: %d old versions of %d apps, %d MB; cache %d files, %d MB (took %v)",
		func() int {
			n := 0
			for _, a := range r.Apps {
				n += len(a.Versions)
			}
			return n
		}(), len(r.Apps), r.OldSize>>20, r.CacheFiles, r.CacheSize>>20, time.Since(begin))

	if ix.PackageCount() == 0 {
		t.Fatal("no manifests found")
	}
	if m := ix.Search(t.Context(), "git", 1); len(m) == 0 || m[0].Pkg.Name != "git" {
		t.Errorf("search git: %+v", m)
	}
	if len(runner.Commands()) != 0 {
		t.Fatalf("commands were started: %q", runner.Commands())
	}
}
