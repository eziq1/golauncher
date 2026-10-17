package quicklinks

import (
	"slices"
	"testing"
)

// TestTranslatorKeywordsMoved: the translator plugin took over "tr", "fy"
// and "翻译" (docs/translator.md §6, R99). Google Translate's web search is
// "gt" (and "translate"), Youdao's "yd" and "有道"; the three are reserved,
// so TestBuiltinKeywords keeps built-in engines from taking them again.
func TestTranslatorKeywordsMoved(t *testing.T) {
	tr, _ := builtinByID("translate")
	yd, _ := builtinByID("youdao")
	if !slices.Equal(tr.keywords, []string{"gt", "translate"}) || !slices.Equal(yd.keywords, []string{"yd", "有道"}) {
		t.Errorf("keywords: translate %v, youdao %v", tr.keywords, yd.keywords)
	}
	for _, kw := range []string{"tr", "fy", "翻译"} {
		if !slices.Contains(reservedPrefixes, kw) {
			t.Errorf("%q is not reserved", kw)
		}
	}
}
