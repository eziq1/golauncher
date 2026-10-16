package timer

import (
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Parsing of what follows a prefix (docs/timers.md §2): durations for
// countdowns ("25m", "1h30m 喝水", "1:30:00", "半小时") and times for
// reminders ("15:30 开会", "tomorrow 9am standup", "明天9点交周报",
// "in 20m tea"). The parsers are pure functions of the text and, for
// reminders, the current time; anything they do not understand is rejected.

// maxDuration bounds countdowns and relative reminders.
const maxDuration = 7 * 24 * time.Hour

// normalize replaces full-width digits, colons, periods and spaces by ASCII
// ones and trims the text. Case is kept: labels are cut from this text,
// while matching uses a lowercased copy (text.low).
func normalize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '０' && r <= '９':
			r = '0' + (r - '０')
		case r == '：':
			r = ':'
		case r == '　':
			r = ' '
		case r == '．':
			r = '.'
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// lowerASCII lowercases the ASCII letters of s, keeping byte offsets.
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// text is an input being scanned: low is the lowercased copy used for
// matching, orig the normalized original that labels are cut from.
type text struct {
	orig, low string
}

func newText(s string) text {
	s = normalize(s)
	return text{orig: s, low: lowerASCII(s)}
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool       { return c >= '0' && c <= '9' }

// boundary reports whether a word of ASCII letters or digits may end before
// s[i:]: at the end, a space, punctuation or a non-ASCII character.
func boundary(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	c := s[i]
	return !isASCIILetter(c) && !isDigit(c)
}

// skipSpaces returns i advanced past spaces and commas.
func skipSpaces(s string, i int) int {
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if !unicode.IsSpace(r) && r != ',' && r != '，' {
			break
		}
		i += n
	}
	return i
}

// hasWordAt reports whether s[i:] starts with word followed by a boundary
// (for ASCII words) and returns the index after it.
func hasWordAt(s string, i int, word string) (int, bool) {
	if !strings.HasPrefix(s[i:], word) {
		return i, false
	}
	j := i + len(word)
	if last := word[len(word)-1]; last < utf8.RuneSelf && (isASCIILetter(last) || isDigit(last)) && !boundary(s, j) {
		return i, false
	}
	return j, true
}

// --- numbers ---

var chineseDigits = map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}

// number reads a decimal number ("25", "1.5") or a Chinese numeral up to
// 999 ("二十五", "两", "一百二十") at s[i:]. frac allows a decimal part.
func number(s string, i int, frac bool) (v float64, j int, ok bool) {
	j = i
	for j < len(s) && isDigit(s[j]) {
		j++
	}
	if j > i {
		if frac && j+1 < len(s) && s[j] == '.' && isDigit(s[j+1]) {
			j++
			for j < len(s) && isDigit(s[j]) {
				j++
			}
		}
		v, err := strconv.ParseFloat(s[i:j], 64)
		if err != nil || j-i > 9 {
			return 0, i, false
		}
		return v, j, true
	}
	return chineseNumber(s, i)
}

// chineseNumber reads a Chinese numeral below 1000.
func chineseNumber(s string, i int) (float64, int, bool) {
	total, cur, j := 0, -1, i
	seen := false
	for j < len(s) {
		r, n := utf8.DecodeRuneInString(s[j:])
		if d, ok := chineseDigits[r]; ok {
			if cur >= 0 && cur != 0 { // "二三" is not a number
				return 0, i, false
			}
			cur = d
		} else if r == '十' {
			if cur < 0 {
				cur = 1
			}
			total += cur * 10
			cur = -1
		} else if r == '百' {
			if cur <= 0 {
				return 0, i, false
			}
			total += cur * 100
			cur = -1
		} else {
			break
		}
		seen = true
		j += n
	}
	if !seen {
		return 0, i, false
	}
	if cur > 0 {
		total += cur
	}
	return float64(total), j, true
}

// --- durations ---

type unit struct {
	word string
	d    time.Duration
}

// units are matched longest first at a position.
var units = sortUnits([]unit{
	{"seconds", time.Second}, {"second", time.Second}, {"secs", time.Second}, {"sec", time.Second}, {"s", time.Second},
	{"minutes", time.Minute}, {"minute", time.Minute}, {"mins", time.Minute}, {"min", time.Minute}, {"m", time.Minute},
	{"hours", time.Hour}, {"hour", time.Hour}, {"hrs", time.Hour}, {"hr", time.Hour}, {"h", time.Hour},
	{"days", 24 * time.Hour}, {"day", 24 * time.Hour}, {"d", 24 * time.Hour},
	{"秒钟", time.Second}, {"秒", time.Second},
	{"分钟", time.Minute}, {"分", time.Minute},
	{"小时", time.Hour}, {"钟头", time.Hour},
	{"刻钟", 15 * time.Minute},
	{"天", 24 * time.Hour},
})

func sortUnits(us []unit) []unit {
	for i := 1; i < len(us); i++ {
		for j := i; j > 0 && len(us[j].word) > len(us[j-1].word); j-- {
			us[j], us[j-1] = us[j-1], us[j]
		}
	}
	return us
}

// unitAt matches a unit at s[i:].
func unitAt(s string, i int) (time.Duration, int, bool) {
	for _, u := range units {
		// A unit may be followed by the next term ("1h30m"), not by a letter.
		if j := i + len(u.word); strings.HasPrefix(s[i:], u.word) && (j >= len(s) || !isASCIILetter(s[j])) {
			return u.d, j, true
		}
	}
	return 0, i, false
}

// clockDuration reads "25:00" (minutes:seconds) or "1:30:00"
// (hours:minutes:seconds) at s[i:].
func clockDuration(s string, i int) (time.Duration, int, bool) {
	var parts []int
	j := i
	for {
		k := j
		for k < len(s) && isDigit(s[k]) {
			k++
		}
		if k == j || k-j > 3 || (len(parts) > 0 && k-j != 2) {
			return 0, i, false
		}
		n, _ := strconv.Atoi(s[j:k])
		parts = append(parts, n)
		j = k
		if j < len(s) && s[j] == ':' && len(parts) < 3 {
			j++
			continue
		}
		break
	}
	if len(parts) < 2 || !boundary(s, j) {
		return 0, i, false
	}
	var d time.Duration
	switch len(parts) {
	case 2:
		if parts[1] > 59 {
			return 0, i, false
		}
		d = time.Duration(parts[0])*time.Minute + time.Duration(parts[1])*time.Second
	case 3:
		if parts[1] > 59 || parts[2] > 59 {
			return 0, i, false
		}
		d = time.Duration(parts[0])*time.Hour + time.Duration(parts[1])*time.Minute + time.Duration(parts[2])*time.Second
	}
	return d, j, true
}

// durationAt reads a duration at s[i:]: a clock form, or one or more
// "<number> <unit>" terms (also "半小时", "一个半小时", "一刻钟"), or a bare
// number of minutes when bare is set.
func durationAt(s string, i int, bare bool) (time.Duration, int, bool) {
	if d, j, ok := clockDuration(s, i); ok {
		if d <= 0 || d > maxDuration {
			return 0, i, false
		}
		return d, j, true
	}
	var total time.Duration
	terms, j := 0, i
	for {
		k := j
		if terms > 0 {
			k = skipSpaces(s, k)
		}
		d, next, ok := durationTerm(s, k)
		if !ok {
			break
		}
		total += d
		terms++
		j = next
	}
	if terms == 0 {
		if !bare {
			return 0, i, false
		}
		v, k, ok := number(s, i, true)
		if !ok || !bareEnd(s, k) {
			return 0, i, false
		}
		total, j = time.Duration(v*float64(time.Minute)), k
	}
	total = total.Round(time.Second)
	if total <= 0 || total > maxDuration {
		return 0, i, false
	}
	return total, j, true
}

// bareEnd reports whether a bare number may end before s[k:]: at the end,
// a space or a non-ASCII character ("25 tea", "25喝水", not "1:60").
func bareEnd(s string, k int) bool { return k >= len(s) || s[k] == ' ' || s[k] >= utf8.RuneSelf }

// durationTerm reads one "<number> <unit>" term.
func durationTerm(s string, i int) (time.Duration, int, bool) {
	// "半小时", "半个小时", "半分钟"
	if j, ok := hasWordAt(s, i, "半"); ok {
		if k, ok := hasWordAt(s, j, "个"); ok {
			j = k
		}
		if u, k, ok := unitAt(s, j); ok && u != 15*time.Minute {
			return u / 2, k, true
		}
		return 0, i, false
	}
	v, j, ok := number(s, i, true)
	if !ok {
		return 0, i, false
	}
	k := j
	for k < len(s) && s[k] == ' ' {
		k++
	}
	half := false
	if m, ok := hasWordAt(s, k, "个"); ok {
		k = m
	}
	if m, ok := hasWordAt(s, k, "半"); ok { // "一个半小时"
		k, half = m, true
	}
	u, m, ok := unitAt(s, k)
	if !ok {
		return 0, i, false
	}
	if half {
		v += 0.5
	}
	return time.Duration(v * float64(u)), m, true
}

// parseTimer parses a countdown: a duration followed or preceded by an
// optional label ("25m", "1h30m 喝水", "喝水 25m", "25分钟喝水").
func parseTimer(s string) (d time.Duration, label string, ok bool) {
	t := newText(s)
	if t.low == "" {
		return 0, "", false
	}
	if d, j, ok := durationAt(t.low, 0, true); ok {
		return d, cleanLabel(t.orig[j:]), true
	}
	// The duration at the end: try every word or rune boundary.
	for i := 1; i < len(t.low); {
		_, n := utf8.DecodeRuneInString(t.low[i:])
		prevSpace := t.low[i-1] == ' '
		prev, _ := utf8.DecodeLastRuneInString(t.low[:i])
		// A duration starts at a space or after a non-ASCII character
		// ("喝水25分钟"); not inside an ASCII word ("x25m").
		_, numeral := chineseDigits[prev]
		if prevSpace || prev >= utf8.RuneSelf && !numeral && prev != '十' && prev != '百' {
			if d, j, ok := durationAt(t.low, i, prevSpace); ok && strings.TrimSpace(t.low[j:]) == "" {
				return d, cleanLabel(t.orig[:i]), true
			}
		}
		i += n
	}
	return 0, "", false
}

// cleanLabel trims a label and the punctuation joining it to the time.
func cleanLabel(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(":,，、;；-—", r)
	})
}

// --- reminders ---

// reminder is a parsed reminder: either In (relative) or At.
type reminder struct {
	In    time.Duration // "in 20m"; 0 for a time of day
	At    time.Time     // when it is due (also set for In)
	Label string
	// Rolled is set when no date was given and the time had passed today,
	// so the reminder is tomorrow.
	Rolled bool
	// DefaultTime is set when a date was given without a time (9:00).
	DefaultTime bool
}

// period is a part of the day that changes how an hour is read.
type period int

const (
	periodNone      period = iota
	periodEarly            // 凌晨: 0–5
	periodMorning          // am
	periodNoon             // 中午: 11–13
	periodAfternoon        // pm
	periodEvening          // pm, default 20:00
)

// dayKind is how a date was given.
type dayKind int

const (
	dayNone     dayKind = iota
	dayOffset           // today, tomorrow: off days from today
	dayWeekday          // monday, 周一
	dayNextWeek         // next monday, 下周一
	dayDate             // 2026-10-20, 10月20日
)

type day struct {
	kind    dayKind
	off     int
	weekday time.Weekday
	year    int // 0: this year (or next, when passed)
	month   time.Month
	date    int
	period  period
}

var dayWords = []struct {
	word   string
	off    int
	period period
}{
	{"day after tomorrow", 2, periodNone},
	{"tomorrow", 1, periodNone}, {"tmrw", 1, periodNone}, {"tmr", 1, periodNone},
	{"today", 0, periodNone}, {"tonight", 0, periodEvening},
	{"大后天", 3, periodNone}, {"后天", 2, periodNone},
	{"明天", 1, periodNone}, {"明日", 1, periodNone}, {"明早", 1, periodMorning}, {"明晚", 1, periodEvening},
	{"今天", 0, periodNone}, {"今日", 0, periodNone}, {"今晚", 0, periodEvening},
}

var weekdayWords = []struct {
	word string
	day  time.Weekday
}{
	{"monday", time.Monday}, {"mon", time.Monday},
	{"tuesday", time.Tuesday}, {"tues", time.Tuesday}, {"tue", time.Tuesday},
	{"wednesday", time.Wednesday}, {"wed", time.Wednesday},
	{"thursday", time.Thursday}, {"thurs", time.Thursday}, {"thur", time.Thursday}, {"thu", time.Thursday},
	{"friday", time.Friday}, {"fri", time.Friday},
	{"saturday", time.Saturday}, {"sat", time.Saturday},
	{"sunday", time.Sunday}, {"sun", time.Sunday},
}

var chineseWeekdays = map[rune]time.Weekday{'一': time.Monday, '二': time.Tuesday, '三': time.Wednesday,
	'四': time.Thursday, '五': time.Friday, '六': time.Saturday, '日': time.Sunday, '天': time.Sunday}

// dayAt reads a date at s[i:]. Without short, English weekdays must be
// written out ("3pm sunday", not "3pm sun screen").
func dayAt(s string, i int, short bool) (day, int, bool) {
	for _, w := range dayWords {
		if j, ok := hasWordAt(s, i, w.word); ok {
			return day{kind: dayOffset, off: w.off, period: w.period}, j, true
		}
	}
	next := false
	j := i
	if k, ok := hasWordAt(s, j, "next "); ok {
		j, next = k, true
	}
	for _, w := range weekdayWords {
		if !short && !strings.HasSuffix(w.word, "day") {
			continue
		}
		if k, ok := hasWordAt(s, j, w.word); ok {
			d := day{kind: dayWeekday, weekday: w.day}
			if next {
				d.kind = dayNextWeek
			}
			return d, k, true
		}
	}
	if next {
		return day{}, i, false
	}
	// 周一, 星期一, 礼拜一, 下周一, 下星期一, 下个星期一
	j = i
	if k, ok := hasWordAt(s, j, "下个"); ok {
		j, next = k, true
	} else if k, ok := hasWordAt(s, j, "下"); ok {
		j, next = k, true
	}
	for _, p := range []string{"星期", "礼拜", "周"} {
		if k, ok := hasWordAt(s, j, p); ok {
			r, n := utf8.DecodeRuneInString(s[k:])
			if wd, ok := chineseWeekdays[r]; ok {
				d := day{kind: dayWeekday, weekday: wd}
				if next {
					d.kind = dayNextWeek
				}
				return d, k + n, true
			}
		}
	}
	if d, j, ok := isoDate(s, i); ok {
		return d, j, true
	}
	return chineseDate(s, i)
}

// isoDate reads "2026-10-20".
func isoDate(s string, i int) (day, int, bool) {
	if len(s)-i < 10 || s[i+4] != '-' || s[i+7] != '-' || !boundary(s, i+10) {
		return day{}, i, false
	}
	y, e1 := strconv.Atoi(s[i : i+4])
	m, e2 := strconv.Atoi(s[i+5 : i+7])
	d, e3 := strconv.Atoi(s[i+8 : i+10])
	if e1 != nil || e2 != nil || e3 != nil || !validDate(y, m, d) {
		return day{}, i, false
	}
	return day{kind: dayDate, year: y, month: time.Month(m), date: d}, i + 10, true
}

// chineseDate reads "10月20日", "10月20号" or "2026年10月20日".
func chineseDate(s string, i int) (day, int, bool) {
	j := i
	year := 0
	if v, k, ok := number(s, j, false); ok {
		if m, ok := hasWordAt(s, k, "年"); ok && v >= 2000 && v < 3000 {
			year, j = int(v), m
		}
	}
	v, k, ok := number(s, j, false)
	if !ok {
		return day{}, i, false
	}
	k, ok = hasWordAt(s, k, "月")
	if !ok {
		return day{}, i, false
	}
	dv, m, ok := number(s, k, false)
	if !ok {
		return day{}, i, false
	}
	if n, ok := hasWordAt(s, m, "日"); ok {
		m = n
	} else if n, ok := hasWordAt(s, m, "号"); ok {
		m = n
	} else {
		return day{}, i, false
	}
	y := year
	if y == 0 {
		y = 2000 // leap year: 2月29日 is checked again when resolved
	}
	if !validDate(y, int(v), int(dv)) {
		return day{}, i, false
	}
	return day{kind: dayDate, year: year, month: time.Month(v), date: int(dv)}, m, true
}

func validDate(y, m, d int) bool {
	if m < 1 || m > 12 || d < 1 || d > 31 {
		return false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t.Day() == d
}

var periodWords = []struct {
	word string
	p    period
}{
	{"凌晨", periodEarly},
	{"早上", periodMorning}, {"早晨", periodMorning}, {"上午", periodMorning}, {"早", periodMorning},
	{"中午", periodNoon},
	{"下午", periodAfternoon}, {"傍晚", periodEvening},
	{"晚上", periodEvening}, {"夜里", periodEvening}, {"晚", periodEvening},
}

// clockTime is a time of day.
type clockTime struct {
	h, m int
	p    period
	ampm bool // given with am/pm (12-hour)
}

// timeAt reads a time of day at s[i:]: "15:30", "9am", "9:30 pm", "noon",
// "9点", "9点半", "下午3点", "15时30分".
func timeAt(s string, i int) (clockTime, int, bool) {
	var ct clockTime
	j := i
	for _, w := range periodWords {
		if k, ok := hasWordAt(s, j, w.word); ok {
			ct.p, j = w.p, skipSpaces(s, k)
			break
		}
	}
	if ct.p == periodNone {
		if k, ok := hasWordAt(s, j, "noon"); ok {
			return clockTime{h: 12}, k, true
		}
		if k, ok := hasWordAt(s, j, "midnight"); ok {
			return clockTime{h: 0}, k, true
		}
	}
	k := j
	for k < len(s) && isDigit(s[k]) {
		k++
	}
	// H:MM
	if n := k - j; n >= 1 && n <= 2 && k < len(s) && s[k] == ':' {
		if k+3 > len(s) || !isDigit(s[k+1]) || !isDigit(s[k+2]) || k+3 < len(s) && (isDigit(s[k+3]) || s[k+3] == ':') {
			return ct, i, false
		}
		ct.h, _ = strconv.Atoi(s[j:k])
		ct.m, _ = strconv.Atoi(s[k+1 : k+3])
		return finishTime(s, k+3, ct, false)
	}
	v, k, ok := number(s, j, false)
	if !ok {
		return ct, i, false
	}
	// Chinese: N点[钟][半|一刻|三刻|MM[分]], N时[MM分]
	for _, w := range []string{"点", "时"} {
		m, ok := hasWordAt(s, k, w)
		if !ok {
			continue
		}
		ct.h = int(v)
		if n, ok := hasWordAt(s, m, "钟"); ok && w == "点" {
			m = n
		}
		switch {
		case strings.HasPrefix(s[m:], "半"):
			ct.m, m = 30, m+len("半")
		case strings.HasPrefix(s[m:], "一刻"):
			ct.m, m = 15, m+len("一刻")
		case strings.HasPrefix(s[m:], "三刻"):
			ct.m, m = 45, m+len("三刻")
		default:
			if mv, n, ok := number(s, m, false); ok {
				if f, ok := hasWordAt(s, n, "分"); ok {
					n = f
				} else if w == "时" {
					return ct, i, false // "15时30" without 分 is unusual; "15时30分"
				}
				ct.m, m = int(mv), n
			}
		}
		return finishTime(s, m, ct, false)
	}
	// H am/pm; a bare number is not a time.
	if !digitsOnly(s[j:k]) {
		return ct, i, false
	}
	ct.h = int(v)
	return finishTime(s, k, ct, true)
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func boundaryNoColon(s string, i int) bool { return boundary(s, i) && (i >= len(s) || s[i] != ':') }

// finishTime reads an optional am/pm after a time ending at s[i] and
// validates it. A bare number ("9") needs am/pm.
func finishTime(s string, i int, ct clockTime, bare bool) (clockTime, int, bool) {
	j := i
	for j < len(s) && s[j] == ' ' {
		j++
	}
	for _, w := range []struct {
		word string
		pm   bool
	}{{"a.m.", false}, {"p.m.", true}, {"am", false}, {"pm", true}} {
		if k, ok := hasWordAt(s, j, w.word); ok {
			if ct.h < 1 || ct.h > 12 || ct.p != periodNone {
				return ct, i, false
			}
			ct.ampm = true
			ct.h %= 12
			if w.pm {
				ct.h += 12
			}
			return ct, k, true
		}
	}
	if bare || !boundaryNoColon(s, i) {
		return ct, i, false
	}
	ct, ok := applyPeriod(ct)
	return ct, i, ok
}

// applyPeriod turns the hour of a time given with a part of the day into a
// 24-hour one ("下午3点" is 15:00) and validates the time.
func applyPeriod(ct clockTime) (clockTime, bool) {
	if !ct.ampm {
		switch ct.p {
		case periodAfternoon, periodEvening:
			if ct.h >= 1 && ct.h < 12 {
				ct.h += 12
			}
		case periodNoon:
			if ct.h >= 1 && ct.h < 3 {
				ct.h += 12
			}
		case periodEarly, periodMorning:
			if ct.h > 12 {
				return ct, false
			}
			if ct.h == 12 && ct.p == periodEarly {
				ct.h = 0
			}
		}
	}
	if ct.h == 24 && ct.m == 0 {
		ct.h = 0 // "24点" is midnight
	}
	return ct, ct.h >= 0 && ct.h <= 23 && ct.m >= 0 && ct.m <= 59
}

// fillers are words before the time that carry no meaning ("remind me at
// 3pm", "提醒我明天9点").
var fillers = []string{"me ", "at ", "on ", "我", "在"}

// labelFillers are words between the time and the label ("to call mom",
// "明天9点提醒我交周报").
var labelFillers = []string{"to ", "提醒我", "提醒", "叫我", "我"}

func skipWords(s string, i int, words []string) int {
	for {
		i = skipSpaces(s, i)
		found := false
		for _, w := range words {
			if strings.HasPrefix(s[i:], w) {
				i += len(w)
				found = true
				break
			}
		}
		if !found {
			return i
		}
	}
}

// parseReminder parses a reminder relative to now ("15:30 开会", "tomorrow
// 9am standup", "提醒 明天 9点 交周报", "in 20m tea", "20分钟后 喝水").
func parseReminder(s string, now time.Time) (reminder, bool) {
	t := newText(s)
	low := t.low
	i := skipWords(low, 0, fillers)
	if i >= len(low) {
		return reminder{}, false
	}
	// Relative: "in 20m", "20分钟后", "20分钟以后".
	if k, ok := hasWordAt(low, i, "in "); ok {
		if d, j, ok := durationAt(low, skipSpaces(low, k), true); ok {
			return relative(t, now, d, j), true
		}
		return reminder{}, false
	}
	if d, j, ok := durationAt(low, i, false); ok {
		for _, w := range []string{"以后", "之后", "后"} {
			if k, ok := hasWordAt(low, j, w); ok {
				return relative(t, now, d, k), true
			}
		}
	}
	// Absolute: [day] time, time [day], or a day alone.
	var (
		dy    day
		ct    clockTime
		hasDy bool
		hasT  bool
	)
	j := i
	if d, k, ok := dayAt(low, j, true); ok {
		dy, hasDy, j = d, true, skipSpaces(low, k)
		j = skipWords(low, j, []string{"at "})
	}
	if c, k, ok := timeAt(low, j); ok {
		ct, hasT, j = c, true, k
		if !hasDy { // "3pm tomorrow"
			k := skipWords(low, j, []string{"on "})
			if d, k, ok := dayAt(low, k, false); ok {
				dy, hasDy, j = d, true, k
			}
		}
	}
	if !hasT && !hasDy {
		return reminder{}, false
	}
	r := reminder{Label: cleanLabel(t.orig[skipWords(low, j, labelFillers):])}
	if !hasT {
		switch dy.period {
		case periodEvening:
			ct.h = 20
		default:
			ct.h = 9
		}
		if dy.kind == dayOffset && dy.off == 0 && dy.period == periodNone {
			return reminder{}, false // "today" alone
		}
		r.DefaultTime = true
	} else if dy.period != periodNone && ct.p == periodNone && !ct.ampm {
		// "今晚8点": the day's part of the day applies.
		ct.p = dy.period
		var ok bool
		if ct, ok = applyPeriod(ct); !ok {
			return reminder{}, false
		}
	}
	at, rolled, ok := resolve(now, dy, hasDy, ct)
	if !ok {
		return reminder{}, false
	}
	r.At, r.Rolled = at, rolled
	return r, true
}

func relative(t text, now time.Time, d time.Duration, j int) reminder {
	return reminder{In: d, At: now.Add(d).Round(0), Label: cleanLabel(t.orig[skipWords(t.low, j, labelFillers):])}
}

// resolve finds when a reminder at ct on dy is due after now.
func resolve(now time.Time, dy day, hasDy bool, ct clockTime) (time.Time, bool, bool) {
	now = now.Round(0)
	loc := now.Location()
	y, m, d := now.Date()
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, ct.h, ct.m, 0, 0, loc) }
	if !hasDy || (dy.kind == dayOffset && dy.off == 0) {
		t := at(y, m, d)
		if !t.After(now) {
			return t.AddDate(0, 0, 1), true, true
		}
		return t, false, true
	}
	switch dy.kind {
	case dayOffset:
		return at(y, m, d+dy.off), false, true
	case dayWeekday:
		delta := (int(dy.weekday) - int(now.Weekday()) + 7) % 7
		t := at(y, m, d+delta)
		if !t.After(now) {
			t = at(y, m, d+delta+7)
		}
		return t, false, true
	case dayNextWeek:
		// The given day of next week (weeks start on Monday).
		iso := func(w time.Weekday) int { return (int(w)+6)%7 + 1 }
		toMonday := 8 - iso(now.Weekday())
		return at(y, m, d+toMonday+iso(dy.weekday)-1), false, true
	case dayDate:
		year := dy.year
		if year == 0 {
			year = y
			if t := at(year, dy.month, dy.date); !t.After(now) || t.Day() != dy.date {
				year++
			}
		}
		t := at(year, dy.month, dy.date)
		if t.Day() != dy.date || !t.After(now) {
			return time.Time{}, false, false
		}
		return t, false, true
	}
	return time.Time{}, false, false
}
