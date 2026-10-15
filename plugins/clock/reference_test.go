package clock

// The implementation before the R41 memory work (docs/world-clock.md §10.4),
// kept as a reference: TestReferenceEquivalence compares the new index with
// it. Delete once the new index is done.

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	"encoding/json/v2"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mozillazg/go-pinyin"
	"golang.org/x/text/transform"
)

// refCity is a place with a clock.
type refCity struct {
	ID        string   `json:"id"`
	GeonameID int      `json:"geonameid"`
	Name      string   `json:"name"`
	Zh        string   `json:"zh"`
	Country   string   `json:"country"`
	CountryZh string   `json:"countryZh"`
	TZ        string   `json:"tz"`
	Aliases   []string `json:"aliases"`

	ascii     string // ASCII name, when it differs from Name
	cc        string // ISO refCountry code ("" for UTC)
	region    string // first-level division (state, province)
	regionZh  string
	curated   bool // from cities.json
	ambiguous bool // another refCity has the same name

	loc  *time.Location
	keys []refMatchKey // names, pinyin and aliases (and countries of curated cities)
	ord  int           // position in refIndex.cities: curated first, then by population
}

// refMatchKey is a normalized string a query is matched against, with the
// forms refScoreKey compares precomputed.
type refMatchKey struct {
	s        string
	w        float64 // weight of a match on this key
	exact    bool    // only an exact match counts (pinyin initials)
	compact  string  // s without spaces
	initials string  // first letters of the words when there are several
}

func refNewMatchKey(s string, w float64) refMatchKey {
	k := refMatchKey{s: s, w: w, compact: strings.ReplaceAll(s, " ", "")}
	if words := strings.FieldsFunc(s, isWordSep); len(words) > 1 {
		var ini strings.Builder
		for _, w := range words {
			r, _ := utf8.DecodeRuneInString(w)
			ini.WriteRune(r)
		}
		k.initials = ini.String()
	}
	return k
}

// refLaterWords calls fn with each word of s after the first, as a suffix of s
// starting at the word ("asia/ho chi minh" -> "ho chi minh", "chi minh",
// "minh"). Words are not stored, which saves memory with 30,000 cities.
func refLaterWords(s string, fn func(rest string) bool) {
	sep := func(b byte) bool { return b == ' ' || b == '/' }
	i := 0
	for i < len(s) && sep(s[i]) { // separators before the first word
		i++
	}
	for i < len(s) && !sep(s[i]) { // the first word
		i++
	}
	for ; i < len(s)-1; i++ {
		if sep(s[i]) && !sep(s[i+1]) && fn(s[i+1:]) {
			return
		}
	}
}

// refCountry is a refCountry of the GeoNames data with its cities.
type refCountry struct {
	code, name, zh string
	keys           []refMatchKey
	cities         []int32 // ords, ascending
}

// refZone is an IANA time refZone with its cities.
type refZone struct {
	id     string
	loc    *time.Location
	keys   []refMatchKey
	cities []int32 // ords, ascending
}

// refIndex holds the cities with their match keys and locations, the
// countries and zones, and the lookup tables (docs/world-clock.md §9.5).
type refIndex struct {
	cities    []*refCity
	countries []*refCountry
	zones     []*refZone
	zoneByID  map[string]*refZone
	names     refNameIndex

	byID        map[string]*refCity // curated cities
	byGeonameID []int32             // ords of the GeoNames cities by GeoNames ID
	zoneByLower map[string]*refZone // lowercase refZone IDs
}

// refLoadIndex loads the embedded cities.
func refLoadIndex() (*refIndex, error) {
	return refBuildIndex(citiesJSON, bytes.NewReader(geonamesData))
}

// refBuildIndex reads the curated cities (JSON) and the gzip'd GeoNames data,
// merges them, loads the time zones and builds the match keys and lookup
// tables.
func refBuildIndex(curatedJSON []byte, refGeonames io.Reader) (*refIndex, error) {
	var curated []*refCity
	if err := json.Unmarshal(curatedJSON, &curated); err != nil {
		return nil, fmt.Errorf("clock: cities.json: %w", err)
	}
	g, err := refParseGeonames(refGeonames)
	if err != nil {
		return nil, err
	}
	idx := &refIndex{zoneByID: map[string]*refZone{}, byID: map[string]*refCity{}}
	byGeoname := map[int]*refCity{}
	for _, c := range curated {
		if _, dup := idx.byID[c.ID]; dup {
			return nil, fmt.Errorf("clock: duplicate refCity id %q", c.ID)
		}
		if _, err := idx.refZone(c.TZ); err != nil {
			return nil, fmt.Errorf("clock: refCity %s: %w", c.ID, err)
		}
		c.curated = true
		idx.add(c)
		if c.GeonameID != 0 {
			byGeoname[c.GeonameID] = c
		}
	}
	countries := map[string]*refCountry{}
	for _, c := range g.countries {
		countries[c.code] = c
		idx.countries = append(idx.countries, c)
	}
	for _, row := range g.cities {
		r := g.regions[row.cc+"."+row.admin1]
		if c, ok := byGeoname[row.id]; ok {
			// The overlay wins; GeoNames adds the region and refCountry code.
			c.cc, c.region, c.regionZh = row.cc, r.name, r.zh
			continue
		}
		if _, err := idx.refZone(row.tz); err != nil {
			continue // a refZone this Go does not know; the generator checks them
		}
		c := &refCity{ID: strconv.Itoa(row.id), GeonameID: row.id, Name: row.name, ascii: row.ascii, Zh: row.zh,
			TZ: row.tz, cc: row.cc, region: r.name, regionZh: r.zh}
		if co := countries[row.cc]; co != nil {
			c.Country, c.CountryZh = co.name, co.zh
		}
		idx.add(c)
	}
	idx.finish(countries)
	return idx, nil
}

// add appends c to the cities.
func (idx *refIndex) add(c *refCity) {
	c.ord = len(idx.cities)
	idx.cities = append(idx.cities, c)
	if c.curated {
		idx.byID[c.ID] = c
	}
}

// refZone returns the refZone with IANA ID id, loading it the first time.
func (idx *refIndex) refZone(id string) (*refZone, error) {
	if z, ok := idx.zoneByID[id]; ok {
		return z, nil
	}
	loc, err := time.LoadLocation(id)
	if err != nil {
		return nil, err
	}
	z := &refZone{id: id, loc: loc}
	z.keys = append(z.keys, refNewMatchKey(refNormalize(id), weightZone))
	if _, last, ok := strings.Cut(id, "/"); ok {
		if i := strings.LastIndex(last, "/"); i >= 0 {
			last = last[i+1:]
		}
		z.keys = append(z.keys, refNewMatchKey(refNormalize(strings.ReplaceAll(last, "_", " ")), weightZone))
	}
	idx.zoneByID[id] = z
	idx.zones = append(idx.zones, z)
	return z, nil
}

// finish computes what needs all cities: locations, match keys, the refCity
// lists of countries and zones, ambiguous names, the lookup tables and the
// name refIndex.
func (idx *refIndex) finish(countries map[string]*refCountry) {
	names := map[string]int{}
	nNames := make([]string, len(idx.cities))
	for _, c := range idx.cities {
		z := idx.zoneByID[c.TZ]
		c.loc = z.loc
		z.cities = append(z.cities, int32(c.ord))
		if co := countries[c.cc]; co != nil {
			co.cities = append(co.cities, int32(c.ord))
		}
		c.keys = refCityKeys(c)
		n := refNormalize(c.Name)
		names[n]++
		nNames[c.ord] = n
		if !c.curated {
			idx.byGeonameID = append(idx.byGeonameID, int32(c.ord))
		}
	}
	for _, c := range idx.cities {
		c.ambiguous = names[nNames[c.ord]] > 1
	}
	slices.SortFunc(idx.byGeonameID, func(a, b int32) int {
		return cmp.Compare(idx.cities[a].GeonameID, idx.cities[b].GeonameID)
	})
	idx.zoneByLower = make(map[string]*refZone, len(idx.zones))
	for _, z := range idx.zones {
		idx.zoneByLower[strings.ToLower(z.id)] = z
	}
	for _, co := range idx.countries {
		co.keys = nil
		add := func(s string) {
			if s = refNormalize(s); s != "" {
				co.keys = append(co.keys, refNewMatchKey(s, weightCountry))
			}
		}
		add(co.name)
		add(co.zh)
		if full, initials := refPinyinOf(co.zh); full != "" {
			add(full)
			co.keys = append(co.keys, refMatchKey{s: initials, w: weightCountry, exact: true})
		}
	}
	idx.names = refNewNameIndex(idx.cities)
}

// refChinaCodes are the countries whose cities' pinyin initials are matched
// ("sz" for 深圳); initials of transliterated foreign names mean nothing.
var refChinaCodes = map[string]bool{"CN": true, "TW": true, "HK": true, "MO": true}

// refCityKeys returns the match keys of c: names, Chinese name and its pinyin,
// aliases, and for curated cities also their refCountry. Countries and zones of
// all cities are matched through refIndex.countries and refIndex.zones.
func refCityKeys(c *refCity) []refMatchKey {
	var keys []refMatchKey
	add := func(s string, w float64) {
		if s = refNormalize(s); s != "" && !slices.ContainsFunc(keys, func(k refMatchKey) bool { return k.s == s && k.w == w }) {
			keys = append(keys, refNewMatchKey(s, w))
		}
	}
	addHan := func(s string, w float64, initials bool) {
		add(s, w)
		if full, ini := refPinyinOf(s); full != "" {
			add(full, w)
			// Initials only count as exact matches: as prefixes or
			// substrings they would match almost anything.
			if initials {
				keys = append(keys, refMatchKey{s: ini, w: w, exact: true})
			}
		}
	}
	add(c.Name, weightName)
	add(c.ascii, weightName)
	addHan(c.Zh, weightName, c.curated || refChinaCodes[c.cc])
	for _, a := range c.Aliases {
		addHan(a, weightName, true)
	}
	if c.curated {
		add(c.Country, weightCountry)
		addHan(c.CountryZh, weightCountry, true)
	}
	return keys
}

// refGeonames is the parsed GeoNames data file.
type refGeonames struct {
	countries []*refCountry
	regions   map[string]refRegionName // "US.IL"
	cities    []refGeonameRow          // by population, largest first
}

type refRegionName struct{ name, zh string }

type refGeonameRow struct {
	id                              int
	name, ascii, zh, cc, admin1, tz string
}

// refParseGeonames parses the gzip'd TSV written by scripts/gencities:
// sections "#countries" (code, name, Chinese name), "#admin1" ("US.IL",
// name, Chinese name) and "#cities" (GeoNames ID, name, ASCII name, Chinese
// name, refCountry code, admin1 code, time refZone). Other lines starting with
// "#" are comments.
func refParseGeonames(r io.Reader) (*refGeonames, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("clock: refCity data: %w", err)
	}
	g := &refGeonames{regions: map[string]refRegionName{}}
	sc := bufio.NewScanner(zr)
	section := ""
	line := 0
	for sc.Scan() {
		line++
		s := sc.Text()
		if s == "" {
			continue
		}
		if s[0] == '#' {
			switch s {
			case "#countries", "#admin1", "#cities":
				section = s
			}
			continue
		}
		f := strings.Split(s, "\t")
		bad := func() error { return fmt.Errorf("clock: refCity data line %d: %d fields in %s", line, len(f), section) }
		switch section {
		case "#countries":
			if len(f) != 3 {
				return nil, bad()
			}
			g.countries = append(g.countries, &refCountry{code: f[0], name: f[1], zh: f[2]})
		case "#admin1":
			if len(f) != 3 {
				return nil, bad()
			}
			g.regions[f[0]] = refRegionName{f[1], f[2]}
		case "#cities":
			if len(f) != 7 {
				return nil, bad()
			}
			id, err := strconv.Atoi(f[0])
			if err != nil {
				return nil, fmt.Errorf("clock: refCity data line %d: %w", line, err)
			}
			g.cities = append(g.cities, refGeonameRow{id: id, name: f[1], ascii: f[2], zh: f[3], cc: f[4], admin1: f[5], tz: f[6]})
		default:
			return nil, fmt.Errorf("clock: refCity data line %d: outside a section", line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("clock: refCity data: %w", err)
	}
	return g, nil
}

// refPinyinOf returns the pinyin of the Han characters in s ("北京" ->
// "beijing", "bj"), or "" when s has none.
func refPinyinOf(s string) (full, initials string) {
	if s == "" {
		return "", ""
	}
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

// refNormalize lowercases s, removes diacritics and punctuation that is often
// left out ("Xi'an", "Washington, D.C.") and collapses spaces. Slashes of
// time refZone IDs are kept.
func refNormalize(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if !ascii {
		if t, _, err := transform.String(stripMarks, s); err == nil {
			s = t
		}
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

// refScoreKey scores query q (normalized; qc is q without spaces) against
// key k: 1 for an exact match, less for prefixes, word prefixes, initials,
// substrings and subsequences, 0 for no match. Scores of at least
// strongScore come from the name refIndex; weaker ones (substrings and
// subsequences) only from a full scan.
func refScoreKey(q, qc string, k *refMatchKey) float64 {
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
	// A query without separators is a prefix of a word exactly when it is a
	// prefix of the rest of the key starting at that word.
	if !strings.ContainsAny(q, " /") {
		word := false
		refLaterWords(k.s, func(rest string) bool {
			word = strings.HasPrefix(rest, q)
			return word
		})
		if word {
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
	if len(qc) >= 4 && qc[0] == kc[0] && refSubsequence(qc, kc) {
		return 0.3 + 0.2*float64(len(qc))/float64(len(kc))
	}
	return 0
}

// refSubsequence reports whether the runes of q appear in k in order.
func refSubsequence(q, k string) bool {
	qr := []rune(q)
	i := 0
	for _, r := range k {
		if i < len(qr) && r == qr[i] {
			i++
		}
	}
	return i == len(qr)
}

// refBestKey is the best weighted score of q against keys.
func refBestKey(q, qc string, keys []refMatchKey) float64 {
	best := 0.0
	for i := range keys {
		k := &keys[i]
		best = max(best, k.w*refScoreKey(q, qc, k))
	}
	return best
}

// lookup finds the refCity a preference entry or favorite names: an ID, an
// English or Chinese name, an alias or an IANA time refZone. Among cities of
// the same name the first (curated, then larger) wins. An IANA refZone that no
// refCity uses becomes a refCity of its own named after the refZone.
func (idx *refIndex) lookup(s string) (*refCity, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	if c := idx.refCity(s); c != nil {
		return c, true
	}
	if id, ok := strings.CutPrefix(s, zonePrefix); ok {
		s = id
	}
	n := refNormalize(s)
	if c := idx.withKey(n, func(c *refCity) bool { return refNormalize(c.Name) == n || c.ascii != "" && refNormalize(c.ascii) == n }); c != nil {
		return c, true
	}
	if c := idx.withKey(n, func(c *refCity) bool { return c.Zh == s }); c != nil {
		return c, true
	}
	if z := idx.zoneByLower[strings.ToLower(s)]; z != nil && len(z.cities) > 0 {
		return idx.cities[z.cities[0]], true
	}
	if c := idx.withKey(n, func(c *refCity) bool {
		return slices.ContainsFunc(c.Aliases, func(a string) bool { return refNormalize(a) == n })
	}); c != nil {
		return c, true
	}
	if !strings.Contains(s, "/") {
		return nil, false
	}
	loc, err := time.LoadLocation(s)
	if err != nil {
		return nil, false
	}
	name := s[strings.LastIndex(s, "/")+1:]
	return &refCity{ID: zonePrefix + s, Name: strings.ReplaceAll(name, "_", " "), TZ: s, loc: loc, ord: len(idx.cities)}, true
}

// refCity returns the refCity with ID id, or nil.
func (idx *refIndex) refCity(id string) *refCity {
	if c, ok := idx.byID[id]; ok {
		return c
	}
	g, err := strconv.Atoi(id)
	if err != nil {
		return nil
	}
	i, ok := slices.BinarySearchFunc(idx.byGeonameID, g, func(ord int32, g int) int {
		return cmp.Compare(idx.cities[ord].GeonameID, g)
	})
	if !ok || idx.cities[idx.byGeonameID[i]].ID != id {
		return nil
	}
	return idx.cities[idx.byGeonameID[i]]
}

// withKey returns the first refCity with a match key whose compact form is
// that of normalized string n and for which match is true, or nil.
func (idx *refIndex) withKey(n string, match func(*refCity) bool) *refCity {
	for _, ord := range idx.names.exactly(strings.ReplaceAll(n, " ", "")) {
		if c := idx.cities[ord]; match(c) {
			return c
		}
	}
	return nil
}

// refNameIndex finds the cities whose name keys a query can match strongly
// (exactly, as a prefix, a word prefix or initials): a sorted list of the
// keys' compact forms, their words after the first and their initials.
type refNameIndex struct {
	entries []refNameEntry
}

type refNameEntry struct {
	s       string
	refCity int32 // ord
}

func refNewNameIndex(cities []*refCity) refNameIndex {
	var es []refNameEntry
	for _, c := range cities {
		ord := int32(c.ord)
		for i := range c.keys {
			k := &c.keys[i]
			if k.exact {
				es = append(es, refNameEntry{k.s, ord})
				continue
			}
			es = append(es, refNameEntry{k.compact, ord})
			refLaterWords(k.s, func(rest string) bool {
				if i := strings.IndexAny(rest, " /"); i >= 0 {
					rest = rest[:i]
				}
				es = append(es, refNameEntry{rest, ord})
				return false
			})
			if k.initials != "" {
				es = append(es, refNameEntry{k.initials, ord})
			}
		}
	}
	slices.SortFunc(es, func(a, b refNameEntry) int {
		return cmp.Or(strings.Compare(a.s, b.s), cmp.Compare(a.refCity, b.refCity))
	})
	es = slices.Compact(es)
	return refNameIndex{es}
}

// withPrefix calls fn with the cities of the entries starting with p.
func (n refNameIndex) withPrefix(p string, fn func(ord int32)) {
	i := sort.Search(len(n.entries), func(i int) bool { return n.entries[i].s >= p })
	for ; i < len(n.entries) && strings.HasPrefix(n.entries[i].s, p); i++ {
		fn(n.entries[i].refCity)
	}
}

// exactly returns the cities of the entries equal to s, in ord order.
func (n refNameIndex) exactly(s string) []int32 {
	var out []int32
	i := sort.Search(len(n.entries), func(i int) bool { return n.entries[i].s >= s })
	for ; i < len(n.entries) && n.entries[i].s == s; i++ {
		out = append(out, n.entries[i].refCity)
	}
	return out
}

// refHit is a refCity matching a search with its score.
type refHit struct {
	c     *refCity
	score float64
}

// find returns the cities matching query q at time now, best first, at
// most limit. Among equal scores the cities in rank (by ord; lower first)
// come first, then the others by ord.
func (idx *refIndex) find(q string, now time.Time, rank map[int]int, limit int) []refHit {
	nq := refNormalize(q)
	if nq == "" {
		return nil
	}
	qc := strings.ReplaceAll(nq, " ", "")
	// Offsets are parsed before normalizing, which turns "-" into a space.
	off, abbr := parseOffset(strings.ToLower(strings.TrimSpace(q))), zonesOf(nq)

	scores := make([]float64, len(idx.cities))
	var touched []int32
	set := func(ord int32, s float64) {
		if s <= 0 {
			return
		}
		if scores[ord] == 0 {
			touched = append(touched, ord)
		}
		scores[ord] = max(scores[ord], s)
	}

	// 1. Strong matches: names through the refIndex; countries and zones
	// (small tables) by their keys, offsets and abbreviations.
	idx.names.withPrefix(qc, func(ord int32) {
		if scores[ord] == 0 {
			set(ord, refBestKey(nq, qc, idx.cities[ord].keys))
		}
	})
	type group struct {
		cities []int32
		score  float64
	}
	var weak []group
	expand := func(cities []int32, raw, w float64) {
		switch {
		case raw >= strongScore:
			for _, ord := range cities {
				set(ord, raw*w)
			}
		case raw > 0:
			weak = append(weak, group{cities, raw * w})
		}
	}
	for _, co := range idx.countries {
		expand(co.cities, refBestRaw(nq, qc, co.keys), weightCountry)
	}
	for _, z := range idx.zones {
		expand(z.cities, refBestRaw(nq, qc, z.keys), weightZone)
		s := 0.0
		if off.ok {
			if _, o := now.In(z.loc).Zone(); o == off.seconds {
				s = scoreOffset
			}
		}
		if abbr[z.id] {
			s = max(s, scoreAbbr)
		}
		if s > 0 {
			for _, ord := range z.cities {
				set(ord, s)
			}
		}
	}

	// 2. Substrings and subsequences can only make the list when fewer than
	// limit cities matched better.
	better := 0
	for _, ord := range touched {
		if scores[ord] > weakScore {
			better++
		}
	}
	if better < limit {
		qr := []rune(qc)
		for _, c := range idx.cities {
			best := 0.0
			for i := range c.keys {
				k := &c.keys[i]
				best = max(best, k.w*refWeakScoreKey(qc, qr, k))
			}
			set(int32(c.ord), best)
		}
		for _, g := range weak {
			for _, ord := range g.cities {
				set(ord, g.score)
			}
		}
	}

	// Sort keys are computed once: the map lookups in a comparison function
	// cost more than the sort itself.
	type cand struct {
		score float64
		key   int // rank, else ord
		ord   int32
	}
	cands := make([]cand, len(touched))
	for i, ord := range touched {
		cands[i] = cand{scores[ord], int(ord), ord}
	}
	if len(rank) > 0 {
		for i := range cands {
			if r, ok := rank[int(cands[i].ord)]; ok {
				cands[i].key = r
			}
		}
	}
	slices.SortFunc(cands, func(a, b cand) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		return cmp.Compare(a.key, b.key)
	})
	cands = cands[:min(len(cands), limit)]
	hits := make([]refHit, len(cands))
	for i, c := range cands {
		hits[i] = refHit{idx.cities[c.ord], c.score}
	}
	return hits
}

// refWeakScoreKey is the score refScoreKey gives for a substring or refSubsequence
// match of qc (qr are its runes), without checking for the stronger matches
// the name refIndex finds.
func refWeakScoreKey(qc string, qr []rune, k *refMatchKey) float64 {
	if k.exact {
		return 0
	}
	kc := k.compact
	if len(qr) >= 2 && strings.Contains(kc, qc) {
		return 0.6
	}
	if len(qc) >= 4 && kc != "" && qc[0] == kc[0] {
		i := 0
		for _, r := range kc {
			if i < len(qr) && r == qr[i] {
				i++
			}
		}
		if i == len(qr) {
			return 0.3 + 0.2*float64(len(qc))/float64(len(kc))
		}
	}
	return 0
}

// refBestRaw is the best unweighted score of q against keys.
func refBestRaw(q, qc string, keys []refMatchKey) float64 {
	best := 0.0
	for i := range keys {
		best = max(best, refScoreKey(q, qc, &keys[i]))
	}
	return best
}
