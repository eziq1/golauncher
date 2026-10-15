package i18n

import (
	"fmt"
	"strings"
	"time"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"

	"golauncher/pkg/plugin"
)

// Localizer translates messages of one namespace. It implements
// plugin.Localizer.
type Localizer struct {
	m  *Manager
	ns string
}

var _ plugin.Localizer = (*Localizer)(nil)

// Locale implements plugin.Localizer.
func (l *Localizer) Locale() string { return l.m.Locale() }

// T implements plugin.Localizer.
func (l *Localizer) T(id string, kv ...any) string {
	msg, _, pseudo := l.m.lookup(l.ns, id)
	if msg == nil {
		l.m.logf("i18n: missing message %s:%s", l.ns, id)
		return id
	}
	text := msg.text
	if msg.plural != nil {
		text = msg.plural["other"]
	}
	return l.finish(text, args(kv), pseudo)
}

// N implements plugin.Localizer.
func (l *Localizer) N(id string, count int, kv ...any) string {
	msg, loc, pseudo := l.m.lookup(l.ns, id)
	if msg == nil {
		l.m.logf("i18n: missing message %s:%s", l.ns, id)
		return id
	}
	data := args(kv)
	data["Count"] = count
	text := msg.text
	if msg.plural != nil {
		var ok bool
		if text, ok = msg.plural[pluralForm(loc, count)]; !ok {
			text = msg.plural["other"]
		}
	}
	return l.finish(text, data, pseudo)
}

func (l *Localizer) finish(text string, data map[string]any, pseudo bool) string {
	if pseudo {
		text = Pseudolocalize(text)
	}
	return l.m.render(text, data)
}

// Resolve translates s if it is a "%key%" reference and returns it unchanged
// otherwise. It is used for manifest strings.
func (l *Localizer) Resolve(s string) string {
	if key, ok := ManifestKey(s); ok {
		return l.T(key)
	}
	return s
}

// ManifestKey reports whether s is a "%key%" reference and returns the key.
func ManifestKey(s string) (string, bool) {
	if len(s) > 2 && s[0] == '%' && s[len(s)-1] == '%' && !strings.ContainsAny(s[1:len(s)-1], "% ") {
		return s[1 : len(s)-1], true
	}
	return "", false
}

func args(kv []any) map[string]any {
	data := make(map[string]any, len(kv)/2+1)
	for i := 0; i+1 < len(kv); i += 2 {
		data[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return data
}

func (l *Localizer) printer() *message.Printer {
	return message.NewPrinter(language.Make(l.Locale()))
}

// Number implements plugin.Localizer.
func (l *Localizer) Number(v float64) string {
	return l.printer().Sprint(number.Decimal(v, number.MaxFractionDigits(12)))
}

// separators returns the locale's decimal mark and digit-group separator.
func (l *Localizer) separators() (dec, group string) {
	// Format a sample and read the separators back; x/text does not expose them.
	s := []rune(l.printer().Sprint(number.Decimal(1234.5, number.MinFractionDigits(1))))
	if len(s) == 7 {
		return string(s[5]), string(s[1])
	}
	return ".", ","
}

// GroupDigits implements plugin.Localizer.
func (l *Localizer) GroupDigits(decimal string) string {
	dec, group := l.separators()
	sign, rest := "", decimal
	if strings.HasPrefix(rest, "-") {
		sign, rest = "-", rest[1:]
	}
	intPart, frac, hasFrac := strings.Cut(rest, ".")
	var b strings.Builder
	b.WriteString(sign)
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteString(group)
		}
		b.WriteRune(r)
	}
	if hasFrac {
		b.WriteString(dec)
		b.WriteString(frac)
	}
	return b.String()
}

// RelativeTime implements plugin.Localizer.
func (l *Localizer) RelativeTime(t, now time.Time) string {
	d := now.Sub(t)
	core := l.m.Localizer(CoreNamespace)
	switch {
	case d < time.Minute:
		return core.T("time.justNow")
	case d < time.Hour:
		return core.N("time.minutesAgo", int(d/time.Minute))
	case d < 24*time.Hour:
		return core.N("time.hoursAgo", int(d/time.Hour))
	case d < 7*24*time.Hour:
		return core.N("time.daysAgo", int(d/(24*time.Hour)))
	default:
		// The layout itself is a message so each locale can choose one.
		return t.Format(core.T("time.dateLayout"))
	}
}

// Pseudolocalize turns English text into the en-XA pseudo-locale: accented
// letters, about 40% longer, and wrapped in brackets. Template actions
// ({{...}}) are left intact.
func Pseudolocalize(s string) string {
	var b strings.Builder
	b.WriteString("[")
	letters := 0
	for rest := s; rest != ""; {
		if i := strings.Index(rest, "{{"); i == 0 {
			end := strings.Index(rest, "}}")
			if end < 0 {
				b.WriteString(rest)
				break
			}
			b.WriteString(rest[:end+2])
			rest = rest[end+2:]
			continue
		}
		r := []rune(rest)[0]
		if a, ok := accents[r]; ok {
			b.WriteRune(a)
		} else {
			b.WriteRune(r)
		}
		if r != ' ' {
			letters++
		}
		rest = rest[len(string(r)):]
	}
	pad := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}
	for i := 0; letters > 0 && i < (letters*2/5+3)/4 && i < len(pad); i++ {
		b.WriteString(" " + pad[i])
	}
	b.WriteString("]")
	return b.String()
}

var accents = map[rune]rune{
	'a': 'å', 'b': 'ƀ', 'c': 'ç', 'd': 'ð', 'e': 'é', 'f': 'ƒ', 'g': 'ĝ', 'h': 'ĥ',
	'i': 'î', 'j': 'ĵ', 'k': 'ķ', 'l': 'ļ', 'n': 'ñ', 'o': 'ö', 'p': 'þ', 'r': 'ŕ',
	's': 'š', 't': 'ţ', 'u': 'û', 'w': 'ŵ', 'y': 'ý', 'z': 'ž',
	'A': 'Å', 'C': 'Ç', 'D': 'Ð', 'E': 'É', 'G': 'Ĝ', 'H': 'Ĥ', 'I': 'Î', 'L': 'Ļ',
	'N': 'Ñ', 'O': 'Ö', 'R': 'Ŕ', 'S': 'Š', 'T': 'Ţ', 'U': 'Û', 'Y': 'Ý', 'Z': 'Ž',
}
