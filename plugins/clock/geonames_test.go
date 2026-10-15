package clock

import (
	"bytes"
	"compress/gzip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/host/hostfake"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Invented fixture data in the formats of cities.json and geonames.tsv.gz.
const (
	fixtureCurated = `[
  {"id": "utc", "name": "UTC", "zh": "协调世界时", "country": "Coordinated Universal Time", "countryZh": "世界时", "tz": "UTC", "aliases": ["utc"]},
  {"id": "harbor-city", "geonameid": 900002, "name": "Harbor City", "zh": "港城", "country": "Exampleland", "countryZh": "示例国", "tz": "America/Chicago", "aliases": ["hc"]}
]`
	fixtureGeonames = "# invented test data\n" +
		"#countries\n" +
		"US\tUnited States\t美国\n" +
		"CN\tChina\t中国\n" +
		"NP\tNepal\t尼泊尔\n" +
		"#admin1\n" +
		"US.IL\tIllinois\t伊利诺伊州\n" +
		"US.MA\tMassachusetts\t\n" +
		"CN.01\tAnhui\t安徽省\n" +
		"#cities\n" +
		"900001\tMaplefield\t\t\tUS\tIL\tAmerica/Chicago\n" +
		"900002\tHarbour Town\t\t港口镇\tUS\tMA\tAmerica/New_York\n" + // replaced by the curated Harbor City
		"900003\tMaplefield\t\t\tUS\tMA\tAmerica/New_York\n" +
		"900004\tQiaozhen\t\t桥镇\tCN\t01\tAsia/Shanghai\n" +
		"900005\tHillpur\t\t山布尔\tNP\t\tAsia/Kathmandu\n" +
		"900006\tŌkubo\tOkubo\t\tUS\tIL\tAmerica/Chicago\n" +
		"900007\tNowhere\t\t\tUS\tIL\tMars/Olympus_Mons\n" // unknown zone: skipped
)

func gz(t testing.TB, s string) *bytes.Reader {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b.Bytes())
}

func fixtureIndex(t testing.TB) *index {
	t.Helper()
	idx, err := buildIndex([]byte(fixtureCurated), gz(t, fixtureGeonames))
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestBuildIndex(t *testing.T) {
	idx := fixtureIndex(t)
	var got []string
	for _, c := range idx.cities {
		got = append(got, c.ID)
	}
	// Curated first, then GeoNames in file (population) order; the curated
	// city replaces its GeoNames row; the unknown zone is skipped.
	if want := []string{"utc", "harbor-city", "900001", "900003", "900004", "900005", "900006"}; !slices.Equal(got, want) {
		t.Fatalf("cities = %v, want %v", got, want)
	}
	// The overlay wins on everything but the region, which GeoNames adds.
	h := idx.city("harbor-city")
	if h.Name != "Harbor City" || h.Zh != "港城" || h.TZ != "America/Chicago" || h.Country != "Exampleland" ||
		!slices.Equal(h.Aliases, []string{"hc"}) || h.cc != "US" || h.region != "Massachusetts" || h.regionZh != "" || !h.curated {
		t.Errorf("harbor-city = %+v", h)
	}
	if _, ok := idx.lookup("Harbour Town"); ok {
		t.Error("the replaced GeoNames name is still found")
	}
	m := idx.city("900001")
	if m.Country != "United States" || m.CountryZh != "美国" || m.region != "Illinois" || m.regionZh != "伊利诺伊州" || m.loc == nil {
		t.Errorf("900001 = %+v", m)
	}
	for id, want := range map[string]bool{"900001": true, "900003": true, "900004": false, "harbor-city": false} {
		if got := idx.city(id).ambiguous; got != want {
			t.Errorf("%s ambiguous = %v, want %v", id, got, want)
		}
	}
	// Lookups: the first city of a name wins; ASCII names, Chinese names,
	// aliases and zones are found.
	for in, want := range map[string]string{
		"maplefield": "900001", "Okubo": "900006", "ōkubo": "900006", "桥镇": "900004", "hc": "harbor-city",
		"Asia/Kathmandu": "900005", "900003": "900003", "utc": "utc",
	} {
		if c, ok := idx.lookup(in); !ok || c.ID != want {
			t.Errorf("lookup(%q) = %v, want %s", in, c, want)
		}
	}
	// Countries and zones list their cities.
	for _, co := range idx.countries {
		if co.code == "US" && !slices.Equal(co.cities, []int32{1, 2, 3, 6}) {
			t.Errorf("US cities = %v", co.cities)
		}
	}
	if z := idx.zoneByID["America/Chicago"]; !slices.Equal(z.cities, []int32{1, 2, 6}) {
		t.Errorf("America/Chicago cities = %v", z.cities)
	}
}

func TestCityKeys(t *testing.T) {
	idx := fixtureIndex(t)
	keys := func(id string) []string {
		var out []string
		for _, k := range idx.city(id).keys {
			s := k.s
			if k.exact {
				s += "!"
			}
			out = append(out, s)
		}
		return out
	}
	// Pinyin for Chinese names; initials only for Chinese cities and curated
	// ones; curated cities also match their country.
	if got, want := keys("900004"), []string{"qiaozhen", "桥镇", "qz!"}; !slices.Equal(got, want) {
		t.Errorf("900004 keys = %v, want %v", got, want)
	}
	if got, want := keys("900005"), []string{"hillpur", "山布尔", "shanbuer"}; !slices.Equal(got, want) {
		t.Errorf("900005 keys = %v, want %v", got, want)
	}
	if got, want := keys("900006"), []string{"okubo"}; !slices.Equal(got, want) {
		t.Errorf("900006 keys = %v, want %v", got, want)
	}
	if got := keys("harbor-city"); !slices.Contains(got, "gc!") || !slices.Contains(got, "exampleland") || !slices.Contains(got, "hc") {
		t.Errorf("harbor-city keys = %v", got)
	}
}

func TestParseGeonamesErrors(t *testing.T) {
	for _, data := range []string{
		"#cities\n1\tToo few\n",
		"#countries\nUS\tUnited States\n",
		"#cities\nx\tA\t\t\tUS\tIL\tUTC\n",
		"1\tOutside\t\t\tUS\tIL\tUTC\n",
	} {
		if _, err := parseGeonames(gz(t, data)); err == nil {
			t.Errorf("%q: no error", data)
		}
	}
	if _, err := parseGeonames(strings.NewReader("not gzip")); err == nil {
		t.Error("not gzip: no error")
	}
}

func TestFind(t *testing.T) {
	idx := fixtureIndex(t)
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	find := func(q string, rank map[int]int, limit int) []string {
		var out []string
		for _, h := range idx.find(q, now, rank, limit) {
			out = append(out, h.c.ID)
		}
		return out
	}
	tests := []struct {
		q    string
		want []string
	}{
		{"maplefield", []string{"900001", "900003"}}, // equal scores: by population
		{"maple", []string{"900001", "900003"}},
		{"hc", []string{"harbor-city"}},
		{"harbor", []string{"harbor-city"}},
		{"qz", []string{"900004"}},
		{"qiao", []string{"900004"}},
		{"桥", []string{"900004"}},
		{"sbe", nil}, // no initials for a transliterated name
		{"shanbu", []string{"900005"}},
		// Country: the curated city's own country, GeoNames countries by code.
		{"exampleland", []string{"harbor-city"}},
		{"united states", []string{"harbor-city", "900001", "900003", "900006"}},
		{"尼泊尔", []string{"900005"}},
		// Zones, offsets and abbreviations.
		{"america/chicago", []string{"harbor-city", "900001", "900006"}},
		{"utc+8", []string{"900004"}},
		{"utc+5:45", []string{"900005"}},
		{"cst", []string{"harbor-city", "900001", "900004", "900006"}},
		// Substrings and subsequences.
		{"plefie", []string{"900001", "900003"}},
		{"mplfld", []string{"900001", "900003"}},
		{"zzz", nil},
	}
	for _, tt := range tests {
		if got := find(tt.q, nil, 30); !slices.Equal(got, tt.want) {
			t.Errorf("find(%q) = %v, want %v", tt.q, got, tt.want)
		}
	}
	// Listed cities come first among equal scores; the limit caps the list.
	if got := find("maplefield", map[int]int{idx.city("900003").ord: -10}, 30); !slices.Equal(got, []string{"900003", "900001"}) {
		t.Errorf("ranked: %v", got)
	}
	if got := find("united states", nil, 2); !slices.Equal(got, []string{"harbor-city", "900001"}) {
		t.Errorf("limited: %v", got)
	}
	// A better match beats a larger city.
	if got := find("okubo", nil, 30); len(got) == 0 || got[0] != "900006" {
		t.Errorf("okubo: %v", got)
	}
}

// TestFindSkipsWeakMatches: substring and subsequence matches are skipped
// when limit cities match better; the result is the same as with the full
// search a huge limit forces.
func TestFindSkipsWeakMatches(t *testing.T) {
	idx, err := loadIndex()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	for _, q := range []string{"a", "an", "san", "ber", "sydny", "york", "china", "us", "cst", "utc+1", "zhou", "上"} {
		got := idx.find(q, now, nil, maxResults)
		full := idx.find(q, now, nil, 1<<30)
		if !slices.Equal(got, full[:min(len(full), maxResults)]) {
			t.Errorf("%q: %d hits differ from the full search", q, len(got))
		}
	}
}

// TestDisambiguation: same-name cities show their region and country, in
// Chinese in a Chinese UI where there are Chinese names.
func TestDisambiguation(t *testing.T) {
	p := NewWithOptions(Options{After: func(time.Duration) <-chan time.Time { return nil }})
	p.index = sync.OnceValues(func() (*index, error) {
		return buildIndex([]byte(fixtureCurated), gz(t, fixtureGeonames))
	})
	h, err := hostfake.New(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.Clock = func() time.Time { return time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC) }
	if err := p.Init(t.Context(), h); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	subtitles := func(q string) []string {
		items, err := p.RootItems(t.Context(), plugin.Query{Text: "time " + q})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range items {
			out = append(out, strings.SplitN(it.Subtitle, " · ", 2)[0])
		}
		return out
	}
	if got, want := subtitles("maplefield"), []string{"Illinois, United States", "Massachusetts, United States"}; !slices.Equal(got, want) {
		t.Errorf("maplefield: %v, want %v", got, want)
	}
	if got, want := subtitles("qiaozhen"), []string{"China"}; !slices.Equal(got, want) {
		t.Errorf("qiaozhen: %v, want %v", got, want)
	}
	if got := subtitles("utc"); got[0] != "Thursday, Oct 15" {
		t.Errorf("utc: %v", got)
	}
	h.I18N.SetLocale("zh-CN")
	// No Chinese name for Massachusetts: the English one.
	if got, want := subtitles("maplefield"), []string{"美国 伊利诺伊州", "美国 Massachusetts"}; !slices.Equal(got, want) {
		t.Errorf("maplefield (zh): %v, want %v", got, want)
	}
	if got, want := subtitles("hc"), []string{"示例国"}; !slices.Equal(got, want) {
		t.Errorf("hc (zh): %v, want %v", got, want)
	}
}

func BenchmarkLoadIndex(b *testing.B) {
	for b.Loop() {
		if _, err := loadIndex(); err != nil {
			b.Fatal(err)
		}
	}
}
