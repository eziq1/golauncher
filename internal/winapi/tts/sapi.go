package tts

import (
	"strconv"
	"strings"
)

// lcids maps normalized language codes to Windows LCIDs, which is how SAPI
// voice tokens declare their language (the token's "Language" attribute).
var lcids = map[string]uint32{
	"en":      0x409,
	"en-us":   0x409,
	"en-gb":   0x809,
	"en-au":   0xC09,
	"en-ca":   0x1009,
	"en-in":   0x4009,
	"zh":      0x804,
	"zh-cn":   0x804,
	"zh-hans": 0x804,
	"zh-sg":   0x1004,
	"zh-tw":   0x404,
	"zh-hant": 0x404,
	"zh-hk":   0xC04,
	"ja":      0x411,
	"ko":      0x412,
	"fr":      0x40C,
	"fr-ca":   0xC0C,
	"de":      0x407,
	"es":      0xC0A,
	"es-mx":   0x80A,
	"ru":      0x419,
	"pt":      0x416,
	"pt-br":   0x416,
	"pt-pt":   0x816,
	"it":      0x410,
	"nl":      0x413,
	"pl":      0x415,
	"tr":      0x41F,
	"ar":      0x401,
	"hi":      0x439,
	"th":      0x41E,
	"vi":      0x42A,
	"id":      0x421,
	"sv":      0x41D,
	"da":      0x406,
	"nb":      0x414,
	"no":      0x414,
	"fi":      0x40B,
	"el":      0x408,
	"cs":      0x405,
	"hu":      0x40E,
	"uk":      0x422,
	"he":      0x40D,
}

// lcidFor returns the LCID for lang, falling back to its primary subtag
// ("en-NZ" → en), or 0 when the language is unknown or lang is "".
func lcidFor(lang string) uint32 {
	l := normLang(lang)
	if l == "" {
		return 0
	}
	if id, ok := lcids[l]; ok {
		return id
	}
	return lcids[primary(l)]
}

// sapiText returns what to pass to ISpVoice::Speak for text in lang and
// whether it is SAPI XML. A known language wraps the text in a <voice> tag;
// "required" makes SAPI keep the current (default) voice when no installed
// voice matches, whereas "optional" alone would switch to the closest-scoring
// voice, which is not necessarily the user's default.
func sapiText(text, lang string) (s string, isXML bool) {
	id := lcidFor(lang)
	if id == 0 {
		return stripControl(text), false
	}
	return `<voice required="Language=` + strings.ToUpper(strconv.FormatUint(uint64(id), 16)) + `">` + xmlEscape(text) + `</voice>`, true
}

// xmlEscape escapes the XML special characters and drops characters XML
// cannot contain, which would otherwise make SAPI reject the whole string.
func xmlEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range stripControl(s) {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&apos;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stripControl removes control characters other than tab, CR and LF; NUL in
// particular would truncate the UTF-16 string handed to SAPI.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0x7F || r == 0xFFFE || r == 0xFFFF {
			return -1
		}
		return r
	}, s)
}
