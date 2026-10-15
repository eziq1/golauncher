package calc

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/eziq1/golauncher/plugins/calc/units"
)

var (
	// errDim reports a dimension mismatch, e.g. "1 m + 1 s" or "1 kg in m".
	errDim = errors.New("calc: incompatible units")
)

// uexpr is a unit expression: a single unit, or units combined with
// products, quotients and integer powers ("km/h", "m/s²", "kW·h").
type uexpr struct {
	text   string // display, e.g. "km/h"
	factor *big.Float
	dim    units.Dim
	// offset is set for a single absolute temperature unit (℃, ℉, K).
	offset *big.Float
	// unit is the single unit this is, or the named unit it equals
	// (kg·m/s² is a newton); nil for other compounds.
	unit *units.Unit
	cat  units.Category
}

// isCurrency reports whether u is a currency.
func (u *uexpr) isCurrency() bool { return u.cat == units.CatCurrency }

// single returns the expression of one unit.
func single(un *units.Unit, env *units.Env) (*uexpr, error) {
	f, err := un.Factor(env)
	if err != nil {
		return nil, err
	}
	return &uexpr{text: un.Sym, factor: f, dim: un.Dim, offset: un.Offset, unit: un, cat: un.Cat}, nil
}

// compound wraps text for use as an operand of "/" or a power.
func compound(text string) string {
	if strings.ContainsAny(text, "/·") || strings.Contains(text, " ") {
		return "(" + text + ")"
	}
	return text
}

// combine returns the product (or quotient) of two unit expressions and
// names it after a unit with the same factor if there is one ("km/h").
func combine(a, b *uexpr, div bool, env *units.Env) *uexpr {
	r := &uexpr{factor: newF(), dim: a.dim.Mul(b.dim)}
	if div {
		r.factor.Quo(a.factor, b.factor)
		r.dim = a.dim.Div(b.dim)
		r.text = a.text + "/" + compound(b.text)
	} else {
		r.factor.Mul(a.factor, b.factor)
		r.text = a.text + "·" + b.text
	}
	r.cat = units.CategoryOf(r.dim)
	switch {
	case div && isVolume(a.cat) && b.cat == units.CatLength:
		r.cat = units.CatFuel
	case !div && (a.cat == units.CatForce && b.cat == units.CatLength || a.cat == units.CatLength && b.cat == units.CatForce):
		r.cat = units.CatTorque
	}
	r.name(env)
	return r
}

func isVolume(c units.Category) bool { return c == units.CatVolume || c == units.CatCooking }

// name sets the unit and text of r to the unit of its category with the
// same factor, if any.
func (r *uexpr) name(env *units.Env) {
	if r.cat == units.CatNone {
		return
	}
	tab := units.Default()
	if u := tab.BySym(r.text); u != nil && u.Dim == r.dim && u.Offset == nil {
		if f, err := u.Factor(env); err == nil && units.Equal(f, r.factor) {
			r.unit = u // as written: "kg/L", not "g/cm³"
			return
		}
	}
	if u := tab.Match(r.dim, r.factor, r.cat, env); u != nil {
		r.unit, r.text = u, u.Sym
	}
}

// power returns u raised to the integer n.
func power(u *uexpr, n int, env *units.Env) (*uexpr, error) {
	if u.offset != nil && u.offset.Sign() != 0 {
		return nil, fmt.Errorf("%w: power of a temperature", errDim)
	}
	f, err := pow(u.factor, newF().SetInt64(int64(n)))
	if err != nil {
		return nil, err
	}
	text := u.text
	if u.unit == nil || strings.ContainsAny(text, "/·") {
		text = "(" + text + ")"
	}
	r := &uexpr{text: text + units.Superscript(n), factor: f, dim: u.dim.Pow(n)}
	r.cat = units.CategoryOf(r.dim)
	r.name(env)
	return r, nil
}

// value is an evaluated (sub-)expression.
type value struct {
	x   *big.Float // in base units
	dim units.Dim
	// u is the unit the value was written in; nil for plain numbers.
	u *uexpr
	// abs marks an absolute temperature, which has an offset (30 ℃ is
	// 303.15 K); temperature differences (Δ5 ℃, 30 ℃ − 25 ℃) are not.
	abs bool
	// bare marks a unit written without a number ("km" in "km in mi").
	bare bool
	// text is the expression as displayed in results.
	text string
}

func number(x *big.Float, text string) *value { return &value{x: x, text: text} }

// plain reports whether v is a dimensionless number without a unit.
func (v *value) plain() bool { return v.u == nil && v.dim.IsZero() }

// offset returns the temperature offset of v's unit, or nil.
func (v *value) offset() *big.Float {
	if v.u == nil || v.u.offset == nil || v.u.offset.Sign() == 0 {
		return nil
	}
	return v.u.offset
}

// quantity makes n units of u: "30 ℃", "10 km".
func quantity(n *value, u *uexpr, text string) (*value, error) {
	if !n.plain() {
		return nil, fmt.Errorf("%w: a unit must follow a number", errSyntax)
	}
	v := &value{x: newF().Mul(n.x, u.factor), dim: u.dim, u: u, text: text}
	if u.offset != nil {
		v.x.Add(v.x, u.offset)
		v.abs = true
	}
	return v, checkRange(v.x)
}

// delta returns the temperature difference of v: Δ5 ℃ is 5 K.
func delta(v *value) (*value, error) {
	if !v.abs {
		return nil, fmt.Errorf("%w: Δ needs a temperature", errDim)
	}
	r := *v
	r.x = newF().Sub(v.x, v.u.offset)
	r.abs = false
	r.text = "Δ" + v.text
	return &r, nil
}

// negate returns -v. For an absolute temperature it negates the number
// written in its unit: -(30 ℃) is -30 ℃.
func negate(v *value) *value {
	r := *v
	r.x = newF().Neg(v.x)
	if v.abs {
		r.x.Add(r.x, newF().Mul(v.u.offset, big.NewFloat(2)))
	}
	r.bare = false
	return &r
}

// addValues adds or subtracts quantities of the same dimension. Adding to
// an absolute temperature adds a difference (30 ℃ + 5 ℃ = 35 ℃);
// subtracting two absolute temperatures gives a difference.
func addValues(a, b *value, sub bool, text string) (*value, error) {
	if a.dim != b.dim {
		return nil, errDim
	}
	r := &value{x: newF(), dim: a.dim, u: a.u, text: text}
	if r.u == nil {
		r.u = b.u
	}
	bx := b.x
	switch {
	case a.abs && b.abs && sub: // 30 ℃ − 25 ℃ = Δ5 ℃
	case a.abs && b.abs: // 30 ℃ + 5 ℃ = 35 ℃
		bx = newF().Sub(b.x, b.u.offset)
		r.abs = true
	case a.abs:
		r.abs = true
	case b.abs && sub:
		return nil, fmt.Errorf("%w: difference minus temperature", errDim)
	case b.abs:
		r.abs, r.u = true, b.u
	}
	if sub {
		r.x.Sub(a.x, bx)
	} else {
		r.x.Add(a.x, bx)
	}
	return r, checkRange(r.x)
}

// mulValues multiplies or divides two values.
func mulValues(a, b *value, div bool, text string, env *units.Env) (*value, error) {
	for _, v := range []*value{a, b} {
		if v.abs && v.offset() != nil {
			return nil, fmt.Errorf("%w: product of a temperature", errDim)
		}
	}
	op := "*"
	if div {
		op = "/"
	}
	x, err := binary(op, a.x, b.x)
	if err != nil {
		return nil, err
	}
	r := &value{x: x, dim: a.dim.Mul(b.dim), text: text}
	if div {
		r.dim = a.dim.Div(b.dim)
	}
	switch {
	case a.u != nil && b.u != nil:
		r.u = combine(a.u, b.u, div, env)
	case a.u != nil:
		r.u = a.u
	case b.u != nil && !div:
		r.u = b.u
	case b.u != nil:
		r.u = combine(&uexpr{text: "1", factor: newF().SetInt64(1)}, b.u, true, env)
	}
	if r.dim.IsZero() {
		r.u = nil // km/km
	}
	return r, nil
}

// inUnit returns v expressed in u and whether it is a temperature
// difference shown in a unit with an offset (Δ).
func inUnit(v *value, u *uexpr) (*big.Float, bool, error) {
	if v.dim != u.dim {
		if units.Reciprocal(v.dim, u.dim) && (v.u != nil && v.u.cat == units.CatFuel || u.cat == units.CatFuel) {
			if v.x.Sign() == 0 {
				return nil, false, errDivZero
			}
			d := newF().Mul(v.x, u.factor)
			return newF().Quo(newF().SetInt64(1), d), false, nil
		}
		return nil, false, errDim
	}
	x := newF().Set(v.x)
	isDelta := false
	if u.offset != nil {
		if v.abs {
			x.Sub(x, u.offset)
		} else {
			isDelta = u.offset.Sign() != 0
		}
	}
	return x.Quo(x, u.factor), isDelta, nil
}
