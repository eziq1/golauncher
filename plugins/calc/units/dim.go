// Package units is the unit library of the calculator (docs/unit-conversion.md
// §5.1): dimensions as exponent vectors, units with their factors, offsets
// and aliases (symbols, English names, Chinese names and their pinyin), SI
// and IEC prefixes, and the per-category lists used to suggest conversions.
//
// A quantity is held as a value in base units (metre, kilogram, second,
// kelvin, bit, US dollar, radian, …) together with its dimension; a unit is
// the factor that converts one of it to base units. Temperatures also have
// an offset, and a few units (pixels, rem, currencies) get their factor from
// an Env at evaluation time.
package units

import (
	"strconv"
	"strings"
)

// Base is a base dimension.
type Base int

const (
	Length Base = iota
	Mass
	Time
	Current
	Temperature
	Amount
	Luminosity
	Data
	Currency
	Angle
	nBase
)

// baseSyms are the base units, used to spell dimensions without a named unit.
var baseSyms = [nBase]string{"m", "kg", "s", "A", "K", "mol", "cd", "bit", "USD", "rad"}

// spellOrder is the order of base units in spelled dimensions (kg·m/s²).
var spellOrder = []Base{Mass, Length, Time, Current, Temperature, Amount, Luminosity, Data, Currency, Angle}

// Dim is a dimension: the exponent of each base dimension.
type Dim [nBase]int8

// Of returns the dimension of base b to the power n.
func Of(b Base, n int) Dim {
	var d Dim
	d[b] = int8(n)
	return d
}

// Mul returns the dimension of a product.
func (d Dim) Mul(e Dim) Dim {
	for i := range d {
		d[i] += e[i]
	}
	return d
}

// Div returns the dimension of a quotient.
func (d Dim) Div(e Dim) Dim {
	for i := range d {
		d[i] -= e[i]
	}
	return d
}

// Pow returns the dimension raised to n.
func (d Dim) Pow(n int) Dim {
	for i := range d {
		d[i] *= int8(n)
	}
	return d
}

// Neg returns the reciprocal dimension.
func (d Dim) Neg() Dim { return d.Pow(-1) }

// IsZero reports whether d is dimensionless.
func (d Dim) IsZero() bool { return d == Dim{} }

// Root returns the n-th root of d if every exponent is divisible by n.
func (d Dim) Root(n int) (Dim, bool) {
	for i, e := range d {
		if int(e)%n != 0 {
			return Dim{}, false
		}
		d[i] = e / int8(n)
	}
	return d, true
}

// String spells d with base units, e.g. "kg·m/s²".
func (d Dim) String() string {
	var num, den []string
	for _, i := range spellOrder {
		e := d[i]
		switch {
		case e > 0:
			num = append(num, baseSyms[i]+Superscript(int(e)))
		case e < 0:
			den = append(den, baseSyms[i]+Superscript(int(-e)))
		}
	}
	s := strings.Join(num, "·")
	if s == "" {
		s = "1"
	}
	if len(den) > 0 {
		s += "/" + strings.Join(den, "·")
	}
	return s
}

// Superscript renders an exponent: "" for 1, "²", "³", or "^n".
func Superscript(n int) string {
	switch n {
	case 1:
		return ""
	case 2:
		return "²"
	case 3:
		return "³"
	}
	return "^" + strconv.Itoa(n)
}
