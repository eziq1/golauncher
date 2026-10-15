package scoop

import (
	"cmp"
	"context"
	"slices"
	"strings"
)

// Match is a search result.
type Match struct {
	Pkg   *Package
	Score int
}

// termScore scores how well one lower-case search term matches e; 0 means
// no match. Name matches beat command (bin) matches, which beat
// description matches.
func termScore(e *entry, t string) int {
	switch {
	case e.name == t:
		return 1000
	case strings.HasPrefix(e.name, t):
		return 800 - min(len(e.name)-len(t), 100)
	}
	best := 0
	for _, b := range e.bins {
		switch {
		case b == t:
			best = max(best, 750)
		case strings.HasPrefix(b, t):
			best = max(best, 550)
		}
	}
	if i := strings.Index(e.name, t); i >= 0 {
		best = max(best, 600-min(i, 50)-min(len(e.name)-len(t), 100))
	}
	if best > 0 {
		return best
	}
	for _, w := range e.words {
		if strings.HasPrefix(w, t) {
			return 300
		}
	}
	if strings.Contains(e.desc, t) {
		return 200
	}
	if len(t) >= 2 && subsequence(e.name, t) {
		return 100
	}
	return 0
}

// subsequence reports whether the letters of t appear in s in order.
func subsequence(s, t string) bool {
	for _, r := range t {
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+len(string(r)):]
	}
	return true
}

// Search returns up to limit packages matching every word of query, best
// first. Ties go to the better-known bucket, then the shorter name.
func (ix *Index) Search(ctx context.Context, query string, limit int) []Match {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 || ix == nil {
		return nil
	}
	type hit struct {
		e     *entry
		score int
	}
	var hits []hit
	for i := range ix.entries {
		if i%1024 == 0 && ctx.Err() != nil {
			return nil
		}
		e := &ix.entries[i]
		total := 0
		for k, t := range terms {
			s := termScore(e, t)
			if s == 0 {
				total = 0
				break
			}
			if k == 0 {
				s *= 2 // the first word is usually the name
			}
			total += s
		}
		if total > 0 {
			hits = append(hits, hit{e, total})
		}
	}
	slices.SortFunc(hits, func(a, b hit) int {
		return cmp.Or(
			cmp.Compare(b.score, a.score),
			cmp.Compare(a.e.rank, b.e.rank),
			cmp.Compare(len(a.e.name), len(b.e.name)),
			strings.Compare(a.e.name, b.e.name),
			strings.Compare(a.e.pkg.Bucket, b.e.pkg.Bucket),
		)
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Match, len(hits))
	for i, h := range hits {
		out[i] = Match{Pkg: h.e.pkg, Score: h.score}
	}
	return out
}
