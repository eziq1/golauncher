package everything

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func requireRunning(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("talks to Everything")
	}
	if os.Getenv("GOLAUNCHER_EVERYTHING_TESTS") != "1" || !Running() {
		t.Skip("Everything is not running")
	}
}

func TestSearchReal(t *testing.T) {
	requireRunning(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	res, err := Search(ctx, Query{Text: "notepad.exe", Max: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("no results for notepad.exe")
	}
	for _, r := range res {
		if !strings.Contains(strings.ToLower(r.Name), "notepad.exe") || !strings.HasSuffix(r.Path, `\`+r.Name) {
			t.Errorf("unexpected result %+v", r)
		}
	}
	t.Logf("%d results, first %+v", len(res), res[0])

	res, err = Search(ctx, Query{Text: `system32\notepad.exe`, MatchPath: true, Max: 1})
	if err != nil || len(res) > 1 {
		t.Fatalf("match path: %v %v", res, err)
	}
	if len(res) == 1 && (res[0].IsFolder || res[0].Size <= 0 || res[0].Modified.IsZero()) {
		t.Errorf("missing fields: %+v", res[0])
	}

	res, err = Search(ctx, Query{Text: `^windows$`, Regex: true, Max: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if !strings.EqualFold(r.Name, "windows") {
			t.Errorf("regex result %+v", r)
		}
	}
}

func TestSearchConcurrent(t *testing.T) {
	requireRunning(t)
	var wg sync.WaitGroup
	for _, q := range []string{"notepad.exe", "explorer.exe", "cmd.exe", "kernel32.dll"} {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			res, err := Search(ctx, Query{Text: q, Max: 3})
			if err != nil {
				t.Errorf("%s: %v", q, err)
				return
			}
			for _, r := range res {
				if !strings.Contains(strings.ToLower(r.Name), strings.TrimSuffix(q, ".exe")) {
					t.Errorf("%s: unexpected %+v", q, r)
				}
			}
		})
	}
	wg.Wait()
}

func TestSearchCanceled(t *testing.T) {
	requireRunning(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Search(ctx, Query{Text: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestInstalled(t *testing.T) {
	exe, ok := Installed()
	t.Logf("installed=%v exe=%q running=%v", ok, exe, Running())
	if Running() && !ok {
		t.Error("running but not installed")
	}
	if p, _ := runningExe(); Running() && p == "" {
		t.Log("could not resolve the running executable")
	}
	t.Logf("registry=%q", registryExe())
	_ = commonPaths()
}

func TestStartErrors(t *testing.T) {
	if err := Start(""); err == nil {
		t.Error("Start(\"\") succeeded")
	}
	if err := Start(`C:\does\not\exist\Everything.exe`); err == nil {
		t.Error("Start(missing) succeeded")
	}
}
