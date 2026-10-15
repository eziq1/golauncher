package calc

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/eziq1/golauncher/plugins/calc/units"
)

// Binding powers of the Pratt parser.
const (
	bpAdd     = 10 // + - and implicit addition (1 h 30 min)
	bpMul     = 20 // * / % and implicit multiplication
	bpUnary   = 25 // prefix - +; looser than ^ so that -2^2 = -4
	bpUnit    = 27 // a unit after a number: tighter than / so 5 GB / 100 Mbps works
	bpPow     = 30 // ^, right-associative
	bpPostfix = 40 // !
)

// maxDepth bounds the nesting of sub-expressions.
const maxDepth = 100

// maxUnitPow bounds the exponent of a unit (m^3).
const maxUnitPow = 9

// evalEnv is what evaluation depends on besides the text.
type evalEnv struct {
	units *units.Env
	// yenJPY makes "¥" a Japanese yen instead of a Chinese yuan.
	yenJPY bool
	// noCurrency turns currencies off: their names are unknown words.
	noCurrency bool
	// usedCurrency is set when a currency was looked up.
	usedCurrency bool
}

// parser is a Pratt parser that evaluates while parsing.
type parser struct {
	toks  []token
	pos   int
	depth int
	env   *evalEnv
	// ops counts the operations applied (binary and postfix operators,
	// function calls and implicit multiplications). Unary signs,
	// parentheses and units do not count.
	ops int
	// hasNum and hasUnit record whether a number and a unit were seen.
	hasNum, hasUnit bool
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) peekAt(i int) token {
	if i >= len(p.toks) {
		return token{kind: tokEOF}
	}
	return p.toks[i]
}

func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != tokEOF {
		p.pos++
	}
	return t
}

func (p *parser) unitEnv() *units.Env {
	if p.env == nil {
		return nil
	}
	return p.env.units
}

// parse evaluates the whole token stream.
func (p *parser) parse() (*value, error) {
	v, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tokEOF {
		return nil, fmt.Errorf("%w: unexpected %q", errSyntax, t.text)
	}
	return v, nil
}

// lookupUnit returns the unit starting at token i and the number of
// tokens it spans, or nil. Phrases of several words ("fl oz") win over
// single words; "m2" and "cm3" are squares and cubes of lengths.
func (p *parser) lookupUnit(i int) (*units.Unit, int, int) {
	t := p.peekAt(i)
	tab := units.Default()
	switch t.kind {
	case tokCur:
		code := "USD"
		switch {
		case units.IsYen(t.raw):
			yen := false
			if p.env != nil {
				yen = p.env.yenJPY
			}
			code = units.YenSymbol(yen)
		case t.raw != "$":
			u, ok := tab.Lookup(t.raw)
			if !ok {
				return nil, 0, 0
			}
			return p.allowed(u), 1, 1
		}
		return p.allowed(tab.BySym(code)), 1, 1
	case tokIdent:
	default:
		return nil, 0, 0
	}
	var words []string
	for j := i; j < len(p.toks) && j-i < tab.MaxWords() && p.toks[j].kind == tokIdent; j++ {
		words = append(words, p.toks[j].raw)
	}
	if u, n := tab.LookupPhrase(words); u != nil {
		if u = p.allowed(u); u != nil {
			return u, n, 1
		}
		return nil, 0, 0
	}
	if s := t.raw; len(s) > 1 && (s[len(s)-1] == '2' || s[len(s)-1] == '3') {
		if u, ok := tab.Lookup(s[:len(s)-1]); ok && u.Dim == units.Of(units.Length, 1) {
			return u, 1, int(s[len(s)-1] - '0')
		}
	}
	return nil, 0, 0
}

// allowed returns u unless it is a currency and currencies are off.
func (p *parser) allowed(u *units.Unit) *units.Unit {
	if u == nil {
		return nil
	}
	if u.Dyn == units.DynCurrency {
		if p.env == nil || p.env.noCurrency {
			return nil
		}
		p.env.usedCurrency = true
	}
	return u
}

// isCall reports whether token i is a function name followed by "(".
func (p *parser) isCall(i int) bool {
	t := p.peekAt(i)
	_, ok := functions[t.text]
	return ok && t.kind == tokIdent && p.peekAt(i+1).kind == tokLParen
}

// isUnitAt reports whether a unit starts at token i.
func (p *parser) isUnitAt(i int) bool {
	t := p.peekAt(i)
	if t.kind == tokIdent && (p.isCall(i) || constants[t.text] != nil) {
		return false
	}
	u, _, _ := p.lookupUnit(i)
	return u != nil
}

// infixBP returns the left binding power of t used as an infix or postfix
// operator, or 0 if t ends an expression.
func (p *parser) infixBP(t token, left *value) int {
	switch t.kind {
	case tokOp:
		switch t.text {
		case "+", "-":
			return bpAdd
		case "*", "/", "%":
			return bpMul
		case "^":
			return bpPow
		case "!":
			return bpPostfix
		}
	case tokIdent:
		if p.isUnitAt(p.pos) {
			return bpUnit
		}
		if conversionWord(t) {
			return 0
		}
		return bpMul // implicit multiplication: 2pi
	case tokLParen:
		return bpMul // 3(4+5)
	case tokNum:
		if left.u != nil && !left.bare && !left.dim.IsZero() {
			return bpAdd // 1 h 30 min, 5 ft 10 in
		}
	}
	return 0
}

// conversionWord reports whether t is "in", "to" or "as" (which, unless
// they are units, end the source of a conversion).
func conversionWord(t token) bool {
	return t.kind == tokIdent && (t.text == "in" || t.text == "to" || t.text == "as") || t.kind == tokConv
}

// expr parses an expression whose operators bind tighter than rbp.
func (p *parser) expr(rbp int) (*value, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return nil, fmt.Errorf("%w: expression too deep", errSyntax)
	}
	left, err := p.prefix()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		bp := p.infixBP(t, left)
		if bp <= rbp {
			return left, nil
		}
		switch {
		case bp == bpUnit:
			var u *uexpr
			if u, err = p.unitExpr(); err == nil {
				left, err = quantity(left, u, withUnit(left.text, u.text))
			}
		case t.kind == tokNum: // implicit addition
			p.ops++
			var right *value
			if right, err = p.expr(bpAdd); err == nil {
				if right.u == nil {
					err = fmt.Errorf("%w: number after a quantity", errSyntax)
				} else {
					left, err = addValues(left, right, false, left.text+" "+right.text)
				}
			}
		case t.text == "!":
			p.ops++
			p.next()
			if !left.plain() {
				return nil, fmt.Errorf("%w: factorial of a quantity", errDim)
			}
			var x *big.Float
			if x, err = factorial(left.x); err == nil {
				left = number(x, left.text+"!")
			}
		case t.kind != tokOp: // implicit multiplication
			p.ops++
			var right *value
			if right, err = p.expr(bpMul); err == nil {
				left, err = mulValues(left, right, false, left.text+right.text, p.unitEnv())
			}
		default:
			p.ops++
			p.next()
			next := bp
			if t.text == "^" {
				next-- // right-associative
			}
			var right *value
			if right, err = p.expr(next); err == nil {
				left, err = p.binary(t.text, left, right)
			}
		}
		if err != nil {
			return nil, err
		}
	}
}

// opText is how operators are shown between quantities.
var opText = map[string]string{"+": " + ", "-": " − ", "*": " × ", "/": " / ", "%": " % ", "^": "^"}

// binary applies a binary operator to values.
func (p *parser) binary(op string, a, b *value) (*value, error) {
	text := a.text + op + b.text
	if a.u != nil || b.u != nil {
		text = a.text + opText[op] + b.text
	}
	switch op {
	case "+", "-":
		if a.plain() && b.plain() {
			x, err := binary(op, a.x, b.x)
			return number(x, text), err
		}
		return addValues(a, b, op == "-", text)
	case "*", "/":
		return mulValues(a, b, op == "/", text, p.unitEnv())
	case "%":
		if a.dim != b.dim {
			return nil, errDim
		}
		x, err := binary(op, a.x, b.x)
		if err != nil {
			return nil, err
		}
		if a.plain() {
			return number(x, text), nil
		}
		return &value{x: x, dim: a.dim, u: a.u, text: text}, nil
	}
	// "^": a quantity to an integer power.
	if !b.plain() {
		return nil, fmt.Errorf("%w: exponent with a unit", errDim)
	}
	x, err := binary(op, a.x, b.x)
	if err != nil {
		return nil, err
	}
	if a.plain() {
		return number(x, text), nil
	}
	n, acc := b.x.Int64()
	if acc != big.Exact || n < -maxUnitPow || n > maxUnitPow || n == 0 || a.abs && a.offset() != nil {
		return nil, fmt.Errorf("%w: unit to a non-integer power", errDim)
	}
	r := &value{x: x, dim: a.dim.Pow(int(n)), text: text}
	if a.u != nil {
		if r.u, err = power(a.u, int(n), p.unitEnv()); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// unitExpr parses a unit expression: units joined by "/" and "*" (or "·")
// with integer powers, such as "km/h", "m/s^2" or "kW·h". A "/" or "*"
// followed by a number ends it, so "5 GB / 100 Mbps" divides quantities.
func (p *parser) unitExpr() (*uexpr, error) {
	u, err := p.unitPow()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tokOp || t.text != "/" && t.text != "*" || !p.isUnitAt(p.pos+1) {
			return u, nil
		}
		p.next()
		r, err := p.unitPow()
		if err != nil {
			return nil, err
		}
		u = combine(u, r, t.text == "/", p.unitEnv())
	}
}

// unitPow parses a unit with an optional integer power ("m^2", "m²", "m2").
func (p *parser) unitPow() (*uexpr, error) {
	un, n, pw := p.lookupUnit(p.pos)
	if un == nil {
		return nil, fmt.Errorf("%w %q", errUnknown, p.peek().text)
	}
	p.pos += n
	p.hasUnit = true
	u, err := single(un, p.unitEnv())
	if err != nil {
		return nil, err
	}
	if p.peek().kind == tokOp && p.peek().text == "^" {
		sign, i := int64(1), p.pos+1
		if t := p.peekAt(i); t.kind == tokOp && t.text == "-" {
			sign, i = -1, i+1
		}
		if t := p.peekAt(i); t.kind == tokNum && t.num.IsInt() {
			e, _ := t.num.Int64()
			if e < 1 || e > maxUnitPow {
				return nil, fmt.Errorf("%w: unit power out of range", errSyntax)
			}
			pw *= int(sign * e)
			p.pos = i + 1
		}
	}
	if pw != 1 {
		return power(u, pw, p.unitEnv())
	}
	return u, nil
}

// prefix parses an operand: a number, constant, unit, function call,
// parenthesized expression or signed operand.
func (p *parser) prefix() (*value, error) {
	t := p.peek()
	switch t.kind {
	case tokNum:
		p.next()
		p.hasNum = true
		return number(newF().Set(t.num), t.text), nil
	case tokCur:
		u, err := p.unitExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != tokNum {
			return &value{x: u.factor, dim: u.dim, u: u, bare: true, text: u.text}, nil
		}
		n, err := p.expr(bpUnit) // $100
		if err != nil {
			return nil, err
		}
		return quantity(n, u, n.text+" "+u.text)
	case tokIdent:
		if f, ok := functions[t.text]; ok && p.peekAt(p.pos+1).kind == tokLParen {
			p.pos += 2
			p.ops++
			args, err := p.args()
			if err != nil {
				return nil, err
			}
			return p.call(t.text, f, args)
		}
		if c, ok := constants[t.text]; ok {
			p.next()
			return number(newF().Set(c), t.text), nil
		}
		if p.isUnitAt(p.pos) {
			u, err := p.unitExpr()
			if err != nil {
				return nil, err
			}
			if u.isCurrency() && p.peek().kind == tokNum { // USD 100
				n, err := p.expr(bpUnit)
				if err != nil {
					return nil, err
				}
				return quantity(n, u, n.text+" "+u.text)
			}
			return &value{x: u.factor, dim: u.dim, u: u, bare: true, text: u.text}, nil
		}
		if _, ok := functions[t.text]; ok {
			return nil, fmt.Errorf("%w: %s needs parentheses", errSyntax, t.text)
		}
		return nil, fmt.Errorf("%w %q", errUnknown, t.text)
	case tokLParen:
		p.next()
		v, err := p.expr(0)
		if err != nil {
			return nil, err
		}
		if p.next().kind != tokRParen {
			return nil, fmt.Errorf("%w: missing )", errSyntax)
		}
		r := *v
		r.text = "(" + v.text + ")"
		r.bare = false
		return &r, nil
	case tokOp:
		p.next()
		switch t.text {
		case "-", "+":
			v, err := p.expr(bpUnary)
			if err != nil {
				return nil, err
			}
			if t.text == "-" {
				v = negate(v)
				v.text = "-" + v.text
				return v, nil
			}
			r := *v
			r.text = "+" + v.text
			r.bare = false
			return &r, nil
		case "Δ":
			v, err := p.expr(bpUnary)
			if err != nil {
				return nil, err
			}
			return delta(v)
		}
		return nil, fmt.Errorf("%w: unexpected %q", errSyntax, t.text)
	}
	p.next()
	return nil, fmt.Errorf("%w: unexpected %q", errSyntax, t.text)
}

// call applies a function. Trigonometric functions take angles, abs, min
// and max keep units, sqrt halves the dimension; the others need plain
// numbers.
func (p *parser) call(name string, f function, args []*value) (*value, error) {
	text := name + "("
	var unit *value
	xs := make([]*big.Float, len(args))
	for i, a := range args {
		if i > 0 {
			text += ","
		}
		text += a.text
		xs[i] = a.x
		if a.abs && a.offset() != nil {
			return nil, fmt.Errorf("%w: function of a temperature", errDim)
		}
		if !a.plain() {
			if unit != nil && unit.dim != a.dim {
				return nil, errDim
			}
			if unit == nil {
				unit = a
			}
		}
	}
	text += ")"
	if unit == nil {
		x, err := f.apply(name, xs)
		return number(x, text), err
	}
	switch name {
	case "sin", "cos", "tan":
		if len(args) != 1 || unit.dim != units.Of(units.Angle, 1) {
			return nil, errDim
		}
		x, err := f.apply(name, xs)
		return number(x, text), err
	case "abs", "min", "max":
		for _, a := range args {
			if a.dim != unit.dim {
				return nil, errDim
			}
		}
		x, err := f.apply(name, xs)
		if err != nil {
			return nil, err
		}
		return &value{x: x, dim: unit.dim, u: unit.u, text: text}, nil
	case "sqrt":
		d, ok := unit.dim.Root(2)
		if !ok {
			return nil, errDim
		}
		x, err := f.apply(name, xs)
		if err != nil {
			return nil, err
		}
		r := &value{x: x, dim: d, text: text}
		if cat := units.CategoryOf(d); cat != units.CatNone {
			r.u = &uexpr{text: d.String(), factor: newF().SetInt64(1), dim: d, cat: cat}
			r.u.name(p.unitEnv())
		}
		return r, nil
	}
	return nil, fmt.Errorf("%w: %s of a quantity", errDim, name)
}

// args parses a function's argument list after its "(".
func (p *parser) args() ([]*value, error) {
	var args []*value
	if p.peek().kind == tokRParen {
		p.next()
		return args, nil
	}
	for {
		v, err := p.expr(0)
		if err != nil {
			return nil, err
		}
		args = append(args, v)
		switch p.next().kind {
		case tokComma:
		case tokRParen:
			return args, nil
		default:
			return nil, fmt.Errorf("%w: missing )", errSyntax)
		}
	}
}

// parseTarget parses the target of a conversion: a unit expression and
// nothing else.
func (p *parser) parseTarget() (*uexpr, error) {
	if !p.isUnitAt(p.pos) {
		return nil, fmt.Errorf("%w: no target unit", errSyntax)
	}
	u, err := p.unitExpr()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tokEOF {
		return nil, fmt.Errorf("%w: unexpected %q", errSyntax, t.text)
	}
	return u, nil
}

// joinTokens renders tokens as a normalized expression.
func joinTokens(toks []token) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(t.text)
	}
	return b.String()
}
