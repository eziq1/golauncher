package calc

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// sigDigits is the number of significant digits of formatted non-integers.
const sigDigits = 15

// plainLimit is the magnitude from which numbers use scientific notation.
var plainLimit = mustParse("1e21")

// formatDecimal formats x in its machine form: integers below 1e21 exactly,
// other values rounded half-even to sigDigits significant digits without
// trailing zeros, in plain notation for 1e-9 <= |x| < 1e21 and scientific
// notation ("1.5e+25") otherwise. It never returns "-0".
func formatDecimal(x *big.Float) string {
	if x.Sign() == 0 {
		return "0"
	}
	if x.IsInt() && newF().Abs(x).Cmp(plainLimit) < 0 {
		i, _ := x.Int(nil)
		return i.String()
	}
	neg, s := x.Sign() < 0, newF().Abs(x).Text('e', sigDigits-1) // "d.dddde+xx"
	mant, expText, _ := strings.Cut(s, "e")
	exp, _ := strconv.Atoi(expText)
	digits := strings.TrimRight(strings.Replace(mant, ".", "", 1), "0")
	var out string
	switch {
	case exp < -9 || exp >= 21:
		out = digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		sign := "+"
		if exp < 0 {
			sign, exp = "-", -exp
		}
		out += "e" + sign + strconv.Itoa(exp)
	case exp < 0:
		out = "0." + strings.Repeat("0", -exp-1) + digits
	case len(digits) <= exp+1:
		out = digits + strings.Repeat("0", exp+1-len(digits))
	default:
		out = digits[:exp+1] + "." + digits[exp+1:]
	}
	if neg {
		out = "-" + out
	}
	return out
}

// formatBase formats the integer x as "0xFF", "0b101", "0o17", or in decimal
// for base "dec".
func formatBase(x *big.Float, base string) (string, error) {
	if !x.IsInt() {
		return "", fmt.Errorf("%w: %s conversion needs an integer", errDomain, base)
	}
	var prefix string
	var radix int
	switch base {
	case "hex":
		prefix, radix = "0x", 16
	case "bin":
		prefix, radix = "0b", 2
	case "oct":
		prefix, radix = "0o", 8
	default:
		return formatDecimal(x), nil
	}
	n, _ := x.Int(nil)
	sign := ""
	if n.Sign() < 0 {
		sign = "-"
		n.Abs(n)
	}
	return sign + prefix + strings.ToUpper(n.Text(radix)), nil
}
