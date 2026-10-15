package calc

import (
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestColors(t *testing.T) {
	p, h := newPluginWith(t, Options{})
	tests := []struct {
		in   string
		want []string
	}{
		{"#ff8800 to rgb", []string{"rgb(255, 136, 0)"}},
		{"#f80 to rgb", []string{"rgb(255, 136, 0)"}},
		{"ff8800 to hsl", []string{"hsl(32, 100%, 50%)"}},
		{"rgb(255,136,0) in hsl", []string{"hsl(32, 100%, 50%)"}},
		{"rgb(255 136 0) as hex", []string{"#FF8800"}},
		{"rgba(255, 136, 0, 0.5) to hex", []string{"#FF8800"}},
		{"rgb(100%, 50%, 0%) to hex", []string{"#FF8000"}},
		{"hsl(32,100%,50%) to hex", []string{"#FF8800"}},
		{"HSL(32deg 100% 50%) => rgb", []string{"rgb(255, 136, 0)"}},
		{"hsl(-328, 100%, 50%) to hex", []string{"#FF8800"}},
		{"#ff8800", []string{"rgb(255, 136, 0)", "hsl(32, 100%, 50%)"}},
		{"rgb(0, 0, 0)", []string{"#000000", "hsl(0, 0%, 0%)"}},
		{"hsl(0, 0%, 100%)", []string{"#FFFFFF", "rgb(255, 255, 255)"}},
		{"#00ff00 to hsl", []string{"hsl(120, 100%, 50%)"}},
		{"#0000ff to hsl", []string{"hsl(240, 100%, 50%)"}},
		{"#ff00ff to hsl", []string{"hsl(300, 100%, 50%)"}},
		{"hsl(200, 50%, 40%) to rgb", []string{"rgb(51, 119, 153)"}},
		{"hsl(90, 50%, 40%) to rgb", []string{"rgb(102, 153, 51)"}},
		{"hsl(160, 50%, 40%) to rgb", []string{"rgb(51, 153, 119)"}},
		{"hsl(280, 50%, 40%) to rgb", []string{"rgb(119, 51, 153)"}},
		{"hsl(340, 50%, 40%) to rgb", []string{"rgb(153, 51, 85)"}},
	}
	for _, tt := range tests {
		if got := titles(root(t, p, tt.in)); !slices.Equal(got, tt.want) {
			t.Errorf("%q = %q, want %q", tt.in, got, tt.want)
		}
	}
	// Not colors: a number in hex stays a number; bad colors give nothing.
	if got := titles(root(t, p, "255 in hex")); !slices.Equal(got, []string{"0xFF"}) {
		t.Errorf("255 in hex = %q", got)
	}
	for _, in := range []string{"#ff88", "#ggg", "ff8800", "ff8800 to hex", "rgb(256, 0, 0)", "rgb(1, 2)", "rgb(a, b, c)",
		"hsl(10, 200%, 50%)", "hsl(x, 1%, 1%)", "rgb(1%, 2%, x%)", "#ff8800 to cmyk", "rgb(1,2,3", "#region"} {
		if items := root(t, p, in); len(items) != 0 {
			t.Errorf("%q gave %q", in, titles(items))
		}
	}
	// The detail has a swatch of the color.
	it := root(t, p, "#ff8800")[0]
	if it.Detail == nil || it.Detail.Image == "" || len(it.Detail.Metadata) != 3 {
		t.Fatalf("detail = %+v", it.Detail)
	}
	f, err := os.Open(it.Detail.Image)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b, _ := img.At(5, 5).RGBA(); r>>8 != 255 || g>>8 != 136 || b>>8 != 0 {
		t.Errorf("swatch color = %d %d %d", r>>8, g>>8, b>>8)
	}
	if filepath.Dir(it.Detail.Image) != filepath.Join(h.CacheDir(), "swatches") {
		t.Errorf("swatch at %s", it.Detail.Image)
	}
	// Written once; a broken cache directory leaves out the swatch.
	if again := root(t, p, "#FF8800 to rgb")[0]; again.Detail.Image != it.Detail.Image {
		t.Errorf("second swatch %s", again.Detail.Image)
	}
	if err := os.RemoveAll(filepath.Join(h.CacheDir(), "swatches")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.CacheDir(), "swatches"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if it := root(t, p, "#123456")[0]; it.Detail.Image != "" {
		t.Errorf("swatch without a cache dir: %s", it.Detail.Image)
	}
}

// TestColorRoundTrip converts every 17th RGB color to HSL and back: HSL
// with integer percentages is within one step of each channel.
func TestColorRoundTrip(t *testing.T) {
	for r := 0; r < 256; r += 17 {
		for g := 0; g < 256; g += 17 {
			for b := 0; b < 256; b += 17 {
				c := rgb{uint8(r), uint8(g), uint8(b)}
				back, _, ok := parseColor(c.format("hsl"), false)
				if !ok {
					t.Fatalf("parse %s", c.format("hsl"))
				}
				for _, d := range []int{int(back.r) - r, int(back.g) - g, int(back.b) - b} {
					if d < -3 || d > 3 {
						t.Errorf("%s → %s → %s", c.format("hex"), c.format("hsl"), back.format("hex"))
					}
				}
				if hex, _, _ := parseColor(c.format("hex"), false); hex != c {
					t.Errorf("hex round trip of %v", c)
				}
				if x, _, _ := parseColor(c.format("rgb"), false); x != c {
					t.Errorf("rgb round trip of %v", c)
				}
			}
		}
	}
}

func TestTimestamps(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	p, h := newPluginWith(t, Options{Location: shanghai})
	h.Clock = func() time.Time { return time.Date(2026, 10, 15, 2, 0, 0, 0, time.UTC) }
	tests := []struct {
		in, title, subtitle string
	}{
		{"1700000000 to date", "2023-11-15 06:13:20", "1700000000 = 2023-11-15 06:13:20 · UTC+08:00"},
		{"1700000000 to date in utc", "2023-11-14 22:13:20", "1700000000 = 2023-11-14 22:13:20 · UTC"},
		{"1700000000 in utc", "2023-11-14 22:13:20", ""},
		{"1700000000 to local", "2023-11-15 06:13:20", ""},
		{"1700000000000 ms to date", "2023-11-15 06:13:20", "1700000000000 ms = 2023-11-15 06:13:20 · UTC+08:00"},
		{"1700000000000 to date", "2023-11-15 06:13:20", ""},
		{"1700000000.5 s to iso", "2023-11-15 06:13:20", ""},
		{"1700000000 秒 换成 日期", "2023-11-15 06:13:20", ""},
		{"0 to date in utc", "1970-01-01 00:00:00", ""},
		{"-86400 to date in utc", "1969-12-31 00:00:00", ""},
		{"now in unix", "1792029600", "now = 1792029600"},
		{"now to unix ms", "1792029600000", ""},
		{"now to timestamp", "1792029600", ""},
		{"现在 换成 时间戳", "1792029600", ""},
		{"now to utc", "2026-10-15 02:00:00", ""},
		{"2026-10-15 10:00 to unix", "1792029600", "2026-10-15 10:00 (UTC+08:00) = 1792029600"},
		{"2026-10-15 to unix", "1791993600", ""},
		{"2026/10/15 10:00:00 to unix", "1792029600", ""},
		{"2026-10-15 02:00 utc to unix", "1792029600", "2026-10-15 02:00 utc = 1792029600"},
		{"2026-10-15T02:00:00Z to unix", "1792029600", ""},
		{"2026-10-15T10:00:00+08:00 to unix ms", "1792029600000", ""},
		{"2026-10-15 10:00 to utc", "2026-10-15 02:00:00", ""},
	}
	for _, tt := range tests {
		items := root(t, p, tt.in)
		if len(items) != 1 {
			t.Errorf("%q gave %q", tt.in, titles(items))
			continue
		}
		if items[0].Title != tt.title || tt.subtitle != "" && items[0].Subtitle != tt.subtitle {
			t.Errorf("%q = %q / %q, want %q / %q", tt.in, items[0].Title, items[0].Subtitle, tt.title, tt.subtitle)
		}
	}
	// The detail has every form, with a relative time.
	it := root(t, p, "1792026000 to date")[0]
	var got []string
	for _, m := range it.Detail.Metadata {
		got = append(got, m.Label+"="+m.Value)
	}
	want := []string{"Local time=2026-10-15 09:00:00", "UTC=2026-10-15 01:00:00", "ISO 8601=2026-10-15T09:00:00+08:00",
		"Unix time (seconds)=1792026000", "Unix time (milliseconds)=1792026000000", "Relative=1 hour ago"}
	if !slices.Equal(got, want) {
		t.Errorf("detail = %q, want %q", got, want)
	}
	if it.Actions[1].Do.Target != "2026-10-15 09:00:00 UTC+08:00" {
		t.Errorf("copy with zone = %q", it.Actions[1].Do.Target)
	}
	// Not timestamps.
	for _, in := range []string{"1700000000", "1700000000 to unix", "now", "now to", "now in kg", "5 kg to date",
		"1e300 to date", "2026-13-45 to unix", "tomorrow to unix", "1700000000 to date unix", "to date", "inf to date"} {
		if items := root(t, p, in); len(items) != 0 {
			t.Errorf("%q gave %q", in, titles(items))
		}
	}
	// "5 s to ms" is a duration, not a timestamp.
	if got := titles(root(t, p, "5 s to ms")); !slices.Equal(got, []string{"5,000 ms"}) {
		t.Errorf("5 s to ms = %q", got)
	}
}

func TestSizes(t *testing.T) {
	p, _ := newPluginWith(t, Options{})
	tests := []struct {
		in       string
		want     []string
		subtitle string
	}{
		{"US 9 shoe in EU", []string{"EU 42.5"}, "US 9 (men's shoes) = EU 42.5"},
		{"鞋码 42 in us", []string{"US 8.5"}, "EU 42 (men's shoes) = US 8.5"},
		{"鞋码42 in us", []string{"US 8.5"}, ""},
		{"女鞋 38 to us", []string{"US 7"}, "EU 38 (women's shoes) = US 7"},
		{"women US 7 shoes to EU", []string{"EU 38"}, ""},
		{"men's shoe size 10 in uk", []string{"UK 9"}, ""},
		{"shoe 270 to us", []string{"US 9"}, ""},
		{"shoe CN 42 to us", []string{"US 8.5"}, ""},
		{"shoe jp 27 in eu", []string{"EU 42.5"}, ""},
		{"男鞋 43 换成 美码", []string{"US 9.5"}, ""},
		{"shoe 42", []string{"US 8.5", "UK 7.5", "CN 265", "JP 26.5"}, ""},
		{"衣服 M in eu", []string{"EU 48"}, "INT M (men's clothing) = EU 48"},
		{"clothing EU 48 to US", []string{"US 38"}, ""},
		{"clothing 170/88A to int", []string{"INT M"}, ""},
		{"shirt xxl in cn", []string{"CN 185/100A"}, ""},
		{"shirt 3xl in eu", []string{"EU 56"}, ""},
		{"clothing 40 to eu", []string{"EU 50"}, ""},
		{"women clothing S", []string{"US 4", "UK 8", "EU 36", "CN 160/84A"}, ""},
		{"女装 38 to us", []string{"US 6"}, ""},
		{"女装 6 to eu", []string{"EU 38"}, ""},
	}
	for _, tt := range tests {
		items := root(t, p, tt.in)
		if got := titles(items); !slices.Equal(got, tt.want) {
			t.Errorf("%q = %q, want %q", tt.in, got, tt.want)
			continue
		}
		if tt.subtitle != "" && items[0].Subtitle != tt.subtitle {
			t.Errorf("%q subtitle %q, want %q", tt.in, items[0].Subtitle, tt.subtitle)
		}
	}
	for _, in := range []string{"us 9", "shoe", "shoe US 30 in EU", "shoe 9 in kg", "shoe 9 in", "clothing M to jp",
		"clothing XL5 in eu", "shoe US 9 9 in EU", "clothing ZZ to eu", "shoe us 9 in xx"} {
		if items := root(t, p, in); len(items) != 0 {
			t.Errorf("%q gave %q", in, titles(items))
		}
	}
}
