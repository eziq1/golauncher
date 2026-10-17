// Command gencities turns the GeoNames dumps into the world clock's city
// data, plugins/clock/geonames.tsv.gz (docs/world-clock.md §9).
//
//	go run ./scripts/data/gencities -fetch -dir <download dir>
//
// downloads cities15000.zip, admin1CodesASCII.txt, countryInfo.txt and
// alternateNamesV2.zip (about 200 MB) from download.geonames.org into dir
// and writes the data file; without -fetch the files must already be there.
// The dumps are not committed. GeoNames data is licensed CC BY 4.0.
package main

import (
	"archive/zip"
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const baseURL = "https://download.geonames.org/export/dump/"

var dumps = []string{"cities15000.zip", "admin1CodesASCII.txt", "countryInfo.txt", "alternateNamesV2.zip"}

func main() {
	dir := flag.String("dir", "", "directory with (or for) the GeoNames dumps")
	fetch := flag.Bool("fetch", false, "download the dumps into -dir first")
	out := flag.String("out", filepath.Join("plugins", "clock", "geonames.tsv.gz"), "output file")
	flag.Parse()
	if *dir == "" {
		log.Fatal("gencities: -dir is required")
	}
	if *fetch {
		for _, f := range dumps {
			if err := download(baseURL+f, filepath.Join(*dir, f)); err != nil {
				log.Fatal(err)
			}
		}
	}
	d, err := load(*dir)
	if err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := d.write(f); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
	log.Printf("gencities: %d countries, %d regions, %d cities (%d with Chinese names, %d dropped) -> %s",
		len(d.countries), len(d.admin1), len(d.cities), d.withZh(), d.dropped, *out)
}

func download(url, path string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gencities: %s: %s", url, resp.Status)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// country is a row of countryInfo.txt.
type country struct {
	code, name, zh string
	geonameid      int
}

// region is a row of admin1CodesASCII.txt (a first-level division: a state
// or province).
type region struct {
	key, name, zh string // key is "US.IL"
	geonameid     int
}

// city is a row of cities15000.txt.
type city struct {
	geonameid  int
	name       string
	ascii      string
	zh         string
	country    string
	admin1     string
	tz         string
	population int
}

// data is everything written to the data file.
type data struct {
	countries []country
	admin1    []region
	cities    []city
	dropped   int // cities whose time zone Go does not know
}

func (d *data) withZh() int {
	n := 0
	for _, c := range d.cities {
		if c.zh != "" {
			n++
		}
	}
	return n
}

// load reads the dumps in dir.
func load(dir string) (*data, error) {
	d := &data{}
	var err error
	if d.countries, err = readFile(filepath.Join(dir, "countryInfo.txt"), parseCountries); err != nil {
		return nil, err
	}
	if d.admin1, err = readFile(filepath.Join(dir, "admin1CodesASCII.txt"), parseRegions); err != nil {
		return nil, err
	}
	if d.cities, err = readZip(filepath.Join(dir, "cities15000.zip"), "cities15000.txt", parseCities); err != nil {
		return nil, err
	}
	d.cities, d.dropped = validZones(d.cities)
	ids := map[int]bool{}
	for _, c := range d.countries {
		ids[c.geonameid] = true
	}
	for _, r := range d.admin1 {
		ids[r.geonameid] = true
	}
	for _, c := range d.cities {
		ids[c.geonameid] = true
	}
	zh, err := readZip(filepath.Join(dir, "alternateNamesV2.zip"), "alternateNamesV2.txt", func(r io.Reader) (map[int]string, error) {
		return chineseNames(r, ids)
	})
	if err != nil {
		return nil, err
	}
	d.apply(zh)
	return d, nil
}

// apply sets the Chinese names and the overrides of country names.
func (d *data) apply(zh map[int]string) {
	for i := range d.countries {
		c := &d.countries[i]
		c.zh = zh[c.geonameid]
		if o, ok := countryZh[c.code]; ok {
			c.zh = o
		}
	}
	for i := range d.admin1 {
		d.admin1[i].zh = zh[d.admin1[i].geonameid]
	}
	for i := range d.cities {
		c := &d.cities[i]
		c.zh = cityZh(zh[c.geonameid], c.country)
	}
}

// countryZh overrides GeoNames' Chinese names of some countries and regions
// with the names used in mainland China.
var countryZh = map[string]string{
	"TW": "中国台湾",
	"HK": "中国香港",
	"MO": "中国澳门",
	"US": "美国",
	"GB": "英国",
	"KR": "韩国",
	"KP": "朝鲜",
	"RU": "俄罗斯",
	"CD": "刚果（金）",
	"CG": "刚果（布）",
	"CI": "科特迪瓦",
	"MK": "北马其顿",
	"SZ": "斯威士兰",
	"MR": "毛里塔尼亚",
	"IM": "马恩岛",
	"FK": "福克兰群岛",
}

// cityZhSuffixes are suffixes dropped from the Chinese names of cities in
// countries that write them ("广州市" -> "广州"): the bare name is what
// people type and read.
var cityZhSuffixes = map[string][]string{
	"CN": {"市"},
	"TW": {"市"},
	"JP": {"市"},
	"KR": {"特别市", "广域市", "市"},
	"KP": {"直辖市", "特别市", "市"},
	"VN": {"市"},
}

// cityZh is the Chinese name of a city in country cc without suffixes such
// as 市, unless that would leave fewer than two characters.
func cityZh(zh, cc string) string {
	for _, suf := range cityZhSuffixes[cc] {
		if rest, ok := strings.CutSuffix(zh, suf); ok && utf8.RuneCountInString(rest) >= 2 {
			return rest
		}
	}
	return zh
}

// fold is s lowercased without diacritics and apostrophes, to tell whether
// the ASCII name of a city adds anything to its name.
func fold(s string) string {
	if t, _, err := transform.String(stripMarks, s); err == nil {
		s = t
	}
	return strings.Map(func(r rune) rune {
		switch r {
		case '\'', '’', '.', ',':
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

var stripMarks = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

func readFile[T any](path string, parse func(io.Reader) (T, error)) (T, error) {
	f, err := os.Open(path)
	if err != nil {
		var zero T
		return zero, err
	}
	defer f.Close()
	return parse(bufio.NewReaderSize(f, 1<<20))
}

func readZip[T any](path, name string, parse func(io.Reader) (T, error)) (T, error) {
	var zero T
	z, err := zip.OpenReader(path)
	if err != nil {
		return zero, err
	}
	defer z.Close()
	f, err := z.Open(name)
	if err != nil {
		return zero, fmt.Errorf("gencities: %s: %w", path, err)
	}
	defer f.Close()
	return parse(bufio.NewReaderSize(f, 1<<20))
}

// eachRow calls fn with the tab-separated fields of each line of r that is
// not empty or a comment.
func eachRow(r io.Reader, fn func(fields []string) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	line := 0
	for sc.Scan() {
		line++
		s := sc.Text()
		if s == "" || s[0] == '#' {
			continue
		}
		if err := fn(strings.Split(s, "\t")); err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
	}
	return sc.Err()
}

func field(fields []string, i int) string {
	if i < len(fields) {
		return strings.TrimSpace(fields[i])
	}
	return ""
}

// parseCountries parses countryInfo.txt: ISO code (0), name (4) and
// GeoNames ID (16).
func parseCountries(r io.Reader) ([]country, error) {
	var out []country
	err := eachRow(r, func(f []string) error {
		if len(f) < 17 {
			return fmt.Errorf("countryInfo: %d fields", len(f))
		}
		id, _ := strconv.Atoi(field(f, 16))
		out = append(out, country{code: field(f, 0), name: field(f, 4), geonameid: id})
		return nil
	})
	return out, err
}

// parseRegions parses admin1CodesASCII.txt: "US.IL", name, ASCII name,
// GeoNames ID.
func parseRegions(r io.Reader) ([]region, error) {
	var out []region
	err := eachRow(r, func(f []string) error {
		if len(f) < 4 {
			return fmt.Errorf("admin1: %d fields", len(f))
		}
		id, err := strconv.Atoi(field(f, 3))
		if err != nil {
			return fmt.Errorf("admin1: %w", err)
		}
		out = append(out, region{key: field(f, 0), name: field(f, 1), geonameid: id})
		return nil
	})
	return out, err
}

// skipCodes are the GeoNames feature codes of places that are not cities:
// sections of a city (neighborhoods and districts such as "Dupont Circle")
// and historical, abandoned or destroyed places.
var skipCodes = map[string]bool{"PPLX": true, "PPLH": true, "PPLQ": true, "PPLW": true}

// parseCities parses cities15000.txt (the GeoNames "geoname" table) and
// returns the cities by population, largest first.
func parseCities(r io.Reader) ([]city, error) {
	var out []city
	err := eachRow(r, func(f []string) error {
		if len(f) < 18 {
			return fmt.Errorf("cities: %d fields", len(f))
		}
		id, err := strconv.Atoi(field(f, 0))
		if err != nil {
			return fmt.Errorf("cities: %w", err)
		}
		pop, _ := strconv.Atoi(field(f, 14))
		c := city{geonameid: id, name: field(f, 1), ascii: field(f, 2), country: field(f, 8),
			admin1: field(f, 10), tz: field(f, 17), population: pop}
		if c.name == "" || c.tz == "" || skipCodes[field(f, 7)] {
			return nil
		}
		out = append(out, c)
		return nil
	})
	slices.SortStableFunc(out, func(a, b city) int {
		if a.population != b.population {
			return b.population - a.population
		}
		return a.geonameid - b.geonameid
	})
	return out, err
}

// validZones drops the cities whose time zone Go cannot load.
func validZones(cs []city) ([]city, int) {
	ok := map[string]bool{}
	out := cs[:0]
	dropped := 0
	for _, c := range cs {
		good, seen := ok[c.tz]
		if !seen {
			_, err := time.LoadLocation(c.tz)
			good = err == nil
			ok[c.tz] = good
			if !good {
				log.Printf("gencities: unknown time zone %q (%s)", c.tz, c.name)
			}
		}
		if good {
			out = append(out, c)
		} else {
			dropped++
		}
	}
	return out, dropped
}

// chineseNames picks a Chinese name for each GeoNames ID in ids from
// alternateNamesV2.txt: alternateNameId, geonameid, isolanguage, name,
// isPreferredName, isShortName, isColloquial, isHistoric, from, to.
// Simplified Chinese (zh-CN, zh-Hans) wins over plain zh, preferred names
// over others, short names over long ones; colloquial and historic names
// and names with Latin letters or digits are skipped. Names are converted
// to simplified characters where the table knows them.
func chineseNames(r io.Reader, ids map[int]bool) (map[int]string, error) {
	type pick struct {
		name string
		rank int
	}
	best := map[int]pick{}
	err := eachRow(r, func(f []string) error {
		if len(f) < 8 {
			return nil
		}
		rank := 0
		switch field(f, 2) {
		case "zh-CN", "zh-Hans":
			rank = 30
		case "zh-SG":
			rank = 20
		case "zh":
			rank = 10
		default:
			return nil
		}
		id, err := strconv.Atoi(field(f, 1))
		if err != nil || !ids[id] || field(f, 6) == "1" || field(f, 7) == "1" {
			return nil
		}
		name := field(f, 3)
		if !chinese(name) {
			return nil
		}
		if field(f, 4) == "1" {
			rank += 4
		}
		if field(f, 5) == "1" {
			rank += 2
		}
		if simp := toSimplified(name); simp == name {
			rank++ // already simplified (or nothing to convert)
		} else {
			name = simp
		}
		if b, ok := best[id]; !ok || rank > b.rank {
			best[id] = pick{name, rank}
		}
		return nil
	})
	out := make(map[int]string, len(best))
	for id, p := range best {
		out[id] = p.name
	}
	return out, err
}

// chinese reports whether s is a Chinese name: it has Han characters and no
// Latin letters or digits.
func chinese(s string) bool {
	han := false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			han = true
		case r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			return false
		case unicode.In(r, unicode.Latin, unicode.Cyrillic, unicode.Greek, unicode.Hangul, unicode.Hiragana, unicode.Katakana):
			if r != 'ー' && r != '・' && r != '·' {
				return false
			}
		}
	}
	return han
}

// toSimplified converts the traditional characters common in place names
// to simplified ones. It is not a full converter: characters it does not
// know stay as they are.
func toSimplified(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { _, ok := trad[r]; return ok }) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if t, ok := trad[r]; ok {
			r = t
		}
		b.WriteRune(r)
	}
	return b.String()
}

// write writes the data file: gzip'd TSV in three sections.
func (d *data) write(w io.Writer) error {
	zw, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(zw)
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == '\t' || r == '\n' || r == '\r' {
				return ' '
			}
			return r
		}, s)
	}
	fmt.Fprintln(bw, "# GeoNames (geonames.org), CC BY 4.0. Generated by scripts/data/gencities; do not edit.")
	fmt.Fprintln(bw, "#countries")
	for _, c := range d.countries {
		fmt.Fprintf(bw, "%s\t%s\t%s\n", c.code, clean(c.name), clean(c.zh))
	}
	fmt.Fprintln(bw, "#admin1")
	used := map[string]bool{}
	for _, c := range d.cities {
		used[c.country+"."+c.admin1] = true
	}
	for _, r := range d.admin1 {
		if used[r.key] {
			fmt.Fprintf(bw, "%s\t%s\t%s\n", r.key, clean(r.name), clean(r.zh))
		}
	}
	fmt.Fprintln(bw, "#cities")
	for _, c := range d.cities {
		ascii := c.ascii
		if fold(ascii) == fold(c.name) {
			ascii = ""
		}
		fmt.Fprintf(bw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", c.geonameid, clean(c.name), clean(ascii), clean(c.zh), c.country, c.admin1, c.tz)
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}
