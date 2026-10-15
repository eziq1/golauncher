package calc

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strings"

	"github.com/eziq1/golauncher/plugins/calc/units"
)

// prec is the precision of all arithmetic, in bits.
const prec = 128

// maxBinExp bounds the binary exponent of every intermediate value (about
// 1e±9864) so that formatting stays fast.
const maxBinExp = 1 << 15

// maxPowExp is the largest integer exponent computed exactly; larger ones go
// through float64.
const maxPowExp = 1 << 20

// maxFactorial is the largest operand of !.
const maxFactorial = 1000

// maxInput bounds the length of the evaluated text.
const maxInput = 1000

var (
	errSyntax        = errors.New("calc: syntax error")
	errUnknown       = errors.New("calc: unknown identifier")
	errDivZero       = errors.New("calc: division by zero")
	errDomain        = errors.New("calc: domain error")
	errRange         = errors.New("calc: result out of range")
	errNotExpression = errors.New("calc: not an expression")
)

func newF() *big.Float { return new(big.Float).SetPrec(prec) }

func mustParse(s string) *big.Float {
	f, ok := newF().SetString(s)
	if !ok {
		panic("calc: bad constant " + s)
	}
	return f
}

var (
	bigPi = mustParse("3.14159265358979323846264338327950288419716939937510582097494459")
	bigE  = mustParse("2.71828182845904523536028747135266249775724709369995957496696763")
)

// constants are the named values, keyed by lower-case name.
var constants = map[string]*big.Float{
	"pi":  bigPi,
	"π":   bigPi,
	"e":   bigE,
	"tau": newF().Mul(bigPi, big.NewFloat(2)),
}

// calculation is an evaluated expression.
type calculation struct {
	expr string // normalized expression, e.g. "1234*5" or "255 in hex"
	raw  string // machine form of a plain result, e.g. "6170", "0xFF"
	base string // "hex", "bin", "oct", "dec" or "" without a conversion

	// val is set for results with units ("10 km in mi", "30 ℃").
	val *value
	// target is the unit converted to, if one was given.
	target *uexpr
	// listing marks a bare quantity ("10 km"), which suggests conversions.
	listing bool
}

// calculate evaluates text with the default environment (no exchange
// rates); see calculateEnv.
func calculate(text string) (calculation, error) {
	return calculateEnv(text, &evalEnv{noCurrency: true})
}

// calculateEnv evaluates text if it looks like an expression: it must start
// with "=" or apply at least one operation (operator, function call,
// implicit multiplication or conversion), or be a number with a unit. It
// returns errNotExpression for plain numbers and constants, and for units
// without a number unless converted ("km in mi"), so that words such as
// "mi" or "pt" stay ordinary searches.
func calculateEnv(text string, env *evalEnv) (calculation, error) {
	s := strings.TrimSpace(text)
	if len(s) > maxInput {
		return calculation{}, fmt.Errorf("%w: input too long", errSyntax)
	}
	s, explicit := strings.CutPrefix(s, "=")
	s = strings.TrimSpace(s)
	if s == "" {
		return calculation{}, errNotExpression
	}
	body, base := cutBase(s)
	toks, err := lex(body)
	if err != nil {
		return calculation{}, err
	}
	src, target, err := splitConversion(toks, env)
	if err != nil {
		return calculation{}, err
	}
	p := parser{toks: src, env: env}
	v, err := p.parse()
	if err != nil && target == nil && len(src) > 2 && conversionWord(src[len(src)-2]) {
		// "10 km in" while typing "10 km in mi": the quantity so far.
		src = append(slices.Clone(src[:len(src)-2]), token{kind: tokEOF})
		p = parser{toks: src, env: env}
		v, err = p.parse()
	}
	if err != nil {
		return calculation{}, err
	}
	if target != nil {
		if v.bare {
			if v, err = quantity(number(newF().SetInt64(1), "1"), v.u, "1 "+v.u.text); err != nil {
				return calculation{}, err
			}
		}
		if v.u == nil {
			return calculation{}, errDim
		}
		if _, _, err := inUnit(v, target); err != nil {
			return calculation{}, err
		}
		return calculation{expr: v.text, val: v, target: target}, nil
	}
	if p.hasUnit {
		if v.bare || !p.hasNum {
			return calculation{}, errNotExpression
		}
		if base != "" || v.u == nil {
			if !v.dim.IsZero() {
				return calculation{}, errDim
			}
			c := calculation{expr: v.text, base: base, raw: formatDecimal(v.x)}
			if base != "" {
				c.expr += " in " + base
				c.raw, err = formatBase(v.x, base)
			}
			return c, err
		}
		return calculation{expr: v.text, val: v, listing: p.ops == 0}, nil
	}
	if !explicit && p.ops == 0 && base == "" {
		return calculation{}, errNotExpression
	}
	c := calculation{expr: joinTokens(src), base: base}
	if base != "" {
		c.expr += " in " + base
		c.raw, err = formatBase(v.x, base)
		return c, err
	}
	c.raw = formatDecimal(v.x)
	return c, nil
}

// splitConversion splits "source in target" at the last conversion keyword
// (in, to, as, =>, 换成, …) outside parentheses that is followed by a
// valid target unit. Without one it returns all tokens and a nil target.
func splitConversion(toks []token, env *evalEnv) ([]token, *uexpr, error) {
	depth := make([]int, len(toks))
	d := 0
	for i, t := range toks {
		switch t.kind {
		case tokLParen:
			d++
		case tokRParen:
			d--
		}
		depth[i] = d
	}
	for i := len(toks) - 2; i >= 1; i-- {
		if !conversionWord(toks[i]) || depth[i] != 0 {
			continue
		}
		tp := parser{toks: toks[i+1:], env: env}
		u, err := tp.parseTarget()
		if err != nil {
			if errors.Is(err, units.ErrNoRates) || errors.Is(err, units.ErrNoRate) {
				return nil, nil, err
			}
			continue
		}
		src := append(slices.Clone(toks[:i]), token{kind: tokEOF})
		return src, u, nil
	}
	return toks, nil, nil
}

// bases are the targets of "in"/"to" conversions.
var bases = []string{"hex", "bin", "oct", "dec"}

// cutBase splits a trailing " in hex" (or " to hex", etc.) from s.
func cutBase(s string) (expr, base string) {
	rest, word, ok := cutLastWord(s)
	if !ok {
		return s, ""
	}
	word = strings.ToLower(word)
	if !slices.Contains(bases, word) {
		return s, ""
	}
	rest, kw, ok := cutLastWord(rest)
	if kw = strings.ToLower(kw); !ok || kw != "in" && kw != "to" {
		return s, ""
	}
	return rest, word
}

// cutLastWord splits s at its last space or tab.
func cutLastWord(s string) (rest, word string, ok bool) {
	s = strings.TrimRight(s, " \t")
	i := strings.LastIndexAny(s, " \t")
	if i < 0 {
		return s, "", false
	}
	return strings.TrimRight(s[:i], " \t"), s[i+1:], true
}

// checkRange rejects values too large or too small to handle.
func checkRange(x *big.Float) error {
	if x.IsInf() {
		return errRange
	}
	if x.Sign() != 0 {
		if e := x.MantExp(nil); e > maxBinExp || e < -maxBinExp {
			return errRange
		}
	}
	return nil
}

// binary applies a binary operator.
func binary(op string, a, b *big.Float) (*big.Float, error) {
	r := newF()
	switch op {
	case "+":
		r.Add(a, b)
	case "-":
		r.Sub(a, b)
	case "*":
		r.Mul(a, b)
	case "/":
		if b.Sign() == 0 {
			return nil, errDivZero
		}
		r.Quo(a, b)
	case "%":
		if b.Sign() == 0 {
			return nil, errDivZero
		}
		// Truncated modulo: the result has the sign of a, like math.Mod.
		q, _ := newF().Quo(a, b).Int(nil)
		r.Sub(a, newF().Mul(b, newF().SetInt(q)))
	case "^":
		var err error
		if r, err = pow(a, b); err != nil {
			return nil, err
		}
	}
	return r, checkRange(r)
}

// pow returns a^b, exactly (to prec bits) for integer exponents.
func pow(a, b *big.Float) (*big.Float, error) {
	n, _ := b.Int(nil)
	if !b.IsInt() || n.CmpAbs(big.NewInt(maxPowExp)) > 0 {
		if a.Sign() < 0 && !b.IsInt() {
			return nil, fmt.Errorf("%w: negative base with fractional exponent", errDomain)
		}
		x, _ := a.Float64()
		y, _ := b.Float64()
		return fromFloat64(math.Pow(x, y))
	}
	neg := n.Sign() < 0
	if neg && a.Sign() == 0 {
		return nil, errDivZero
	}
	n.Abs(n)
	r, base := newF().SetInt64(1), newF().Set(a)
	for {
		if n.Bit(0) == 1 {
			if err := checkRange(r.Mul(r, base)); err != nil {
				return nil, err
			}
		}
		if n.Rsh(n, 1); n.Sign() == 0 {
			break
		}
		if err := checkRange(base.Mul(base, base)); err != nil {
			return nil, err
		}
	}
	if neg {
		r.Quo(newF().SetInt64(1), r)
	}
	return r, nil
}

// factorial returns x! for integers 0 <= x <= maxFactorial.
func factorial(x *big.Float) (*big.Float, error) {
	n, _ := x.Int64()
	if !x.IsInt() || n < 0 || n > maxFactorial {
		return nil, fmt.Errorf("%w: factorial needs an integer in [0, %d]", errDomain, maxFactorial)
	}
	return newF().SetInt(new(big.Int).MulRange(1, n)), nil
}

// fromFloat64 converts a float64 result, rejecting NaN and infinities.
func fromFloat64(f float64) (*big.Float, error) {
	switch {
	case math.IsNaN(f):
		return nil, errDomain
	case math.IsInf(f, 0):
		return nil, errRange
	}
	return newF().SetFloat64(f), nil
}

// function is a built-in function.
type function struct {
	minArgs, maxArgs int // maxArgs < 0: no limit
	fn               func(args []*big.Float) (*big.Float, error)
}

func (f function) apply(name string, args []*big.Float) (*big.Float, error) {
	if len(args) < f.minArgs || f.maxArgs >= 0 && len(args) > f.maxArgs {
		return nil, fmt.Errorf("%w: wrong number of arguments to %s", errSyntax, name)
	}
	r, err := f.fn(args)
	if err != nil {
		return nil, err
	}
	return r, checkRange(r)
}

// unary makes a one-argument function.
func unary(fn func(x *big.Float) (*big.Float, error)) function {
	return function{1, 1, func(args []*big.Float) (*big.Float, error) { return fn(args[0]) }}
}

// viaFloat evaluates fn in float64.
func viaFloat(fn func(float64) float64) function {
	return unary(func(x *big.Float) (*big.Float, error) {
		f, _ := x.Float64()
		return fromFloat64(fn(f))
	})
}

// trig evaluates a trigonometric function in float64 and snaps results that
// are zero up to float64 error (such as sin(pi)) to zero.
func trig(fn func(float64) float64) function {
	return viaFloat(func(x float64) float64 {
		if r := fn(x); !(math.Abs(r) < 1e-15) || math.Abs(x) < 1 { // NaN is kept
			return r
		}
		return 0
	})
}

// logarithm returns the logarithm in base ln(base) = lnBase. It works for
// values outside the float64 range by splitting off the binary exponent.
func logarithm(lnBase float64) function {
	return unary(func(x *big.Float) (*big.Float, error) {
		if x.Sign() <= 0 {
			return nil, fmt.Errorf("%w: logarithm of non-positive number", errDomain)
		}
		if f, _ := x.Float64(); f != 0 && !math.IsInf(f, 0) {
			switch lnBase {
			case math.Ln10:
				return fromFloat64(math.Log10(f))
			case math.Ln2:
				return fromFloat64(math.Log2(f))
			}
			return fromFloat64(math.Log(f) / lnBase)
		}
		mant := new(big.Float)
		exp := x.MantExp(mant)
		m, _ := mant.Float64()
		return fromFloat64((math.Log(m) + float64(exp)*math.Ln2) / lnBase)
	})
}

func floor(x *big.Float) *big.Float {
	i, acc := x.Int(nil) // truncates toward zero
	if acc == big.Above {
		i.Sub(i, big.NewInt(1))
	}
	return newF().SetInt(i)
}

func ceil(x *big.Float) *big.Float {
	i, acc := x.Int(nil)
	if acc == big.Below {
		i.Add(i, big.NewInt(1))
	}
	return newF().SetInt(i)
}

// round rounds half away from zero.
func round(x *big.Float) *big.Float {
	half := big.NewFloat(0.5)
	if x.Sign() >= 0 {
		return floor(newF().Add(x, half))
	}
	r := floor(newF().Sub(half, x))
	return r.Neg(r)
}

// extreme returns min (sign -1) or max (sign 1) of its arguments.
func extreme(sign int) function {
	return function{1, -1, func(args []*big.Float) (*big.Float, error) {
		r := args[0]
		for _, a := range args[1:] {
			if a.Cmp(r) == sign {
				r = a
			}
		}
		return newF().Set(r), nil
	}}
}

// functions are the built-in functions, keyed by lower-case name.
var functions = map[string]function{
	"sqrt": unary(func(x *big.Float) (*big.Float, error) {
		if x.Sign() < 0 {
			return nil, fmt.Errorf("%w: square root of negative number", errDomain)
		}
		return newF().Sqrt(x), nil
	}),
	"cbrt":  viaFloat(math.Cbrt),
	"abs":   unary(func(x *big.Float) (*big.Float, error) { return newF().Abs(x), nil }),
	"round": unary(func(x *big.Float) (*big.Float, error) { return round(x), nil }),
	"floor": unary(func(x *big.Float) (*big.Float, error) { return floor(x), nil }),
	"ceil":  unary(func(x *big.Float) (*big.Float, error) { return ceil(x), nil }),
	"ln":    logarithm(1),
	"log":   logarithm(math.Ln10),
	"log2":  logarithm(math.Ln2),
	"exp":   viaFloat(math.Exp),
	"sin":   trig(math.Sin),
	"cos":   trig(math.Cos),
	"tan":   trig(math.Tan),
	"asin":  viaFloat(math.Asin),
	"acos":  viaFloat(math.Acos),
	"atan":  viaFloat(math.Atan),
	"min":   extreme(-1),
	"max":   extreme(1),
}
