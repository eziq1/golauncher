package mathview

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Unicode returns tex, math without its delimiters, as readable Unicode
// text: (期末值 / 期初值)^(1/n) − 1 for
// \left(\frac{\text{期末值}}{\text{期初值}}\right)^{\frac{1}{n}} - 1, with
// Greek letters and operators as their characters and simple scripts as
// superscript and subscript characters. It reads the MathML treeblood makes
// of tex, or, where treeblood fails, tex itself, as best it can.
func Unicode(tex string) string {
	unicodeCache.Lock()
	s, ok := unicodeCache.m[tex]
	unicodeCache.Unlock()
	if ok {
		return s
	}
	if n, err := Parse(tex); err == nil {
		s = TreeUnicode(n)
	} else {
		s = TeXUnicode(tex)
	}
	unicodeCache.Lock()
	if len(unicodeCache.m) >= cacheSize {
		clear(unicodeCache.m)
	}
	unicodeCache.m[tex] = s
	unicodeCache.Unlock()
	return s
}

// cacheSize bounds the caches of formulas: an answer has fewer.
const cacheSize = 512

var unicodeCache = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

// TreeUnicode returns the MathML tree n as readable Unicode text.
func TreeUnicode(n *Node) string {
	var u uniWriter
	u.node(n, 0)
	return tidy(u.b.String())
}

// tidy collapses runs of spaces and trims them.
func tidy(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, " ", " ")), " ")
}

type uniWriter struct{ b strings.Builder }

// node writes n; level is how deep in scripts it is, where fractions are
// written without spaces.
func (u *uniWriter) node(n *Node, level int) {
	switch n.Tag {
	case "mi":
		u.b.WriteString(n.Text)
		u.spaceAfterOp(n)
	case "mn", "mtext", "ms":
		u.b.WriteString(n.Text)
	case "mo":
		u.op(n, level)
	case "mspace":
		if w, ok := length(n.Get("width"), 1); ok && w > 0 {
			u.b.WriteByte(' ')
		}
	case "merror":
		u.b.WriteString(errorText(n.Text))
	case "mphantom", "none", "annotation":
	case "mfrac":
		u.frac(n, level)
	case "msup", "msub", "msubsup":
		u.scripts(n, level)
	case "munder", "mover", "munderover":
		u.underOver(n, level)
	case "msqrt":
		u.b.WriteString("√")
		u.atom(row(n.Kids), level)
	case "mroot":
		idx := u.sub(n.Kids[1], level+1)
		switch idx {
		case "3":
			u.b.WriteString("∛")
		case "4":
			u.b.WriteString("∜")
		default:
			if sup, ok := mapScript(idx, superscripts); ok {
				u.b.WriteString(sup)
			} else {
				u.b.WriteString("(" + idx + ")")
			}
			u.b.WriteString("√")
		}
		u.atom(n.Kids[0], level)
	case "mtable":
		u.table(n, level)
	default:
		// mrow, mstyle, mpadded, menclose, mtd, math, semantics …
		u.row(n.Kids, level)
	}
}

// row writes the children of a row, a fraction among others in brackets
// where it would read as part of a product.
func (u *uniWriter) row(kids []*Node, level int) {
	for i, k := range kids {
		if k.Tag == "mfrac" && level == 0 && len(kids) > 1 {
			prev, next := sibling(kids, i, -1), sibling(kids, i, 1)
			if !loose(prev) || !loose(next) {
				u.b.WriteString("(" + u.sub(k, level) + ")")
				continue
			}
		}
		u.node(k, level)
		if next := sibling(kids, i, 1); next != nil && next.Tag == "mo" && opClass(next.Text) == classOpen && k.Tag != "mo" {
			// sin(x), not sin (x).
			s := strings.TrimRight(u.b.String(), " ")
			u.b.Reset()
			u.b.WriteString(s)
		}
	}
}

// spaceAfterOp writes a space after a large operator or a function's
// name, as ∑ and sin, before what they apply to.
func (u *uniWriter) spaceAfterOp(n *Node) {
	for n.Tag == "mrow" && len(n.Kids) == 1 {
		n = n.Kids[0]
	}
	switch {
	case n.Tag == "mo" && (n.Get("largeop") == "true" || opClass(n.Text) == classLarge):
	case n.Tag == "mi" && (n.Get("lspace") != "" || n.Get("movablelimits") == "true") && utf8.RuneCountInString(n.Text) > 1:
	default:
		return
	}
	u.b.WriteByte(' ')
}

// sibling returns the child of a row next to i in direction dir,
// skipping spaces, or nil.
func sibling(kids []*Node, i, dir int) *Node {
	for j := i + dir; j >= 0 && j < len(kids); j += dir {
		if kids[j].Tag != "mspace" {
			return kids[j]
		}
	}
	return nil
}

// loose reports whether a fraction next to n reads without brackets: next
// to nothing, a relation, an addition or a bracket.
func loose(n *Node) bool {
	if n == nil {
		return true
	}
	if n.Tag != "mo" {
		return false
	}
	switch opClass(n.Text) {
	case classRel, classPunct, classOpen, classClose:
		return true
	}
	return n.Text == "+" || n.Text == "−" || n.Text == "-" || n.Text == "±" || n.Text == "∓"
}

func (u *uniWriter) op(n *Node, level int) {
	t := n.Text
	if t == "" {
		return
	}
	if level > 0 {
		u.b.WriteString(t)
		return
	}
	switch opClass(t) {
	case classRel:
		u.b.WriteString(" " + t + " ")
	case classBin:
		// A sign at the start is not an operation.
		s := strings.TrimRight(u.b.String(), " ")
		if s == "" || strings.HasSuffix(s, "(") || strings.HasSuffix(s, "[") || strings.HasSuffix(s, "{") || endsWithOperator(s) {
			u.b.WriteString(t)
		} else {
			u.b.WriteString(" " + t + " ")
		}
	case classPunct:
		u.b.WriteString(t + " ")
	case classLarge:
		u.b.WriteString(t)
	default:
		u.b.WriteString(t)
	}
}

// endsWithOperator reports whether s ends with a relation or an operation,
// after which a sign is a sign.
func endsWithOperator(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	c := opClass(string(r))
	return c == classRel || c == classBin || c == classPunct
}

// sub returns n written alone.
func (u *uniWriter) sub(n *Node, level int) string {
	var w uniWriter
	w.node(n, level)
	return tidy(w.b.String())
}

// atom writes n, in brackets unless it reads as one thing.
func (u *uniWriter) atom(n *Node, level int) {
	s := u.sub(n, level)
	if isAtom(n, s) {
		u.b.WriteString(s)
	} else {
		u.b.WriteString("(" + s + ")")
	}
}

// isAtom reports whether n, written s, reads as one thing: a letter, a
// number, a word, or something in brackets.
func isAtom(n *Node, s string) bool {
	if s == "" {
		return true
	}
	if utf8.RuneCountInString(s) == 1 {
		return true
	}
	if r, k := utf8.DecodeRuneInString(s); (r == '−' || r == '-') && !strings.ContainsAny(s[k:], " ") {
		// A negative: −b.
		return isAtom(&Node{Tag: "mrow"}, s[k:])
	}
	switch n.Tag {
	case "mi", "mn", "mtext":
		return !strings.ContainsAny(s, " +−-=/")
	case "msqrt", "mroot":
		return true
	case "msup", "msub", "msubsup":
		return isAtom(n.Kids[0], "x") && !strings.ContainsAny(s, " ")
	}
	if fenced(s) {
		return true
	}
	if i := strings.IndexAny(s, "([{"); i > 0 && fenced(s[i:]) && isAtom(&Node{Tag: "mi"}, s[:i]) {
		// A function applied: f(x).
		return true
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.Is(unicode.Mn, r) {
			return false
		}
	}
	return true
}

// fenced reports whether s is wholly in one pair of brackets.
func fenced(s string) bool {
	pairs := map[rune]rune{'(': ')', '[': ']', '{': '}', '|': '|', '⟨': '⟩', '‖': '‖', '⌊': '⌋', '⌈': '⌉'}
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	closeR, ok := pairs[first]
	if !ok || last != closeR || len(s) < 2 {
		return false
	}
	// The opening bracket closes at the end, not before.
	depth := 0
	for i, r := range s {
		switch {
		case r == first && first != closeR:
			depth++
		case r == closeR:
			if first == closeR {
				if i == 0 {
					depth++
					continue
				}
				depth--
			} else {
				depth--
			}
			if depth == 0 && i+utf8.RuneLen(r) < len(s) {
				return false
			}
		}
	}
	return true
}

func (u *uniWriter) frac(n *Node, level int) {
	num, den := u.sub(n.Kids[0], level), u.sub(n.Kids[1], level)
	if !isAtom(n.Kids[0], num) {
		num = "(" + num + ")"
	}
	if !isAtom(n.Kids[1], den) {
		den = "(" + den + ")"
	}
	if lt := n.Get("linethickness"); lt == "0" || lt == "0em" || lt == "0pt" {
		// A binomial coefficient.
		u.b.WriteString(num + " " + den)
		return
	}
	if level > 0 {
		u.b.WriteString(num + "/" + den)
	} else {
		u.b.WriteString(num + " / " + den)
	}
}

func (u *uniWriter) scripts(n *Node, level int) {
	u.atom(n.Kids[0], level)
	defer u.spaceAfterOp(n.Kids[0])
	switch n.Tag {
	case "msub":
		u.script(n.Kids[1], level, false)
	case "msup":
		u.script(n.Kids[1], level, true)
	default:
		u.script(n.Kids[1], level, false)
		u.script(n.Kids[2], level, true)
	}
}

// script writes a superscript or subscript: in script characters where
// they all have one, else after ^ or _.
func (u *uniWriter) script(n *Node, level int, sup bool) {
	s := u.sub(n, level+1)
	if s == "" {
		return
	}
	if sup && (s == "′" || s == "″" || s == "‴" || s == "'") {
		u.b.WriteString(s)
		return
	}
	table, mark := subscripts, "_"
	if sup {
		table, mark = superscripts, "^"
	}
	if m, ok := mapScript(s, table); ok {
		u.b.WriteString(m)
		return
	}
	if isAtom(n, s) && utf8.RuneCountInString(s) == 1 {
		u.b.WriteString(mark + s)
		return
	}
	u.b.WriteString(mark + "(" + s + ")")
}

// mapScript returns s in the characters of table, if all of s has them.
func mapScript(s string, table map[rune]rune) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		m, ok := table[r]
		if !ok {
			return "", false
		}
		b.WriteRune(m)
	}
	return b.String(), true
}

// accentMarks are the combining marks of accents over (and under) a letter.
var accentMarks = map[string]string{
	"^": "̂", "ˆ": "̂", "̂": "̂",
	"¯": "̄", "̄": "̄", "‾": "̅",
	"→": "⃗", "⃗": "⃗",
	"~": "̃", "˜": "̃", "̃": "̃",
	"˙": "̇", "¨": "̈", "´": "́", "`": "̀", "˘": "̆", "ˇ": "̌", "˚": "̊",
	"̲": "̲", "_": "̲", "←": "⃖", "↔": "⃡",
}

func (u *uniWriter) underOver(n *Node, level int) {
	base := n.Kids[0]
	defer u.spaceAfterOp(base)
	var under, over *Node
	switch n.Tag {
	case "munder":
		under = n.Kids[1]
	case "mover":
		over = n.Kids[1]
	default:
		under, over = n.Kids[1], n.Kids[2]
	}
	// An accent: a mark on each character of the base.
	for _, acc := range []*Node{over, under} {
		if acc == nil || acc.Tag != "mo" {
			continue
		}
		if acc.Text == "⏟" || acc.Text == "⏞" || acc.Text == "⎵" || acc.Text == "⎴" || acc.Text == "⏝" || acc.Text == "⏜" {
			if acc == over {
				over = nil
			} else {
				under = nil
			}
			continue
		}
		mark, ok := accentMarks[acc.Text]
		if !ok {
			continue
		}
		s := u.sub(base, level)
		var b strings.Builder
		for _, r := range s {
			b.WriteRune(r)
			if r != ' ' {
				b.WriteString(mark)
			}
		}
		base = &Node{Tag: "mtext", Text: b.String()}
		if acc == over {
			over = nil
		} else {
			under = nil
		}
	}
	u.b.WriteString(u.sub(base, level))
	if under != nil {
		u.script(under, level, false)
	}
	if over != nil {
		u.script(over, level, true)
	}
}

func (u *uniWriter) table(n *Node, level int) {
	aligned := false
	for i, r := range n.Kids {
		if i > 0 {
			u.b.WriteString("; ")
		}
		for j, c := range r.Kids {
			if j == 0 && c.Get("columnalign") == "right" {
				aligned = true
			}
			if j > 0 {
				if aligned {
					u.b.WriteString(" ")
				} else {
					u.b.WriteString(", ")
				}
			}
			u.b.WriteString(u.sub(c, level))
		}
	}
}

// row makes a row of nodes.
func row(kids []*Node) *Node {
	if len(kids) == 1 {
		return kids[0]
	}
	return &Node{Tag: "mrow", Kids: kids}
}

// errorText is what treeblood's error for an unknown command reads as.
func errorText(name string) string {
	switch name {
	case "xrightarrow", "longrightarrow":
		return "→"
	case "xleftarrow", "longleftarrow":
		return "←"
	case "tag", "label", "nonumber", "notag", "boxed", "phantom", "hphantom", "vphantom", "displaystyle", "textstyle":
		return ""
	}
	if name == "" {
		return ""
	}
	return "\\" + name + " "
}

// Operator classes, for spacing.
const (
	classOrd = iota
	classBin
	classRel
	classPunct
	classOpen
	classClose
	classLarge
)

var opClasses = func() map[string]int {
	m := map[string]int{}
	for _, s := range strings.Fields("+ − - ± ∓ × ÷ · ⋅ ∗ ∘ ∙ ∪ ∩ ∧ ∨ ⊕ ⊗ ⊙ ⊖ ⋆ ⋄ ∖ ⊎ ⊓ ⊔ ⊲ ⊳ ≀ ⨿ ⋉ ⋊ ⋈") {
		m[s] = classBin
	}
	for _, s := range strings.Fields("= < > ≤ ≥ ≠ ≈ ≡ ∼ ≃ ≅ ∝ ∈ ∉ ∋ ⊂ ⊃ ⊆ ⊇ ⊄ ⊊ ⊋ → ← ↔ ⇒ ⇐ ⇔ ↦ ⟶ ⟵ ⟹ ⟸ ⟺ ⟼ ↑ ↓ ⇑ ⇓ ∣ ∥ ≪ ≫ ≺ ≻ ≼ ≽ ⊥ ⊢ ⊨ ≔ ≐ ≜ ≲ ≳ ≍ ⊏ ⊐ ⊑ ⊒ ⋘ ⋙ ↪ ↩ ⇀ ↽ ⇌ ≺ ∷ : ≟ ⩽ ⩾ ≦ ≧ ≮ ≯ ≰ ≱ ∤ ∦ ≉ ≢ ↗ ↘ ↙ ↖ ⟷ ⇄") {
		m[s] = classRel
	}
	for _, s := range strings.Fields(", ;") {
		m[s] = classPunct
	}
	for _, s := range strings.Fields("( [ { ⟨ ⌊ ⌈ ⟦ ⟮") {
		m[s] = classOpen
	}
	for _, s := range strings.Fields(") ] } ⟩ ⌋ ⌉ ⟧ ⟯") {
		m[s] = classClose
	}
	for _, s := range strings.Fields("∑ ∏ ∐ ∫ ∬ ∭ ∮ ∯ ∰ ⋃ ⋂ ⋁ ⋀ ⨁ ⨂ ⨀ ⨄ ⨆") {
		m[s] = classLarge
	}
	return m
}()

// opClass returns the class of an operator.
func opClass(s string) int { return opClasses[s] }

var superscripts = scriptTable(
	"0123456789+−-=()ni",
	"⁰¹²³⁴⁵⁶⁷⁸⁹⁺⁻⁻⁼⁽⁾ⁿⁱ",
	"abcdefghijklmoprstuvwxyzABDEGHIJKLMNOPRTUVW",
	"ᵃᵇᶜᵈᵉᶠᵍʰⁱʲᵏˡᵐᵒᵖʳˢᵗᵘᵛʷˣʸᶻᴬᴮᴰᴱᴳᴴᴵᴶᴷᴸᴹᴺᴼᴾᴿᵀᵁⱽᵂ",
	"αβγδεθφχ′*",
	"ᵅᵝᵞᵟᵋᶿᵠᵡ′*",
)

var subscripts = scriptTable(
	"0123456789+−-=()",
	"₀₁₂₃₄₅₆₇₈₉₊₋₋₌₍₎",
	"aehijklmnoprstuvx",
	"ₐₑₕᵢⱼₖₗₘₙₒₚᵣₛₜᵤᵥₓ",
	"βγρφχ",
	"ᵦᵧᵨᵩᵪ",
)

// scriptTable makes a table of characters from pairs of strings: each
// character of the first of a pair to that of the second at its place.
func scriptTable(pairs ...string) map[rune]rune {
	m := map[rune]rune{}
	for i := 0; i+1 < len(pairs); i += 2 {
		a, b := []rune(pairs[i]), []rune(pairs[i+1])
		for j := range a {
			m[a[j]] = b[j]
		}
	}
	return m
}
