package everything

import (
	"strings"
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestContextSearch: with a folder in the context the plugin offers to
// search it; the pushed view searches only inside that folder.
func TestContextSearch(t *testing.T) {
	p, _ := start(t, FixtureBackend("testdata/files.json"))
	if items, _ := p.ContextItems(t.Context(), plugin.Context{Kind: plugin.ContextExplorer}); len(items) != 0 {
		t.Errorf("items without a folder: %+v", items)
	}
	dir := `C:\Users\demo\Documents\Reports`
	items, err := p.ContextItems(t.Context(), plugin.Context{Kind: plugin.ContextExplorer, Folder: dir})
	if err != nil || len(items) != 1 {
		t.Fatalf("items %+v, %v", items, err)
	}
	do := items[0].Actions[0].Do
	if do.Kind != plugin.ActionPush || do.Target != cmdSearch {
		t.Fatalf("action %+v", do)
	}
	req := plugin.ViewRequest{ID: cmdSearch, Payload: do.Payload}
	v, err := p.View(t.Context(), req)
	if err != nil || v.Title != "Search in Reports" || !strings.Contains(v.EmptyText, dir) {
		t.Fatalf("empty scoped view %+v, %v", v, err)
	}
	req.Query.Text = "quarterly"
	v, err = p.View(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := items0(v)
	if len(got) != 2 {
		t.Fatalf("results %v, want the two reports", got)
	}
	for _, path := range got {
		if !strings.HasPrefix(path, dir+`\`) {
			t.Errorf("%s is outside %s", path, dir)
		}
	}
	// Elsewhere the same word finds nothing.
	req.Payload = plugin.MustPayload(searchScope{Scope: `D:\Photos`})
	if v, _ = p.View(t.Context(), req); len(items0(v)) != 0 {
		t.Errorf("results outside the scope: %v", items0(v))
	}
	if got := scopeTerm(`C:\`); got != `"C:\"` {
		t.Errorf("scopeTerm(C:\\) = %s", got)
	}
}

func items0(v plugin.View) []string {
	var out []string
	for _, it := range items(v) {
		out = append(out, it.ID)
	}
	return out
}
