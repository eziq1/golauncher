package units

import (
	"errors"
	"math/big"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/mozillazg/go-pinyin"
)

// Prec is the precision of factors, in bits (the calculator's precision).
const Prec = 128

// Category groups the units offered as conversions of each other. Several
// categories may share a dimension (typography and length, cooking and
// volume, torque and energy).
type Category string

const (
	CatNone        Category = ""
	CatLength      Category = "length"
	CatArea        Category = "area"
	CatVolume      Category = "volume"
	CatMass        Category = "mass"
	CatTemperature Category = "temperature"
	CatData        Category = "data"
	CatDataRate    Category = "datarate"
	CatSpeed       Category = "speed"
	CatDuration    Category = "duration"
	CatCurrency    Category = "currency"
	CatPressure    Category = "pressure"
	CatEnergy      Category = "energy"
	CatPower       Category = "power"
	CatAngle       Category = "angle"
	CatFrequency   Category = "frequency"
	CatFuel        Category = "fuel"
	CatForce       Category = "force"
	CatTorque      Category = "torque"
	CatDensity     Category = "density"
	CatTypography  Category = "typography"
	CatCooking     Category = "cooking"
)

// Dyn marks units whose factor depends on the Env.
type Dyn uint8

const (
	DynNone     Dyn = iota
	DynPixel        // 1/DPI inch
	DynRem          // RemBase pixels
	DynCurrency     // 1/Rate(Code) US dollars
)

// Unit is a unit of measurement.
type Unit struct {
	Sym string // display symbol, e.g. "km", "℃", "mpg"
	Cat Category
	Dim Dim
	// Offset is set for absolute temperatures: base = value*factor + Offset.
	Offset *big.Float
	Dyn    Dyn
	Code   string // ISO 4217 code of a currency
	// Reciprocal marks fuel economy units, which convert to fuel
	// consumption units (and back) through the reciprocal.
	Reciprocal bool
	// Chinese is set for Chinese market units (市制), offered as
	// conversions only when the user asks for them.
	Chinese bool

	factor *big.Float
}

var (
	// ErrNoRates means no exchange rates are available at all.
	ErrNoRates = errors.New("units: no exchange rates")
	// ErrNoRate means the rates lack this currency.
	ErrNoRate = errors.New("units: no exchange rate for currency")
)

// Env holds what dynamic units depend on.
type Env struct {
	DPI     float64 // pixels per inch (default 96)
	RemBase float64 // pixels per rem and em (default 16)
	// Rate returns how many units of the currency one US dollar buys, or
	// ErrNoRates / ErrNoRate. Nil means no rates.
	Rate func(code string) (*big.Float, error)
}

var inch = ratFloat("0.0254")

// Factor returns the value of one u in base units.
func (u *Unit) Factor(env *Env) (*big.Float, error) {
	switch u.Dyn {
	case DynPixel, DynRem:
		dpi, rem := 96.0, 16.0
		if env != nil && env.DPI > 0 {
			dpi = env.DPI
		}
		if env != nil && env.RemBase > 0 {
			rem = env.RemBase
		}
		f := newF().Quo(inch, newF().SetFloat64(dpi))
		if u.Dyn == DynRem {
			f.Mul(f, newF().SetFloat64(rem))
		}
		return f, nil
	case DynCurrency:
		if env == nil || env.Rate == nil {
			return nil, ErrNoRates
		}
		r, err := env.Rate(u.Code)
		if err != nil {
			return nil, err
		}
		if r.Sign() <= 0 {
			return nil, ErrNoRate
		}
		return newF().Quo(newF().SetInt64(1), r), nil
	}
	return u.factor, nil
}

func newF() *big.Float { return new(big.Float).SetPrec(Prec) }

// ratFloat evaluates a factor written as decimal literals joined by "*"
// and "/" (left to right), exactly with big.Rat and rounded once.
func ratFloat(expr string) *big.Float {
	r := new(big.Rat)
	op := byte('*')
	first := true
	for expr != "" {
		i := strings.IndexAny(expr, "*/")
		lit := expr
		next := byte(0)
		if i >= 0 {
			lit, next, expr = expr[:i], expr[i], expr[i+1:]
		} else {
			expr = ""
		}
		v, ok := new(big.Rat).SetString(strings.TrimSpace(lit))
		if !ok {
			panic("units: bad factor " + lit)
		}
		switch {
		case first:
			r.Set(v)
			first = false
		case op == '*':
			r.Mul(r, v)
		default:
			r.Quo(r, v)
		}
		op = next
	}
	return newF().SetRat(r)
}

// Table is the unit library: every unit and alias.
type Table struct {
	exact map[string]*Unit // case-sensitive symbols
	fold  map[string]*Unit // lower-cased symbols, names, phrases and pinyin
	units []*Unit          // in definition order
	bySym map[string]*Unit // display symbol → unit
	// maxWords is the most words of a phrase alias ("us fl oz").
	maxWords int
}

// Default returns the unit table, built once.
var Default = sync.OnceValue(build)

// Lookup returns the unit spelled name: a case-sensitive symbol first
// ("Mb" is a megabit, "MB" a megabyte), then a case-insensitive symbol,
// name or pinyin of at least two characters (so "k" is not a kelvin).
func (t *Table) Lookup(name string) (*Unit, bool) {
	if u, ok := t.exact[name]; ok {
		return u, true
	}
	if utf8.RuneCountInString(name) < 2 && !isHan(name) {
		return nil, false
	}
	u, ok := t.fold[fold(name)]
	return u, ok
}

// LookupPhrase returns the unit named by the longest phrase made of the
// first words (at most MaxWords), and the number of words it used.
func (t *Table) LookupPhrase(words []string) (*Unit, int) {
	for n := min(len(words), t.maxWords); n >= 2; n-- {
		if u, ok := t.fold[fold(strings.Join(words[:n], " "))]; ok {
			return u, n
		}
	}
	if len(words) > 0 {
		if u, ok := t.Lookup(words[0]); ok {
			return u, 1
		}
	}
	return nil, 0
}

// MaxWords is the most words a phrase alias has.
func (t *Table) MaxWords() int { return t.maxWords }

// BySym returns the unit with display symbol sym.
func (t *Table) BySym(sym string) *Unit { return t.bySym[sym] }

// Units returns every unit in definition order.
func (t *Table) Units() []*Unit { return t.units }

// Match returns a unit of category cat (any category for CatNone) with
// dimension d whose factor equals f, preferring earlier definitions.
func (t *Table) Match(d Dim, f *big.Float, cat Category, env *Env) *Unit {
	for _, u := range t.units {
		if u.Dim != d || u.Offset != nil || cat != CatNone && u.Cat != cat {
			continue
		}
		if u.Dyn == DynCurrency {
			continue
		}
		uf, err := u.Factor(env)
		if err == nil && Equal(uf, f) {
			return u
		}
	}
	return nil
}

// Equal reports whether a and b agree to about 30 significant digits.
func Equal(a, b *big.Float) bool {
	if a.Sign() != b.Sign() {
		return false
	}
	if a.Sign() == 0 {
		return true
	}
	d := newF().Sub(a, b)
	d.Quo(d, a)
	d.Abs(d)
	return d.Cmp(big.NewFloat(1e-30)) < 0
}

// fold normalizes a name for case-insensitive lookup.
func fold(s string) string { return strings.ToLower(s) }

func (t *Table) addExact(s string, u *Unit) {
	if _, ok := t.exact[s]; !ok {
		t.exact[s] = u
	}
}

func (t *Table) addFold(s string, u *Unit) {
	k := fold(s)
	if _, ok := t.fold[k]; ok {
		return
	}
	t.fold[k] = u
	if n := strings.Count(k, " ") + 1; n > t.maxWords {
		t.maxWords = n
	}
}

// pinyinArgs makes pinyin without tones.
var pinyinArgs = pinyin.NewArgs()

// pinyinKeys returns the full pinyin ("gongli") and, for names of two or
// more characters, the initials ("gl") of a Chinese name.
func pinyinKeys(s string) []string {
	syl := pinyin.LazyPinyin(s, pinyinArgs)
	if len(syl) != utf8.RuneCountInString(s) {
		return nil // not all Chinese
	}
	keys := []string{strings.Join(syl, "")}
	if len(syl) >= 2 {
		var b strings.Builder
		for _, x := range syl {
			b.WriteByte(x[0])
		}
		keys = append(keys, b.String())
	}
	return keys
}

// isHan reports whether s is all Chinese characters.
func isHan(s string) bool {
	for _, r := range s {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return s != ""
}

// build makes the table from defs: symbols first, then names, then the
// pinyin of Chinese names, so that pinyin never shadows a real symbol or
// name ("mi" stays a mile, not 米).
func build() *Table {
	t := &Table{exact: map[string]*Unit{}, fold: map[string]*Unit{}, bySym: map[string]*Unit{}, maxWords: 1}
	type entry struct {
		u     *Unit
		syms  []string
		names []string
		zh    []string
	}
	var entries []entry
	for _, d := range defs() {
		base := &Unit{Sym: d.sym, Cat: d.cat, Dim: d.dim, Dyn: d.dyn, Reciprocal: d.recip, Chinese: d.chinese}
		if d.dyn == DynCurrency {
			base.Code = d.sym
		}
		if d.dyn == DynNone {
			base.factor = ratFloat(d.factor)
		}
		if d.offset != "" {
			base.Offset = ratFloat(d.offset)
		}
		syms := append([]string{d.sym}, d.syms...)
		if d.displayOnly {
			syms = d.syms
		}
		entries = append(entries, entry{base, syms, d.names, d.zh})
		for _, ps := range d.prefixes {
			p := prefixes[ps]
			sym := ps + d.sym
			u := &Unit{Sym: sym, Cat: d.cat, Dim: d.dim, Reciprocal: d.recip}
			u.factor = newF().Mul(base.factor, p.factor())
			e := entry{u: u, syms: []string{sym}, zh: d.zhPrefixed[ps]}
			if !d.strictPrefixes {
				for _, a := range p.syms {
					e.syms = append(e.syms, a+d.sym)
				}
			}
			for _, n := range d.pnames {
				e.names = append(e.names, p.name+n)
			}
			entries = append(entries, e)
		}
	}
	for _, e := range entries {
		t.units = append(t.units, e.u)
		if _, ok := t.bySym[e.u.Sym]; !ok {
			t.bySym[e.u.Sym] = e.u
		}
		for _, s := range e.syms {
			t.addExact(s, e.u)
		}
	}
	for _, e := range entries {
		for _, s := range e.syms {
			t.addFold(s, e.u)
		}
	}
	for _, e := range entries {
		for _, n := range e.names {
			t.addFold(n, e.u)
		}
		for _, z := range e.zh {
			t.addFold(z, e.u)
		}
	}
	for _, e := range entries {
		for _, z := range append(slices.Clone(e.zh), e.u.Sym) {
			if !isHan(z) {
				continue
			}
			for _, k := range pinyinKeys(z) {
				if _, ok := t.exact[k]; !ok {
					t.addFold(k, e.u)
				}
			}
		}
	}
	return t
}

// prefix is an SI or IEC prefix.
type prefix struct {
	name string
	base int64 // 10 or 2
	exp  int
	syms []string // other spellings
}

func (p prefix) factor() *big.Float {
	r := new(big.Rat).SetInt64(1)
	b := new(big.Rat).SetInt64(p.base)
	for range max(p.exp, -p.exp) {
		r.Mul(r, b)
	}
	if p.exp < 0 {
		r.Inv(r)
	}
	return newF().SetRat(r)
}

// prefixes by key; a definition lists the keys it accepts.
var prefixes = map[string]prefix{
	"n":  {name: "nano", base: 10, exp: -9},
	"µ":  {name: "micro", base: 10, exp: -6, syms: []string{"u", "μ"}},
	"m":  {name: "milli", base: 10, exp: -3},
	"c":  {name: "centi", base: 10, exp: -2},
	"d":  {name: "deci", base: 10, exp: -1},
	"h":  {name: "hecto", base: 10, exp: 2},
	"k":  {name: "kilo", base: 10, exp: 3},
	"K":  {name: "kilo", base: 10, exp: 3, syms: []string{"k"}}, // data: KB, kB
	"M":  {name: "mega", base: 10, exp: 6},
	"G":  {name: "giga", base: 10, exp: 9},
	"T":  {name: "tera", base: 10, exp: 12},
	"P":  {name: "peta", base: 10, exp: 15},
	"E":  {name: "exa", base: 10, exp: 18},
	"Ki": {name: "kibi", base: 2, exp: 10},
	"Mi": {name: "mebi", base: 2, exp: 20},
	"Gi": {name: "gibi", base: 2, exp: 30},
	"Ti": {name: "tebi", base: 2, exp: 40},
	"Pi": {name: "pebi", base: 2, exp: 50},
	"Ei": {name: "exbi", base: 2, exp: 60},
}

// Listing returns the units suggested for a quantity of category cat:
// imperial units first when imperial is set, Chinese market units appended
// when chinese is set.
func Listing(cat Category, imperial, chinese bool) []*Unit {
	l, ok := listings[cat]
	if !ok {
		return nil
	}
	syms := l.metric
	if imperial && l.imperial != nil {
		syms = l.imperial
	}
	if chinese {
		syms = append(slices.Clone(syms), l.zh...)
	}
	t := Default()
	out := make([]*Unit, 0, len(syms))
	for _, s := range syms {
		if u := t.BySym(s); u != nil {
			out = append(out, u)
		}
	}
	return out
}

// ByMagnitude reports whether conversions of cat are picked by how readable
// the number is (data sizes, durations) rather than by a fixed order.
func ByMagnitude(cat Category) bool { return listings[cat].magnitude }

// CategoryOf returns the category of quantities of dimension d that do not
// come from a single unit, or CatNone.
func CategoryOf(d Dim) Category {
	for _, c := range dimCategories {
		if c.dim == d {
			return c.cat
		}
	}
	return CatNone
}

// Reciprocal reports whether quantities of dimensions a and b convert
// through the reciprocal (fuel consumption and fuel economy).
func Reciprocal(a, b Dim) bool {
	return a == b.Neg() && (CategoryOf(a) == CatFuel || CategoryOf(b) == CatFuel)
}
