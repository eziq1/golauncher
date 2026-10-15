package scoop

import (
	"strings"
	"unicode"
)

// versionParts splits a version into numeric and non-numeric parts:
// "1.2.0-beta3" → ["1" "2" "0" "beta" "3"]. Separators (. - _ +) are dropped.
func versionParts(v string) []string {
	var parts []string
	start, digit := -1, false
	flush := func(i int) {
		if start >= 0 {
			parts = append(parts, v[start:i])
		}
		start = -1
	}
	for i, r := range v {
		switch {
		case r == '.' || r == '-' || r == '_' || r == '+':
			flush(i)
		case start < 0:
			start, digit = i, unicode.IsDigit(r)
		case unicode.IsDigit(r) != digit:
			flush(i)
			start, digit = i, unicode.IsDigit(r)
		}
	}
	flush(len(v))
	return parts
}

func isNumber(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !unicode.IsDigit(r) }) < 0
}

// compareNumbers compares two decimal strings of any length.
func compareNumbers(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// CompareVersions compares two Scoop versions like Scoop's Compare-Version:
// numeric parts compare as numbers, other parts case-insensitively, a number
// is newer than a word at the same position, and a pre-release suffix
// ("1.0-beta") is older than the release ("1.0"). It returns -1, 0 or 1.
func CompareVersions(a, b string) int {
	if strings.EqualFold(a, b) {
		return 0
	}
	pa, pb := versionParts(a), versionParts(b)
	for i := range max(len(pa), len(pb)) {
		switch {
		case i >= len(pa):
			return extraSign(pb[i], -1)
		case i >= len(pb):
			return extraSign(pa[i], 1)
		}
		x, y := pa[i], pb[i]
		nx, ny := isNumber(x), isNumber(y)
		var c int
		switch {
		case nx && ny:
			c = compareNumbers(x, y)
		case nx:
			c = 1
		case ny:
			c = -1
		default:
			c = strings.Compare(strings.ToLower(x), strings.ToLower(y))
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

// extraSign is the result when one version has the extra part p after the
// common ones: sign if p is a number ("1.0.1" > "1.0"), else -sign
// ("1.0-rc" < "1.0").
func extraSign(p string, sign int) int {
	if isNumber(p) {
		return sign
	}
	return -sign
}

// outdated reports whether latest (the bucket's version) is newer than the
// installed one. Nightly builds never are.
func outdated(installed, latest string) bool {
	if installed == "" || latest == "" || strings.EqualFold(installed, "nightly") {
		return false
	}
	return CompareVersions(installed, latest) < 0
}
