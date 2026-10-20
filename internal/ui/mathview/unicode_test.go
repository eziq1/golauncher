package mathview

import "testing"

func TestUnicode(t *testing.T) {
	for _, tc := range []struct{ tex, want string }{
		{`\left(\frac{\text{期末值}}{\text{期初值}}\right)^{\frac{1}{n}} - 1`, `(期末值 / 期初值)^(1/n) − 1`},
		{`\text{CAGR} = \left(\frac{\text{期末值}}{\text{期初值}}\right)^{\frac{1}{n}} - 1`, `CAGR = (期末值 / 期初值)^(1/n) − 1`},
		{`\alpha + \beta \le \gamma`, `α + β ≤ γ`},
		{`\Delta x \cdot \pi \ne \Omega`, `Δx ⋅ π ≠ Ω`},
		{`x^2 + y^2 = r^2`, `x² + y² = r²`},
		{`a_{n+1} = a_n + a_{n-1}`, `aₙ₊₁ = aₙ + aₙ₋₁`},
		{`e^{-x}`, `e⁻ˣ`},
		{`x^{\frac{1}{2}}`, `x^(1/2)`},
		{`\sqrt{a^2+b^2}`, `√(a² + b²)`},
		{`\sqrt{x}`, `√x`},
		{`\sqrt[3]{x}`, `∛x`},
		{`\sum_{i=1}^{n} x_i`, `∑ᵢ₌₁ⁿ xᵢ`},
		{`\int_0^\infty e^{-x}\,dx = 1`, `∫₀^∞ e⁻ˣ dx = 1`},
		{`\lim_{x \to 0} \frac{\sin x}{x} = 1`, `lim_(x→0) ((sin x) / x) = 1`},
		{`\frac{a+b}{c}`, `(a + b) / c`},
		{`\begin{pmatrix} 1 & 2 \\ 3 & 4 \end{pmatrix}`, `(1, 2; 3, 4)`},
		{`\begin{bmatrix} a & b \\ c & d \end{bmatrix}`, `[a, b; c, d]`},
		{`f(x) = \begin{cases} 1 & x > 0 \\ 0 & \text{otherwise} \end{cases}`, `f(x) = {1, x > 0; 0, otherwise`},
		{`\hat{x} + \bar{y}`, "x̂ + ȳ"},
		{`\vec{v}`, "v⃗"},
		{`\mathbb{R}^n`, `ℝⁿ`},
		{`\binom{n}{k}`, `(n k)`},
		{`\operatorname{tr}(A)`, `tr(A)`},
		{`\left| x \right|`, `|x|`},
		{`\sin(x) + \log_2 n`, `sin(x) + log₂ n`},
		{`\boxed{x}`, `x`},
		{`\foo{x} + 1`, `\foo x + 1`},
		{`P(A \mid B) = \frac{P(B \mid A) P(A)}{P(B)}`, `P(A ∣ B) = (P(B ∣ A)P(A)) / P(B)`},
	} {
		if got := Unicode(tc.tex); got != tc.want {
			t.Errorf("Unicode(%q)\n got %q\nwant %q", tc.tex, got, tc.want)
		}
	}
}

func TestTeXUnicode(t *testing.T) {
	for _, tc := range []struct{ tex, want string }{
		{`\left(\frac{\text{期末值}}{\text{期初值}}\right)^{\frac{1}{n}} - 1`, `(期末值/期初值)^(1/n) − 1`},
		{`\alpha^2 + \beta_1`, `α² + β₁`},
		{`\frac{a+b}{c} \le \sqrt{x}`, `(a + b)/c ≤ √x`},
		{`\sin x \cdot \pi`, `sin x ⋅ π`},
		{`\frac{1}{2`, `1/2`},
		{`\unknown{x}`, `\unknownx`},
	} {
		if got := TeXUnicode(tc.tex); got != tc.want {
			t.Errorf("TeXUnicode(%q)\n got %q\nwant %q", tc.tex, got, tc.want)
		}
	}
}
