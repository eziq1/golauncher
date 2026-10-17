// Package tts speaks text aloud with the system's voices (docs/translator.md §5).
package tts

import (
	"context"
	"errors"
	"strings"
)

// ErrUnsupported is returned where the system has no speech synthesizer usable.
var ErrUnsupported = errors.New("tts: not supported on this system")

// Speaker speaks text aloud.
type Speaker interface {
	// Speak speaks text in language lang (a code such as "en", "zh-CN", "zh-TW", "ja", "ko", "fr", "de"; "" for the default voice),
	// choosing an installed voice for that language when there is one, else the default voice.
	// It blocks until the speech ends or ctx is cancelled, which stops it at once.
	Speak(ctx context.Context, text, lang string) error
}

// normLang lowercases a language code and uses "-" as the separator, so
// "zh_CN", "ZH-cn" and "zh-CN" compare equal.
func normLang(lang string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(lang), "_", "-"))
}

// primary returns the primary language subtag of a normalized code ("zh" for "zh-cn").
func primary(lang string) string {
	p, _, _ := strings.Cut(lang, "-")
	return p
}
