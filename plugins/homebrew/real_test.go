package homebrew

import (
	"os"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestRealHomebrew reads the Homebrew of this machine and logs counts and
// timings (GOLAUNCHER_HOMEBREW_TESTS=1 go test -run RealHomebrew -v). It
// only reads: the API index in brew's cache, `brew --cache`,
// `brew info --json=v2 --installed` and `brew info --json=v2 <name>`, all
// with HOMEBREW_NO_AUTO_UPDATE. No operation starts (the fake host's task
// runner is fake, and the test fails if a task was created).
func TestRealHomebrew(t *testing.T) {
	if os.Getenv("GOLAUNCHER_HOMEBREW_TESTS") != "1" {
		t.Skip("set GOLAUNCHER_HOMEBREW_TESTS=1 to read the Homebrew of this machine")
	}
	exe := FindBrew(nil, nil)
	if exe == "" {
		t.Skip("Homebrew is not installed")
	}
	t.Logf("brew: %s", exe)
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
	t.Logf("Init (brew --cache, read the index, brew info --installed): %v", time.Since(begin))
	t.Logf("cache: %s", p.cacheDir())

	ix := p.Index()
	if ix == nil {
		t.Fatalf("no index: %v", p.indexErr)
	}
	f, c := ix.Counts()
	t.Logf("index: %s, %d formulae, %d casks", ix.Format, f, c)
	files, err := findIndex(p.cacheDir())
	if err != nil {
		t.Fatal(err)
	}
	begin = time.Now()
	if _, err := readIndex(files, time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Logf("reading the index again: %v", time.Since(begin))

	queries := []string{"git", "python", "vs code", "firefox", "node", "微信", "x"}
	const rounds = 20
	begin = time.Now()
	for range rounds {
		for _, q := range queries {
			ix.Search(t.Context(), q, defaultMax)
		}
	}
	t.Logf("search: %v per query", time.Since(begin)/time.Duration(rounds*len(queries)))
	for _, q := range queries {
		var names []string
		for _, pkg := range ix.Search(t.Context(), q, 5) {
			names = append(names, pkg.Kind+"/"+pkg.Name)
		}
		t.Logf("  %q: %v", q, names)
	}

	st := p.Installed()
	if st == nil || st.Err != "" {
		t.Fatalf("installed: %+v", st)
	}
	outdated := 0
	for _, in := range st.Items {
		if in.Outdated {
			outdated++
		}
	}
	t.Logf("installed: %d packages, %d outdated", len(st.Items), outdated)

	for _, ir := range []infoRequest{{Kind: KindFormula, Name: "wget"}, {Kind: KindCask, Name: "firefox"}} {
		begin = time.Now()
		v, err := p.View(t.Context(), plugin.ViewRequest{ID: viewInfo, Payload: plugin.MustPayload(ir)})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("brew info %s: %v, %q", ir.Name, time.Since(begin), v.EmptyText)
	}
	if n := len(h.Runner.Commands()); n != 0 {
		t.Fatalf("%d tasks were started", n)
	}
}
