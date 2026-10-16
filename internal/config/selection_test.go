package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestSelectionDefaults: a file without the selection settings gets the
// defaults (copying off, password managers excluded, 10,000 characters);
// a partial one keeps the defaults it leaves out; an emptied list stays
// empty; the maximum is kept within bounds.
func TestSelectionDefaults(t *testing.T) {
	dir := t.TempDir()
	load := func(body string) Config {
		t.Helper()
		p := filepath.Join(dir, "c.json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := load(`{"schemaVersion": 1}`)
	if c.Selection.Clipboard || c.Selection.EffectiveMaxChars() != DefaultSelectionMaxChars || !slices.Equal(c.Selection.ExcludeApps, DefaultSelectionExcludeApps) {
		t.Errorf("defaults %+v", c.Selection)
	}
	c = load(`{"schemaVersion": 1, "selection": {"clipboard": true}}`)
	if !c.Selection.Clipboard || !slices.Equal(c.Selection.ExcludeApps, DefaultSelectionExcludeApps) {
		t.Errorf("partial %+v", c.Selection)
	}
	c = load(`{"schemaVersion": 1, "selection": {"excludeApps": [], "maxChars": 5}}`)
	if len(c.Selection.ExcludeApps) != 0 || c.Selection.EffectiveMaxChars() != MinSelectionMaxChars {
		t.Errorf("emptied %+v, max %d", c.Selection, c.Selection.EffectiveMaxChars())
	}
	if (SelectionConfig{MaxChars: 1 << 30}).EffectiveMaxChars() != MaxSelectionMaxChars {
		t.Error("no upper bound")
	}
}
