package everything

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSpotlightSearch runs real, read-only mdfind searches. Spotlight may
// be off or not have indexed a new folder yet, so finding nothing is not a
// failure; what is found must match.
func TestSpotlightSearch(t *testing.T) {
	if _, ok := Installed(); !ok {
		t.Skip("no mdfind")
	}
	if !Running() {
		t.Error("Running is false with mdfind installed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	res, err := Search(ctx, Query{Text: "ext:app", Max: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) > 5 {
		t.Errorf("%d results, want at most 5", len(res))
	}
	for _, r := range res {
		if !strings.HasSuffix(strings.ToLower(r.Name), ".app") || r.Path == "" || filepath.Base(r.Path) != r.Name {
			t.Errorf("result %+v", r)
		}
		if r.IsFolder && r.Size != -1 {
			t.Errorf("folder size %d", r.Size)
		}
	}
	// Scoped to a folder that cannot hold anything.
	dir := t.TempDir()
	res, err = Search(ctx, Query{Text: `"` + dir + `\" x`})
	if err != nil || len(res) != 0 {
		t.Errorf("empty scope: %v, %v", res, err)
	}

	// A cancelled search fails with the context's error.
	cctx, ccancel := context.WithCancel(t.Context())
	ccancel()
	if _, err := Search(cctx, Query{Text: "a"}); err == nil {
		t.Error("cancelled search succeeded")
	}
}

func TestUserFolders(t *testing.T) {
	home, _ := os.UserHomeDir()
	for _, d := range UserFolders() {
		if !strings.HasPrefix(d, home+"/") {
			t.Errorf("user folder %s outside %s", d, home)
		}
	}
}
