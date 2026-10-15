package clock

import (
	"cmp"
	"slices"
	"sort"
	"strings"
	"time"
)

// nameIndex finds the cities whose name keys a query can match strongly
// (exactly, as a prefix, a word prefix or initials): a sorted list of the
// keys' compact forms, their words after the first and their initials.
type nameIndex struct {
	entries []nameEntry
}

type nameEntry struct {
	s    string
	city int32 // ord
}

func newNameIndex(cities []*city) nameIndex {
	var es []nameEntry
	for _, c := range cities {
		ord := int32(c.ord)
		for i := range c.keys {
			k := &c.keys[i]
			if k.exact {
				es = append(es, nameEntry{k.s, ord})
				continue
			}
			es = append(es, nameEntry{k.compact, ord})
			laterWords(k.s, func(rest string) bool {
				if i := strings.IndexAny(rest, " /"); i >= 0 {
					rest = rest[:i]
				}
				es = append(es, nameEntry{rest, ord})
				return false
			})
			if k.initials != "" {
				es = append(es, nameEntry{k.initials, ord})
			}
		}
	}
	slices.SortFunc(es, func(a, b nameEntry) int {
		return cmp.Or(strings.Compare(a.s, b.s), cmp.Compare(a.city, b.city))
	})
	es = slices.Compact(es)
	return nameIndex{es}
}

// withPrefix calls fn with the cities of the entries starting with p.
func (n nameIndex) withPrefix(p string, fn func(ord int32)) {
	i := sort.Search(len(n.entries), func(i int) bool { return n.entries[i].s >= p })
	for ; i < len(n.entries) && strings.HasPrefix(n.entries[i].s, p); i++ {
		fn(n.entries[i].city)
	}
}

// exactly returns the cities of the entries equal to s, in ord order.
func (n nameIndex) exactly(s string) []int32 {
	var out []int32
	i := sort.Search(len(n.entries), func(i int) bool { return n.entries[i].s >= s })
	for ; i < len(n.entries) && n.entries[i].s == s; i++ {
		out = append(out, n.entries[i].city)
	}
	return out
}

// hit is a city matching a search with its score.
type hit struct {
	c     *city
	score float64
}

// weakScore is the highest score a substring or subsequence match gives
// (docs/world-clock.md §9.5).
const weakScore = 0.6

// find returns the cities matching query q at time now, best first, at
// most limit. Among equal scores the cities in rank (by ord; lower first)
// come first, then the others by ord.
func (idx *index) find(q string, now time.Time, rank map[int]int, limit int) []hit {
	nq := normalize(q)
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

	// 1. Strong matches: names through the index; countries and zones
	// (small tables) by their keys, offsets and abbreviations.
	idx.names.withPrefix(qc, func(ord int32) {
		if scores[ord] == 0 {
			set(ord, bestKey(nq, qc, idx.cities[ord].keys))
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
		expand(co.cities, bestRaw(nq, qc, co.keys), weightCountry)
	}
	for _, z := range idx.zones {
		expand(z.cities, bestRaw(nq, qc, z.keys), weightZone)
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
				best = max(best, k.w*weakScoreKey(qc, qr, k))
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
	hits := make([]hit, len(cands))
	for i, c := range cands {
		hits[i] = hit{idx.cities[c.ord], c.score}
	}
	return hits
}

// weakScoreKey is the score scoreKey gives for a substring or subsequence
// match of qc (qr are its runes), without checking for the stronger matches
// the name index finds.
func weakScoreKey(qc string, qr []rune, k *matchKey) float64 {
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

// bestRaw is the best unweighted score of q against keys.
func bestRaw(q, qc string, keys []matchKey) float64 {
	best := 0.0
	for i := range keys {
		best = max(best, scoreKey(q, qc, &keys[i]))
	}
	return best
}

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
