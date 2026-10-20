package mathview

import (
	"strings"
	"unicode/utf8"
)

// TeXUnicode returns tex as readable Unicode text without treeblood, for
// TeX it cannot read: commands as their characters from a small table,
// \frac, \sqrt and scripts as Unicode writes them (TreeUnicode), braces
// dropped and unknown commands as written. It is a best effort.
func TeXUnicode(tex string) string {
	p := texReader{s: tex}
	return tidy(p.until(0))
}

type texReader struct {
	s string
	i int
}

// until reads to the end, or to the closing brace of a group when close is
// '}'.
func (p *texReader) until(close byte) string {
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == close:
			p.i++
			return b.String()
		case c == '{':
			p.i++
			b.WriteString(p.until('}'))
		case c == '}':
			// Unbalanced: ignore.
			p.i++
		case c == '^' || c == '_':
			p.i++
			arg := p.arg()
			table, mark := subscripts, "_"
			if c == '^' {
				table, mark = superscripts, "^"
			}
			if m, ok := mapScript(arg, table); ok {
				b.WriteString(m)
			} else if utf8.RuneCountInString(arg) == 1 || fenced(arg) {
				b.WriteString(mark + arg)
			} else {
				b.WriteString(mark + "(" + arg + ")")
			}
		case c == '\\':
			b.WriteString(p.command())
		case c == '&':
			p.i++
			b.WriteByte(' ')
		case c == '~':
			p.i++
			b.WriteByte(' ')
		case c == '-':
			p.i++
			b.WriteString("−")
		default:
			r, n := utf8.DecodeRuneInString(p.s[p.i:])
			p.i += n
			if opClass(string(r)) == classRel || r == '+' {
				b.WriteString(" " + string(r) + " ")
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// arg reads the argument of a command or a script: a group, a command or
// a character.
func (p *texReader) arg() string {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
	if p.i >= len(p.s) {
		return ""
	}
	switch p.s[p.i] {
	case '{':
		p.i++
		return tidy(p.until('}'))
	case '\\':
		return strings.TrimSpace(p.command())
	}
	r, n := utf8.DecodeRuneInString(p.s[p.i:])
	p.i += n
	if r == '-' {
		return "−"
	}
	return string(r)
}

// optional reads an optional argument in brackets, if any.
func (p *texReader) optional() string {
	if p.i < len(p.s) && p.s[p.i] == '[' {
		j := strings.IndexByte(p.s[p.i:], ']')
		if j > 0 {
			o := TeXUnicode(p.s[p.i+1 : p.i+j])
			p.i += j + 1
			return o
		}
	}
	return ""
}

// command reads a command at a backslash.
func (p *texReader) command() string {
	p.i++ // the backslash
	if p.i >= len(p.s) {
		return ""
	}
	start := p.i
	for p.i < len(p.s) && isASCIILetter(p.s[p.i]) {
		p.i++
	}
	name := p.s[start:p.i]
	if name == "" {
		// A symbol: \{ \, \\ …
		c := p.s[p.i]
		p.i++
		switch c {
		case ',', ':', ';', ' ', '>':
			return " "
		case '!':
			return ""
		case '\\':
			return "; "
		case '{', '}', '%', '$', '#', '&', '_', '|':
			if c == '|' {
				return "‖"
			}
			return string(c)
		}
		return string(c)
	}
	switch name {
	case "frac", "dfrac", "tfrac", "cfrac":
		num, den := p.arg(), p.arg()
		if !simple(num) {
			num = "(" + num + ")"
		}
		if !simple(den) {
			den = "(" + den + ")"
		}
		return num + "/" + den
	case "binom", "dbinom", "tbinom":
		return "C(" + p.arg() + ", " + p.arg() + ")"
	case "sqrt":
		idx := p.optional()
		arg := p.arg()
		if !simple(arg) {
			arg = "(" + arg + ")"
		}
		switch idx {
		case "":
			return "√" + arg
		case "3":
			return "∛" + arg
		case "4":
			return "∜" + arg
		}
		return "(" + idx + ")√" + arg
	case "text", "mathrm", "textrm", "mathit", "textit", "mathbf", "textbf", "mathsf", "texttt", "mathtt", "operatorname", "boldsymbol", "bm", "mbox", "hbox", "emph", "mathcal", "mathscr", "mathfrak":
		return p.arg()
	case "mathbb":
		a := p.arg()
		if m, ok := doubleStruck[a]; ok {
			return m
		}
		return a
	case "left", "right", "big", "Big", "bigg", "Bigg", "bigl", "bigr", "Bigl", "Bigr", "biggl", "biggr", "Biggl", "Biggr", "middle":
		for p.i < len(p.s) && p.s[p.i] == ' ' {
			p.i++
		}
		if p.i < len(p.s) && p.s[p.i] == '.' {
			p.i++
			return ""
		}
		return ""
	case "begin", "end":
		env := p.arg()
		if name == "begin" {
			switch env {
			case "pmatrix":
				return "("
			case "bmatrix":
				return "["
			case "vmatrix":
				return "|"
			case "cases":
				return "{"
			case "array":
				p.arg() // the columns
			}
			return ""
		}
		switch env {
		case "pmatrix":
			return ")"
		case "bmatrix":
			return "]"
		case "vmatrix":
			return "|"
		}
		return ""
	case "hat", "bar", "vec", "tilde", "dot", "ddot", "overline", "underline", "widehat", "widetilde", "overrightarrow", "acute", "grave", "breve", "check":
		a := p.arg()
		mark := map[string]string{"hat": "̂", "widehat": "̂", "bar": "̄", "overline": "̅", "underline": "̲", "vec": "⃗", "overrightarrow": "⃗", "tilde": "̃", "widetilde": "̃", "dot": "̇", "ddot": "̈", "acute": "́", "grave": "̀", "breve": "̆", "check": "̌"}[name]
		var b strings.Builder
		for _, r := range a {
			b.WriteRune(r)
			if r != ' ' {
				b.WriteString(mark)
			}
		}
		return b.String()
	case "quad", "qquad", "enspace", "thinspace":
		return " "
	case "displaystyle", "textstyle", "scriptstyle", "limits", "nolimits", "nonumber", "notag":
		return ""
	case "tag", "label":
		p.arg()
		return ""
	}
	if s, ok := texSymbols[name]; ok {
		switch opClass(s) {
		case classRel, classBin:
			return " " + s + " "
		}
		if functionNames[name] {
			return s + " "
		}
		return s
	}
	return "\\" + name
}

// simple reports whether s reads as one thing without brackets.
func simple(s string) bool {
	if utf8.RuneCountInString(s) <= 1 || fenced(s) {
		return true
	}
	return !strings.ContainsAny(s, " +−-=/,")
}

func isASCIILetter(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }

var doubleStruck = map[string]string{"R": "ℝ", "N": "ℕ", "Z": "ℤ", "Q": "ℚ", "C": "ℂ", "P": "ℙ", "E": "𝔼"}

// functionNames are the commands written as words, as \sin.
var functionNames = map[string]bool{}

// texSymbols are the characters of TeX's commands that answers use.
var texSymbols = func() map[string]string {
	m := map[string]string{
		"alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "epsilon": "ϵ", "varepsilon": "ε", "zeta": "ζ", "eta": "η",
		"theta": "θ", "vartheta": "ϑ", "iota": "ι", "kappa": "κ", "lambda": "λ", "mu": "μ", "nu": "ν", "xi": "ξ", "pi": "π",
		"varpi": "ϖ", "rho": "ρ", "varrho": "ϱ", "sigma": "σ", "varsigma": "ς", "tau": "τ", "upsilon": "υ", "phi": "ϕ",
		"varphi": "φ", "chi": "χ", "psi": "ψ", "omega": "ω",
		"Gamma": "Γ", "Delta": "Δ", "Theta": "Θ", "Lambda": "Λ", "Xi": "Ξ", "Pi": "Π", "Sigma": "Σ", "Upsilon": "Υ",
		"Phi": "Φ", "Psi": "Ψ", "Omega": "Ω",
		"times": "×", "cdot": "⋅", "div": "÷", "pm": "±", "mp": "∓", "ast": "∗", "star": "⋆", "circ": "∘", "bullet": "∙",
		"cup": "∪", "cap": "∩", "wedge": "∧", "land": "∧", "vee": "∨", "lor": "∨", "oplus": "⊕", "otimes": "⊗", "setminus": "∖",
		"le": "≤", "leq": "≤", "ge": "≥", "geq": "≥", "ne": "≠", "neq": "≠", "approx": "≈", "equiv": "≡", "sim": "∼",
		"simeq": "≃", "cong": "≅", "propto": "∝", "in": "∈", "notin": "∉", "ni": "∋", "subset": "⊂", "supset": "⊃",
		"subseteq": "⊆", "supseteq": "⊇", "to": "→", "rightarrow": "→", "leftarrow": "←", "gets": "←", "leftrightarrow": "↔",
		"Rightarrow": "⇒", "Leftarrow": "⇐", "Leftrightarrow": "⇔", "implies": "⇒", "iff": "⇔", "mapsto": "↦",
		"longrightarrow": "⟶", "longleftarrow": "⟵", "Longrightarrow": "⟹", "uparrow": "↑", "downarrow": "↓",
		"mid": "∣", "parallel": "∥", "ll": "≪", "gg": "≫", "perp": "⊥", "vdash": "⊢", "models": "⊨",
		"infty": "∞", "partial": "∂", "nabla": "∇", "forall": "∀", "exists": "∃", "nexists": "∄", "emptyset": "∅",
		"varnothing": "∅", "neg": "¬", "lnot": "¬", "angle": "∠", "triangle": "△", "degree": "°", "prime": "′",
		"hbar": "ℏ", "ell": "ℓ", "Re": "ℜ", "Im": "ℑ", "aleph": "ℵ", "wp": "℘",
		"sum": "∑", "prod": "∏", "coprod": "∐", "int": "∫", "iint": "∬", "iiint": "∭", "oint": "∮",
		"bigcup": "⋃", "bigcap": "⋂", "bigoplus": "⨁", "bigotimes": "⨂", "bigvee": "⋁", "bigwedge": "⋀",
		"cdots": "⋯", "ldots": "…", "dots": "…", "vdots": "⋮", "ddots": "⋱", "therefore": "∴", "because": "∵",
		"langle": "⟨", "rangle": "⟩", "lfloor": "⌊", "rfloor": "⌋", "lceil": "⌈", "rceil": "⌉", "vert": "|", "Vert": "‖",
		"lbrace": "{", "rbrace": "}", "lvert": "|", "rvert": "|", "backslash": "∖",
	}
	for _, f := range strings.Fields("sin cos tan cot sec csc arcsin arccos arctan sinh cosh tanh log ln lg exp lim limsup liminf max min sup inf det dim ker deg gcd arg Pr mod") {
		m[f] = f
		functionNames[f] = true
	}
	return m
}()
