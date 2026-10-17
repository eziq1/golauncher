package engines

import (
	"strings"
	"unicode"
)

// Canonical codes of the languages engines often name differently.
const (
	English     = "en"
	Simplified  = "zh-CN"
	Traditional = "zh-TW"
	Japanese    = "ja"
	Korean      = "ko"
	Russian     = "ru"
)

// aliases maps codes engines use (lower case) to canonical ones.
var aliases = map[string]string{
	"zh": Simplified, "zh-cn": Simplified, "zh-hans": Simplified, "zh-chs": Simplified, "zh-sg": Simplified, "cn": Simplified,
	"zh-tw": Traditional, "zh-hant": Traditional, "zh-cht": Traditional, "zh-hk": Traditional, "cht": Traditional,
	"jp": Japanese, "jpn": Japanese, "kor": Korean, "kr": Korean,
	"fra": "fr", "spa": "es", "ara": "ar", "vie": "vi", "pt-br": "pt", "pt-pt": "pt",
	"en-us": English, "en-gb": English, "nb": "no", "iw": "he", "in": "id",
}

// Canon is the canonical form of a language code an engine returned:
// "ZH" → "zh-CN", "EN-US" → "en", "jp" → "ja". Unknown codes are returned
// in lower case; "" and "auto" give "".
func Canon(code string) string {
	c := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(code, "_", "-")))
	switch c {
	case "", "auto", "autodetect", "und":
		return ""
	}
	if a, ok := aliases[c]; ok {
		return a
	}
	return c
}

// Guess roughly tells the language of text by its script, for engines that
// do not report the source language: Han → Simplified Chinese, Kana →
// Japanese, Hangul → Korean, Cyrillic → Russian. Latin and other scripts
// give "" (many languages share them).
func Guess(text string) string {
	var han, kana, hangul, cyrillic, other int
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.Is(unicode.Cyrillic, r):
			cyrillic++
		case unicode.IsLetter(r):
			other++
		}
	}
	switch {
	case kana > 0 && (han+kana)*3 >= other:
		return Japanese
	case han*3 > other && han >= hangul:
		return Simplified
	case hangul*3 > other:
		return Korean
	case cyrillic > other:
		return Russian
	}
	return ""
}
