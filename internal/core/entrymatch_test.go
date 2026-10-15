package core

import (
	"testing"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// TestEntryMatchChineseKeyword covers R40: an app listed under its English
// name matches its Chinese keyword, by characters and by pinyin.
func TestEntryMatchChineseKeyword(t *testing.T) {
	e := Entry{Item: plugin.Item{Title: "Contoso Chat", Keywords: []string{"ContosoChat", "康托聊天"}}}
	for _, q := range []string{"康托聊天", "康托", "kangtuo", "ktlt", "contoso"} {
		if s := entryMatch(q, e); s < 0.5 {
			t.Errorf("entryMatch(%q) = %v, want a match", q, s)
		}
	}
	if s := entryMatch("微信", e); s != 0 {
		t.Errorf("entryMatch(微信) = %v, want 0", s)
	}
}
