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
// Entries are 8 bytes: the string is a slice of the index's blob.
type nameIndex struct {
	blob    string
	entries []nameEntry
}

// nameEntry is a string at off in the blob with its length (the top
// nameLenBits bits of lc) and a city's ord (the rest).
type nameEntry struct {
	off uint32
	lc  uint32
}

const (
	nameLenBits = 10
	nameOrdBits = 32 - nameLenBits
	maxNameLen  = 1<<nameLenBits - 1
	nameOrdMask = 1<<nameOrdBits - 1
)

func (n *nameIndex) str(i int) string {
	e := n.entries[i]
	return n.blob[e.off : e.off+e.lc>>nameOrdBits]
}

func (n *nameIndex) city(i int) int32 { return int32(n.entries[i].lc & nameOrdMask) }

func newNameIndex(idx *index) nameIndex {
	if idx.len() > nameOrdMask {
		panic("clock: too many cities for the name index")
	}
	var es []nameEntry
	add := func(off uint32, n int, ord int32) {
		es = append(es, nameEntry{off, uint32(n)<<nameOrdBits | uint32(ord)})
	}
	for ord := range int32(idx.len()) {
		keys := idx.cityKeyRecs(ord)
		for i := range keys {
			k := &keys[i]
			add(k.off, int(k.clen), ord) // exact keys: s
			if k.kind&kindExact != 0 {
				continue
			}
			s, soff := idx.s(k)
			laterWords(s, func(rest string) bool {
				n := len(rest)
				if i := strings.IndexAny(rest, " /"); i >= 0 {
					n = i
				}
				add(soff+uint32(len(s)-len(rest)), n, ord)
				return false
			})
			if k.ilen > 0 {
				add(soff+uint32(len(s)), int(k.ilen), ord)
			}
		}
	}
	n := nameIndex{blob: idx.blob}
	str := func(e nameEntry) string { return n.blob[e.off : e.off+e.lc>>nameOrdBits] }
	slices.SortFunc(es, func(a, b nameEntry) int {
		return cmp.Or(strings.Compare(str(a), str(b)), cmp.Compare(a.lc&nameOrdMask, b.lc&nameOrdMask))
	})
	es = slices.CompactFunc(es, func(a, b nameEntry) bool {
		return a.lc&nameOrdMask == b.lc&nameOrdMask && str(a) == str(b)
	})
	n.entries = trim(es)
	return n
}

// search returns the first entry not less than s.
func (n *nameIndex) search(s string) int {
	return sort.Search(len(n.entries), func(i int) bool { return n.str(i) >= s })
}

// withPrefix calls fn with the cities of the entries starting with p.
func (n *nameIndex) withPrefix(p string, fn func(ord int32)) {
	for i := n.search(p); i < len(n.entries) && strings.HasPrefix(n.str(i), p); i++ {
		fn(n.city(i))
	}
}

// exactly returns the cities of the entries equal to s, in ord order.
func (n *nameIndex) exactly(s string) []int32 {
	var out []int32
	for i := n.search(s); i < len(n.entries) && n.str(i) == s; i++ {
		out = append(out, n.city(i))
	}
	return out
}

// hit is a city matching a search with its score.
type hit struct {
	ord   int32
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

	scores := make([]float64, idx.len())
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
			set(ord, idx.bestKey(nq, qc, ord))
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
		for ord := range idx.rows {
			r := &idx.rows[ord]
			best := 0.0
			for _, k := range idx.keyRecs[r.keys : r.keys+uint32(r.n)] {
				// A key shorter than the query neither contains it nor has
				// its runes in order.
				if int(k.clen) < len(qc) || k.kind&kindExact != 0 {
					continue
				}
				w := weightName
				if k.kind&kindCountry != 0 {
					w = weightCountry
				}
				best = max(best, w*weakScoreKey(qc, qr, idx.blob[k.off:k.off+uint32(k.clen)]))
			}
			set(int32(ord), best)
		}
		for _, g := range weak {
			for _, ord := range g.cities {
				set(ord, g.score)
			}
		}
	}

	// Sort keys are computed once: the map lookups in a comparison function
	// cost more than the sort itself.
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
	cands = first(cands, limit)
	hits := make([]hit, len(cands))
	for i, c := range cands {
		hits[i] = hit{c.ord, c.score}
	}
	return hits
}

// cand is a city matching a search, with its sort key.
type cand struct {
	score float64
	key   int // rank, else ord
	ord   int32
}

// before reports whether a comes before b: a higher score, else a lower
// key. Keys differ, so this is a total order.
func (a cand) before(b cand) bool {
	return a.score > b.score || a.score == b.score && a.key < b.key
}

// first returns the first limit candidates of s in order, sorted, without
// sorting all of s (a search can touch thousands of cities). The result is
// that of sorting s and cutting it.
func first(s []cand, limit int) []cand {
	if limit <= 0 {
		return s[:0]
	}
	if len(s) <= 2*limit {
		slices.SortFunc(s, func(a, b cand) int {
			if c := cmp.Compare(b.score, a.score); c != 0 {
				return c
			}
			return cmp.Compare(a.key, b.key)
		})
		return s[:min(len(s), limit)]
	}
	top := make([]cand, 0, limit)
	for _, e := range s {
		n := len(top)
		if n == limit && !e.before(top[n-1]) {
			continue
		}
		i := sort.Search(n, func(i int) bool { return e.before(top[i]) })
		if n < limit {
			top = top[:n+1]
			n++
		}
		copy(top[i+1:n], top[i:n-1])
		top[i] = e
	}
	return top
}

// weakScoreKey is the score scoreKey gives for a substring or subsequence
// match of qc (qr are its runes) on a key with compact form kc, without
// checking for the stronger matches the name index finds.
func weakScoreKey(qc string, qr []rune, kc string) float64 {
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
