// Package match scores how well a search query matches a piece of text.
//
// It understands prefixes, word prefixes, acronyms ("vsc" -> "Visual Studio
// Code"), substrings and subsequences, and matches Chinese text by pinyin
// ("wx" or "weixin" -> "微信").
package match

import (
	"strings"
	"sync"
	"unicode"

	"github.com/mozillazg/go-pinyin"
)

// Score returns how well query matches target, in [0, 1]. 0 means no match.
// Both are compared case-insensitively.
func Score(query, target string) float64 {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" || target == "" {
		return 0
	}
	t := strings.ToLower(target)
	whole := score(q, t, target)
	// "vs code" also matches when every word matches on its own.
	words := strings.Fields(q)
	if len(words) < 2 {
		return whole
	}
	total := 0.0
	for _, w := range words {
		s := score(w, t, target)
		if s == 0 {
			return whole
		}
		total += s
	}
	return max(whole, 0.9*total/float64(len(words)))
}

// score matches a lowercase query against lowercase t; orig keeps the case
// for camelCase word boundaries.
func score(q, t, orig string) float64 {
	switch {
	case q == t:
		return 1
	case strings.HasPrefix(t, q):
		return 0.9 + 0.05*float64(len(q))/float64(len(t))
	}
	starts := wordStarts(orig)
	tr := []rune(t)
	qr := []rune(q)
	for _, s := range starts {
		if hasRunePrefix(tr[s:], qr) {
			return 0.85
		}
	}
	if isAcronym(qr, tr, starts) {
		return 0.8
	}
	if strings.Contains(t, q) {
		return 0.7
	}
	return subsequence(qr, tr, starts)
}

func hasRunePrefix(s, prefix []rune) bool {
	if len(prefix) > len(s) {
		return false
	}
	for i, r := range prefix {
		if s[i] != r {
			return false
		}
	}
	return true
}

// wordStarts returns the rune indexes where words begin: after separators,
// at camelCase humps, and at digits following letters.
func wordStarts(s string) []int {
	var starts []int
	var prev rune
	i := 0
	for _, r := range s {
		switch {
		case isSep(r):
		case i == 0, isSep(prev),
			unicode.IsUpper(r) && unicode.IsLower(prev),
			unicode.IsDigit(r) && unicode.IsLetter(prev):
			starts = append(starts, i)
		}
		prev = r
		i++
	}
	return starts
}

func isSep(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("-_./\\()[]:,", r)
}

// isAcronym reports whether q matches the initials of consecutive words,
// allowing each word to contribute a prefix ("vscod" matches "VS Code").
func isAcronym(q, t []rune, starts []int) bool {
	if len(starts) < 2 {
		return false
	}
	qi := 0
	for wi, s := range starts {
		if qi == len(q) {
			break
		}
		if t[s] != q[qi] {
			continue
		}
		// Consume as many query runes as this word provides.
		end := len(t)
		if wi+1 < len(starts) {
			end = starts[wi+1]
		}
		for p := s; p < end && qi < len(q) && t[p] == q[qi]; p++ {
			qi++
		}
	}
	return qi == len(q)
}

// subsequence scores q as a scattered subsequence of t, preferring compact
// matches and matches at word starts. It returns 0 when q is not a subsequence.
func subsequence(q, t []rune, starts []int) float64 {
	isStart := make(map[int]bool, len(starts))
	for _, s := range starts {
		isStart[s] = true
	}
	first, last, bonus, qi := -1, -1, 0, 0
	for i := 0; i < len(t) && qi < len(q); i++ {
		if t[i] != q[qi] {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
		if isStart[i] {
			bonus++
		}
		qi++
	}
	if qi < len(q) {
		return 0
	}
	compact := float64(len(q)) / float64(last-first+1)
	return 0.3 + 0.2*compact + 0.15*float64(bonus)/float64(len(q))
}

// Keys returns the strings a text should be matched by: the text itself plus,
// when it contains Chinese characters, its pinyin spelled with and without
// spaces ("微信" -> "wei xin", "weixin").
func Keys(text string) []string {
	py := pinyinOf(text)
	if py == "" {
		return []string{text}
	}
	return []string{text, py, strings.ReplaceAll(py, " ", "")}
}

var (
	pinyinCache sync.Map // string -> string
	pinyinArgs  = pinyin.NewArgs()
)

// pinyinOf converts the Han characters of s to space-separated pinyin and
// keeps other runs as words. It returns "" if s has no Han characters.
func pinyinOf(s string) string {
	if v, ok := pinyinCache.Load(s); ok {
		return v.(string)
	}
	var words []string
	var run strings.Builder
	hasHan := false
	flush := func() {
		if run.Len() > 0 {
			words = append(words, run.String())
			run.Reset()
		}
	}
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			if p := pinyin.SinglePinyin(r, pinyinArgs); len(p) > 0 {
				flush()
				words = append(words, p[0])
				hasHan = true
				continue
			}
		}
		if isSep(r) {
			flush()
			continue
		}
		run.WriteRune(r)
	}
	flush()
	out := ""
	if hasHan {
		out = strings.Join(words, " ")
	}
	pinyinCache.Store(s, out)
	return out
}
