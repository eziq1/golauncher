package i18n

import (
	"os"
	"os/exec"
	"strings"

	"github.com/eziq1/golauncher/internal/darwin/ns"
)

// systemLanguages returns the user's preferred languages of System Settings
// › General › Language & Region, most preferred first (e.g. "zh-Hans-CN",
// "en-US"), which the matcher reads as BCP 47 tags. An app started from
// Finder or at login has no LANG, so the environment is only the last
// resort (docs/macos-port.md §4.1).
func systemLanguages() []string {
	var langs []string
	ns.Pool(func() {
		langs = ns.Strings(ns.Send(ns.Class("NSLocale"), "preferredLanguages"))
	})
	if langs = normalizeLanguages(langs); len(langs) > 0 {
		return langs
	}
	if out, err := exec.Command("/usr/bin/defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
		if langs = normalizeLanguages(parseAppleLanguages(string(out))); len(langs) > 0 {
			return langs
		}
	}
	return envLanguages(os.Getenv)
}

// parseAppleLanguages parses the output of "defaults read -g
// AppleLanguages", an old-style property list array:
//
//	(
//	    "zh-Hans-CN",
//	    en
//	)
func parseAppleLanguages(s string) []string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
		return nil
	}
	var langs []string
	for _, f := range strings.Split(s[1:len(s)-1], ",") {
		if f = strings.Trim(strings.TrimSpace(f), `"`); f != "" {
			langs = append(langs, f)
		}
	}
	return langs
}

// normalizeLanguages turns macOS language identifiers into BCP 47 tags:
// "en_US" is "en-US"; empty ones are dropped.
func normalizeLanguages(langs []string) []string {
	out := langs[:0:0]
	for _, l := range langs {
		if l = strings.ReplaceAll(strings.TrimSpace(l), "_", "-"); l != "" {
			out = append(out, l)
		}
	}
	return out
}
