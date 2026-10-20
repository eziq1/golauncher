package mathview

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// The layout follows MathML Core's (https://www.w3.org/TR/mathml-core/):
// each element lays out as a box of a width, an ascent above its baseline
// and a descent below it, from its children's boxes, with the constants of
// an OpenType MATH table (those of Latin Modern Math, in ems) standing in
// for the system font's, which has none. A box holds what it paints —
// glyph runs, rules and paths — placed relative to its origin: its left
// end, on its baseline, y growing down.

// Measurer measures text for the layout, in DIPs.
type Measurer interface {
	// Measure returns the width of s in font f, and the glyphs that
	// draw it, which the layout hands back to Painter.Glyphs; a measurer
	// for tests may return none.
	Measure(s string, f ui.Font) (width float32, glyphs []ui.Glyph)
}

// shaper is the Measurer of MyGo's text system.
type shaper struct{}

func (shaper) Measure(s string, f ui.Font) (float32, []ui.Glyph) {
	g := ui.Shape(s, f)
	w := float32(0)
	for _, x := range g {
		w = max(w, x.X+x.Advance)
	}
	return w, g
}

// Box is laid-out math: Width wide, Ascent above its baseline and Descent
// below it, in DIPs.
type Box struct {
	Width, Ascent, Descent float32
	items                  []item
	// tok tells a box of a token element, a single glyph run: scripts
	// do not drop below its ink.
	tok bool
	// italic tells a slanted letter, after which a superscript moves right.
	italic bool
	// op is the operator a box of an mo is, for spacing.
	op *opBox
	// fn tells a function's name, as sin and lim, with its scripts.
	fn bool
}

type opBox struct {
	text                 string
	class                int
	lspace               float32
	rspace               float32
	explicitL, explicitR bool
	largeop              bool
	movable              bool
	stretch              bool // stretches vertically to its row
}

type itemKind uint8

const (
	itemText   itemKind = iota // glyphs on a baseline at x, y
	itemRect                   // a filled rectangle
	itemStroke                 // a stroked path
	itemFill                   // a filled path
)

type item struct {
	kind   itemKind
	x, y   float32
	w, h   float32
	text   string
	font   ui.Font
	glyphs []ui.Glyph
	path   []pathSeg
	width  float32
	color  ui.Color // zero: the formula's color
}

type pathSeg struct {
	op  byte // 'M', 'L', 'Q', 'C', 'Z'
	pts [3][2]float32
}

// place adds the items of b to a, its origin at (dx, dy) of a's.
func (a *Box) place(b *Box, dx, dy float32) {
	for _, it := range b.items {
		it.x += dx
		it.y += dy
		if it.path != nil {
			p := make([]pathSeg, len(it.path))
			for i, s := range it.path {
				for j := range s.pts {
					s.pts[j][0] += dx
					s.pts[j][1] += dy
				}
				p[i] = s
			}
			it.path = p
		}
		a.items = append(a.items, it)
	}
}

// Items returns how many things the box paints: for tests.
func (a *Box) Items() int { return len(a.items) }

// Rules returns the rectangles the box fills, as x, y, w, h relative to its
// origin: for tests.
func (a *Box) Rules() [][4]float32 {
	var out [][4]float32
	for _, it := range a.items {
		if it.kind == itemRect {
			out = append(out, [4]float32{it.x, it.y, it.w, it.h})
		}
	}
	return out
}

// Texts returns the text of the glyph runs the box paints, with their
// baselines' x and y: for tests.
func (a *Box) Texts() []TextRun {
	var out []TextRun
	for _, it := range a.items {
		if it.kind == itemText {
			out = append(out, TextRun{Text: it.text, X: it.x, Y: it.y, Size: it.font.Size})
		}
	}
	return out
}

// Paths returns how many paths the box strokes or fills: for tests.
func (a *Box) Paths() int {
	n := 0
	for _, it := range a.items {
		if it.kind == itemStroke || it.kind == itemFill {
			n++
		}
	}
	return n
}

// TextRun is a run of text a Box paints.
type TextRun struct {
	Text       string
	X, Y, Size float32
}

// Style is how a formula is laid out.
type Style struct {
	// Font is the family of its text, the system's when empty.
	Font string
	// Size is the size of its text in DIPs.
	Size float32
	// Display lays it out in display style, as math on its own lines,
	// else as math within text.
	Display bool
}

// Layout lays out the MathML tree n, which Check accepts, in style s,
// measuring text with m.
func Layout(n *Node, s Style, m Measurer) *Box {
	if s.Size <= 0 {
		s.Size = 14
	}
	l := &layout{m: m, family: s.Font, base: s.Size}
	return l.node(n, mstyle{size: s.Size, display: s.Display})
}

type layout struct {
	m      Measurer
	family string
	base   float32
}

// mstyle is the style inherited down the tree.
type mstyle struct {
	size    float32
	level   int
	display bool
	cramped bool
	variant string
	color   ui.Color
	scale   float32 // mathsize, 1 when 0
}

// em returns v ems of the style's size.
func (s mstyle) em(v float32) float32 { return v * s.size }

// script returns the style of scripts: a level down, no longer display.
func (l *layout) script(s mstyle, levels int) mstyle {
	s.level += levels
	s.display = false
	s.size = l.sizeAt(s)
	return s
}

// sizeAt is the size of text at a style's script level: 71% at the first
// level and 50% from the second, as MathML Core's scriptlevel with the
// ScriptPercentScaleDown and ScriptScriptPercentScaleDown of Latin Modern.
func (l *layout) sizeAt(s mstyle) float32 {
	f := float32(1)
	switch {
	case s.level == 1:
		f = 0.71
	case s.level >= 2:
		f = 0.5
	}
	if s.scale > 0 {
		f *= s.scale
	}
	return l.base * f
}

// Constants of the layout, in ems, as Latin Modern Math's MATH table has
// them.
const (
	axisHeight            = 0.25
	fracNumShiftUp        = 0.394
	fracNumDisplayShiftUp = 0.677
	fracDenShiftDown      = 0.345
	fracDenDisplayShift   = 0.686
	stackTopShiftUp       = 0.444
	stackTopDisplayShift  = 0.677
	stackBottomShiftDown  = 0.345
	stackBottomDisplay    = 0.686
	supShiftUp            = 0.363
	supShiftUpCramped     = 0.289
	supBottomMin          = 0.108
	supBaselineDropMax    = 0.250
	subShiftDown          = 0.247
	subTopMax             = 0.344
	subBaselineDropMin    = 0.200
	subSupGapMin          = 0.160
	supBottomMaxWithSub   = 0.344
	spaceAfterScript      = 0.056
	upperLimitGapMin      = 0.111
	upperLimitRiseMin     = 0.111
	lowerLimitGapMin      = 0.167
	lowerLimitDropMin     = 0.600
	radicalGap            = 0.054
	radicalDisplayGap     = 0.148
	radicalKernBefore     = 0.278
	radicalKernAfter      = -0.556
	radicalDegreeRaise    = 0.60
	fracPad               = 0.12
)

// rule is the thickness of fraction rules, radicals and overlines: at
// least a DIP.
func rule(s mstyle) float32 { return max(s.em(0.05), 1) }

func (l *layout) node(n *Node, s mstyle) *Box {
	switch n.Tag {
	case "mi":
		return l.mi(n, s)
	case "mn":
		return l.text(n.Text, s, l.font(s, n.Get("mathvariant"), false))
	case "mtext":
		return l.text(n.Text, s, l.font(s, n.Get("mathvariant"), false))
	case "mo":
		return l.mo(n, s, 0, 0, false)
	case "mspace":
		w, _ := length(n.Get("width"), s.size)
		h, _ := length(n.Get("height"), s.size)
		d, _ := length(n.Get("depth"), s.size)
		return &Box{Width: w, Ascent: h, Descent: d}
	case "mfrac":
		return l.frac(n, s)
	case "msup", "msub", "msubsup":
		return l.scripts(n, s)
	case "munder", "mover", "munderover":
		return l.underOver(n, s)
	case "msqrt":
		return l.radical(l.row(n.Kids, crampedStyle(s)), nil, s)
	case "mroot":
		idx := l.node(n.Kids[1], l.script(s, 2))
		return l.radical(l.node(n.Kids[0], crampedStyle(s)), idx, s)
	case "mtable":
		return l.table(n, s)
	case "mstyle":
		return l.row(n.Kids, l.styled(n, s))
	case "mphantom":
		b := l.row(n.Kids, s)
		b.items = nil
		return b
	case "menclose":
		return l.enclose(n, s)
	}
	// mrow, mpadded, mtd, mtr, math, semantics
	return l.row(n.Kids, l.styled(n, s))
}

func crampedStyle(s mstyle) mstyle {
	s.cramped = true
	return s
}

// styled applies the style attributes of n (mstyle's, and those any
// element may have) to s.
func (l *layout) styled(n *Node, s mstyle) mstyle {
	if d := n.Get("displaystyle"); d != "" {
		s.display = d == "true"
	}
	if v := n.Get("scriptlevel"); v != "" {
		if k, err := strconv.Atoi(strings.TrimPrefix(v, "+")); err == nil {
			if strings.HasPrefix(v, "+") || strings.HasPrefix(v, "-") {
				s.level += k
			} else {
				s.level = k
			}
			s.level = max(s.level, 0)
			s.size = l.sizeAt(s)
		}
	}
	if v := n.Get("mathsize"); strings.HasSuffix(v, "%") {
		if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 32); err == nil && f > 0 {
			s.scale = float32(f) / 100
			s.size = l.sizeAt(s)
		}
	}
	if v := n.Get("mathvariant"); v != "" {
		s.variant = v
	}
	if v := n.Get("mathcolor"); v != "" {
		if c, ok := parseColor(v); ok {
			s.color = c
		}
	}
	return s
}

// font returns the font of text in style s of the mathvariant variant (the
// inherited one when empty), italic where italic says.
func (l *layout) font(s mstyle, variant string, italic bool) ui.Font {
	if variant == "" {
		variant = s.variant
	}
	f := ui.Font{Family: l.family, Size: s.size}
	switch variant {
	case "normal":
		italic = false
	case "bold":
		f.Weight, italic = 700, false
	case "bold-italic":
		f.Weight, italic = 700, true
	case "italic":
		italic = true
	}
	f.Italic = italic
	return f
}

// text lays out a run of text.
func (l *layout) text(t string, s mstyle, f ui.Font) *Box {
	if t == "" {
		return &Box{tok: true}
	}
	w, g := l.m.Measure(t, f)
	a, d := ink(t)
	b := &Box{Width: w, Ascent: s.em(a), Descent: s.em(d), tok: true}
	b.items = append(b.items, item{kind: itemText, text: t, font: f, glyphs: g, color: s.color})
	return b
}

// mi lays out an identifier: a single letter in italic, as MathML Core's
// mathvariant transform does, a word upright.
func (l *layout) mi(n *Node, s mstyle) *Box {
	t := n.Text
	italic := false
	if utf8.RuneCountInString(t) == 1 {
		r, _ := utf8.DecodeRuneInString(t)
		italic = slanted(r)
	}
	b := l.text(t, s, l.font(s, n.Get("mathvariant"), italic))
	b.italic = b.items != nil && b.items[0].font.Italic
	b.fn = (n.Get("lspace") != "" || utf8.RuneCountInString(t) > 1) && isWord(t)
	if italic {
		// The slant leans out of the advance.
		b.Width += s.em(0.03)
	}
	return b
}

// slanted reports whether a letter is italic as an identifier: Latin
// letters and lowercase Greek ones, as TeX does.
func slanted(r rune) bool {
	switch {
	case r < 0x80:
		return r|0x20 >= 'a' && r|0x20 <= 'z'
	case r >= 'α' && r <= 'ω', r == 'ϵ', r == 'ϑ', r == 'ϕ', r == 'ϖ', r == 'ϱ', r == 'ς', r == 'ı', r == 'ȷ':
		return true
	}
	return false
}

// largeOps are scaled up in display style, the integrals more.
var integrals = map[string]bool{"∫": true, "∬": true, "∭": true, "∮": true, "∯": true, "∰": true}

// mo lays out an operator. When stretch, a stretchy fence grows to cover
// an ascent a and descent d.
func (l *layout) mo(n *Node, s mstyle, a, d float32, stretch bool) *Box {
	t := n.Text
	ob := &opBox{text: t, class: opClass(t)}
	ob.largeop = n.Get("largeop") == "true" || ob.class == classLarge
	ob.movable = n.Get("movablelimits") == "true"
	ob.stretch = (n.Get("stretchy") == "true" || n.Get("strechy") == "true" || n.Get("fence") == "true" && n.Get("stretchy") != "false") && fences[t]
	if v := n.Get("lspace"); v != "" {
		ob.lspace, ob.explicitL = lengthOr(v, s.size, 0), true
	}
	if v := n.Get("rspace"); v != "" {
		ob.rspace, ob.explicitR = lengthOr(v, s.size, 0), true
	}
	var b *Box
	switch {
	case t == "":
		b = &Box{}
	case ob.largeop && s.display:
		scale := float32(1.45)
		if integrals[t] {
			scale = 1.8
		}
		b = l.bigGlyph(t, s, scale)
	case ob.largeop:
		b = l.bigGlyph(t, s, 1.1)
	case n.Get("scriptlevel") != "" && fences[t]:
		// \big( and its kin: a fence of a fixed size.
		k, _ := strconv.Atoi(n.Get("scriptlevel"))
		h := s.em([]float32{1, 1.2, 1.8, 2.4, 3}[min(max(-k, 0), 4)])
		b = l.fence(t, s, s.em(axisHeight)+h/2, h/2-s.em(axisHeight))
	case stretch && ob.stretch:
		b = l.fence(t, s, a, d)
	default:
		b = l.text(t, s, l.font(s, "normal", false))
		if t == "−" || t == "+" || t == "=" || t == "±" || t == "×" {
			// Signs sit on the math axis, whatever the font does.
			b.Ascent, b.Descent = max(b.Ascent, s.em(0.58)), max(b.Descent, s.em(0.08))
		}
	}
	b.op = ob
	return b
}

// bigGlyph lays out a large operator, scale times the size, centered on
// the math axis.
func (l *layout) bigGlyph(t string, s mstyle, scale float32) *Box {
	f := l.font(s, "normal", false)
	f.Size *= scale
	w, g := l.m.Measure(t, f)
	ia, id := ink(t)
	a, d := ia*f.Size, id*f.Size
	// Move the glyph so that its ink centers on the axis.
	shift := (a-d)/2 - s.em(axisHeight)
	b := &Box{Width: w, Ascent: a - shift, Descent: d + shift}
	b.items = append(b.items, item{kind: itemText, text: t, font: f, glyphs: g, y: shift, color: s.color})
	return b
}

// fences are the operators that stretch vertically.
var fences = map[string]bool{
	"(": true, ")": true, "[": true, "]": true, "{": true, "}": true, "|": true, "‖": true, "∥": true,
	"⟨": true, "⟩": true, "⌊": true, "⌋": true, "⌈": true, "⌉": true, "/": true, "⟦": true, "⟧": true,
}

// fence lays out a fence covering an ascent a and a descent d: the font's
// glyph when that is tall enough, else drawn as a path.
func (l *layout) fence(t string, s mstyle, a, d float32) *Box {
	axis := s.em(axisHeight)
	// Symmetric about the axis, as TeX's \left and \right.
	half := max(a-axis, d+axis)
	h := 2 * half * 0.95
	if h <= s.em(1.2) {
		b := l.text(t, s, l.font(s, "normal", false))
		return b
	}
	top, bot := -(axis + h/2), -(axis - h/2) // y of the top and the bottom
	lw := max(rule(s)*1.2, s.em(0.06))
	w := s.em(0.32) + min(h, s.em(6))*0.04
	b := &Box{Width: w + s.em(0.1), Ascent: -top, Descent: bot}
	x0, x1 := s.em(0.06), s.em(0.06)+w // the fence's ink
	var p []pathSeg
	mid := (top + bot) / 2
	switch t {
	case "(":
		p = []pathSeg{moveTo(x1, top), cubeTo(x0-w*0.1, top+h*0.18, x0-w*0.1, bot-h*0.18, x1, bot)}
	case ")":
		p = []pathSeg{moveTo(x0, top), cubeTo(x1+w*0.1, top+h*0.18, x1+w*0.1, bot-h*0.18, x0, bot)}
	case "[", "⟦":
		xm := x0 + w*0.25
		p = []pathSeg{moveTo(x1, top), lineTo(xm, top), lineTo(xm, bot), lineTo(x1, bot)}
	case "]", "⟧":
		xm := x1 - w*0.25
		p = []pathSeg{moveTo(x0, top), lineTo(xm, top), lineTo(xm, bot), lineTo(x0, bot)}
	case "⌊":
		xm := x0 + w*0.25
		p = []pathSeg{moveTo(xm, top), lineTo(xm, bot), lineTo(x1, bot)}
	case "⌋":
		xm := x1 - w*0.25
		p = []pathSeg{moveTo(xm, top), lineTo(xm, bot), lineTo(x0, bot)}
	case "⌈":
		xm := x0 + w*0.25
		p = []pathSeg{moveTo(x1, top), lineTo(xm, top), lineTo(xm, bot)}
	case "⌉":
		xm := x1 - w*0.25
		p = []pathSeg{moveTo(x0, top), lineTo(xm, top), lineTo(xm, bot)}
	case "{":
		xm := (x0 + x1) / 2
		p = []pathSeg{
			moveTo(x1, top), quadTo(xm, top, xm, top+h*0.1), lineTo(xm, mid-h*0.1), quadTo(xm, mid, x0, mid),
			quadTo(xm, mid, xm, mid+h*0.1), lineTo(xm, bot-h*0.1), quadTo(xm, bot, x1, bot),
		}
	case "}":
		xm := (x0 + x1) / 2
		p = []pathSeg{
			moveTo(x0, top), quadTo(xm, top, xm, top+h*0.1), lineTo(xm, mid-h*0.1), quadTo(xm, mid, x1, mid),
			quadTo(xm, mid, xm, mid+h*0.1), lineTo(xm, bot-h*0.1), quadTo(xm, bot, x0, bot),
		}
	case "|":
		xm := (x0 + x1) / 2
		p = []pathSeg{moveTo(xm, top), lineTo(xm, bot)}
	case "‖", "∥":
		xa, xb := x0+w*0.3, x1-w*0.3
		p = []pathSeg{moveTo(xa, top), lineTo(xa, bot), moveTo(xb, top), lineTo(xb, bot)}
	case "⟨":
		p = []pathSeg{moveTo(x1, top), lineTo(x0, mid), lineTo(x1, bot)}
	case "⟩":
		p = []pathSeg{moveTo(x0, top), lineTo(x1, mid), lineTo(x0, bot)}
	case "/":
		p = []pathSeg{moveTo(x1, top), lineTo(x0, bot)}
	}
	b.items = append(b.items, item{kind: itemStroke, path: p, width: lw, color: s.color})
	return b
}

func moveTo(x, y float32) pathSeg { return pathSeg{op: 'M', pts: [3][2]float32{{x, y}}} }
func lineTo(x, y float32) pathSeg { return pathSeg{op: 'L', pts: [3][2]float32{{x, y}}} }
func quadTo(cx, cy, x, y float32) pathSeg {
	return pathSeg{op: 'Q', pts: [3][2]float32{{cx, cy}, {x, y}}}
}
func cubeTo(ax, ay, bx, by, x, y float32) pathSeg {
	return pathSeg{op: 'C', pts: [3][2]float32{{ax, ay}, {bx, by}, {x, y}}}
}

// row lays out children side by side, spacing operators as MathML Core's
// operator dictionary does (as TeX spaces its classes), and stretching the
// stretchy fences to the height of the rest.
func (l *layout) row(kids []*Node, s mstyle) *Box {
	boxes := make([]*Box, len(kids))
	var a, d float32
	stretchy := false
	for i, k := range kids {
		boxes[i] = l.node(k, s)
		if op := boxes[i].op; op != nil && op.stretch {
			stretchy = true
			continue
		}
		a, d = max(a, boxes[i].Ascent), max(d, boxes[i].Descent)
	}
	if stretchy {
		if a == 0 && d == 0 {
			a, d = s.em(0.7), s.em(0.2)
		}
		for i, k := range kids {
			if op := boxes[i].op; op != nil && op.stretch && k.Tag == "mo" {
				boxes[i] = l.mo(k, s, a, d, true)
			}
		}
	}
	out := &Box{}
	if len(boxes) == 1 {
		// A row of one is that one, for scripts' sake.
		*out = *boxes[0]
		out.items = append([]item(nil), boxes[0].items...)
		return out
	}
	x := float32(0)
	for i, b := range boxes {
		ls, rs := l.spacing(boxes, i, s)
		x += ls
		out.place(b, x, 0)
		x += b.Width + rs
		out.Ascent, out.Descent = max(out.Ascent, b.Ascent), max(out.Descent, b.Descent)
	}
	out.Width = max(x, 0)
	return out
}

// spacing returns the space before and after child i of a row.
func (l *layout) spacing(boxes []*Box, i int, s mstyle) (float32, float32) {
	b := boxes[i]
	if b.op == nil {
		// A function's name, as \sin, is spaced as an operator: a thin
		// space before it after something, and after it before its
		// argument unless that is in brackets.
		if b.fn {
			var ls, rs float32
			if prev := prevOp(boxes, i); i > 0 && prev == nil {
				ls = s.em(1.0 / 6)
			}
			if i+1 < len(boxes) {
				if n := boxes[i+1].op; n == nil || n.class != classOpen && n.class != classClose && n.class != classPunct && n.class != classBin && n.class != classRel {
					rs = s.em(1.0 / 6)
				}
			}
			return ls, rs
		}
		return 0, 0
	}
	op := b.op
	var ls, rs float32
	switch op.class {
	case classRel:
		if s.level == 0 {
			ls, rs = s.em(5.0/18), s.em(5.0/18)
		}
	case classBin:
		// A binary operator after nothing, an operator or an opening
		// bracket is a sign.
		prev := prevOp(boxes, i)
		if i == 0 || prev != nil && (prev.class == classBin || prev.class == classRel || prev.class == classOpen || prev.class == classPunct || prev.class == classLarge) {
			break
		}
		if i == len(boxes)-1 {
			break
		}
		if s.level == 0 {
			ls, rs = s.em(4.0/18), s.em(4.0/18)
		}
	case classPunct:
		rs = s.em(3.0 / 18)
	case classLarge:
		rs = s.em(1.0 / 6)
	}
	if op.explicitL {
		ls = op.lspace
	}
	if op.explicitR {
		rs = op.rspace
	}
	return ls, rs
}

// prevOp returns the operator before child i, nil when it is not one.
func prevOp(boxes []*Box, i int) *opBox {
	if i == 0 {
		return nil
	}
	return boxes[i-1].op
}

func isWord(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return s != ""
}

// frac lays out a fraction, centered on the math axis (MathML Core 3.3.2).
func (l *layout) frac(n *Node, s mstyle) *Box {
	cs := s
	if !s.display {
		cs = l.script(s, 1)
	}
	cs.display = false
	num := l.node(n.Kids[0], cs)
	den := l.node(n.Kids[1], crampedStyle(cs))
	t := rule(s)
	noRule := false
	if lt := n.Get("linethickness"); lt != "" {
		if v, ok := length(lt, s.size); ok {
			t, noRule = v, v == 0
		}
	}
	axis := s.em(axisHeight)
	var up, down float32
	if noRule {
		gap := 3 * rule(s)
		up, down = s.em(stackTopShiftUp), s.em(stackBottomShiftDown)
		if s.display {
			gap, up, down = 7*rule(s), s.em(stackTopDisplayShift), s.em(stackBottomDisplay)
		}
		if g := (up - num.Descent) - (den.Ascent - down); g < gap {
			up += (gap - g) / 2
			down += (gap - g) / 2
		}
	} else {
		gap := t
		up, down = s.em(fracNumShiftUp), s.em(fracDenShiftDown)
		if s.display {
			gap, up, down = 3*t, s.em(fracNumDisplayShiftUp), s.em(fracDenDisplayShift)
		}
		up = max(up, axis+t/2+gap+num.Descent)
		down = max(down, gap+den.Ascent-(axis-t/2))
	}
	pad := s.em(fracPad)
	w := max(num.Width, den.Width)
	b := &Box{Width: w + 2*pad, Ascent: up + num.Ascent, Descent: down + den.Descent}
	b.place(num, pad+(w-num.Width)/2, -up)
	b.place(den, pad+(w-den.Width)/2, down)
	if !noRule {
		b.items = append(b.items, item{kind: itemRect, x: pad / 2, y: -axis - t/2, w: w + pad, h: t, color: s.color})
	}
	return b
}

// scripts lays out msub, msup and msubsup (MathML Core 3.4.1).
func (l *layout) scripts(n *Node, s mstyle) *Box {
	base := l.node(n.Kids[0], s)
	var sub, sup *Node
	switch n.Tag {
	case "msub":
		sub = n.Kids[1]
	case "msup":
		sup = n.Kids[1]
	default:
		sub, sup = n.Kids[1], n.Kids[2]
	}
	return l.attach(base, sub, sup, s)
}

// attach places a subscript and a superscript, either may be nil, by base.
func (l *layout) attach(base *Box, subN, supN *Node, s mstyle) *Box {
	ss := l.script(s, 1)
	// A glyph's scripts do not drop below its ink; a large operator's do,
	// as TeX boxes them.
	drop := !base.tok || base.op != nil && base.op.largeop
	var sub, sup *Box
	var subShift, supShift float32
	if subN != nil {
		sub = l.node(subN, crampedStyle(ss))
		subShift = s.em(subShiftDown)
		if drop {
			subShift = max(subShift, base.Descent+s.em(subBaselineDropMin))
		}
		if supN == nil {
			subShift = max(subShift, sub.Ascent-s.em(subTopMax))
		}
	}
	if supN != nil {
		cs := ss
		cs.cramped = s.cramped
		sup = l.node(supN, cs)
		supShift = s.em(supShiftUp)
		if s.cramped {
			supShift = s.em(supShiftUpCramped)
		}
		if drop {
			supShift = max(supShift, base.Ascent-s.em(supBaselineDropMax))
		}
		supShift = max(supShift, s.em(supBottomMin)+sup.Descent)
	}
	if sub != nil && sup != nil {
		gap := (supShift - sup.Descent) - (sub.Ascent - subShift)
		if minGap := s.em(subSupGapMin); gap < minGap {
			delta := minGap - gap
			if bottom := supShift - sup.Descent; bottom < s.em(supBottomMaxWithSub) {
				up := min(s.em(supBottomMaxWithSub)-bottom, delta)
				supShift += up
				delta -= up
			}
			subShift += delta
		}
	}
	b := &Box{Ascent: base.Ascent, Descent: base.Descent, op: base.op, fn: base.fn}
	b.place(base, 0, 0)
	w := float32(0)
	supX := base.Width
	if base.italic {
		supX += s.em(0.06)
	}
	if sub != nil {
		b.place(sub, base.Width, subShift)
		w = max(w, base.Width+sub.Width)
		b.Descent = max(b.Descent, subShift+sub.Descent)
		b.Ascent = max(b.Ascent, sub.Ascent-subShift)
	}
	if sup != nil {
		b.place(sup, supX, -supShift)
		w = max(w, supX+sup.Width)
		b.Ascent = max(b.Ascent, supShift+sup.Ascent)
		b.Descent = max(b.Descent, sup.Descent-supShift)
	}
	b.Width = w + s.em(spaceAfterScript)
	if b.op != nil {
		// Spaced as its base, but no longer a fence to stretch.
		op := *b.op
		op.stretch = false
		b.op = &op
	}
	return b
}

// underOver lays out munder, mover and munderover (MathML Core 3.4.3):
// limits under and over large operators in display style, beside them
// in text style when they are movable, accents and lines.
func (l *layout) underOver(n *Node, s mstyle) *Box {
	var underN, overN *Node
	switch n.Tag {
	case "munder":
		underN = n.Kids[1]
	case "mover":
		overN = n.Kids[1]
	default:
		underN, overN = n.Kids[1], n.Kids[2]
	}
	baseN := n.Kids[0]
	movable := baseN.Get("movablelimits") == "true" || baseN.Tag == "mrow" && len(baseN.Kids) == 1 && baseN.Kids[0].Get("movablelimits") == "true"
	if movable && !s.display {
		return l.attach(l.node(baseN, s), underN, overN, s)
	}
	base := l.node(baseN, s)
	// Accents and lines drawn rather than set from the font.
	if overN != nil && overN.Tag == "mo" && (n.Get("accent") == "true" || overN.Get("stretchy") == "true" || drawnMarks[overN.Text]) {
		if b, ok := l.mark(base, overN.Text, s, true, baseN); ok {
			base = b
			overN = nil
		}
	}
	if underN != nil && underN.Tag == "mo" && (n.Get("accentunder") == "true" || n.Get("accent") == "true" || underN.Get("stretchy") == "true" || drawnMarks[underN.Text]) {
		if b, ok := l.mark(base, underN.Text, s, false, baseN); ok {
			base = b
			underN = nil
		}
	}
	if underN == nil && overN == nil {
		return base
	}
	ss := l.script(s, 1)
	limits := base.op != nil && base.op.largeop
	var under, over *Box
	if underN != nil {
		under = l.node(underN, crampedStyle(ss))
	}
	if overN != nil {
		over = l.node(overN, ss)
	}
	w := base.Width
	if under != nil {
		w = max(w, under.Width)
	}
	if over != nil {
		w = max(w, over.Width)
	}
	b := &Box{Width: w, Ascent: base.Ascent, Descent: base.Descent, op: base.op, fn: base.fn}
	b.place(base, (w-base.Width)/2, 0)
	if over != nil {
		rise := base.Ascent + max(s.em(upperLimitGapMin)+over.Descent, s.em(upperLimitRiseMin))
		if !limits {
			rise = base.Ascent + s.em(0.08) + over.Descent
		}
		b.place(over, (w-over.Width)/2, -rise)
		b.Ascent = rise + over.Ascent
	}
	if under != nil {
		drop := base.Descent + max(s.em(lowerLimitGapMin)+under.Ascent, s.em(lowerLimitDropMin))
		if !limits {
			drop = base.Descent + s.em(0.08) + under.Ascent
		}
		b.place(under, (w-under.Width)/2, drop)
		b.Descent = drop + under.Descent
	}
	if b.op != nil {
		op := *b.op
		op.stretch = false
		b.op = &op
	}
	return b
}

// drawnMarks are the accents and lines mark draws.
var drawnMarks = map[string]bool{
	"‾": true, "¯": true, "̅": true, "̄": true, "_": true, "̲": true,
	"^": true, "ˆ": true, "̂": true, "ˇ": true, "̌": true, "~": true, "˜": true, "̃": true,
	"→": true, "⃗": true, "←": true, "↔": true,
	"˙": true, "̇": true, "¨": true, "̈": true, "´": true, "`": true, "˘": true, "˚": true,
	"⏞": true, "⏟": true, "⎴": true, "⎵": true, "⏜": true, "⏝": true,
}

// mark draws an accent or a line over (or under) base.
func (l *layout) mark(base *Box, t string, s mstyle, over bool, baseN *Node) (*Box, bool) {
	if !drawnMarks[t] {
		return nil, false
	}
	t0 := rule(s)
	lw := max(t0, s.em(0.055))
	// A letter's accent is the size of a letter's; a word's spans it.
	single := baseN.Tag == "mi" && utf8.RuneCountInString(baseN.Text) == 1 || baseN.Tag == "mn" && utf8.RuneCountInString(baseN.Text) == 1
	skew := float32(0)
	if base.italic {
		skew = s.em(0.08)
	}
	cx := base.Width/2 + skew
	aw := base.Width
	if single {
		aw = min(max(base.Width, s.em(0.45)), s.em(0.55))
	}
	x0, x1 := cx-aw/2, cx+aw/2
	b := &Box{Width: base.Width, Ascent: base.Ascent, Descent: base.Descent, op: base.op, italic: base.italic}
	b.place(base, 0, 0)
	if !over {
		switch t {
		case "⏟", "⎵", "⏝":
			y := base.Descent + s.em(0.1)
			h := s.em(0.28)
			b.items = append(b.items, item{kind: itemStroke, path: hbrace(0, base.Width, y, y+h, true), width: lw, color: s.color})
			b.Descent = y + h
		default:
			y := base.Descent + 3*t0
			b.items = append(b.items, item{kind: itemRect, x: 0, y: y, w: base.Width, h: t0, color: s.color})
			b.Descent = y + 2*t0
		}
		return b, true
	}
	top := -base.Ascent
	switch t {
	case "‾", "̅":
		y := top - 3*t0 - t0
		b.items = append(b.items, item{kind: itemRect, x: 0, y: y, w: base.Width, h: t0, color: s.color})
		b.Ascent = -y + t0
	case "¯", "̄":
		y := top - s.em(0.1) - t0
		b.items = append(b.items, item{kind: itemRect, x: x0, y: y, w: aw, h: t0, color: s.color})
		b.Ascent = -y + t0
	case "^", "ˆ", "̂", "ˇ", "̌":
		h := s.em(0.16)
		if !single {
			h = min(s.em(0.16)+base.Width*0.03, s.em(0.3))
		}
		yb := top - s.em(0.08)
		yt := yb - h
		p := []pathSeg{moveTo(x0, yb), lineTo(cx, yt), lineTo(x1, yb)}
		if t == "ˇ" || t == "̌" {
			p = []pathSeg{moveTo(x0, yt), lineTo(cx, yb), lineTo(x1, yt)}
		}
		b.items = append(b.items, item{kind: itemStroke, path: p, width: lw, color: s.color})
		b.Ascent = -yt + lw
	case "~", "˜", "̃":
		h := s.em(0.12)
		yb := top - s.em(0.1)
		yt := yb - h
		p := []pathSeg{moveTo(x0, yb), cubeTo(x0+aw*0.25, yt-h*0.6, x0+aw*0.4, yt, cx, (yt+yb)/2), cubeTo(x0+aw*0.6, yb, x0+aw*0.75, yb+h*0.6, x1, yt)}
		b.items = append(b.items, item{kind: itemStroke, path: p, width: lw, color: s.color})
		b.Ascent = -yt + lw
	case "→", "⃗", "←", "↔":
		if single {
			x0, x1 = cx-aw/2, cx+aw/2
		} else {
			x0, x1 = 0, base.Width
		}
		y := top - s.em(0.16)
		hd := s.em(0.1)
		p := []pathSeg{moveTo(x0, y), lineTo(x1, y)}
		if t != "←" {
			p = append(p, moveTo(x1-hd*1.2, y-hd), lineTo(x1, y), lineTo(x1-hd*1.2, y+hd))
		}
		if t == "←" || t == "↔" {
			p = append(p, moveTo(x0+hd*1.2, y-hd), lineTo(x0, y), lineTo(x0+hd*1.2, y+hd))
		}
		b.items = append(b.items, item{kind: itemStroke, path: p, width: lw, color: s.color})
		b.Ascent = -y + hd + lw
	case "˙", "̇", "¨", "̈":
		r := s.em(0.055)
		y := top - s.em(0.1) - r
		var p []pathSeg
		if t == "˙" || t == "̇" {
			p = circle(cx, y, r)
		} else {
			p = append(circle(cx-s.em(0.12), y, r), circle(cx+s.em(0.12), y, r)...)
		}
		b.items = append(b.items, item{kind: itemFill, path: p, color: s.color})
		b.Ascent = -y + r
	case "´", "`":
		y0, y1 := top-s.em(0.06), top-s.em(0.24)
		p := []pathSeg{moveTo(cx-s.em(0.05), y0), lineTo(cx+s.em(0.08), y1)}
		if t == "`" {
			p = []pathSeg{moveTo(cx+s.em(0.05), y0), lineTo(cx-s.em(0.08), y1)}
		}
		b.items = append(b.items, item{kind: itemStroke, path: p, width: lw, color: s.color})
		b.Ascent = -y1 + lw
	case "˘":
		yt, yb := top-s.em(0.22), top-s.em(0.08)
		p := []pathSeg{moveTo(cx-s.em(0.12), yt), quadTo(cx, yb+s.em(0.06), cx+s.em(0.12), yt)}
		b.items = append(b.items, item{kind: itemStroke, path: p, width: lw, color: s.color})
		b.Ascent = -yt + lw
	case "˚":
		r := s.em(0.07)
		y := top - s.em(0.1) - r
		b.items = append(b.items, item{kind: itemStroke, path: circle(cx, y, r), width: lw, color: s.color})
		b.Ascent = -y + r + lw
	case "⏞", "⎴", "⏜":
		h := s.em(0.28)
		y := top - s.em(0.1)
		b.items = append(b.items, item{kind: itemStroke, path: hbrace(0, base.Width, y, y-h, false), width: lw, color: s.color})
		b.Ascent = -(y - h)
	case "⏟", "⎵", "⏝", "_", "̲":
		return l.mark(base, t, s, false, baseN)
	}
	b.Width = max(b.Width, x1, base.Width)
	return b, true
}

// hbrace is a horizontal curly brace from x0 to x1, its ends at y0 and its
// tip at y1.
func hbrace(x0, x1, y0, y1 float32, _ bool) []pathSeg {
	w := x1 - x0
	ym := (y0 + y1) / 2
	xm := (x0 + x1) / 2
	r := min(w*0.1, (y1-y0)*2)
	if r < 0 {
		r = min(w*0.1, (y0-y1)*2)
	}
	return []pathSeg{
		moveTo(x0, y0), quadTo(x0, ym, x0+r, ym), lineTo(xm-r, ym), quadTo(xm, ym, xm, y1),
		quadTo(xm, ym, xm+r, ym), lineTo(x1-r, ym), quadTo(x1, ym, x1, y0),
	}
}

// circle is a circle of four quadratic arcs.
func circle(cx, cy, r float32) []pathSeg {
	k := r * 1.0
	return []pathSeg{
		moveTo(cx+r, cy),
		quadTo(cx+r, cy+k, cx, cy+r),
		quadTo(cx-r, cy+k, cx-r, cy),
		quadTo(cx-r, cy-k, cx, cy-r),
		quadTo(cx+r, cy-k, cx+r, cy),
		{op: 'Z'},
	}
}

// radical lays out a square root, or a root of index idx, its sign drawn
// to the height of the radicand (MathML Core 3.3.3).
func (l *layout) radical(rad, idx *Box, s mstyle) *Box {
	t := rule(s)
	gap := s.em(radicalGap) + t/4
	if s.display {
		gap = s.em(radicalDisplayGap)
	}
	top := rad.Ascent + gap + t // the top of the line over the radicand
	bottom := max(rad.Descent, s.em(0.12)) + s.em(0.04)
	h := top + bottom
	sw := s.em(0.56) + min(h, s.em(5))*0.06 // the sign's width
	shift := float32(0)
	if idx != nil {
		shift = max(s.em(radicalKernBefore)+idx.Width+s.em(radicalKernAfter), 0)
	}
	pad := s.em(0.06)
	b := &Box{Width: shift + sw + rad.Width + pad, Ascent: top + t, Descent: bottom}
	b.place(rad, shift+sw, 0)
	// The sign: a short tick up, the stroke down to the bottom, the long
	// one up to the top, and the line over the radicand.
	x := shift
	ytop := -top + t/2
	ybot := bottom
	tickH := min(h*0.42, s.em(0.5))
	ax, ay := x+sw*0.06, ybot-tickH*0.9
	bx, by := x+sw*0.2, ybot-tickH
	cx, cy := x+sw*0.48, ybot
	dx := x + sw*0.98
	p := []pathSeg{moveTo(ax, ay), lineTo(bx, by), lineTo(cx, cy), lineTo(dx, ytop), lineTo(shift+sw+rad.Width+pad, ytop)}
	b.items = append(b.items, item{kind: itemStroke, path: p, width: t * 1.1, color: s.color})
	// The stroke down is heavier, as in type.
	b.items = append(b.items, item{kind: itemStroke, path: []pathSeg{moveTo(bx, by), lineTo(cx, cy)}, width: t * 2.2, color: s.color})
	if idx != nil {
		ix := max(s.em(radicalKernBefore), 0)
		if shift == 0 {
			ix = max(0, (sw*0.3)-idx.Width)
		}
		iy := ybot - h*radicalDegreeRaise - idx.Descent
		b.place(idx, ix, iy)
		b.Ascent = max(b.Ascent, -iy+idx.Ascent)
	}
	return b
}

// table lays out an mtable: its cells' columns and rows, centered on the
// math axis (MathML Core 3.5).
func (l *layout) table(n *Node, s mstyle) *Box {
	cs := l.styled(n, s)
	cs.display = n.Get("displaystyle") == "true"
	type cell struct {
		b          *Box
		align      string
		padL, padR float32
		hasPad     bool
	}
	var rows [][]cell
	cols := 0
	tableAligns := strings.Fields(n.Get("columnalign"))
	for _, r := range n.Kids {
		var row []cell
		for j, c := range r.Kids {
			b := l.row(c.Kids, cs)
			ce := cell{b: b, align: c.Get("columnalign")}
			st := parseCSS(c.Get("style"))
			if ce.align == "" {
				ce.align = st["text-align"]
			}
			if ce.align == "" && len(tableAligns) > 0 {
				ce.align = tableAligns[min(j, len(tableAligns)-1)]
			}
			if v, ok := st["padding-left"]; ok {
				ce.padL, ce.hasPad = lengthOr(v, s.size, 0), true
			}
			if v, ok := st["padding-right"]; ok {
				ce.padR, ce.hasPad = lengthOr(v, s.size, 0), true
			}
			row = append(row, ce)
		}
		cols = max(cols, len(row))
		rows = append(rows, row)
	}
	widths := make([]float32, cols)
	padL := make([]float32, cols)
	padR := make([]float32, cols)
	padded := make([]bool, cols)
	for _, r := range rows {
		for j, c := range r {
			widths[j] = max(widths[j], c.b.Width)
			if c.hasPad {
				padded[j] = true
				padL[j], padR[j] = max(padL[j], c.padL), max(padR[j], c.padR)
			}
		}
	}
	// Columns without padding of their own are 0.8em apart, MathML's
	// columnspacing; the table's outer ends have none.
	for j := range cols {
		if !padded[j] {
			if j > 0 {
				padL[j] = s.em(0.4)
			}
			if j < cols-1 {
				padR[j] = s.em(0.4)
			}
		}
	}
	if cols > 0 {
		padL[0], padR[cols-1] = 0, 0
	}
	xs := make([]float32, cols)
	x := float32(0)
	for j := range cols {
		x += padL[j]
		xs[j] = x
		x += widths[j] + padR[j]
	}
	// Rows have a strut's height at least, and are apart.
	strutA, strutD := s.em(0.8), s.em(0.3)
	gap := s.em(0.15)
	if cs.display {
		gap = s.em(0.35)
	}
	b := &Box{Width: x}
	y := float32(0) // the top of the row
	for i, r := range rows {
		ra, rd := strutA, strutD
		for _, c := range r {
			ra, rd = max(ra, c.b.Ascent), max(rd, c.b.Descent)
		}
		if i > 0 {
			y += gap
		}
		base := y + ra
		for j, c := range r {
			cx := xs[j]
			switch c.align {
			case "left":
			case "right":
				cx += widths[j] - c.b.Width
			default:
				cx += (widths[j] - c.b.Width) / 2
			}
			b.place(c.b, cx, base)
		}
		y = base + rd
	}
	// Center on the axis: shift everything up.
	axis := s.em(axisHeight)
	off := -(y/2 + axis)
	shifted := &Box{Width: b.Width, Ascent: y/2 + axis, Descent: y/2 - axis}
	shifted.place(b, 0, off)
	return shifted
}

// enclose lays out a menclose: its child with the notations drawn around.
func (l *layout) enclose(n *Node, s mstyle) *Box {
	in := l.row(n.Kids, s)
	notation := strings.Fields(n.Get("notation"))
	if len(notation) == 0 {
		notation = []string{"longdiv"}
	}
	pad := s.em(0.15)
	t := rule(s)
	b := &Box{Width: in.Width + 2*pad, Ascent: in.Ascent + pad, Descent: in.Descent + pad}
	b.place(in, pad, 0)
	x0, x1 := float32(0), in.Width+2*pad
	y0, y1 := -b.Ascent, b.Descent
	var p []pathSeg
	for _, k := range notation {
		switch k {
		case "box", "roundedbox", "circle", "longdiv":
			p = append(p, moveTo(x0, y0), lineTo(x1, y0), lineTo(x1, y1), lineTo(x0, y1), pathSeg{op: 'Z'})
		case "updiagonalstrike":
			p = append(p, moveTo(x0, y1), lineTo(x1, y0))
		case "downdiagonalstrike":
			p = append(p, moveTo(x0, y0), lineTo(x1, y1))
		case "horizontalstrike":
			p = append(p, moveTo(x0, -s.em(axisHeight)), lineTo(x1, -s.em(axisHeight)))
		case "verticalstrike":
			p = append(p, moveTo((x0+x1)/2, y0), lineTo((x0+x1)/2, y1))
		case "top":
			p = append(p, moveTo(x0, y0), lineTo(x1, y0))
		case "bottom":
			p = append(p, moveTo(x0, y1), lineTo(x1, y1))
		case "left":
			p = append(p, moveTo(x0, y0), lineTo(x0, y1))
		case "right":
			p = append(p, moveTo(x1, y0), lineTo(x1, y1))
		}
	}
	if len(p) > 0 {
		b.items = append(b.items, item{kind: itemStroke, path: p, width: t, color: s.color})
	}
	return b
}

// ink returns how far the ink of text reaches above and below its
// baseline, in ems: the font has no MATH table to say, so it comes from the
// shapes of the characters.
func ink(t string) (a, d float32) {
	for _, r := range t {
		ra, rd := runeInk(r)
		a, d = max(a, ra), max(d, rd)
	}
	return a, d
}

func runeInk(r rune) (a, d float32) {
	switch {
	case r == ' ' || r == ' ':
		return 0, 0
	case strings.ContainsRune("acemnorsuvwxzıȷαεικνοσςτυωϵ", r):
		return 0.45, 0
	case strings.ContainsRune("gpqyγημρχφϕψς", r):
		return 0.45, 0.21
	case r == 'j':
		return 0.67, 0.21
	case strings.ContainsRune("βδζθλξϑ", r):
		return 0.71, 0.21
	case r >= '0' && r <= '9':
		return 0.66, 0.01
	case strings.ContainsRune("([{|)]}⟨⟩⌊⌋⌈⌉‖/\\", r):
		return 0.75, 0.25
	case strings.ContainsRune("+−±∓=≠<>≤≥≈≡×÷·⋅∼≃≅∝→←↔⇒⇐⇔↦∈∉⊂⊃⊆⊇∪∩∧∨⊕⊗", r):
		return 0.58, 0.08
	case strings.ContainsRune("∑∏∐⋃⋂⋁⋀⨁⨂", r):
		return 0.75, 0.25
	case integrals[string(r)]:
		return 0.8, 0.3
	case strings.ContainsRune(",;.", r):
		return 0.1, 0.18
	case strings.ContainsRune("′″‴'", r):
		return 0.75, 0
	case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
		return 0.82, 0.12
	}
	return 0.7, 0
}

// length reads a MathML length — 0.17em, 3pt, 2px, 1ex, 0 — in DIPs for a
// font of size em.
func length(v string, em float32) (float32, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	unit := ""
	for _, u := range []string{"em", "ex", "px", "pt", "mu", "%"} {
		if strings.HasSuffix(v, u) {
			unit, v = u, strings.TrimSuffix(v, u)
			break
		}
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 32)
	if err != nil {
		switch v {
		case "thinmathspace":
			return em / 6, true
		case "mediummathspace":
			return em * 2 / 9, true
		case "thickmathspace":
			return em * 5 / 18, true
		}
		return 0, false
	}
	x := float32(f)
	switch unit {
	case "em":
		return x * em, true
	case "ex":
		return x * em * 0.43, true
	case "pt":
		return x * em / 10, true
	case "mu":
		return x * em / 18, true
	case "%":
		return 0, false
	}
	return x, true
}

func lengthOr(v string, em, def float32) float32 {
	if x, ok := length(v, em); ok {
		return x
	}
	return def
}

// parseCSS reads an inline style's properties.
func parseCSS(s string) map[string]string {
	m := map[string]string{}
	for _, decl := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(decl, ":")
		if ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// colors are the named colors of \color and \textcolor.
var colors = map[string]ui.Color{
	"red": ui.RGB(220, 38, 38), "blue": ui.RGB(37, 99, 235), "green": ui.RGB(22, 163, 74),
	"orange": ui.RGB(234, 88, 12), "purple": ui.RGB(147, 51, 234), "magenta": ui.RGB(219, 39, 119),
	"cyan": ui.RGB(8, 145, 178), "brown": ui.RGB(146, 64, 14), "gray": ui.RGB(107, 114, 128), "grey": ui.RGB(107, 114, 128),
}

func parseColor(v string) (ui.Color, bool) {
	if c, ok := colors[strings.ToLower(v)]; ok {
		return c, true
	}
	if strings.HasPrefix(v, "#") && (len(v) == 7 || len(v) == 4) {
		return ui.Hex(v), true
	}
	return ui.Color{}, false
}
