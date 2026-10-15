package calc

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// Unix timestamps and dates (docs/unit-conversion.md §5.2):
// "1700000000 to date", "1700000000000 ms to date", "1700000000 in utc",
// "now in unix", "2026-10-15 10:00 to unix". Dates are in the local time
// zone (Options.Location) unless "utc" is written.

// tsMsThreshold: bare numbers from 10^12 on are milliseconds (a seconds
// value that large is after the year 33000).
const tsMsThreshold = 1e12

var (
	dateWords  = []string{"date", "time", "datetime", "iso", "日期", "时间"}
	unixWords  = []string{"unix", "timestamp", "epoch", "ts", "unixtime", "时间戳"}
	msWords    = []string{"ms", "millis", "milliseconds", "毫秒"}
	utcWords   = []string{"utc", "gmt", "z"}
	localWords = []string{"local", "本地"}
	nowWords   = []string{"now", "现在"}
	// dateLayouts are accepted date and time spellings.
	dateLayouts = []string{
		"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02",
		"2006/01/02 15:04:05", "2006/01/02 15:04", "2006/01/02",
	}
	// tsUnits scale a timestamp to seconds.
	tsUnits = map[string]float64{"s": 1, "sec": 1, "secs": 1, "seconds": 1, "秒": 1,
		"ms": 1e-3, "毫秒": 1e-3, "us": 1e-6, "µs": 1e-6, "μs": 1e-6, "ns": 1e-9}
)

func oneOf(s string, words []string) bool {
	for _, w := range words {
		if s == w {
			return true
		}
	}
	return false
}

// tsTarget is what a timestamp conversion asks for.
type tsTarget struct {
	unix, ms, date, utc bool
}

// parseTSTarget parses "date", "date in utc", "unix", "unix ms", "utc"…
func parseTSTarget(s string) (tsTarget, bool) {
	var t tsTarget
	words := strings.Fields(strings.ToLower(s))
	if len(words) == 0 {
		return t, false
	}
	for _, w := range words {
		switch {
		case oneOf(w, dateWords):
			t.date = true
		case oneOf(w, unixWords):
			t.unix = true
		case oneOf(w, msWords):
			t.ms = true
		case oneOf(w, utcWords):
			t.utc = true
		case oneOf(w, localWords), w == "in", w == "to", w == "as":
		default:
			return t, false
		}
	}
	if t.ms && !t.unix && !t.date {
		t.unix = true
	}
	if !t.unix && !t.date && !t.utc && !oneOf(words[0], localWords) {
		return t, false
	}
	return t, !(t.unix && t.date)
}

// tsSource is the kind of source of a timestamp conversion.
type tsSource int

const (
	srcNone   tsSource = iota
	srcNow             // "now"
	srcNumber          // a timestamp
	srcDate            // a date in the local zone
	srcZoned           // a date with a zone ("… utc", RFC 3339)
)

// parseTSSource parses "now", a number with an optional unit (a
// timestamp) or a date.
func (p *Plugin) parseTSSource(s string) (time.Time, tsSource) {
	low := strings.ToLower(strings.TrimSpace(s))
	if oneOf(low, nowWords) {
		return p.h.Now(), srcNow
	}
	num, unit, _ := strings.Cut(low, " ")
	unit = strings.TrimSpace(unit)
	if f, err := strconv.ParseFloat(num, 64); err == nil && isDigit(rune(num[0])) || err == nil && num[0] == '-' {
		scale, ok := tsUnits[unit]
		switch {
		case unit == "" && math.Abs(f) >= tsMsThreshold:
			scale = 1e-3
		case unit == "":
			scale = 1
		case !ok:
			return time.Time{}, srcNone
		}
		secs := f * scale
		if math.Abs(secs) > 253402300799 { // after 9999-12-31
			return time.Time{}, srcNone
		}
		whole := math.Floor(secs)
		return time.Unix(int64(whole), int64(math.Round((secs-whole)*1e9))), srcNumber
	}
	loc, kind := p.opts.Location, srcDate
	for _, z := range utcWords {
		if rest, ok := strings.CutSuffix(low, " "+z); ok {
			low, loc, kind = strings.TrimSpace(rest), time.UTC, srcZoned
			break
		}
	}
	if t, err := time.Parse(time.RFC3339, strings.ToUpper(low)); err == nil {
		return t, srcZoned
	}
	for _, layout := range dateLayouts {
		if t, err := time.ParseInLocation(layout, low, loc); err == nil {
			return t, kind
		}
	}
	return time.Time{}, srcNone
}

// timeResult converts a timestamp or a date, or returns nil.
func (p *Plugin) timeResult(text string) *result {
	src, target, ok := splitSpecial(text)
	if !ok || src == "" {
		return nil
	}
	tgt, ok := parseTSTarget(target)
	if !ok {
		return nil
	}
	t, kind := p.parseTSSource(src)
	isNum := kind == srcNumber
	if kind == srcNone || isNum && tgt.unix {
		return nil
	}
	l := p.h.I18n()
	zone := p.opts.Location
	if tgt.utc {
		zone = time.UTC
	}
	zt := t.In(zone)
	stamp := func(tt time.Time) string { return tt.Format("2006-01-02 15:04:05") }
	r := &result{source: src}
	secs := strconv.FormatInt(t.Unix(), 10)
	ms := strconv.FormatInt(t.UnixMilli(), 10)
	local := row{id: "local", title: stamp(t.In(p.opts.Location)), label: l.T("ts.local")}
	local.full = local.title + " " + zoneName(t.In(p.opts.Location))
	utc := row{id: "utc", title: stamp(t.UTC()), label: l.T("ts.utc")}
	utc.full = utc.title + " UTC"
	iso := row{id: "iso", title: zt.Format(time.RFC3339), label: l.T("ts.iso")}
	unix := row{id: "unix", title: secs, label: l.T("ts.unix")}
	unixMs := row{id: "unixms", title: ms, label: l.T("ts.unixMs")}
	rel := row{id: "relative", title: l.RelativeTime(t, p.h.Now()), label: l.T("ts.relative")}
	var rows []row
	switch {
	case tgt.unix && tgt.ms:
		rows = []row{unixMs, unix, local, utc, iso, rel}
	case tgt.unix:
		rows = []row{unix, unixMs, local, utc, iso, rel}
	case tgt.utc:
		rows = []row{utc, local, iso, unix, unixMs, rel}
		r.note = "UTC"
	default:
		rows = []row{local, utc, iso, unix, unixMs, rel}
		r.note = zoneName(t.In(p.opts.Location))
	}
	if kind == srcDate {
		r.source += " (" + zoneName(t) + ")"
	}
	for i := range rows {
		x := &rows[i]
		x.raw, x.shown = x.title, x.title
		if x.full == "" {
			x.full = x.title
		}
	}
	r.rows, r.root = rows, 1
	return r
}

// zoneName names the zone of t: "UTC" or "UTC+08:00".
func zoneName(t time.Time) string {
	if _, off := t.Zone(); off == 0 {
		return "UTC"
	}
	return "UTC" + t.Format("-07:00")
}
