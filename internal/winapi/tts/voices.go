package tts

import (
	"strings"
)

// sayVoice is one entry of `say -v ?`.
type sayVoice struct {
	Name   string
	Locale string // normalized: lowercase with "-", e.g. "zh-cn"
}

// parseSayVoices parses `say -v ?` output, whose lines look like
//
//	Samantha            en_US    # Hello, my name is Samantha.
//	Eddy (English (US)) en_US    # Hello! My name is Eddy.
//
// Names may contain spaces, so the locale is the last field before "#".
func parseSayVoices(out string) []sayVoice {
	var vs []sayVoice
	for line := range strings.Lines(out) {
		left, _, _ := strings.Cut(line, "#")
		fields := strings.Fields(left)
		if len(fields) < 2 {
			continue
		}
		loc := fields[len(fields)-1]
		name := strings.TrimSpace(left[:strings.LastIndex(left, loc)])
		if name == "" {
			continue
		}
		vs = append(vs, sayVoice{Name: name, Locale: normLang(loc)})
	}
	return vs
}

// localeCandidates lists, in order of preference, the locales whose voices
// suit lang. An entry without a region matches any region of that language.
func localeCandidates(lang string) []string {
	l := normLang(lang)
	switch l {
	case "":
		return nil
	case "zh", "zh-cn", "zh-hans", "zh-sg":
		return []string{"zh-cn", "zh"}
	case "zh-tw", "zh-hant":
		// Cantonese (zh_HK) voices read traditional characters too.
		return []string{"zh-tw", "zh-hk", "zh"}
	case "zh-hk":
		return []string{"zh-hk", "zh-tw", "zh"}
	}
	if p := primary(l); p != l {
		return []string{l, p}
	}
	return []string{l}
}

// chooseSayVoice returns the name of the first voice suiting lang, or "" to
// use the default voice.
func chooseSayVoice(voices []sayVoice, lang string) string {
	for _, want := range localeCandidates(lang) {
		for _, v := range voices {
			if v.Locale == want || (!strings.Contains(want, "-") && primary(v.Locale) == want) {
				return v.Name
			}
		}
	}
	return ""
}
