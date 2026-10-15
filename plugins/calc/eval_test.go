package calc

import (
	"errors"
	"math/big"
	"strings"
	"testing"
)

func TestCalculate(t *testing.T) {
	tests := []struct {
		in, raw, expr string
	}{
		// Arithmetic and precedence.
		{"1+2", "3", "1+2"},
		{"1234 * 5", "6170", "1234*5"},
		{"0.1+0.2", "0.3", "0.1+0.2"},
		{"1+2*3", "7", "1+2*3"},
		{"(1+2)*3", "9", "(1+2)*3"},
		{"10-4-3", "3", "10-4-3"},
		{"100/10/5", "2", "100/10/5"},
		{"1/3", "0.333333333333333", "1/3"},
		{"2/3", "0.666666666666667", "2/3"},
		{"10 % 3", "1", "10%3"},
		{"-7 % 3", "-1", "-7%3"},
		{"7.5 % 2", "1.5", "7.5%2"},
		{"3 × 4 ÷ 2", "6", "3*4/2"},
		{"5 − 7", "-2", "5-7"},
		{"1 - 2.5", "-1.5", "1-2.5"},
		{"0.1*3 - 0.3", "0", "0.1*3-0.3"},
		{"-(1-1)", "0", "-(1-1)"},
		// Powers.
		{"2^10", "1024", "2^10"},
		{"2**10", "1024", "2^10"},
		{"2^3^2", "512", "2^3^2"},
		{"-2^2", "-4", "-2^2"},
		{"(-2)^2", "4", "(-2)^2"},
		{"2^-2", "0.25", "2^-2"},
		{"0^0", "1", "0^0"},
		{"4^0.5", "2", "4^0.5"},
		{"2^0.5", "1.4142135623731", "2^0.5"},
		{"1^10000000", "1", "1^10000000"},
		{"2^64", "18446744073709551616", "2^64"},
		{"2^70", "1.18059162071741e+21", "2^70"},
		{"0.5^40", "9.09494701772928e-13", "0.5^40"},
		// Unary signs.
		{"+3*-2", "-6", "+3*-2"},
		{"--3+0", "3", "--3+0"},
		// Implicit multiplication.
		{"2pi", "6.28318530717959", "2pi"},
		{"3(4+5)", "27", "3(4+5)"},
		{"(1+1)(2+2)", "8", "(1+1)(2+2)"},
		{"2sqrt(16)", "8", "2sqrt(16)"},
		{"2e", "5.43656365691809", "2e"},
		{"-2pi", "-6.28318530717959", "-2pi"},
		// Factorial.
		{"5!", "120", "5!"},
		{"0!", "1", "0!"},
		{"3!!", "720", "3!!"},
		{"-3!", "-6", "-3!"},
		{"2^3!", "64", "2^3!"},
		{"20!", "2432902008176640000", "20!"},
		{"25!", "1.5511210043331e+25", "25!"},
		{"30!", "2.65252859812191e+32", "30!"},
		// Constants.
		{"pi*2", "6.28318530717959", "pi*2"},
		{"π+0", "3.14159265358979", "π+0"},
		{"PI + E", "5.85987448204884", "pi+e"},
		{"tau/2", "3.14159265358979", "tau/2"},
		// Functions.
		{"sqrt(2)^2", "2", "sqrt(2)^2"},
		{"SQRT(144)", "12", "sqrt(144)"},
		{"cbrt(27)", "3", "cbrt(27)"},
		{"abs(-5)", "5", "abs(-5)"},
		{"round(2.5)", "3", "round(2.5)"},
		{"round(-2.5)", "-3", "round(-2.5)"},
		{"round(2.4)", "2", "round(2.4)"},
		{"floor(-2.5)", "-3", "floor(-2.5)"},
		{"floor(2.5)", "2", "floor(2.5)"},
		{"ceil(2.1)", "3", "ceil(2.1)"},
		{"ceil(-2.1)", "-2", "ceil(-2.1)"},
		{"ln(e)", "1", "ln(e)"},
		{"log(1000)", "3", "log(1000)"},
		{"log2(8)", "3", "log2(8)"},
		{"log(10^400)", "400", "log(10^400)"},
		{"log(10^-400)", "-400", "log(10^-400)"},
		{"exp(1)", "2.71828182845905", "exp(1)"},
		{"sin(pi/2)", "1", "sin(pi/2)"},
		{"sin(pi)", "0", "sin(pi)"},
		{"cos(pi)", "-1", "cos(pi)"},
		{"cos(pi/2)", "0", "cos(pi/2)"},
		{"tan(pi/4)", "1", "tan(pi/4)"},
		{"sin(1e-20)", "1e-20", "sin(1e-20)"},
		{"asin(1)*2", "3.14159265358979", "asin(1)*2"},
		{"acos(1)", "0", "acos(1)"},
		{"atan(1)*4", "3.14159265358979", "atan(1)*4"},
		{"min(3, 1, 2)", "1", "min(3,1,2)"},
		{"max(3, 1, 2)", "3", "max(3,1,2)"},
		{"max(-1)", "-1", "max(-1)"},
		// Number literals.
		{"1.5e3+0", "1500", "1.5e3+0"},
		{"2E-3*1", "0.002", "2E-3*1"},
		{".5+.25", "0.75", ".5+.25"},
		{"0xFF+1", "256", "0xFF+1"},
		{"0b1010+0", "10", "0b1010+0"},
		{"0o17+0", "15", "0o17+0"},
		// Base conversions.
		{"255 in hex", "0xFF", "255 in hex"},
		{"255 to HEX", "0xFF", "255 in hex"},
		{"255 in bin", "0b11111111", "255 in bin"},
		{"255 in oct", "0o377", "255 in oct"},
		{"0xff in dec", "255", "0xff in dec"},
		{"-255 in hex", "-0xFF", "-255 in hex"},
		{"16*16 in hex", "0x100", "16*16 in hex"},
		// Explicit "=".
		{"=42", "42", "42"},
		{"= pi", "3.14159265358979", "pi"},
		{"=-5", "-5", "-5"},
		// Formatting.
		{"1e20+1", "100000000000000000001", "1e20+1"},
		{"1e21*1", "1e+21", "1e21*1"},
		{"123456789*1e20", "1.23456789e+28", "123456789*1e20"},
		{"1/1e9", "0.000000001", "1/1e9"},
		{"1.5/1e9", "0.0000000015", "1.5/1e9"},
		{"1/1e10", "1e-10", "1/1e10"},
		{"-1.25/1e12", "-1.25e-12", "-1.25/1e12"},
		{"1234567.891*1", "1234567.891", "1234567.891*1"},
		{"0.1*0.1", "0.01", "0.1*0.1"},
		{"999999999999999.9+0", "1000000000000000", "999999999999999.9+0"},
		{"0.00000000099999999999999999*1", "0.000000001", "0.00000000099999999999999999*1"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			c, err := calculate(tt.in)
			if err != nil {
				t.Fatalf("calculate(%q) error: %v", tt.in, err)
			}
			if c.raw != tt.raw {
				t.Errorf("raw = %q, want %q", c.raw, tt.raw)
			}
			if c.expr != tt.expr {
				t.Errorf("expr = %q, want %q", c.expr, tt.expr)
			}
		})
	}
}

func TestCalculateErrors(t *testing.T) {
	tests := []struct {
		in   string
		want error
	}{
		// Not expressions.
		{"", errNotExpression},
		{"=", errNotExpression},
		{"42", errNotExpression},
		{"3.14", errNotExpression},
		{"pi", errNotExpression},
		{"e", errNotExpression},
		{"-5", errNotExpression},
		{"(42)", errNotExpression},
		// Words.
		{"code", errUnknown},
		{"notepad", errUnknown},
		{"2x", errUnknown},
		{"foo(1)", errUnknown},
		{"1+foo", errUnknown},
		// Syntax.
		{"1+", errSyntax},
		{"*2", errSyntax},
		{"(1+2", errSyntax},
		{"1+2)", errSyntax},
		{"1 2", errSyntax},
		{"1,234+1", errSyntax},
		{"1+2 $", errSyntax},
		{"1.2.3", errSyntax},
		{"sqrt 4", errSyntax},
		{"sqrt(1,2)", errSyntax},
		{"sqrt()", errSyntax},
		{"min()", errSyntax},
		{"max(1 2)", errSyntax},
		{"sqrt(1+)", errSyntax},
		{"max(1,", errSyntax},
		{"2*(1+3", errSyntax},
		{"-", errSyntax},
		{"1+(+)", errSyntax},
		{"1+2!3", errSyntax},
		{"=" + strings.Repeat("(", 200) + "1" + strings.Repeat(")", 200), errSyntax},
		{strings.Repeat("1+", 600) + "1", errSyntax},
		// Arithmetic.
		{"1/0", errDivZero},
		{"5%0", errDivZero},
		{"0^-1", errDivZero},
		{"sqrt(-1)", errDomain},
		{"ln(0)", errDomain},
		{"log(-1)", errDomain},
		{"asin(2)", errDomain},
		{"(-8)^(1/3)", errDomain},
		{"1.5!", errDomain},
		{"(-1)!", errDomain},
		{"1001!", errDomain},
		{"1.5 in hex", errDomain},
		{"sin(10^400)", errDomain},
		{"exp(1000)", errRange},
		{"10^100000", errRange},
		{"0.1^100000", errRange},
		{"10^5000*10^5000", errRange},
		{"2^(2^30)", errRange},
		{"1e9999999+1", errRange},
		{"(-2)^2000001", errRange},
	}
	for _, tt := range tests {
		name := tt.in
		if len(name) > 30 {
			name = name[:30]
		}
		t.Run(name, func(t *testing.T) {
			c, err := calculate(tt.in)
			if !errors.Is(err, tt.want) {
				t.Errorf("calculate(%q) = %+v, %v; want error %v", tt.in, c, err, tt.want)
			}
		})
	}
}

func TestCutBase(t *testing.T) {
	tests := []struct{ in, expr, base string }{
		{"255 in hex", "255", "hex"},
		{"255  to  bin ", "255", "bin"},
		{"255 hex", "255 hex", ""},
		{"in hex", "in hex", ""},
		{"255 in hexa", "255 in hexa", ""},
		{"255", "255", ""},
	}
	for _, tt := range tests {
		expr, base := cutBase(tt.in)
		if expr != tt.expr || base != tt.base {
			t.Errorf("cutBase(%q) = %q, %q; want %q, %q", tt.in, expr, base, tt.expr, tt.base)
		}
	}
}

func TestFormatDecimal(t *testing.T) {
	tests := []struct{ in, want string }{
		{"0", "0"},
		{"-0", "0"},
		{"1", "1"},
		{"-1234567", "-1234567"},
		{"0.5", "0.5"},
		{"0.125", "0.125"},
		{"1e21", "1e+21"},
		{"1.2345e25", "1.2345e+25"},
		{"-1.2345e25", "-1.2345e+25"},
		{"1e-9", "0.000000001"},
		{"1e-10", "1e-10"},
		{"123456789012345678", "123456789012345678"},
		{"1234567890.123456789", "1234567890.12346"},
		{"0.1234567890123456", "0.123456789012346"},
		// Round half to even at the 15th digit.
		{"100000000000000.5", "100000000000000"},
		{"100000000000001.5", "100000000000002"},
		{"999999999999999.5", "1000000000000000"},
	}
	for _, tt := range tests {
		x, ok := newF().SetString(tt.in)
		if !ok {
			t.Fatalf("bad input %q", tt.in)
		}
		if got := formatDecimal(x); got != tt.want {
			t.Errorf("formatDecimal(%s) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatBase(t *testing.T) {
	tests := []struct {
		in   int64
		base string
		want string
	}{
		{255, "hex", "0xFF"},
		{0, "hex", "0x0"},
		{5, "bin", "0b101"},
		{8, "oct", "0o10"},
		{-8, "oct", "-0o10"},
		{1234, "dec", "1234"},
	}
	for _, tt := range tests {
		got, err := formatBase(big.NewFloat(float64(tt.in)), tt.base)
		if err != nil || got != tt.want {
			t.Errorf("formatBase(%d, %s) = %q, %v; want %q", tt.in, tt.base, got, err, tt.want)
		}
	}
}

func TestLexNumber(t *testing.T) {
	tests := []struct {
		in   string
		text string
	}{
		{"0x1Fg", "0x1F"},
		{"0b012", "0b01"},
		{"0o78", "0o7"},
		{"0xg", "0"},
		{"1e", "1"},
		{"1e+", "1"},
		{"1e+5x", "1e+5"},
		{"12.", "12."},
		{"7", "7"},
	}
	for _, tt := range tests {
		tok, n, err := lexNumber(tt.in)
		if err != nil || tok.text != tt.text || n != len(tt.text) {
			t.Errorf("lexNumber(%q) = %q, %d, %v; want %q", tt.in, tok.text, n, err, tt.text)
		}
	}
}

func BenchmarkCalculate(b *testing.B) {
	for b.Loop() {
		if _, err := calculate("sqrt(1234 * 5) + 2^10 / 3 - sin(pi/4)"); err != nil {
			b.Fatal(err)
		}
	}
}
