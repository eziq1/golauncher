package mathview

import (
	"errors"
	"strings"
	"testing"
)

// dump writes a tree compactly: tag"text"(children).
func dump(n *Node) string {
	var b strings.Builder
	var walk func(n *Node)
	walk = func(n *Node) {
		b.WriteString(n.Tag)
		if n.Text != "" {
			b.WriteString(`"` + n.Text + `"`)
		}
		if len(n.Kids) > 0 {
			b.WriteString("(")
			for i, k := range n.Kids {
				if i > 0 {
					b.WriteString(" ")
				}
				walk(k)
			}
			b.WriteString(")")
		}
	}
	walk(n)
	return b.String()
}

func TestParse(t *testing.T) {
	for _, tc := range []struct{ tex, want string }{
		{`\text{CAGR} = \left(\frac{\text{期末值}}{\text{期初值}}\right)^{\frac{1}{n}} - 1`,
			`mrow(mtext"CAGR" mo"=" msup(mrow(mo"(" mfrac(mtext"期末值" mtext"期初值") mo")") mfrac(mn"1" mi"n")) mo"−" mn"1")`},
		{`x^2`, `mrow(msup(mi"x" mn"2"))`},
		{`\sqrt[3]{x}`, `mrow(mroot(mi"x" mn"3"))`},
		{`\sum_{i=1}^{n} i`, `mrow(mrow(munderover(mo"∑" mrow(mi"i" mo"=" mn"1") mi"n")) mi"i")`},
		{`\begin{pmatrix} 1 & 2 \\ 3 & 4 \end{pmatrix}`,
			`mrow(mo"(" mtable(mtr(mtd(mn"1") mtd(mn"2")) mtr(mtd(mn"3") mtd(mn"4"))) mo")")`},
		{`a < b \text{if }`, `mrow(mi"a" mo"<" mi"b" mtext"if` + " " + `")`},
		{`\overbrace{a}^{n}`, `mrow(mrow(mover(mover(mi"a" mo"⏞") mi"n")))`},
		{`\begin{aligned} a &= b \\ &< c \end{aligned}`,
			`mrow(mtable(mtr(mtd(mi"a") mtd(mo"=" mi"b")) mtr(mtd mtd(mo"<" mi"c"))))`},
		{`\operatorname{tr} A`, `mrow(mpadded(mi"t" mi"r") mi"A")`},
	} {
		n, err := Parse(tc.tex)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.tex, err)
			continue
		}
		if got := dump(n); got != tc.want {
			t.Errorf("Parse(%q)\n got %s\nwant %s", tc.tex, got, tc.want)
		}
		if err := Check(n); err != nil {
			t.Errorf("Check(%q): %v", tc.tex, err)
		}
	}
}

func TestCheckUnsupported(t *testing.T) {
	for _, tex := range []string{`\boxed{x}`, `\foo + 1`, `\tag{1} x`} {
		n, err := Parse(tex)
		if err != nil {
			continue // unsupported all the same
		}
		if err := Check(n); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Check(%q) = %v, want ErrUnsupported", tex, err)
		}
	}
}

func TestParseMathMLAmpersandInAnnotation(t *testing.T) {
	n, err := ParseMathML(`<math><semantics><mrow><mi>a</mi><mo>&lt;</mo></mrow><annotation encoding="application/x-tex">a &&lt; b</annotation></semantics></math>`)
	if err != nil {
		t.Fatal(err)
	}
	if got := dump(content(n)); got != `mrow(mi"a" mo"<")` {
		t.Fatalf("got %s", got)
	}
}
