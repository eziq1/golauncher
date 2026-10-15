package units

import (
	"errors"
	"math/big"
	"testing"
)

func TestDim(t *testing.T) {
	speed := Of(Length, 1).Div(Of(Time, 1))
	if got := speed.String(); got != "m/s" {
		t.Errorf("speed = %q", got)
	}
	force := Of(Mass, 1).Mul(speed).Div(Of(Time, 1))
	if got := force.String(); got != "kg·m/s²" {
		t.Errorf("force = %q", got)
	}
	if got := Of(Time, 1).Neg().String(); got != "1/s" {
		t.Errorf("1/s = %q", got)
	}
	if got := Of(Length, 4).String(); got != "m^4" {
		t.Errorf("m^4 = %q", got)
	}
	if !(Dim{}).IsZero() || speed.IsZero() {
		t.Error("IsZero")
	}
	if r, ok := Of(Length, 2).Root(2); !ok || r != Of(Length, 1) {
		t.Errorf("Root(m²) = %v, %v", r, ok)
	}
	if _, ok := Of(Length, 3).Root(2); ok {
		t.Error("Root(m³, 2) succeeded")
	}
	if Of(Length, 1).Pow(3) != Of(Length, 3) {
		t.Error("Pow")
	}
}

func TestRatFloat(t *testing.T) {
	for _, tt := range []struct {
		expr string
		want string
	}{
		{"1609.344", "1609.344"},
		{"1000/3600", "0.2777777777777777777777777777777777777778"},
		{"0.45359237*9.80665", "4.4482216152605"},
		{"1/3*3", "1"},
	} {
		want, _ := new(big.Float).SetPrec(Prec).SetString(tt.want)
		if got := ratFloat(tt.expr); !Equal(got, want) {
			t.Errorf("ratFloat(%q) = %s, want %s", tt.expr, got.Text('g', 40), tt.want)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("bad factor did not panic")
		}
	}()
	ratFloat("abc")
}

func factor(t *testing.T, u *Unit) *big.Float {
	t.Helper()
	f, err := u.Factor(nil)
	if err != nil {
		t.Fatalf("%s: %v", u.Sym, err)
	}
	return f
}

func TestLookup(t *testing.T) {
	tab := Default()
	tests := []struct {
		name, sym string
	}{
		// Symbols, case-sensitive first.
		{"km", "km"}, {"KM", "km"}, {"Km", "km"}, {"mm", "mm"}, {"µm", "µm"}, {"um", "µm"}, {"μm", "µm"},
		{"MB", "MB"}, {"Mb", "Mb"}, {"mb", "MB"}, {"kB", "KB"}, {"KB", "KB"}, {"kb", "KB"}, {"Kb", "Kb"},
		{"MiB", "MiB"}, {"mib", "MiB"}, {"Mib", "Mib"}, {"B", "B"}, {"b", "b"}, {"bit", "b"}, {"Mbit", "Mbit"},
		{"Mbps", "Mbps"}, {"mbps", "Mbps"}, {"MBps", "MB/s"}, {"°C", "℃"}, {"C", "℃"}, {"c", "℃"}, {"F", "℉"},
		{"K", "K"}, {"ms", "ms"}, {"Pa", "Pa"}, {"kPa", "kPa"}, {"hPa", "hPa"}, {"kWh", "kWh"}, {"mL", "mL"},
		{"ml", "mL"}, {"l", "L"}, {"cc", "cm³"}, {"lbs", "lb"}, {"kph", "km/h"}, {"rpm", "rpm"}, {"RPM", "rpm"},
		{"°", "°"}, {"deg", "°"}, {"Nm", "N·m"}, {"lb-ft", "lbf·ft"}, {"sqft", "ft²"}, {"in", "in"},
		// Names and plurals.
		{"kilometers", "km"}, {"Kilometre", "km"}, {"miles", "mi"}, {"feet", "ft"}, {"inches", "in"},
		{"pounds", "lb"}, {"kilograms", "kg"}, {"celsius", "℃"}, {"Fahrenheit", "℉"}, {"kibibytes", "KiB"},
		{"megabits", "Mb"}, {"gigabytes", "GB"}, {"hectares", "ha"}, {"knots", "kn"}, {"days", "d"},
		{"liters", "L"}, {"litres", "L"}, {"calories", "cal"}, {"kilocalories", "kcal"}, {"horsepower", "hp"},
		// Chinese names.
		{"公里", "km"}, {"千米", "km"}, {"米", "m"}, {"厘米", "cm"}, {"斤", "斤"}, {"市斤", "斤"}, {"公斤", "kg"},
		{"千克", "kg"}, {"两", "两"}, {"亩", "亩"}, {"平方米", "m²"}, {"摄氏度", "℃"}, {"升", "L"}, {"毫升", "mL"},
		{"小时", "h"}, {"分钟", "min"}, {"天", "d"}, {"英里", "mi"}, {"英尺", "ft"}, {"字节", "B"}, {"千瓦时", "kWh"},
		{"度电", "kWh"}, {"马力", "PS"}, {"美元", "USD"}, {"人民币", "CNY"}, {"元", "CNY"}, {"日元", "JPY"},
		{"欧元", "EUR"}, {"英镑", "GBP"}, {"港币", "HKD"}, {"新台币", "TWD"}, {"韩元", "KRW"},
		// Pinyin: full and initials of names with two or more characters.
		{"gongli", "km"}, {"gl", "km"}, {"jin", "斤"}, {"gongjin", "kg"}, {"liang", "两"}, {"mu", "亩"},
		{"chi", "尺"}, {"cun", "寸"}, {"limi", "cm"}, {"haomi", "mm"}, {"xiaoshi", "h"}, {"meiyuan", "USD"},
		{"renminbi", "CNY"}, {"rmb", "CNY"},
		// Currencies.
		{"USD", "USD"}, {"usd", "USD"}, {"Eur", "EUR"}, {"dollars", "USD"}, {"yuan", "CNY"}, {"€", "EUR"},
		{"CUP", "CUP"}, {"cup", "cup"}, {"Cup", "cup"},
	}
	for _, tt := range tests {
		u, ok := tab.Lookup(tt.name)
		if !ok || u.Sym != tt.sym {
			sym := "<none>"
			if u != nil {
				sym = u.Sym
			}
			t.Errorf("Lookup(%q) = %s, want %s", tt.name, sym, tt.sym)
		}
	}
	// Pinyin never shadows a symbol or name: "mi" is a mile, not 米.
	if u, _ := tab.Lookup("mi"); u.Sym != "mi" {
		t.Errorf("mi = %s", u.Sym)
	}
	for _, name := range []string{"", "k", "x", "foo", "code", "notepad", "tea", "d", "kilo", "hex"} {
		if u, ok := tab.Lookup(name); ok {
			t.Errorf("Lookup(%q) = %s, want none", name, u.Sym)
		}
	}
}

func TestLookupPhrase(t *testing.T) {
	tab := Default()
	tests := []struct {
		words []string
		sym   string
		n     int
	}{
		{[]string{"fl", "oz", "in"}, "fl oz", 2},
		{[]string{"km", "in", "mi"}, "km", 1},
		{[]string{"us", "gal"}, "gal", 2},
		{[]string{"imp", "gal"}, "imp gal", 2},
		{[]string{"metric", "cup"}, "metric cup", 2},
		{[]string{"nautical", "miles"}, "nmi", 2},
		{[]string{"square", "feet"}, "ft²", 2},
		{[]string{"miles", "per", "hour"}, "mph", 3},
		{[]string{"Light", "Years"}, "ly", 2},
		{[]string{"uk", "mpg"}, "mpg (UK)", 2},
	}
	for _, tt := range tests {
		u, n := tab.LookupPhrase(tt.words)
		if u == nil || u.Sym != tt.sym || n != tt.n {
			t.Errorf("LookupPhrase(%q) = %v, %d; want %s, %d", tt.words, u, n, tt.sym, tt.n)
		}
	}
	if u, n := tab.LookupPhrase([]string{"foo", "bar"}); u != nil || n != 0 {
		t.Errorf("LookupPhrase(foo bar) = %v, %d", u, n)
	}
	if u, n := tab.LookupPhrase(nil); u != nil || n != 0 {
		t.Errorf("LookupPhrase(nil) = %v, %d", u, n)
	}
	if tab.MaxWords() < 3 {
		t.Errorf("MaxWords = %d", tab.MaxWords())
	}
}

func TestPrefixes(t *testing.T) {
	tab := Default()
	tests := []struct {
		sym, want string // factor in base units
	}{
		{"km", "1000"}, {"cm", "0.01"}, {"mm", "0.001"}, {"µm", "1e-6"}, {"nm", "1e-9"}, {"dm", "0.1"},
		{"mg", "1e-6"}, {"kg", "1"}, {"µs", "1e-6"}, {"ns", "1e-9"}, {"hPa", "100"}, {"MPa", "1e6"}, {"GPa", "1e9"},
		{"kJ", "1000"}, {"MJ", "1e6"}, {"mW", "0.001"}, {"GW", "1e9"}, {"kHz", "1000"}, {"GHz", "1e9"}, {"kN", "1000"},
		{"KB", "8000"}, {"MB", "8e6"}, {"GB", "8e9"}, {"TB", "8e12"}, {"PB", "8e15"}, {"EB", "8e18"},
		{"KiB", "8192"}, {"MiB", "8388608"}, {"GiB", "8589934592"}, {"TiB", "8796093022208"},
		{"Kb", "1000"}, {"Gb", "1e9"}, {"Kib", "1024"}, {"Gbps", "1e9"}, {"kbps", "1000"}, {"kWh", "3.6e6"},
		{"mL", "1e-6"}, {"hL", "0.1"}, {"kcal", "4184"},
	}
	for _, tt := range tests {
		u, ok := tab.Lookup(tt.sym)
		if !ok {
			t.Errorf("no unit %s", tt.sym)
			continue
		}
		want, _ := new(big.Float).SetPrec(Prec).SetString(tt.want)
		if got := factor(t, u); !Equal(got, want) {
			t.Errorf("%s factor = %s, want %s", tt.sym, got.Text('g', 20), tt.want)
		}
	}
}

func TestDynamicFactors(t *testing.T) {
	tab := Default()
	px, rem, usd := tab.BySym("px"), tab.BySym("rem"), tab.BySym("EUR")
	f := factor(t, px) // 96 dpi
	if want := ratFloat("0.0254/96"); !Equal(f, want) {
		t.Errorf("px = %s", f.Text('g', 20))
	}
	f, _ = rem.Factor(&Env{DPI: 72, RemBase: 10})
	if want := ratFloat("0.0254/72*10"); !Equal(f, want) {
		t.Errorf("rem at 72 dpi, 10 px = %s", f.Text('g', 20))
	}
	if _, err := usd.Factor(nil); !errors.Is(err, ErrNoRates) {
		t.Errorf("EUR without env: %v", err)
	}
	if _, err := usd.Factor(&Env{}); !errors.Is(err, ErrNoRates) {
		t.Errorf("EUR without rates: %v", err)
	}
	env := &Env{Rate: func(code string) (*big.Float, error) {
		switch code {
		case "EUR":
			return big.NewFloat(0.5), nil
		case "JPY":
			return big.NewFloat(0), nil
		}
		return nil, ErrNoRate
	}}
	if f, err := usd.Factor(env); err != nil || f.Cmp(big.NewFloat(2)) != 0 {
		t.Errorf("EUR at 0.5 = %v, %v", f, err)
	}
	if _, err := tab.BySym("JPY").Factor(env); !errors.Is(err, ErrNoRate) {
		t.Errorf("JPY at 0: %v", err)
	}
	if _, err := tab.BySym("GBP").Factor(env); !errors.Is(err, ErrNoRate) {
		t.Errorf("GBP: %v", err)
	}
	if u := tab.BySym("USD"); u.Code != "USD" || u.Cat != CatCurrency {
		t.Errorf("USD = %+v", u)
	}
}

// TestListings checks that every unit offered as a conversion exists, has
// the dimension of its category and does not repeat.
func TestListings(t *testing.T) {
	tab := Default()
	for cat, l := range listings {
		seen := map[string]bool{}
		for _, list := range [][]string{l.metric, l.imperial, l.zh} {
			for _, s := range list {
				u := tab.BySym(s)
				if u == nil {
					t.Errorf("%s: no unit %q", cat, s)
					continue
				}
				if c := u.Cat; c != cat && !(cat == CatTypography && c == CatLength) && !(cat == CatCooking && c == CatVolume) {
					t.Errorf("%s: %q is in category %s", cat, s, c)
				}
				if seen[s] && list == nil {
					t.Errorf("%s: %q twice", cat, s)
				}
			}
			seen = map[string]bool{}
			for _, s := range list {
				if seen[s] {
					t.Errorf("%s: %q twice", cat, s)
				}
				seen[s] = true
			}
		}
	}
	if got := Listing(CatLength, false, false); got[0].Sym != "m" {
		t.Errorf("metric length starts with %s", got[0].Sym)
	}
	if got := Listing(CatLength, true, false); got[0].Sym != "ft" {
		t.Errorf("imperial length starts with %s", got[0].Sym)
	}
	if got := Listing(CatMass, false, true); got[len(got)-1].Sym != "钱" {
		t.Errorf("Chinese mass ends with %s", got[len(got)-1].Sym)
	}
	if got := Listing(CatTemperature, true, false); got[0].Sym != "℉" {
		t.Errorf("imperial temperature starts with %s", got[0].Sym)
	}
	if Listing(CatNone, false, false) != nil || Listing(CatCurrency, false, false) != nil {
		t.Error("listing of none/currency")
	}
	if !ByMagnitude(CatData) || ByMagnitude(CatLength) {
		t.Error("ByMagnitude")
	}
}

func TestCategoryOf(t *testing.T) {
	tests := []struct {
		dim Dim
		cat Category
	}{
		{Of(Length, 1), CatLength}, {Of(Length, 2), CatArea}, {Of(Length, 3), CatVolume},
		{Of(Length, 1).Div(Of(Time, 1)), CatSpeed}, {Of(Time, -1), CatFrequency},
		{Of(Data, 1).Div(Of(Time, 1)), CatDataRate}, {dForce, CatForce}, {dEnergy, CatEnergy},
		{dPower, CatPower}, {dPressure, CatPressure}, {dEconomy, CatFuel}, {dDensity, CatDensity},
		{Of(Length, 1).Div(Of(Time, 2)), CatNone}, {Dim{}, CatNone},
	}
	for _, tt := range tests {
		if got := CategoryOf(tt.dim); got != tt.cat {
			t.Errorf("CategoryOf(%s) = %q, want %q", tt.dim, got, tt.cat)
		}
	}
	if !Reciprocal(FuelDim, dEconomy) || !Reciprocal(dEconomy, FuelDim) || Reciprocal(Of(Time, 1), Of(Time, -1)) {
		t.Error("Reciprocal")
	}
	if TorqueDim != dEnergy {
		t.Error("TorqueDim")
	}
}

func TestMatch(t *testing.T) {
	tab := Default()
	kmh := ratFloat("1000/3600")
	if u := tab.Match(dSpeed, kmh, CatNone, nil); u == nil || u.Sym != "km/h" {
		t.Errorf("Match(km/h) = %v", u)
	}
	if u := tab.Match(dEnergy, big.NewFloat(1), CatTorque, nil); u == nil || u.Sym != "N·m" {
		t.Errorf("Match(N·m) = %v", u)
	}
	if u := tab.Match(dEnergy, big.NewFloat(1), CatNone, nil); u == nil || u.Sym != "J" {
		t.Errorf("Match(J) = %v", u)
	}
	if u := tab.Match(dSpeed, big.NewFloat(3), CatNone, nil); u != nil {
		t.Errorf("Match(3 m/s) = %v", u)
	}
	if u := tab.Match(dCurrency, big.NewFloat(1), CatNone, nil); u != nil {
		t.Errorf("Match(currency) = %v", u)
	}
	if Equal(big.NewFloat(1), big.NewFloat(-1)) || !Equal(new(big.Float), new(big.Float)) {
		t.Error("Equal")
	}
	if len(Codes()) < 160 || len(tab.Units()) < 300 {
		t.Errorf("%d codes, %d units", len(Codes()), len(tab.Units()))
	}
	if YenSymbol(false) != "CNY" || YenSymbol(true) != "JPY" || !IsYen("￥") || IsYen("$") {
		t.Error("yen")
	}
}

// TestRoundTrip converts 1.2345 of every unit to every other unit of the
// same dimension and back.
func TestRoundTrip(t *testing.T) {
	tab := Default()
	x := ratFloat("1.2345")
	env := &Env{DPI: 120, RemBase: 18}
	for _, a := range tab.Units() {
		if a.Dyn == DynCurrency {
			continue
		}
		fa, _ := a.Factor(env)
		for _, b := range tab.Units() {
			if b.Dim != a.Dim || b.Dyn == DynCurrency {
				continue
			}
			fb, _ := b.Factor(env)
			base := newF().Mul(x, fa)
			if a.Offset != nil && b.Offset != nil {
				base.Add(base, a.Offset)
				y := newF().Sub(base, b.Offset)
				y.Quo(y, fb)
				back := newF().Mul(y, fb)
				back.Add(back, b.Offset)
				back.Sub(back, a.Offset)
				back.Quo(back, fa)
				if !Equal(back, x) {
					t.Errorf("%s → %s → %s: %s", a.Sym, b.Sym, a.Sym, back.Text('g', 40))
				}
				continue
			}
			y := newF().Quo(base, fb)
			back := newF().Quo(newF().Mul(y, fb), fa)
			if !Equal(back, x) {
				t.Errorf("%s → %s → %s: %s", a.Sym, b.Sym, a.Sym, back.Text('g', 40))
			}
		}
	}
}

func BenchmarkBuild(b *testing.B) {
	for b.Loop() {
		build()
	}
}
