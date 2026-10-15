package calc

import (
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// Clothing and shoe sizes (docs/unit-conversion.md §5.2), looked up in
// charts: "US 9 shoe in EU", "鞋码 42 in us", "women US 7 shoes to EU",
// "衣服 M in eu", "clothing EU 48 to US". A kind word (shoe, 鞋, clothing,
// 衣服…) is required so that "us 9" stays an ordinary search. Sizes vary
// by brand; the charts are typical ones.

// Size systems.
const (
	sysUS  = "US"
	sysUK  = "UK"
	sysEU  = "EU"
	sysCN  = "CN"  // shoes: foot length in mm (GB/T 3293.1); clothing: height/chest
	sysJP  = "JP"  // shoes: foot length in cm
	sysINT = "INT" // clothing: letter sizes
)

// sizeChart is a chart: one column per system, one row per size.
type sizeChart struct {
	systems []string
	rows    [][]string
	// tolerance is how far a number may be from a chart value.
	tolerance float64
}

var (
	shoeSystems = []string{sysUS, sysUK, sysEU, sysCN, sysJP}
	menShoes    = sizeChart{systems: shoeSystems, tolerance: 0.5, rows: [][]string{
		{"6", "5.5", "38.5", "240", "24"}, {"6.5", "6", "39", "245", "24.5"}, {"7", "6", "40", "250", "25"},
		{"7.5", "6.5", "40.5", "255", "25.5"}, {"8", "7", "41", "260", "26"}, {"8.5", "7.5", "42", "265", "26.5"},
		{"9", "8", "42.5", "270", "27"}, {"9.5", "8.5", "43", "275", "27.5"}, {"10", "9", "44", "280", "28"},
		{"10.5", "9.5", "44.5", "285", "28.5"}, {"11", "10", "45", "290", "29"}, {"11.5", "10.5", "45.5", "295", "29.5"},
		{"12", "11", "46", "300", "30"}, {"13", "12", "47.5", "310", "31"}, {"14", "13", "48.5", "320", "32"},
	}}
	womenShoes = sizeChart{systems: shoeSystems, tolerance: 0.5, rows: [][]string{
		{"5", "2.5", "35.5", "220", "22"}, {"5.5", "3", "36", "225", "22.5"}, {"6", "3.5", "36.5", "230", "23"},
		{"6.5", "4", "37.5", "235", "23.5"}, {"7", "4.5", "38", "240", "24"}, {"7.5", "5", "38.5", "245", "24.5"},
		{"8", "5.5", "39", "250", "25"}, {"8.5", "6", "40", "255", "25.5"}, {"9", "6.5", "40.5", "260", "26"},
		{"9.5", "7", "41", "265", "26.5"}, {"10", "7.5", "42", "270", "27"}, {"10.5", "8", "42.5", "275", "27.5"},
		{"11", "8.5", "43", "280", "28"},
	}}
	// Men's tops: US is the chest in inches.
	menClothing = sizeChart{systems: []string{sysINT, sysUS, sysEU, sysCN}, tolerance: 1, rows: [][]string{
		{"XS", "34", "44", "160/80A"}, {"S", "36", "46", "165/84A"}, {"M", "38", "48", "170/88A"},
		{"L", "40", "50", "175/92A"}, {"XL", "42", "52", "180/96A"}, {"XXL", "44", "54", "185/100A"},
		{"XXXL", "46", "56", "190/104A"},
	}}
	womenClothing = sizeChart{systems: []string{sysINT, sysUS, sysUK, sysEU, sysCN}, tolerance: 1, rows: [][]string{
		{"XS", "2", "6", "34", "155/80A"}, {"S", "4", "8", "36", "160/84A"}, {"M", "6", "10", "38", "165/88A"},
		{"L", "8", "12", "40", "170/92A"}, {"XL", "10", "14", "42", "175/96A"}, {"XXL", "12", "16", "44", "180/100A"},
	}}
)

// sizeWords map words to what they say about a size query.
var sizeWords = map[string]struct {
	kind   string // "shoe" or "clothing"
	gender string // "men" or "women"
	system string
}{
	"shoe": {kind: "shoe"}, "shoes": {kind: "shoe"}, "shoe size": {kind: "shoe"}, "鞋": {kind: "shoe"}, "鞋码": {kind: "shoe"},
	"鞋子": {kind: "shoe"}, "sneakers": {kind: "shoe"},
	"男鞋": {kind: "shoe", gender: "men"}, "女鞋": {kind: "shoe", gender: "women"},
	"clothing": {kind: "clothing"}, "clothes": {kind: "clothing"}, "shirt": {kind: "clothing"}, "shirts": {kind: "clothing"},
	"top": {kind: "clothing"}, "tops": {kind: "clothing"}, "jacket": {kind: "clothing"}, "衣服": {kind: "clothing"},
	"上衣": {kind: "clothing"}, "衣码": {kind: "clothing"}, "服装": {kind: "clothing"}, "尺码": {kind: "clothing"},
	"男装": {kind: "clothing", gender: "men"}, "女装": {kind: "clothing", gender: "women"},
	"men": {gender: "men"}, "mens": {gender: "men"}, "men's": {gender: "men"}, "man": {gender: "men"}, "male": {gender: "men"},
	"男": {gender: "men"}, "男款": {gender: "men"}, "男士": {gender: "men"},
	"women": {gender: "women"}, "womens": {gender: "women"}, "women's": {gender: "women"}, "woman": {gender: "women"},
	"female": {gender: "women"}, "ladies": {gender: "women"}, "lady": {gender: "women"}, "女": {gender: "women"},
	"女款": {gender: "women"}, "女士": {gender: "women"},
	"us": {system: sysUS}, "usa": {system: sysUS}, "美": {system: sysUS}, "美码": {system: sysUS}, "美国": {system: sysUS},
	"uk": {system: sysUK}, "英": {system: sysUK}, "英码": {system: sysUK}, "英国": {system: sysUK},
	"eu": {system: sysEU}, "euro": {system: sysEU}, "欧": {system: sysEU}, "欧码": {system: sysEU}, "欧洲": {system: sysEU},
	"cn": {system: sysCN}, "china": {system: sysCN}, "中": {system: sysCN}, "国码": {system: sysCN}, "中国": {system: sysCN},
	"jp": {system: sysJP}, "japan": {system: sysJP}, "日": {system: sysJP}, "日码": {system: sysJP}, "日本": {system: sysJP},
	"int": {system: sysINT}, "intl": {system: sysINT}, "international": {system: sysINT}, "国际": {system: sysINT},
}

// letterSizes normalize letter sizes.
var letterSizes = map[string]string{"xs": "XS", "s": "S", "m": "M", "l": "L", "xl": "XL", "xxl": "XXL", "2xl": "XXL",
	"xxxl": "XXXL", "3xl": "XXXL"}

// sizeTokens splits a size query into words, separating Chinese from
// other characters ("鞋码42" → "鞋码", "42").
func sizeTokens(s string) []string {
	var b strings.Builder
	prevHan := false
	for i, r := range strings.ToLower(s) {
		han := unicode.Is(unicode.Han, r)
		if i > 0 && han != prevHan {
			b.WriteByte(' ')
		}
		prevHan = han
		b.WriteRune(r)
	}
	return strings.Fields(b.String())
}

// sizeQuery is a parsed size query.
type sizeQuery struct {
	kind, gender, system, value string
}

func parseSizeSource(s string) (sizeQuery, bool) {
	var q sizeQuery
	words := sizeTokens(s)
	for i := 0; i < len(words); i++ {
		w := words[i]
		if i+1 < len(words) {
			if m, ok := sizeWords[w+" "+words[i+1]]; ok && m.kind != "" {
				q.kind = m.kind
				i++
				continue
			}
		}
		if m, ok := sizeWords[w]; ok {
			q.kind = cmpOr(m.kind, q.kind)
			q.gender = cmpOr(m.gender, q.gender)
			q.system = cmpOr(m.system, q.system)
			continue
		}
		if letter, ok := letterSizes[w]; ok && q.value == "" {
			q.value = letter
			continue
		}
		if _, err := strconv.ParseFloat(w, 64); err == nil && q.value == "" || strings.Contains(w, "/") && q.value == "" {
			q.value = strings.ToUpper(w)
			continue
		}
		return q, false
	}
	return q, q.kind != "" && q.value != ""
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// chart returns the chart of q and the system its value is in.
func (q sizeQuery) chart() (sizeChart, string) {
	if q.kind == "shoe" {
		c := menShoes
		if q.gender == "women" {
			c = womenShoes
		}
		sys := q.system
		v, _ := strconv.ParseFloat(q.value, 64)
		switch {
		case sys == sysCN && v < 100: // CN shoe sizes are often EU numbers
			sys = sysEU
		case sys == "" && v >= 100:
			sys = sysCN
		case sys == "" && v >= 30:
			sys = sysEU
		case sys == "":
			sys = sysUS
		}
		return c, sys
	}
	c := menClothing
	if q.gender == "women" {
		c = womenClothing
	}
	sys := q.system
	if sys == "" {
		v, err := strconv.ParseFloat(q.value, 64)
		switch {
		case strings.Contains(q.value, "/"):
			sys = sysCN
		case err != nil:
			sys = sysINT
		case q.gender == "women" && v >= 30 || q.gender != "women" && v >= 44:
			sys = sysEU
		default:
			sys = sysUS
		}
	}
	return c, sys
}

// find returns the chart row of value in system's column.
func (c sizeChart) find(system, value string) []string {
	col := -1
	for i, s := range c.systems {
		if s == system {
			col = i
		}
	}
	if col < 0 {
		return nil
	}
	v, err := strconv.ParseFloat(value, 64)
	var best []string
	bestDiff := math.Inf(1)
	for _, r := range c.rows {
		cell := r[col]
		if err != nil {
			if strings.EqualFold(cell, value) {
				return r
			}
			continue
		}
		if cv, cerr := strconv.ParseFloat(cell, 64); cerr == nil {
			if d := math.Abs(cv - v); d < bestDiff {
				best, bestDiff = r, d
			}
		}
	}
	if bestDiff > c.tolerance {
		return nil
	}
	return best
}

// sizeResult converts a clothing or shoe size, or returns nil.
func (p *Plugin) sizeResult(text string) *result {
	src, target, conv := splitSpecial(text)
	q, ok := parseSizeSource(src)
	if !ok {
		return nil
	}
	var to string
	if conv {
		for _, w := range sizeTokens(target) {
			m, ok := sizeWords[w]
			if !ok {
				return nil
			}
			to = cmpOr(m.system, to)
		}
		if to == "" {
			return nil
		}
	}
	c, from := q.chart()
	r := c.find(from, q.value)
	if r == nil {
		return nil
	}
	l := p.h.I18n()
	kind := sizeKind(l, q)
	res := &result{source: from + " " + q.value + " (" + kind + ")", markdown: l.T("size.note")}
	var first []row
	var rest []row
	for i, sys := range c.systems {
		x := row{id: sys, title: sys + " " + r[i], raw: r[i], full: sys + " " + r[i], label: sys, shown: r[i]}
		switch {
		case conv && sys == to:
			first = append(first, x)
		case !conv && sys != from:
			first = append(first, x)
		default:
			rest = append(rest, x)
		}
	}
	if conv && len(first) == 0 {
		return nil // e.g. JP clothing
	}
	res.rows = append(first, rest...)
	res.root = min(len(first), maxSuggestions)
	return res
}

// sizeKind names the chart, e.g. "men's shoes".
func sizeKind(l plugin.Localizer, q sizeQuery) string {
	switch {
	case q.kind == "shoe" && q.gender == "women":
		return l.T("size.kind.womenShoes")
	case q.kind == "shoe":
		return l.T("size.kind.menShoes")
	case q.gender == "women":
		return l.T("size.kind.womenClothing")
	}
	return l.T("size.kind.menClothing")
}
