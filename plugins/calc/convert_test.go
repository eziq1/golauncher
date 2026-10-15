package calc

import (
	"encoding/json/jsontext"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
	"github.com/eziq1/golauncher/plugins/calc/units"
)

// testRates are fixed rates for conversions.
var testRates = &Rates{
	Provider: providerERAPI,
	Updated:  time.Date(2026, 10, 15, 0, 2, 31, 0, time.UTC),
	Fetched:  time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC),
	Rates:    map[string]float64{"USD": 1, "CNY": 6.7, "EUR": 0.9, "JPY": 158, "GBP": 0.75, "HKD": 7.8, "KRW": 1340},
}

// newPluginWith returns an initialized plugin with options o.
func newPluginWith(t *testing.T, o Options) (*Plugin, *hostfake.Host) {
	t.Helper()
	if o.Location == nil {
		o.Location = time.UTC
	}
	p := NewWithOptions(o)
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	return p, h
}

// setPref sets a preference to a JSON value.
func setPref(h *hostfake.Host, name, value string) { h.Prefs.Set(name, jsontext.Value(value)) }

// root returns the root items for text.
func root(t *testing.T, p *Plugin, text string) []plugin.Item {
	t.Helper()
	items, err := p.RootItems(t.Context(), plugin.Query{Text: text})
	if err != nil {
		t.Fatalf("RootItems(%q): %v", text, err)
	}
	return items
}

func titles(items []plugin.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Title)
	}
	return out
}

// TestConversions converts with an explicit target, per category.
func TestConversions(t *testing.T) {
	p, _ := newPluginWith(t, Options{Rates: testRates})
	tests := []struct {
		in, title, raw, subtitle string
	}{
		// Length.
		{"10 km in mi", "6.21371 mi", "6.21371", "10 km = 6.21371 mi"},
		{"10 kilometers to miles", "6.21371 mi", "6.21371", "10 km = 6.21371 mi"},
		{"1 mi in m", "1,609.34 m", "1609.34", ""},
		{"5 ft 10 in in cm", "177.8 cm", "177.8", "5 ft 10 in = 177.8 cm"},
		{"1 in in in", "1 in", "1", ""},
		{"1 nmi in km", "1.852 km", "1.852", ""},
		{"1 ly in km", "9,460,730,000,000 km", "9460730000000", ""},
		{"1 丈 in m", "3.33333 m", "3.33333", ""},
		{"1 里 to km", "0.5 km", "0.5", ""},
		{"10 公里 换成 英里", "6.21371 mi", "6.21371", ""},
		{"10公里转换为英里", "6.21371 mi", "6.21371", ""},
		{"10 km => mi", "6.21371 mi", "6.21371", ""},
		{"10 km as mi", "6.21371 mi", "6.21371", ""},
		{"10 gongli in mi", "6.21371 mi", "6.21371", "10 km = 6.21371 mi"},
		{"1 KM IN MI", "0.621371 mi", "0.621371", ""},
		// Area.
		{"1 亩 in m²", "666.667 m²", "666.667", "1 亩 = 666.667 m²"},
		{"1 acre in m2", "4,046.86 m²", "4046.86", ""},
		{"10 m^2 in ft^2", "107.639 ft²", "107.639", "10 m² = 107.639 ft²"},
		{"1 ha in 亩", "15 亩", "15", ""},
		{"1 sq mi in km²", "2.58999 km²", "2.58999", ""},
		{"2 m * 3 m in ft²", "64.5835 ft²", "64.5835", ""},
		// Volume and cooking.
		{"1 gal in L", "3.78541 L", "3.78541", ""},
		{"1 imp gal in L", "4.54609 L", "4.54609", ""},
		{"1 fl oz in ml", "29.5735 mL", "29.5735", ""},
		{"1 m³ in L", "1,000 L", "1000", ""},
		{"1 cup in ml", "236.588 mL", "236.588", ""},
		{"1 metric cup in ml", "250 mL", "250", ""},
		{"2 tbsp in tsp", "6 tsp", "6", ""},
		{"1 metric tbsp in metric tsp", "3 metric tsp", "3", ""},
		{"1 斗 in L", "10 L", "10", ""},
		// Mass.
		{"3 斤 to kg", "1.5 kg", "1.5", "3 斤 = 1.5 kg"},
		{"3斤换成kg", "1.5 kg", "1.5", ""},
		{"1 lb in kg", "0.453592 kg", "0.453592", ""},
		{"1 oz in g", "28.3495 g", "28.3495", ""},
		{"1 ozt in g", "31.1035 g", "31.1035", ""},
		{"5 gongjin to lb", "11.0231 lb", "11.0231", ""},
		{"1 两 in g", "50 g", "50", ""},
		{"1 st in lb", "14 lb", "14", ""},
		// Temperature.
		{"30 ℃ in ℉", "86 ℉", "86", "30 ℃ = 86 ℉"},
		{"30 °C to F", "86 ℉", "86", ""},
		{"-40 c in f", "-40 ℉", "-40", "-40 ℃ = -40 ℉"},
		{"0 K in ℃", "-273.15 ℃", "-273.15", ""},
		{"212 fahrenheit in celsius", "100 ℃", "100", ""},
		{"20 摄氏度 to 华氏度", "68 ℉", "68", ""},
		{"Δ5 ℃ in ℉", "Δ9 ℉", "9", "Δ5 ℃ = Δ9 ℉"},
		{"Δ10 ℉ in K", "5.55556 K", "5.55556", ""},
		{"20 ℃ + 5 ℃", "25 ℃", "25", ""},
		{"30 ℃ - 20 ℃", "Δ10 ℃", "10", "30 ℃ − 20 ℃ = Δ10 ℃"},
		{"30 ℃ - 20 ℃ in ℉", "Δ18 ℉", "18", ""},
		{"20 ℃ + Δ9 ℉", "25 ℃", "25", ""},
		{"Δ5 K + 20 ℃", "25 ℃", "25", ""},
		{"-(30 ℃) in ℃", "-30 ℃", "-30", ""},
		{"2 * 300 K", "600 K", "600", ""},
		// Data (SI vs IEC, bits vs bytes).
		{"1 GiB in MB", "1,073.74 MB", "1073.74", ""},
		{"1 GB in MiB", "953.674 MiB", "953.674", ""},
		{"100 Mb in MB", "12.5 MB", "12.5", ""},
		{"100 MB in Mb", "800 Mb", "800", ""},
		{"1 KB in b", "8,000 b", "8000", ""},
		{"1 Kb in b", "1,000 b", "1000", ""},
		{"1 mb in kb", "1,000 KB", "1000", ""},
		{"1 KiB in B", "1,024 B", "1024", ""},
		{"1 TiB in GiB", "1,024 GiB", "1024", ""},
		{"2 bytes in bits", "16 b", "16", ""},
		{"1 gigabyte in megabytes", "1,000 MB", "1000", ""},
		// Data rate.
		{"0.5 MB/s in Mbps", "4 Mbps", "4", ""},
		{"100 Mbps in MB/s", "12.5 MB/s", "12.5", ""},
		{"1 Gbps in MiB/s", "119.209 MiB/s", "119.209", ""},
		{"5 GB / 100 Mbps", "6.66667 min", "6.66667", "5 GB / 100 Mbps = 6.66667 min"},
		{"1 TB / 100 MB/s", "2.77778 h", "2.77778", ""},
		{"5 GB / 100 Mbps in s", "400 s", "400", ""},
		// Speed.
		{"100 km/h in mph", "62.1371 mph", "62.1371", ""},
		{"1 mph in km/h", "1.60934 km/h", "1.60934", ""},
		{"1 kn in km/h", "1.852 km/h", "1.852", ""},
		{"10 m/s in kph", "36 km/h", "36", ""},
		{"100 km / 2 h", "50 km/h", "50", "100 km / 2 h = 50 km/h"},
		{"60 mi / 1 h in km/h", "96.5606 km/h", "96.5606", ""},
		{"100 公里每小时 in mph", "62.1371 mph", "62.1371", ""},
		// Duration.
		{"1 h 30 min to min", "90 min", "90", "1 h 30 min = 90 min"},
		{"2 days in h", "48 h", "48", ""},
		{"1 wk in days", "7 d", "7", ""},
		{"1 yr in days", "365.243 d", "365.243", ""},
		{"90 分钟 in 小时", "1.5 h", "1.5", ""},
		{"1500 ms in s", "1.5 s", "1.5", ""},
		// Pressure, energy, power.
		{"1 atm in psi", "14.6959 psi", "14.6959", ""},
		{"1 bar in kPa", "100 kPa", "100", ""},
		{"760 mmHg in atm", "1 atm", "1", ""},
		{"1 kWh in J", "3,600,000 J", "3600000", ""},
		{"1 kcal in kJ", "4.184 kJ", "4.184", ""},
		{"1 度电 in MJ", "3.6 MJ", "3.6", ""},
		{"100 hp in kW", "74.57 kW", "74.57", ""},
		{"1 PS in W", "735.499 W", "735.499", ""},
		{"2 kW * 3 h in kWh", "6 kWh", "6", ""},
		{"2 kW·h in kJ", "7,200 kJ", "7200", ""},
		// Angle and frequency.
		{"90° in rad", "1.5708 rad", "1.5708", "90° = 1.5708 rad"},
		{"1 turn in deg", "360°", "360", ""},
		{"200 grad in deg", "180°", "180", ""},
		{"3000 rpm in Hz", "50 Hz", "50", ""},
		{"2.4 GHz in MHz", "2,400 MHz", "2400", ""},
		{"1 / 10 ms in Hz", "100 Hz", "100", ""},
		// Fuel (reciprocal).
		{"8 L/100km in mpg", "29.4018 mpg", "29.4018", "8 L/100km = 29.4018 mpg"},
		{"30 mpg in L/100km", "7.84049 L/100km", "7.84049", ""},
		{"30 mpg in uk mpg", "36.0285 mpg (UK)", "36.0285", ""},
		{"10 km/L in L/100km", "10 L/100km", "10", ""},
		{"40 L / 500 km in L/100km", "8 L/100km", "8", ""},
		// Force, torque, density.
		{"3 kg * 9.8 m/s^2", "29.4 N", "29.4", "3 kg × 9.8 m/s² = 29.4 N"},
		{"1 kgf in N", "9.80665 N", "9.80665", ""},
		{"1 lbf in N", "4.44822 N", "4.44822", ""},
		{"10 N*m in lbf-ft", "7.37562 lbf·ft", "7.37562", ""},
		{"100 N * 2 m", "200 N·m", "200", ""},
		{"1 g/cm³ in kg/m³", "1,000 kg/m³", "1000", ""},
		{"1 kg/L in lb/ft^3", "62.428 lb/ft³", "62.428", "1 kg/L = 62.428 lb/ft³"},
		// Typography (96 dpi, 16 px rem).
		{"16 px in rem", "1 rem", "1", ""},
		{"1 in in px", "96 px", "96", ""},
		{"12 pt to px", "16 px", "16", ""},
		{"2 em in px", "32 px", "32", ""},
		{"160 dp in in", "1 in", "1", ""},
		{"10 mm in px", "37.7953 px", "37.7953", ""},
		// Arithmetic with units.
		{"1 mi + 200 m", "1.12427 mi", "1.12427", "1 mi + 200 m = 1.12427 mi"},
		{"10 km / 2", "5 km", "5", ""},
		{"2 * 10 km", "20 km", "20", ""},
		{"(2 m)^2", "4 m²", "4", ""},
		{"sqrt(16 m^2)", "4 m", "4", ""},
		{"abs(-5 m)", "5 m", "5", ""},
		{"max(1 km, 900 m)", "1 km", "1", ""},
		{"sin(90°)", "1", "1", ""},
		{"cos(180°)", "-1", "-1", ""},
		{"10 km / 5 km", "2", "2", ""},
		{"10 m % 3 m", "1 m", "1", ""},
		{"1e3 m in km", "1 km", "1", ""},
		{"1 km - 1 m", "0.999 km", "0.999", ""},
		// Conversions of units without a number.
		{"km in mi", "0.621371 mi", "0.621371", "1 km = 0.621371 mi"},
		{"c to f", "33.8 ℉", "33.8", ""},
		// Currency.
		{"100 usd in cny", "670.00 CNY", "670.00", "100 USD = 670.00 CNY · rates from 2026-10-15"},
		{"$100 to eur", "90.00 EUR", "90.00", ""},
		{"100 美元 换成 人民币", "670.00 CNY", "670.00", ""},
		{"100 元 to usd", "14.93 USD", "14.93", ""},
		{"¥670 in usd", "100.00 USD", "100.00", ""},
		{"€9 in usd", "10.00 USD", "10.00", ""},
		{"usd to cny", "6.70 CNY", "6.70", ""},
		{"1 krw in usd", "0.000746269 USD", "0.000746269", ""},
		{"100 usd + 90 eur in usd", "200.00 USD", "200.00", ""},
		{"10 usd / 2", "5.00 USD", "5.00", ""},
		{"USD 100 in JPY", "15,800.00 JPY", "15800.00", ""},
		// Plain math and bases keep working.
		{"255 in hex", "0xFF", "0xFF", "255 in hex"},
		{"1+2", "3", "3", "1+2"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			items := root(t, p, tt.in)
			if len(items) != 1 {
				t.Fatalf("got %d items %q, want 1", len(items), titles(items))
			}
			it := items[0]
			if it.Title != tt.title || it.Actions[0].Do.Target != tt.raw {
				t.Errorf("title %q raw %q, want %q %q", it.Title, it.Actions[0].Do.Target, tt.title, tt.raw)
			}
			if tt.subtitle != "" && it.Subtitle != tt.subtitle {
				t.Errorf("subtitle %q, want %q", it.Subtitle, tt.subtitle)
			}
		})
	}
}

// TestMismatchesAndNonResults: incompatible units and text that is not a
// calculation give nothing.
func TestMismatchesAndNonResults(t *testing.T) {
	p, _ := newPluginWith(t, Options{Rates: testRates})
	for _, in := range []string{
		"10 kg in m", "5 km + 3 kg", "1 m in s", "1 h in Hz", "30 ℃ in kg", "2 * 30 ℃", "30 ℃ * 2 m",
		"Δ5 kg", "Δ5", "1/0 km", "10 km in hex", "1.5 in hex", "5 km!", "2^(1 m)", "(1 m)^0.5", "sin(1 m)",
		"sqrt(1 m^3)", "ln(1 m)", "1 m + 1", "Δ5 K - 30 ℃", "3 m 5", "10 km 5 kg", "100 usd in kg", "km in kg",
		"1 m^20", "1 xyz in m", "1 m in xyz", "30 ℃^2", "-", "10 in", "1 kg in in",
	} {
		if in == "10 in" {
			continue // a bare quantity: 10 inches
		}
		if items := root(t, p, in); len(items) != 0 {
			t.Errorf("%q gave %q", in, titles(items))
		}
	}
}

// TestNoHijacking: ordinary searches (app names, words, unit names without
// a number) never produce calculator results.
func TestNoHijacking(t *testing.T) {
	p, _ := newPluginWith(t, Options{Rates: testRates})
	for _, in := range []string{
		"code", "notepad", "mi", "pt", "tea", "chrome", "word", "excel", "steam", "vlc", "git", "go", "edge",
		"teams", "zoom", "c", "r", "d", "s", "m", "b", "k", "in", "to", "as", "km", "usd", "ft", "cup", "gal",
		"min", "max", "hex", "date", "now", "time", "unix", "rgb", "shoe", "us 9", "3d", "4k", "7zip", "python 3",
		"windows 11", "office 365", "win 10", "photoshop 2024", "mp3", "1password", "qq", "wechat", "微信",
		"计算器", "米", "斤", "kg", "km/h", "m/s^2", "kg*m", "ps", "pa", "ha", "rem", "px", "dp", "em", "ly",
		"word to pdf", "png to jpg", "mp4 to mp3", "how to", "to do", "go to", "notepad++", "c++", "c#", "f#",
		"#", "#region", "rgb(", "now in", "in to", "1 to 2", "me", "mm", "mins", "deg", "rad", "hz", "light",
		"teams 2", "2048", "360", "42", "1", "pi", "e", "",
	} {
		if items := root(t, p, in); len(items) != 0 {
			t.Errorf("%q gave %q", in, titles(items))
		}
	}
}

// TestBareQuantity lists up to five conversions in the preferred order.
func TestBareQuantity(t *testing.T) {
	p, h := newPluginWith(t, Options{Rates: testRates})
	tests := []struct {
		in   string
		want []string
	}{
		{"10 km", []string{"10,000 m", "6.21371 mi", "5.39957 nmi", "1,000,000 cm", "10,000,000 mm"}},
		{"30 ℃", []string{"86 ℉", "303.15 K"}},
		{"Δ5 ℃", []string{"Δ9 ℉", "5 K"}},
		{"300 K", []string{"26.85 ℃", "80.33 ℉"}},
		{"100 km/h", []string{"27.7778 m/s", "62.1371 mph", "53.9957 kn", "91.1344 ft/s"}},
		{"1.5 GB", []string{"12 Gb", "1.39698 GiB", "1,430.51 MiB", "1,500 MB", "12,000 Mb"}},
		{"90 min", []string{"1.5 h", "0.0625 d", "5,400 s", "0.00892857 wk", "0.00205343 mo"}},
		{"30 mpg", []string{"7.84049 L/100km", "12.7543 km/L", "36.0285 mpg (UK)"}},
		{"100 usd", []string{"670.00 CNY", "90.00 EUR", "15,800.00 JPY", "75.00 GBP", "780.00 HKD"}},
		{"3 jin", []string{"1.5 kg", "1,500 g", "3.30693 lb", "52.9109 oz", "0.23621 st"}},
		{"16 px", []string{"12 pt", "1 rem", "1 em", "26.6667 dp", "4.23333 mm"}},
		{"9.8 m/s^2", []string{"9.8 m/s²"}},
		{"1 cup", []string{"236.588 mL", "16 tbsp", "48 tsp", "8 fl oz", "0.946353 metric cup"}},
	}
	for _, tt := range tests {
		if got := titles(root(t, p, tt.in)); !slices.Equal(got, tt.want) {
			t.Errorf("%q = %q, want %q", tt.in, got, tt.want)
		}
	}
	// Imperial first, Chinese units added, scores decreasing.
	setPref(h, prefUnitSystem, `"imperial"`)
	setPref(h, prefChinese, `true`)
	items := root(t, p, "1 kg")
	if got := titles(items); !slices.Equal(got, []string{"2.20462 lb", "35.274 oz", "0.157473 st", "1,000 g", "5,000 ct"}) {
		t.Errorf("imperial 1 kg = %q", got)
	}
	if items[0].Score <= items[1].Score {
		t.Errorf("scores %v, %v", items[0].Score, items[1].Score)
	}
	var labels []string
	for _, m := range items[0].Detail.Metadata {
		labels = append(labels, m.Label)
	}
	if !slices.Contains(labels, "斤") || !slices.Contains(labels, "kg") {
		t.Errorf("detail labels %q", labels)
	}
	setPref(h, prefCurrencies, `["eur", "JPY", "XXX", "BTC"]`)
	if got := titles(root(t, p, "1 usd")); !slices.Equal(got, []string{"0.90 EUR", "158.00 JPY"}) {
		t.Errorf("1 usd with favorites = %q", got)
	}
}

func TestPreferences(t *testing.T) {
	p, h := newPluginWith(t, Options{Rates: testRates})
	setPref(h, prefSigDigits, `3`)
	if got := titles(root(t, p, "10 km in mi")); got[0] != "6.21 mi" {
		t.Errorf("3 digits: %q", got)
	}
	setPref(h, prefSigDigits, `0`) // out of range: default
	if got := titles(root(t, p, "10 km in mi")); got[0] != "6.21371 mi" {
		t.Errorf("0 digits: %q", got)
	}
	setPref(h, prefDPI, `144`)
	setPref(h, prefRemBase, `10`)
	if got := titles(root(t, p, "1 in in px")); got[0] != "144 px" {
		t.Errorf("144 dpi: %q", got)
	}
	if got := titles(root(t, p, "2 rem in px")); got[0] != "20 px" {
		t.Errorf("rem 10: %q", got)
	}
	if got := titles(root(t, p, "12 pt in px")); got[0] != "24 px" {
		t.Errorf("pt at 144 dpi: %q", got)
	}
	setPref(h, prefYenMeansJPY, `true`)
	if got := titles(root(t, p, "¥158 in usd")); got[0] != "1.00 USD" {
		t.Errorf("yen as JPY: %q", got)
	}
	setPref(h, prefProvider, `"off"`)
	for _, in := range []string{"100 usd", "100 usd in cny", "$5", "100 美元"} {
		if items := root(t, p, in); len(items) != 0 {
			t.Errorf("currency off: %q gave %q", in, titles(items))
		}
	}
}

func TestResultItem(t *testing.T) {
	p, _ := newPluginWith(t, Options{Rates: testRates})
	it := root(t, p, "10 km in mi")[0]
	if it.ID != "convert:mi" || it.Group != "Calculator" || it.Score != 1 || it.Icon.Name != "calculator" {
		t.Errorf("item = %+v", it)
	}
	want := []struct{ id, kind, target, shortcut string }{
		{"copy", "CopyText", "6.21371", ""},
		{"copy-unit", "CopyText", "6.21371 mi", ""},
		{"paste", "Paste", "6.21371", "Ctrl+Shift+V"},
		{"conversions", "Push", viewConversions, "Ctrl+Shift+A"},
	}
	if len(it.Actions) != len(want) {
		t.Fatalf("actions = %+v", it.Actions)
	}
	for i, w := range want {
		a := it.Actions[i]
		if a.ID != w.id || string(a.Do.Kind) != w.kind || a.Do.Target != w.target || a.Shortcut != w.shortcut || a.Title == "" {
			t.Errorf("action %d = %+v, want %+v", i, a, w)
		}
	}
	d := it.Detail
	if d == nil || !strings.Contains(d.Markdown, "10 km") || len(d.Metadata) < 5 || d.Metadata[0].Label != "mi" {
		t.Fatalf("detail = %+v", d)
	}
	// Currency results credit the provider.
	it = root(t, p, "1 usd in eur")[0]
	if !strings.Contains(it.Detail.Markdown, "Exchange Rate API") || !strings.Contains(it.Subtitle, "rates from 2026-10-15") {
		t.Errorf("currency item = %+v", it)
	}
	p.opts.Rates = &Rates{Provider: providerFrankfurter, Updated: testRates.Updated, Rates: testRates.Rates}
	if it := root(t, p, "1 usd in eur")[0]; !strings.Contains(it.Detail.Markdown, "Frankfurter") {
		t.Errorf("frankfurter attribution: %q", it.Detail.Markdown)
	}
}

func TestConversionsView(t *testing.T) {
	p, _ := newPluginWith(t, Options{Rates: testRates})
	push := root(t, p, "10 km")[0].Actions[3].Do
	v, err := p.View(t.Context(), plugin.ViewRequest{ID: push.Target, Payload: push.Payload})
	if err != nil {
		t.Fatal(err)
	}
	if v.Title != "10 km" || !v.ShowDetail || len(v.Sections) != 1 || len(v.Sections[0].Items) < 8 {
		t.Fatalf("view = %+v", v)
	}
	if it := v.Sections[0].Items[0]; it.Title != "10,000 m" || it.Detail == nil || len(it.Actions) != 3 {
		t.Errorf("first item = %+v", it)
	}
	// Text that no longer converts: an empty view.
	v, err = p.View(t.Context(), plugin.ViewRequest{ID: viewConversions, Payload: plugin.MustPayload(payload{Text: "code"})})
	if err != nil || len(v.Sections) != 0 || v.EmptyText == "" {
		t.Errorf("empty view = %+v, %v", v, err)
	}
	if _, err := p.View(t.Context(), plugin.ViewRequest{ID: "other"}); err == nil {
		t.Error("unknown view did not fail")
	}
	if _, err := p.View(t.Context(), plugin.ViewRequest{ID: viewConversions}); err == nil {
		t.Error("view without payload did not fail")
	}
	if _, err := p.View(t.Context(), plugin.ViewRequest{ID: viewConversions, Payload: jsontext.Value(`{`)}); err == nil {
		t.Error("view with a bad payload did not fail")
	}
}

func TestCalculateUnits(t *testing.T) {
	// The default environment knows units but no currencies.
	c, err := calculate("10 km in mi")
	if err != nil || c.val == nil || c.target == nil || c.expr != "10 km" {
		t.Errorf("calculate(10 km in mi) = %+v, %v", c, err)
	}
	if c, err := calculate("10 km"); err != nil || !c.listing {
		t.Errorf("calculate(10 km) = %+v, %v", c, err)
	}
	if c, err := calculate("10 km + 1 m"); err != nil || c.listing {
		t.Errorf("calculate(10 km + 1 m) = %+v, %v", c, err)
	}
	for in, want := range map[string]error{
		"100 usd":    errUnknown,
		"1 m + 1 s":  errDim,
		"km":         errNotExpression,
		"km/h":       errNotExpression,
		"10 km/5 km": nil,
		"=km":        errNotExpression,
		"5 m in s":   errDim,
		"1 km in":    nil,
		"1 km to":    nil,
	} {
		if _, err := calculate(in); !errors.Is(err, want) && !(want == nil && err == nil) {
			t.Errorf("calculate(%q) error %v, want %v", in, err, want)
		}
	}
	if c, _ := calculate("10 km/5 km"); c.raw != "2" || c.val != nil {
		t.Errorf("10 km/5 km = %+v", c)
	}
	// While typing "1 km in mi", "1 km in" is still 1 km.
	if c, _ := calculate("1 km in"); !c.listing || c.expr != "1 km" {
		t.Errorf("1 km in = %+v", c)
	}
}

func TestLexUnits(t *testing.T) {
	toks, err := lex("3斤换成kg")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tk := range toks {
		got = append(got, tk.raw)
	}
	if want := []string{"3", "斤", "换成", "kg", ""}; !slices.Equal(got, want) {
		t.Errorf("lex = %q, want %q", got, want)
	}
	toks, _ = lex("8 L/100 km => mpg")
	if toks[1].raw != "L/100km" || toks[2].kind != tokConv {
		t.Errorf("lex fuel = %+v", toks)
	}
	toks, _ = lex("30°C m² Δ5 $")
	if toks[1].raw != "°C" || toks[3].text != "^" || toks[4].text != "2" || toks[5].text != "Δ" || toks[7].kind != tokCur {
		t.Errorf("lex symbols = %+v", toks)
	}
	if _, err := lex("1 @ 2"); !errors.Is(err, errSyntax) {
		t.Errorf("lex(@) = %v", err)
	}
}

func TestInUnitErrors(t *testing.T) {
	zero := &value{x: newF(), dim: units.FuelDim, u: &uexpr{cat: units.CatFuel}}
	mpg, _ := single(units.Default().BySym("mpg"), nil)
	if _, _, err := inUnit(zero, mpg); !errors.Is(err, errDivZero) {
		t.Errorf("0 L/100km in mpg: %v", err)
	}
}

func BenchmarkRootItems(b *testing.B) {
	p := NewWithOptions(Options{Rates: testRates, Location: time.UTC})
	h, err := hostfake.New(p, b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	if err := p.Init(b.Context(), h); err != nil {
		b.Fatal(err)
	}
	defer p.Close()
	units.Default()
	for _, q := range []string{"10 km in mi", "30 ℃", "100 usd", "5 GB / 100 Mbps", "notepad", "1234 * 5", "#ff8800", "1700000000 to date"} {
		b.Run(q, func(b *testing.B) {
			for b.Loop() {
				if _, err := p.RootItems(b.Context(), plugin.Query{Text: q}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestFormatHelpers(t *testing.T) {
	for in, want := range map[string]string{"0": "0", "-1234.567": "-1234.57", "0.004": "0.004", "-0.001": "-0.001",
		"1e20": "1e+20", "12.5e15": "1.25e+16", "0.5": "0.50", "-0.0049": "-0.0049"} {
		if got := formatMoney(mustParse(in), 6); got != want {
			t.Errorf("formatMoney(%s) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"1e15": "1e+15", "999999999999999": "999999999999999",
		"1234567.891": "1234570", "0.0000012345678": "0.00000123457", "1.23456789e-7": "1.23457e-7", "2.5e16": "2.5e+16"} {
		if got := formatSig(mustParse(in), 6); got != want {
			t.Errorf("formatSig(%s) = %q, want %q", in, got, want)
		}
	}
	if got := zoneName(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); got != "UTC" {
		t.Errorf("zoneName(UTC) = %q", got)
	}
	if got := zoneName(time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("x", -5*3600-1800))); got != "UTC-05:30" {
		t.Errorf("zoneName(-05:30) = %q", got)
	}
	if cmpFloat(1, 1) != 0 || cmpFloat(2, 1) != 1 || cmpBool(true, true) != 0 || cmpBool(false, true) != -1 {
		t.Error("cmp helpers")
	}
	if withUnit("90", "°") != "90°" || withUnit("1", "km") != "1 km" {
		t.Error("withUnit")
	}
}

func TestNaturalUnit(t *testing.T) {
	p, _ := newPluginWith(t, Options{Rates: testRates})
	tests := map[string]string{
		"9.8 m/s^2 * 2":         "19.6 m/s²",    // no category: as written
		"2 kg * 3 m":            "6 kg·m",       // no category
		"1 m^3 / 1 s":           "1 m³/s",       // flow: no category
		"10 N / 2 m^2":          "5 Pa",         // named
		"3 GB / 2":              "1.5 GB",       // keeps its unit
		"1 / 1 s":               "1 Hz",         // named
		"100 J / 10 s":          "10 W",         // named
		"2 h * 3 km/h":          "6 km",         // named
		"1 kg / 1 L":            "1 kg/L",       // as written
		"1 bit / 1 ms":          "1 kbps",       // magnitude: 1000 bps
		"(1 km)^2 / 1 m":        "1,000,000 m",  // named
		"30 mi / 1 gal in km/L": "12.7543 km/L", // fuel economy
	}
	for in, want := range tests {
		got := titles(root(t, p, in))
		if len(got) == 0 || got[0] != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}
