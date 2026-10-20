package ui_test

import (
	"strings"
	"testing"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// An AI answer with math shows display math laid out and math within text
// as Unicode text, never as its TeX, and copies as the TeX.
func TestAIChatMath(t *testing.T) {
	answer := "The growth rate is $\\alpha^2$ per year:\n\n$$\\text{CAGR} = \\left(\\frac{\\text{期末值}}{\\text{期初值}}\\right)^{\\frac{1}{n}} - 1$$\n\nDone."
	fake := &backend.Fake{Reply: func(string) []string { return []string{answer} }}
	lt := newAILauncher(t, fake, true)
	lt.typeText("ai chat")
	lt.key(0, mui.KeyEnter)
	lt.typeText("cagr")
	lt.key(0, mui.KeyEnter)
	lt.waitFor("the answer", func(md string) bool { return strings.Contains(md, "Done.") })
	lt.step()

	if !lt.tt.HasText("per year") || !lt.tt.HasText("α²") {
		t.Fatalf("inline math not shown as text: %q", lt.tt.Texts())
	}
	// The display formula is laid out, named by its Unicode text.
	if _, ok := lt.tt.Find("CAGR = (期末值 / 期初值)^(1/n) − 1"); !ok {
		t.Fatalf("display math not laid out: %q", lt.tt.Texts())
	}
	for _, s := range lt.tt.Texts() {
		// Only the formula's copy source, which does not show, holds TeX.
		if (strings.Contains(s, `\frac`) || strings.Contains(s, `$\alpha`)) && !strings.HasPrefix(s, "$$") {
			t.Fatalf("raw TeX shows: %q", s)
		}
	}

	// Copying the answer copies its TeX.
	lt.key(mui.Ctrl|mui.Shift, mui.KeyC)
	eff := lt.rec.Effects()
	if len(eff) == 0 || eff[len(eff)-1].Kind != plugin.ActionCopyText || !strings.Contains(eff[len(eff)-1].Target, `$$\text{CAGR}`) || !strings.Contains(eff[len(eff)-1].Target, `$\alpha^2$`) {
		t.Fatalf("effects = %+v", eff)
	}
}
