package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"slices"
	"strings"
	"testing"
)

// The fixtures are invented rows in the formats of the GeoNames dumps.

func TestParseCountries(t *testing.T) {
	in := "# ISO\tISO3\t...\n" +
		"XA\tXAA\t001\tXA\tExampleland\tCapital\t1\t2\tEU\t.xa\tXAD\tDollar\t1\t\t\txa\t900100\t\t\n" +
		"XB\tXBB\t002\tXB\tOtherland\tTown\t1\t2\tAS\t.xb\tXBD\tDollar\t2\t\t\txb\t900200\t\t\n"
	got, err := parseCountries(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []country{{code: "XA", name: "Exampleland", geonameid: 900100}, {code: "XB", name: "Otherland", geonameid: 900200}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if _, err := parseCountries(strings.NewReader("XA\tshort\n")); err == nil {
		t.Error("short row: no error")
	}
}

func TestParseRegions(t *testing.T) {
	got, err := parseRegions(strings.NewReader("XA.01\tNorth Provïnce\tNorth Province\t900101\nXA.02\tSouth\tSouth\t900102\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []region{{key: "XA.01", name: "North Provïnce", geonameid: 900101}, {key: "XA.02", name: "South", geonameid: 900102}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// cityRow is a cities15000.txt row with the fields the generator reads.
func cityRow(id, name, ascii, code, cc, admin1, pop, tz string) string {
	f := make([]string, 19)
	f[0], f[1], f[2], f[7], f[8], f[10], f[14], f[17] = id, name, ascii, code, cc, admin1, pop, tz
	return strings.Join(f, "\t") + "\n"
}

func TestParseCities(t *testing.T) {
	in := cityRow("900001", "Smallton", "Smallton", "PPL", "XA", "01", "16000", "Europe/Paris") +
		cityRow("900002", "Bïgville", "Bigville", "PPLA", "XA", "02", "900000", "Europe/Paris") +
		cityRow("900003", "Old Quarter", "Old Quarter", "PPLX", "XA", "02", "500000", "Europe/Paris") + // a section of a city
		cityRow("900004", "Ruins", "Ruins", "PPLH", "XA", "02", "20000", "Europe/Paris") + // historical
		cityRow("900005", "Midtown", "Midtown", "PPL", "XB", "01", "16000", "Asia/Tokyo") +
		cityRow("900006", "Nozone", "Nozone", "PPL", "XB", "01", "30000", "")
	got, err := parseCities(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, c := range got {
		ids = append(ids, c.geonameid)
	}
	// Largest first; equal populations by ID.
	if want := []int{900002, 900001, 900005}; !slices.Equal(ids, want) {
		t.Errorf("cities = %v, want %v", ids, want)
	}
	if c := got[0]; c.name != "Bïgville" || c.ascii != "Bigville" || c.country != "XA" || c.admin1 != "02" || c.tz != "Europe/Paris" || c.population != 900000 {
		t.Errorf("Bigville = %+v", c)
	}
}

func TestValidZones(t *testing.T) {
	cs := []city{{geonameid: 1, tz: "Europe/Paris"}, {geonameid: 2, tz: "Mars/Olympus_Mons"}, {geonameid: 3, tz: "Asia/Tokyo"}}
	got, dropped := validZones(cs)
	if len(got) != 2 || got[1].geonameid != 3 || dropped != 1 {
		t.Errorf("got %+v, dropped %d", got, dropped)
	}
}

// altRow is an alternateNamesV2.txt row.
func altRow(id, lang, name, preferred, short, colloquial, historic string) string {
	return strings.Join([]string{"1", id, lang, name, preferred, short, colloquial, historic, "", ""}, "\t") + "\n"
}

func TestChineseNames(t *testing.T) {
	in := altRow("900001", "en", "Smallton", "1", "", "", "") +
		altRow("900001", "zh", "斯莫爾頓", "", "", "", "") + // traditional, converted
		// A simplified name in plain zh beats a converted one.
		altRow("900002", "zh", "大維爾", "", "", "", "") +
		altRow("900002", "zh", "大维尔", "", "", "", "") +
		// zh-CN beats zh, even preferred.
		altRow("900003", "zh", "甲", "1", "", "", "") +
		altRow("900003", "zh-CN", "乙城", "", "", "", "") +
		// Preferred beats short beats plain.
		altRow("900004", "zh", "丙", "", "", "", "") +
		altRow("900004", "zh", "丁", "", "1", "", "") +
		altRow("900004", "zh", "戊", "1", "", "", "") +
		// Colloquial, historic and Latin names are skipped.
		altRow("900005", "zh", "己", "", "", "1", "") +
		altRow("900005", "zh", "庚", "", "", "", "1") +
		altRow("900005", "zh", "辛 City", "", "", "", "") +
		altRow("900005", "zh", "壬", "", "", "", "") +
		// IDs not asked for and other languages are ignored.
		altRow("900009", "zh", "癸", "", "", "", "") +
		altRow("900006", "ja", "東京", "", "", "", "")
	ids := map[int]bool{900001: true, 900002: true, 900003: true, 900004: true, 900005: true, 900006: true}
	got, err := chineseNames(strings.NewReader(in), ids)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{900001: "斯莫尔顿", 900002: "大维尔", 900003: "乙城", 900004: "戊", 900005: "壬"}
	if len(got) != len(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%d: %q, want %q", id, got[id], w)
		}
	}
}

func TestChineseAndSimplified(t *testing.T) {
	for s, want := range map[string]bool{"北京": true, "纽约·市": true, "New 北京": false, "東京ー": true, "서울": false, "": false, "abc": false} {
		if got := chinese(s); got != want {
			t.Errorf("chinese(%q) = %v", s, got)
		}
	}
	for in, want := range map[string]string{"紐約": "纽约", "蘭州": "兰州", "北京": "北京", "臺灣": "台湾"} {
		if got := toSimplified(in); got != want {
			t.Errorf("toSimplified(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[[2]string]string{
		{"广州市", "CN"}: "广州", {"首尔特别市", "KR"}: "首尔", {"胡志明市", "FR"}: "胡志明市", {"萩市", "JP"}: "萩市", {"", "CN"}: "",
	} {
		if got := cityZh(in[0], in[1]); got != want {
			t.Errorf("cityZh(%q, %s) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestWrite(t *testing.T) {
	d := &data{
		countries: []country{{code: "XA", name: "Exampleland", geonameid: 9}},
		admin1:    []region{{key: "XA.01", name: "North", geonameid: 8}, {key: "XA.09", name: "Unused"}},
		cities: []city{
			{geonameid: 900002, name: "Bïgville", ascii: "Bigville", zh: "大维尔", country: "XA", admin1: "01", tz: "Europe/Paris"},
			{geonameid: 900001, name: "Saint-Tab\tton", ascii: "Saint Tabton", country: "XA", admin1: "01", tz: "Europe/Paris"},
		},
	}
	d.apply(map[int]string{9: "示例国", 8: "北省", 900002: "大维尔"})
	var b bytes.Buffer
	if err := d.write(&b); err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(&b)
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	// The ASCII name is left out when it only drops diacritics; tabs in
	// names become spaces; unused regions are left out.
	want := "# GeoNames (geonames.org), CC BY 4.0. Generated by scripts/gencities; do not edit.\n" +
		"#countries\nXA\tExampleland\t示例国\n" +
		"#admin1\nXA.01\tNorth\t北省\n" +
		"#cities\n" +
		"900002\tBïgville\t\t大维尔\tXA\t01\tEurope/Paris\n" +
		"900001\tSaint-Tab ton\tSaint Tabton\t\tXA\t01\tEurope/Paris\n"
	if string(out) != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}
