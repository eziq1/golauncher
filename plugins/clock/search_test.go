package clock

import (
	"cmp"
	"math/rand/v2"
	"slices"
	"testing"
)

// TestFirst: first gives the result of sorting and cutting.
func TestFirst(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 1))
	for range 200 {
		n := r.IntN(300)
		var s []cand
		for i, k := range r.Perm(n) {
			s = append(s, cand{score: float64(r.IntN(4)) / 4, key: k - n/2, ord: int32(i)})
		}
		want := slices.SortedFunc(slices.Values(s), func(a, b cand) int {
			return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.key, b.key))
		})
		for _, limit := range []int{0, 1, 5, 30, 1000} {
			if got := first(slices.Clone(s), limit); !slices.Equal(got, want[:min(len(want), limit)]) {
				t.Fatalf("first(%v, %d) = %v, want %v", s, limit, got, want[:min(len(want), limit)])
			}
		}
	}
}
