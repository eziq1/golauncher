package mathview

import (
	"math"
	"testing"
	"unicode"

	"github.com/egoist/mygo/ui"
)

// fakeMeasurer measures each character half an em wide, a CJK one a whole
// em: deterministic, without a text system.
type fakeMeasurer struct{}

func (fakeMeasurer) Measure(s string, f ui.Font) (float32, []ui.Glyph) {
	w := float32(0)
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			w += f.Size
		} else {
			w += f.Size / 2
		}
	}
	return w, nil
}

const size = 20

func lay(t *testing.T, tex string, display bool) *Box {
	t.Helper()
	n, err := Parse(tex)
	if err != nil {
		t.Fatalf("Parse(%q): %v", tex, err)
	}
	if err := Check(n); err != nil {
		t.Fatalf("Check(%q): %v", tex, err)
	}
	return Layout(n, Style{Size: size, Display: display}, fakeMeasurer{})
}

func run(t *testing.T, b *Box, text string) TextRun {
	t.Helper()
	for _, r := range b.Texts() {
		if r.Text == text {
			return r
		}
	}
	t.Fatalf("no run %q in %+v", text, b.Texts())
	return TextRun{}
}

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 0.01 }

func TestLayoutFraction(t *testing.T) {
	b := lay(t, `\frac{a}{b}`, true)
	num, den := run(t, b, "a"), run(t, b, "b")
	if num.Y >= 0 || den.Y <= 0 {
		t.Fatalf("numerator at %v, denominator at %v", num.Y, den.Y)
	}
	// Display style: the parts keep the size, at least the display shifts.
	if num.Size != size || den.Size != size {
		t.Fatalf("sizes %v %v", num.Size, den.Size)
	}
	if -num.Y < fracNumDisplayShiftUp*size-0.01 || den.Y < fracDenDisplayShift*size-0.01 {
		t.Fatalf("shifts %v %v", -num.Y, den.Y)
	}
	rules := b.Rules()
	if len(rules) != 1 {
		t.Fatalf("rules %v", rules)
	}
	// The rule is centered on the math axis.
	if r := rules[0]; !near(r[1]+r[3]/2, -axisHeight*size) {
		t.Fatalf("rule at %v", r)
	}
	// An italic letter, and the padding on each side.
	if want := float32(size/2 + 0.03*size + 2*fracPad*size); !near(b.Width, want) {
		t.Fatalf("width %v, want %v", b.Width, want)
	}
	if b.Ascent <= -num.Y || b.Descent < den.Y {
		t.Fatalf("box %v/%v does not hold its parts", b.Ascent, b.Descent)
	}
}

func TestLayoutNestedFractionShrinks(t *testing.T) {
	b := lay(t, `\frac{\frac{1}{2}}{3}`, true)
	if got := run(t, b, "3").Size; got != size {
		t.Fatalf("outer denominator %v", got)
	}
	// The inner fraction is in text style: its parts are scripts.
	if got := run(t, b, "1").Size; !near(got, size*0.71) {
		t.Fatalf("inner numerator %v", got)
	}
	b = lay(t, `\frac{\frac{1}{2}}{3}`, false)
	if got := run(t, b, "1").Size; !near(got, size*0.5) {
		t.Fatalf("inner numerator in text style %v", got)
	}
}

func TestLayoutScripts(t *testing.T) {
	b := lay(t, `x^2`, true)
	x, two := run(t, b, "x"), run(t, b, "2")
	if !near(two.Size, size*0.71) {
		t.Fatalf("script size %v", two.Size)
	}
	if !near(-two.Y, supShiftUp*size) || two.X < x.X+size/2 {
		t.Fatalf("superscript at %v,%v", two.X, two.Y)
	}
	b = lay(t, `x_i`, true)
	if i := run(t, b, "i"); !near(i.Y, subShiftDown*size) {
		t.Fatalf("subscript at %v", i.Y)
	}
	b = lay(t, `x_i^2`, true)
	i, two := run(t, b, "i"), run(t, b, "2")
	// The gap between them is SubSuperscriptGapMin at least.
	iA, _ := ink("i")
	_, twoD := ink("2")
	supBottom := two.Y + twoD*two.Size // y grows down
	subTop := i.Y - iA*i.Size
	if subTop-supBottom < subSupGapMin*size-0.01 {
		t.Fatalf("scripts too close: sup bottom %v, sub top %v", supBottom, subTop)
	}
	if two.Y >= 0 || i.Y <= 0 || !near(two.X, i.X+0.06*size) {
		t.Fatalf("scripts at %+v %+v", two, i)
	}
}

func TestLayoutSqrt(t *testing.T) {
	small := lay(t, `\sqrt{x}`, true)
	x := run(t, small, "x")
	if small.Paths() == 0 || x.X <= 0 {
		t.Fatalf("no radical sign: %d paths, x at %v", small.Paths(), x.X)
	}
	if small.Ascent <= 0.45*size {
		t.Fatalf("ascent %v does not clear the radicand", small.Ascent)
	}
	tall := lay(t, `\sqrt{\frac{a}{b}}`, true)
	// The sign grows with the radicand.
	if tall.Ascent+tall.Descent <= small.Ascent+small.Descent || run(t, tall, "a").X <= x.X {
		t.Fatalf("tall radical %v/%v", tall.Ascent, tall.Descent)
	}
	root := lay(t, `\sqrt[3]{x}`, true)
	if three := run(t, root, "3"); !near(three.Size, size*0.5) || three.Y >= 0 {
		t.Fatalf("index %+v", three)
	}
}

func TestLayoutFences(t *testing.T) {
	// Around a fraction, fences are drawn to its height.
	b := lay(t, `\left(\frac{a}{b}\right)`, true)
	for _, r := range b.Texts() {
		if r.Text == "(" || r.Text == ")" {
			t.Fatalf("fence set from the font: %+v", b.Texts())
		}
	}
	if b.Paths() != 2 {
		t.Fatalf("%d paths", b.Paths())
	}
	frac := lay(t, `\frac{a}{b}`, true)
	if b.Ascent < frac.Ascent || b.Descent < frac.Descent {
		t.Fatalf("fences %v/%v shorter than the fraction %v/%v", b.Ascent, b.Descent, frac.Ascent, frac.Descent)
	}
	// Around a letter, they are the font's.
	b = lay(t, `\left( x \right)`, true)
	run(t, b, "(")
	run(t, b, ")")
	// Matrices' fences stretch too.
	if b := lay(t, `\begin{pmatrix} 1 & 2 \\ 3 & 4 \end{pmatrix}`, true); b.Paths() != 2 {
		t.Fatalf("matrix fences: %d paths", b.Paths())
	}
}

func TestLayoutTableOnAxis(t *testing.T) {
	b := lay(t, `\begin{matrix} 1 & 2 \\ 3 & 4 \end{matrix}`, true)
	if !near(b.Ascent-b.Descent, 2*axisHeight*size) {
		t.Fatalf("table %v/%v not centered on the axis", b.Ascent, b.Descent)
	}
	one, two, three := run(t, b, "1"), run(t, b, "2"), run(t, b, "3")
	if one.Y != two.Y || three.Y <= one.Y || one.X != three.X || two.X <= one.X {
		t.Fatalf("cells %+v %+v %+v", one, two, three)
	}
}

func TestLayoutLimits(t *testing.T) {
	b := lay(t, `\sum_{i=1}^{n} x`, true)
	sum, n, i := run(t, b, "∑"), run(t, b, "n"), run(t, b, "i")
	if !near(sum.Size, size*1.45) || n.Y >= sum.Y || i.Y <= sum.Y {
		t.Fatalf("limits: sum %+v over %+v under %+v", sum, n, i)
	}
	// In text style, they move to the side.
	b = lay(t, `\sum_{i=1}^{n} x`, false)
	sum, n = run(t, b, "∑"), run(t, b, "n")
	if n.X <= sum.X {
		t.Fatalf("text-style limits not beside: %+v %+v", sum, n)
	}
}

func TestLayoutSpacing(t *testing.T) {
	b := lay(t, `a+b`, true)
	a, plus, bb := run(t, b, "a"), run(t, b, "+"), run(t, b, "b")
	medium := float32(4.0 / 18 * size)
	if !near(plus.X-(a.X+size/2+0.03*size), medium) || !near(bb.X-(plus.X+size/2), medium) {
		t.Fatalf("a %v + %v b %v", a.X, plus.X, bb.X)
	}
	// A sign is not spaced.
	b = lay(t, `-b`, true)
	if got := run(t, b, "b").X; !near(got, size/2) {
		t.Fatalf("b after a sign at %v", got)
	}
}

func TestLayoutAccents(t *testing.T) {
	b := lay(t, `\hat{x} \overline{AB}`, true)
	if b.Paths() == 0 || len(b.Rules()) == 0 {
		t.Fatalf("accents not drawn: %d paths, rules %v", b.Paths(), b.Rules())
	}
}

func TestPrepareFallsBackWhole(t *testing.T) {
	f := Prepare(`\frac{1}{2} + \boxed{x}`, Style{Size: size}, fakeMeasurer{})
	if f.Box != nil || f.Err == nil {
		t.Fatalf("laid out half: %+v", f)
	}
	if f.Text != "1 / 2 + x" {
		t.Fatalf("text %q", f.Text)
	}
	f = Prepare(`\frac{1}{2`, Style{Size: size}, fakeMeasurer{})
	if f.Box != nil || f.Text == "" {
		t.Fatalf("unbalanced: %+v", f)
	}
	f = Prepare(`\frac{1}{2}`, Style{Size: size}, fakeMeasurer{})
	if f.Box == nil || f.Text != "1 / 2" {
		t.Fatalf("%+v", f)
	}
}
