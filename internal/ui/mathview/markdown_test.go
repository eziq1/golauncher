package mathview

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func inline(src string) (string, []Replaced) {
	d := parser.New(parser.Default | parser.ExtDollarMath)
	d.Set(src)
	return Inline(d)
}

// shown strips the markers and the escapes, as the view shows the text.
func shown(s string) string {
	s = strings.NewReplacer(string(markStart), "", string(markEnd), "").Replace(s)
	return strings.NewReplacer(`\(`, "(", `\)`, ")", `\/`, "/", `\^`, "^", `\_`, "_", `\-`, "-").Replace(s)
}

func TestInline(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{`Area is $\pi r^2$ here.`, `Area is πr² here.`},
		{`Growth \(\frac{a}{b}\) and $x_i$.`, `Growth a / b and xᵢ.`},
		{"Code `$x^2$` stays, as does \\$5 and $5.", "Code `$x^2$` stays, as does \\$5 and $5."},
		{`No math here.`, `No math here.`},
		{"- item $\\alpha$\n\n| a | $\\beta$ |\n|---|---|\n| 1 | 2 |", "- item α\n\n| a | β |\n|---|---|\n| 1 | 2 |"},
	} {
		out, _ := inline(tc.src)
		if got := shown(out); got != tc.want {
			t.Errorf("Inline(%q)\n got %q\nwant %q", tc.src, got, tc.want)
		}
	}
	out, repl := inline(`Area $\pi r^2$, $\frac{1}{n}$.`)
	if len(repl) != 2 || repl[0].TeX != `$\pi r^2$` || repl[1].Text != "1 / n" {
		t.Fatalf("replaced %+v", repl)
	}
	// Punctuation is escaped, so that Markdown shows it as it is.
	if !strings.Contains(out, `1 \/ n`) {
		t.Fatalf("not escaped: %q", out)
	}
	if got := Restore(shown2(out), repl); got != `Area $\pi r^2$, $\frac{1}{n}$.` {
		t.Fatalf("Restore = %q", got)
	}
}

// shown2 strips the escapes but keeps the markers, as the view's text has
// them.
func shown2(s string) string { return strings.ReplaceAll(s, `\/`, "/") }

func TestRestorePartial(t *testing.T) {
	repl := []Replaced{{TeX: `$x^2$`, Text: "x²"}, {TeX: `$\alpha$`, Text: "α"}}
	m := func(s string) string { return string(markStart) + s + string(markEnd) }
	for _, tc := range []struct{ text, want string }{
		{"a " + m("x²") + " b " + m("α"), `a $x^2$ b $\alpha$`},
		{"²" + string(markEnd) + " b", `$x^2$ b`},   // starts within math
		{"b " + string(markStart) + "x", `b $x^2$`}, // ends within math
		{"no math", "no math"},
	} {
		if got := Restore(tc.text, repl); got != tc.want {
			t.Errorf("Restore(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func clipText(d transfer.Data, f transfer.Format) string {
	b, _ := d.Read(f)
	return string(b)
}

func TestMarkdownCopiesTeX(t *testing.T) {
	src := "Growth is $\\frac{a}{b}$ a year:\n\n$$x = \\frac{-b}{2a}$$\n\nDone."
	md := &Markdown{}
	md.Options.Extensions = parser.Default | parser.ExtDollarMath
	view := func(c *ui.Context) {
		ui.Column(c).Fill().Padding(8).Children(func() { md.View(c, src) })
	}
	tt := ui.NewTester(view, 600, 400)
	// Inline math shows as Unicode text, display math laid out.
	if tt.HasText(`\frac{a}{b}$ a year`) || !tt.HasText("a / b") {
		t.Fatalf("inline math not replaced: %q", tt.Texts())
	}
	if _, ok := tt.Find("x = −b / 2a"); !ok {
		t.Fatalf("no laid-out formula labeled with its text: %q", tt.Texts())
	}
	for _, s := range tt.Texts() {
		if strings.Contains(s, `\frac`) && !strings.HasPrefix(s, "$$") {
			t.Fatalf("raw TeX shows: %q", s)
		}
	}
	if err := tt.Click("Done."); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	got := clipText(tt.ClipboardData(), transfer.Text)
	for _, want := range []string{`$\frac{a}{b}$`, `$$x = \frac{-b}{2a}$$`, "Done."} {
		if !strings.Contains(got, want) {
			t.Fatalf("copied %q, want it to hold %q", got, want)
		}
	}
	if strings.ContainsRune(got, markStart) || strings.Contains(got, "a / b") {
		t.Fatalf("copied the shown text: %q", got)
	}
}

func TestMarkdownWithoutInlineMathCopiesAsMarkdownDoes(t *testing.T) {
	src := "Before\n\n$$\\sqrt{2}$$\n\nAfter"
	md := &Markdown{}
	view := func(c *ui.Context) {
		ui.Column(c).Fill().Padding(8).Children(func() { md.View(c, src) })
	}
	tt := ui.NewTester(view, 600, 400)
	if err := tt.Click("After"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	got := clipText(tt.ClipboardData(), transfer.Text)
	if !strings.Contains(got, "Before") || !strings.Contains(got, `$$\sqrt{2}$$`) || !strings.Contains(got, "After") {
		t.Fatalf("copied %q", got)
	}
}

// BenchmarkInlineStreaming measures the replacement as an answer of 40
// paragraphs, each with math, grows by a few characters a frame.
func BenchmarkInlineStreaming(b *testing.B) {
	var sb strings.Builder
	for i := range 40 {
		sb.WriteString("Paragraph with $\\alpha_" + string(rune('a'+i%26)) + "^2 + \\frac{1}{n}$ and some more words to read, and `$code$` too.\n\n")
	}
	full := sb.String()
	md := &Markdown{}
	md.Options.Extensions = parser.Default | parser.ExtDollarMath
	b.ResetTimer()
	n := 0
	for b.Loop() {
		n = (n + 7) % len(full)
		md.inline(full[:n+1])
	}
}

// BenchmarkInlineUnchanged measures a frame whose answer did not change.
func BenchmarkInlineUnchanged(b *testing.B) {
	src := strings.Repeat("Text $x^2$ and $\\beta$.\n\n", 40)
	md := &Markdown{}
	md.Options.Extensions = parser.Default | parser.ExtDollarMath
	md.inline(src)
	for b.Loop() {
		md.inline(src)
	}
}
