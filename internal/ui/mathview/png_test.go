package mathview

import (
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// sampleFormulas are formulas as answers write them.
var sampleFormulas = []string{
	`\text{CAGR} = \left(\frac{\text{期末值}}{\text{期初值}}\right)^{\frac{1}{n}} - 1`,
	`x = \frac{-b \pm \sqrt{b^2 - 4ac}}{2a}`,
	`\sum_{i=1}^{n} i^2 = \frac{n(n+1)(2n+1)}{6}`,
	`\int_0^\infty e^{-x^2}\,dx = \frac{\sqrt{\pi}}{2}`,
	`\lim_{x \to 0} \frac{\sin x}{x} = 1, \quad \hat{\theta} = \bar{x} + \vec{v}`,
	`A = \begin{pmatrix} 1 & 2 \\ 3 & 4 \end{pmatrix}, \quad B = \begin{bmatrix} a & b \\ c & d \end{bmatrix}`,
	`f(x) = \begin{cases} x^2 & x \ge 0 \\ -x & \text{otherwise} \end{cases}`,
	`\begin{aligned} (a+b)^2 &= a^2 + 2ab + b^2 \\ &\le 2(a^2 + b^2) \end{aligned}`,
	`\sqrt[3]{x^3 + y_1^2} \neq \overline{AB} + \left| \frac{a}{b} \right|`,
	`\frac{\frac{1}{x}+\frac{1}{y}}{x_{i+1}^{2}} \cdot \binom{n}{k} \left\{ \frac{a}{b} \right\}`,
}

// TestRenderPNG draws the sample formulas to the PNG file MATHVIEW_PNG
// names, to look at the layout; it does nothing without it.
func TestRenderPNG(t *testing.T) {
	out := os.Getenv("MATHVIEW_PNG")
	if out == "" {
		t.Skip("MATHVIEW_PNG not set")
	}
	md := &Markdown{}
	md.Options.Extensions = parser.Default | parser.ExtDollarMath
	view := func(c *ui.Context) {
		c.Root().Background(c.Theme().Background)
		ui.Column(c).Fill().Padding(16).Gap(8).Children(func() {
			md.View(c, "Inline: area $\\pi r^2$, growth \\(\\frac{a}{b}\\), and $x_i^2$ in **bold $\\alpha$**.\n\n$$\\text{CAGR} = \\left(\\frac{\\text{期末值}}{\\text{期初值}}\\right)^{\\frac{1}{n}} - 1$$")
			for _, tex := range sampleFormulas {
				Display(c, tex, &Options{Size: 18})
			}
		})
	}
	tt := ui.NewTester(view, 900, 1250)
	tt.SetScale(2)
	tt.Frame()
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}
