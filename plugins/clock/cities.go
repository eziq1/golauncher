package clock

import (
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mozillazg/go-pinyin"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

//go:embed cities.json
var citiesJSON []byte

// city is a place with a clock.
type city struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Zh        string   `json:"zh"`
	Country   string   `json:"country"`
	CountryZh string   `json:"countryZh"`
	TZ        string   `json:"tz"`
	Aliases   []string `json:"aliases"`

	loc      *time.Location
	keys     []matchKey
	ord      int      // position in cities.json; larger cities come first
	nName    string   // normalized Name
	nAliases []string // normalized Aliases
}

// matchKey is a normalized string a query is matched against, with the
// forms scoreKey compares precomputed.
type matchKey struct {
	s        string
	w        float64 // weight of a match on this key
	exact    bool    // only an exact match counts (pinyin initials)
	compact  string  // s without spaces
	words    []string
	initials string // first letters of the words when there are several
}

func newMatchKey(s string, w float64) matchKey {
	k := matchKey{s: s, w: w, compact: strings.ReplaceAll(s, " ", "")}
	k.words = strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '/' })
	if len(k.words) > 1 {
		var ini strings.Builder
		for _, w := range k.words {
			r, _ := utf8.DecodeRuneInString(w)
			ini.WriteRune(r)
		}
		k.initials = ini.String()
	}
	return k
}

// Weights of the kinds of keys.
const (
	weightName    = 1.0
	weightCountry = 0.8
	weightZone    = 0.85
	// scoreOffset and scoreAbbr are the scores of cities found by UTC offset
	// ("utc+8") or time zone abbreviation ("pst").
	scoreOffset = 0.9
	scoreAbbr   = 0.9
)

// index holds the cities with their match keys and locations.
type index struct {
	cities []*city
	byID   map[string]*city
}

// loadIndex parses cities.json, loads the time zones and builds the match
// keys, including pinyin of the Chinese names.
func loadIndex() (*index, error) {
	var cs []*city
	if err := json.Unmarshal(citiesJSON, &cs); err != nil {
		return nil, fmt.Errorf("clock: cities.json: %w", err)
	}
	idx := &index{byID: make(map[string]*city, len(cs))}
	locs := map[string]*time.Location{}
	for i, c := range cs {
		loc, ok := locs[c.TZ]
		if !ok {
			var err error
			if loc, err = time.LoadLocation(c.TZ); err != nil {
				return nil, fmt.Errorf("clock: city %s: %w", c.ID, err)
			}
			locs[c.TZ] = loc
		}
		if _, dup := idx.byID[c.ID]; dup {
			return nil, fmt.Errorf("clock: duplicate city id %q", c.ID)
		}
		c.loc, c.ord = loc, i
		c.keys = cityKeys(c)
		c.nName = normalize(c.Name)
		for _, a := range c.Aliases {
			c.nAliases = append(c.nAliases, normalize(a))
		}
		idx.cities = append(idx.cities, c)
		idx.byID[c.ID] = c
	}
	return idx, nil
}

// cityKeys returns the match keys of c: names, pinyin, aliases, country and
// time zone ID.
func cityKeys(c *city) []matchKey {
	var keys []matchKey
	add := func(s string, w float64) {
		if s = normalize(s); s != "" {
			keys = append(keys, newMatchKey(s, w))
		}
	}
	addHan := func(s string, w float64) {
		add(s, w)
		if full, initials := pinyinOf(s); full != "" {
			add(full, w)
			// Initials of transliterated names (班加罗尔 -> bjle) would match
			// almost anything as prefixes or substrings.
			keys = append(keys, matchKey{s: initials, w: w, exact: true})
		}
	}
	add(c.Name, weightName)
	addHan(c.Zh, weightName)
	for _, a := range c.Aliases {
		addHan(a, weightName)
	}
	add(c.Country, weightCountry)
	addHan(c.CountryZh, weightCountry)
	add(c.TZ, weightZone)
	if _, last, ok := strings.Cut(c.TZ, "/"); ok {
		if i := strings.LastIndex(last, "/"); i >= 0 {
			last = last[i+1:]
		}
		add(strings.ReplaceAll(last, "_", " "), weightZone)
	}
	return keys
}

var pinyinArgs = pinyin.NewArgs()

// pinyinOf returns the pinyin of the Han characters in s ("北京" ->
// "beijing", "bj"), or "" when s has none.
func pinyinOf(s string) (full, initials string) {
	syl := pinyin.LazyPinyin(s, pinyinArgs)
	if len(syl) == 0 {
		return "", ""
	}
	var ini strings.Builder
	for _, p := range syl {
		ini.WriteByte(p[0])
	}
	return strings.Join(syl, ""), ini.String()
}

// stripMarks removes diacritics: "São Paulo" -> "Sao Paulo".
var stripMarks = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// normalize lowercases s, removes diacritics and punctuation that is often
// left out ("Xi'an", "Washington, D.C.") and collapses spaces. Slashes of
// time zone IDs are kept.
func normalize(s string) string {
	if t, _, err := transform.String(stripMarks, s); err == nil {
		s = t
	}
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\'', '’', '.', ',':
			return -1
		case '-', '_':
			return ' '
		}
		return unicode.ToLower(r)
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// scoreKey scores query q (normalized; qc is q without spaces) against
// key k: 1 for an exact match, less for prefixes, word prefixes, initials,
// substrings and subsequences, 0 for no match.
func scoreKey(q, qc string, k *matchKey) float64 {
	if k.exact {
		if q == k.s {
			return 1
		}
		return 0
	}
	kc := k.compact
	switch {
	case q == k.s, qc == kc: // "newyork"
		return 1
	case strings.HasPrefix(kc, qc):
		return 0.9
	}
	for i, w := range k.words {
		if i > 0 && strings.HasPrefix(w, q) {
			return 0.8 // "york", "shanghai" in "asia/shanghai"
		}
	}
	if k.initials != "" && len(qc) >= 2 {
		switch {
		case k.initials == qc:
			return 0.9 // "hcmc"
		case strings.HasPrefix(k.initials, qc):
			return 0.75
		}
	}
	if utf8.RuneCountInString(qc) >= 2 && strings.Contains(kc, qc) {
		return 0.6
	}
	if len(qc) >= 4 && qc[0] == kc[0] && subsequence(qc, kc) {
		return 0.3 + 0.2*float64(len(qc))/float64(len(kc))
	}
	return 0
}

// subsequence reports whether the runes of q appear in k in order.
func subsequence(q, k string) bool {
	qr := []rune(q)
	i := 0
	for _, r := range k {
		if i < len(qr) && r == qr[i] {
			i++
		}
	}
	return i == len(qr)
}

// scoreCity scores normalized query q against c at time now.
func scoreCity(q, qc string, c *city, now time.Time, off offsetQuery, zones map[string]bool) float64 {
	best := 0.0
	for i := range c.keys {
		k := &c.keys[i]
		best = max(best, k.w*scoreKey(q, qc, k))
	}
	if off.ok {
		if _, o := now.In(c.loc).Zone(); o == off.seconds {
			best = max(best, scoreOffset)
		}
	}
	if zones[c.TZ] {
		best = max(best, scoreAbbr)
	}
	return best
}

// offsetQuery is a query for a UTC offset.
type offsetQuery struct {
	seconds int
	ok      bool
}

// parseOffset parses "utc+8", "gmt-5:30", "UTC+0545", "+8" and "utc" (a
// normalized query) as a UTC offset.
func parseOffset(q string) offsetQuery {
	q = strings.ReplaceAll(q, " ", "")
	q = strings.ReplaceAll(q, "−", "-") // minus sign
	rest, named := strings.CutPrefix(q, "utc")
	if !named {
		rest, named = strings.CutPrefix(q, "gmt")
	}
	if rest == "" {
		return offsetQuery{0, named}
	}
	sign := 1
	switch rest[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return offsetQuery{}
	}
	rest = rest[1:]
	h, m, colon := strings.Cut(rest, ":")
	if !colon && len(rest) > 2 {
		h, m = rest[:len(rest)-2], rest[len(rest)-2:] // "0530"
	}
	hours, ok := digits(h, 1, 2)
	if !ok || hours > 14 {
		return offsetQuery{}
	}
	minutes := 0
	if colon || m != "" {
		if minutes, ok = digits(m, 2, 2); !ok || minutes >= 60 {
			return offsetQuery{}
		}
	}
	return offsetQuery{sign * (hours*3600 + minutes*60), true}
}

// digits parses a decimal number of minLen to maxLen ASCII digits.
func digits(s string, minLen, maxLen int) (int, bool) {
	if len(s) < minLen || len(s) > maxLen {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// abbreviations maps common time zone abbreviations to the zones of the
// cities they stand for. Abbreviations are ambiguous; this is a fixed,
// documented choice (docs/world-clock.md §4). "cst" lists both US Central
// and China Standard Time.
var abbreviations = func() map[string][]string {
	us := map[string][]string{
		"pt":  {"America/Los_Angeles", "America/Vancouver"},
		"mt":  {"America/Denver", "America/Phoenix", "America/Edmonton"},
		"ct":  {"America/Chicago", "America/Mexico_City"},
		"et":  {"America/New_York", "America/Toronto"},
		"at":  {"America/Halifax", "America/Puerto_Rico"},
		"akt": {"America/Anchorage"},
		"nt":  {"America/St_Johns"},
	}
	m := map[string][]string{
		"gmt":  {"UTC", "Europe/London", "Europe/Dublin", "Africa/Accra", "Africa/Dakar", "Atlantic/Reykjavik"},
		"utc":  {"UTC"},
		"bst":  {"Europe/London"},
		"ist":  {"Asia/Kolkata"},
		"wet":  {"Europe/Lisbon"},
		"west": {"Europe/Lisbon"},
		"cet": {"Europe/Paris", "Europe/Berlin", "Europe/Madrid", "Europe/Rome", "Europe/Amsterdam", "Europe/Brussels",
			"Europe/Luxembourg", "Europe/Zurich", "Europe/Vienna", "Europe/Copenhagen", "Europe/Oslo", "Europe/Stockholm",
			"Europe/Warsaw", "Europe/Prague", "Europe/Budapest", "Europe/Belgrade", "Europe/Zagreb", "Africa/Algiers", "Africa/Tunis"},
		"eet": {"Europe/Athens", "Europe/Helsinki", "Europe/Kyiv", "Europe/Bucharest", "Europe/Sofia", "Europe/Riga",
			"Europe/Vilnius", "Europe/Tallinn", "Africa/Cairo", "Asia/Beirut"},
		"msk":  {"Europe/Moscow"},
		"trt":  {"Europe/Istanbul"},
		"gst":  {"Asia/Dubai", "Asia/Muscat"},
		"pkt":  {"Asia/Karachi"},
		"npt":  {"Asia/Kathmandu"},
		"ict":  {"Asia/Bangkok", "Asia/Ho_Chi_Minh", "Asia/Phnom_Penh"},
		"wib":  {"Asia/Jakarta"},
		"sgt":  {"Asia/Singapore"},
		"myt":  {"Asia/Kuala_Lumpur"},
		"pht":  {"Asia/Manila"},
		"hkt":  {"Asia/Hong_Kong"},
		"jst":  {"Asia/Tokyo"},
		"kst":  {"Asia/Seoul"},
		"aest": {"Australia/Sydney", "Australia/Melbourne", "Australia/Brisbane"},
		"aedt": {"Australia/Sydney", "Australia/Melbourne"},
		"acst": {"Australia/Adelaide", "Australia/Darwin"},
		"acdt": {"Australia/Adelaide"},
		"awst": {"Australia/Perth"},
		"nzst": {"Pacific/Auckland"},
		"nzdt": {"Pacific/Auckland"},
		"hst":  {"Pacific/Honolulu"},
		"sast": {"Africa/Johannesburg"},
		"eat":  {"Africa/Nairobi", "Africa/Addis_Ababa", "Africa/Dar_es_Salaam"},
		"wat":  {"Africa/Lagos", "Africa/Kinshasa", "Africa/Luanda"},
		"brt":  {"America/Sao_Paulo"},
		"art":  {"America/Argentina/Buenos_Aires"},
	}
	// US-style zones: "pt", "pst" and "pdt" all mean Pacific Time.
	for k, zones := range us {
		base := k[:len(k)-1]
		for _, v := range []string{k, base + "st", base + "dt"} {
			m[v] = zones
		}
	}
	m["cst"] = slices.Concat(m["cst"], []string{"Asia/Shanghai", "Asia/Taipei", "Asia/Macau", "Asia/Hong_Kong"})
	m["cest"], m["eest"] = m["cet"], m["eet"]
	return m
}()

// zonesOf returns the set of zones abbreviation q stands for, or nil.
func zonesOf(q string) map[string]bool {
	zones := abbreviations[strings.ReplaceAll(q, " ", "")]
	if len(zones) == 0 {
		return nil
	}
	set := make(map[string]bool, len(zones))
	for _, z := range zones {
		set[z] = true
	}
	return set
}

// lookup finds the city a preference entry or favorite names: an ID, an
// English or Chinese name, an alias or an IANA time zone. An IANA zone that
// no city uses becomes a city of its own named after the zone.
func (idx *index) lookup(s string) (*city, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	if c, ok := idx.byID[s]; ok {
		return c, true
	}
	if id, ok := strings.CutPrefix(s, zonePrefix); ok {
		s = id
	}
	n := normalize(s)
	for _, c := range idx.cities {
		if c.nName == n || c.Zh == s || strings.EqualFold(c.TZ, s) {
			return c, true
		}
	}
	for _, c := range idx.cities {
		if slices.Contains(c.nAliases, n) {
			return c, true
		}
	}
	if !strings.Contains(s, "/") {
		return nil, false
	}
	loc, err := time.LoadLocation(s)
	if err != nil {
		return nil, false
	}
	name := s[strings.LastIndex(s, "/")+1:]
	return &city{ID: zonePrefix + s, Name: strings.ReplaceAll(name, "_", " "), TZ: s, loc: loc, ord: len(idx.cities)}, true
}

// zonePrefix starts the ID of a city made up from an IANA zone.
const zonePrefix = "tz:"
