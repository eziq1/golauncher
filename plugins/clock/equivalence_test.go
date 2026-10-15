package clock

// TestReferenceEquivalence compares the compact index with the reference
// implementation (reference_test.go) on the real data: the same cities with
// the same fields and match keys, the same countries and zones, the same
// lookups, and for several thousand generated queries the same top 30
// (cities and scores, in order). Delete together with reference_test.go.

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReferenceEquivalence(t *testing.T) {
	ref, err := refLoadIndex()
	if err != nil {
		t.Fatal(err)
	}
	idx, err := loadIndex()
	if err != nil {
		t.Fatal(err)
	}
	compareCities(t, ref, idx)
	queries := equivalenceQueries(ref)
	t.Logf("%d queries generated", len(queries))
	rand.New(rand.NewPCG(3, 5)).Shuffle(len(queries), func(i, j int) { queries[i], queries[j] = queries[j], queries[i] })
	queries = queries[:min(len(queries), 8000)]
	if raceEnabled || testing.Short() {
		queries = queries[:min(len(queries), 400)]
	}
	t.Logf("%d queries", len(queries))
	r := rand.New(rand.NewPCG(1, 2))
	nows := []time.Time{
		time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 15, 22, 30, 0, 0, time.UTC),
	}
	n := len(ref.cities)
	diffs := 0
	for i, q := range queries {
		now := nows[i%2]
		var rank map[int]int
		if i%3 == 0 {
			rank = map[int]int{}
			for j := range 8 {
				rank[r.IntN(n)] = j - n - 1
			}
		}
		want := refHits(ref.find(q, now, rank, maxResults))
		got := newHits(idx, idx.find(q, now, rank, maxResults))
		if !slices.Equal(got, want) {
			diffs++
			if diffs <= 20 {
				t.Errorf("find(%q):\n got %v\nwant %v", q, got, want)
			}
		}
		// Lookups by the query text too.
		wc, wok := ref.lookup(q)
		gc, gok := idx.lookup(q)
		if wok != gok || wok && (wc.ID != gc.ID || wc.Name != gc.Name || wc.TZ != gc.TZ || wc.ord != gc.ord) {
			diffs++
			if diffs <= 20 {
				t.Errorf("lookup(%q) = %v %v, want %v %v", q, gc, gok, wc, wok)
			}
		}
	}
	if diffs > 0 {
		t.Errorf("%d differences in %d queries", diffs, len(queries))
	}
}

func refHits(hs []refHit) []string {
	var out []string
	for _, h := range hs {
		out = append(out, fmt.Sprintf("%s=%v", h.c.ID, h.score))
	}
	return out
}

func newHits(idx *index, hs []hit) []string {
	var out []string
	for _, h := range hs {
		out = append(out, fmt.Sprintf("%s=%v", idx.at(h.ord).ID, h.score))
	}
	return out
}

func compareCities(t *testing.T, ref *refIndex, idx *index) {
	t.Helper()
	if idx.len() != len(ref.cities) {
		t.Fatalf("%d cities, want %d", idx.len(), len(ref.cities))
	}
	bad := 0
	for _, w := range ref.cities {
		g := idx.at(int32(w.ord))
		wk := refKeyStrings(w.keys)
		gk := keyStrings(idx.keys(int32(w.ord)))
		if g.ID != w.ID || g.GeonameID != w.GeonameID || g.Name != w.Name || g.Zh != w.Zh || g.Country != w.Country ||
			g.CountryZh != w.CountryZh || g.TZ != w.TZ || !slices.Equal(g.Aliases, w.Aliases) || g.ascii != w.ascii ||
			g.cc != w.cc || g.region != w.region || g.regionZh != w.regionZh || g.curated != w.curated ||
			g.ambiguous != w.ambiguous || g.ord != w.ord || g.loc.String() != w.loc.String() || !slices.Equal(gk, wk) {
			bad++
			if bad <= 10 {
				t.Errorf("city %d:\n got %+v %v\nwant %+v %v", w.ord, *g, gk, *w, wk)
			}
		}
	}
	if len(idx.countries) != len(ref.countries) {
		t.Fatalf("%d countries, want %d", len(idx.countries), len(ref.countries))
	}
	for i, w := range ref.countries {
		g := idx.countries[i]
		if g.code != w.code || g.name != w.name || g.zh != w.zh || !slices.Equal(g.cities, w.cities) ||
			!slices.Equal(keyStrings(g.keys), refKeyStrings(w.keys)) {
			t.Errorf("country %s differs", w.code)
		}
	}
	if len(idx.zones) != len(ref.zones) {
		t.Fatalf("%d zones, want %d", len(idx.zones), len(ref.zones))
	}
	for i, w := range ref.zones {
		g := idx.zones[i]
		if g.id != w.id || !slices.Equal(g.cities, w.cities) || !slices.Equal(keyStrings(g.keys), refKeyStrings(w.keys)) {
			t.Errorf("zone %s differs", w.id)
		}
	}
	// The name index: the same entries in the same order.
	var we, ge []string
	for _, e := range ref.names.entries {
		we = append(we, fmt.Sprintf("%s:%d", e.s, e.refCity))
	}
	for i := range idx.names.entries {
		ge = append(ge, fmt.Sprintf("%s:%d", idx.names.str(i), idx.names.city(i)))
	}
	if !slices.Equal(ge, we) {
		t.Errorf("name index: %d entries, want %d", len(ge), len(we))
	}
}

func refKeyStrings(keys []refMatchKey) []string {
	var out []string
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%q/%v/%v/%q/%q", k.s, k.w, k.exact, k.compact, k.initials))
	}
	return out
}

func keyStrings(keys []matchKey) []string {
	var out []string
	for _, k := range keys {
		out = append(out, fmt.Sprintf("%q/%v/%v/%q/%q", k.s, k.w, k.exact, k.compact, k.initials))
	}
	return out
}

// equivalenceQueries generates queries from the reference data: prefixes
// of names, ASCII names, Chinese names, pinyin and initials, countries,
// zone IDs, UTC offsets, abbreviations, typos, substrings and mixed case.
func equivalenceQueries(ref *refIndex) []string {
	r := rand.New(rand.NewPCG(7, 11))
	var qs []string
	add := func(q string) {
		if strings.TrimSpace(q) != "" {
			qs = append(qs, q)
		}
	}
	prefixRunes := func(s string, n int) string {
		i := 0
		for j := range s {
			if i == n {
				return s[:j]
			}
			i++
		}
		return s
	}
	variants := func(s string) {
		n := utf8.RuneCountInString(s)
		if n == 0 {
			return
		}
		add(s)
		add(prefixRunes(s, 1+r.IntN(n)))
		add(prefixRunes(s, min(n, 2)))
		add(strings.ToUpper(s[:1]) + s[1:])
		rs := []rune(s)
		if len(rs) >= 4 {
			i := r.IntN(len(rs))
			add(string(slices.Delete(slices.Clone(rs), i, i+1))) // typo: a letter left out
			j := r.IntN(len(rs) - 1)
			sw := slices.Clone(rs)
			sw[j], sw[j+1] = sw[j+1], sw[j]
			add(string(sw)) // typo: swapped letters
			a := r.IntN(len(rs) - 2)
			add(string(rs[a : a+2+r.IntN(len(rs)-a-1)])) // substring
		}
	}
	for i, c := range ref.cities {
		if !c.curated && i%25 != 0 {
			continue
		}
		variants(c.Name)
		variants(c.ascii)
		variants(c.Zh)
		for _, k := range c.keys {
			if k.exact {
				add(k.s)
				continue
			}
			variants(k.s)
			if k.initials != "" {
				add(k.initials)
				add(k.initials[:1+r.IntN(len(k.initials))])
			}
		}
		for _, a := range c.Aliases {
			add(a)
		}
		add(c.ID)
		add(c.region)
	}
	for _, co := range ref.countries {
		variants(co.name)
		variants(co.zh)
		add(co.code)
	}
	for _, z := range ref.zones {
		add(z.id)
		add(strings.ToLower(z.id))
		_, last, _ := strings.Cut(z.id, "/")
		variants(strings.ReplaceAll(last, "_", " "))
		add("tz:" + z.id)
	}
	for h := -12; h <= 14; h++ {
		add(fmt.Sprintf("utc%+d", h))
		add(fmt.Sprintf("gmt%+d", h))
		add(fmt.Sprintf("%+d", h))
		add(fmt.Sprintf("UTC%+03d:30", h))
		add(fmt.Sprintf("utc%+03d45", h))
	}
	for _, q := range []string{"utc", "gmt", "utc+15", "utc+5:99", "utc−3", "utc +8", "Etc/GMT+5", "America/Boise",
		"xyz/abc", "local", "本地", "here", "zzzz", "a", "an", "san", "ber", "上", "zhou", "new york", "newyork", "nyc",
		"san josé", "tromsø", "são", "xi'an", "washington, d.c.", "ho chi minh", "hcmc", "  Beijing  "} {
		add(q)
	}
	for a := range abbreviations {
		add(a)
		add(strings.ToUpper(a))
	}
	const letters = "abcdefghijklmnopqrstuvwxyz"
	for range 300 {
		b := make([]byte, 2+r.IntN(4))
		for i := range b {
			b[i] = letters[r.IntN(len(letters))]
		}
		add(string(b))
	}
	return qs
}

// BenchmarkFindVsReference times find on the reference and the compact
// index in the same process.
func BenchmarkFindVsReference(b *testing.B) {
	ref, err := refLoadIndex()
	if err != nil {
		b.Fatal(err)
	}
	idx, err := loadIndex()
	if err != nil {
		b.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	for _, q := range []string{"b", "bj", "北京", "springfield", "japan", "utc+8", "cst", "sydny"} {
		b.Run(q+"/ref", func(b *testing.B) {
			for b.Loop() {
				ref.find(q, now, nil, maxResults)
			}
		})
		b.Run(q+"/new", func(b *testing.B) {
			for b.Loop() {
				idx.find(q, now, nil, maxResults)
			}
		})
	}
	b.Run("load/ref", func(b *testing.B) {
		for b.Loop() {
			refLoadIndex()
		}
	})
	b.Run("load/new", func(b *testing.B) {
		for b.Loop() {
			loadIndex()
		}
	})
}
