package clock

import (
	"fmt"
	"strconv"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// formatter formats times with the plugin's messages and preferences.
type formatter struct {
	l       plugin.Localizer
	hour24  bool
	seconds bool
}

// clock formats the time of day of t: "14:32:05", "2:32 PM".
func (f formatter) clock(t time.Time, seconds bool) string {
	h := t.Hour()
	hour := fmt.Sprintf("%02d", h)
	if !f.hour24 {
		hour = strconv.Itoa((h+11)%12 + 1)
	}
	mm, ss := fmt.Sprintf("%02d", t.Minute()), fmt.Sprintf("%02d", t.Second())
	var s string
	if seconds {
		s = f.l.T("format.timeSeconds", "hour", hour, "minute", mm, "second", ss)
	} else {
		s = f.l.T("format.time", "hour", hour, "minute", mm)
	}
	if f.hour24 {
		return s
	}
	period := f.l.T("format.am")
	if h >= 12 {
		period = f.l.T("format.pm")
	}
	return f.l.T("format.time12", "time", s, "period", period)
}

// date formats the day of t: "Thursday, Oct 15".
func (f formatter) date(t time.Time) string {
	return f.l.T("format.date", "weekday", f.weekday(t.Weekday()), "month", f.shortMonth(t.Month()), "day", t.Day())
}

// longDate formats the day of t: "Thursday, October 15, 2026".
func (f formatter) longDate(t time.Time) string {
	return f.l.T("format.dateLong", "weekday", f.weekday(t.Weekday()), "month", f.longMonth(t.Month()), "day", t.Day(), "year", t.Year())
}

// offset formats the UTC offset of t: "UTC+8", "UTC+5:30", "UTC".
func (f formatter) offset(t time.Time) string {
	_, off := t.Zone()
	return f.offsetOf(off)
}

func (f formatter) offsetOf(seconds int) string {
	if seconds == 0 {
		return f.l.T("format.utc")
	}
	return f.l.T("format.offset", "offset", signedHM(seconds))
}

// signedHM formats seconds as "+8", "-3:30", "+5:45".
func signedHM(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	h, m := seconds/3600, seconds%3600/60
	if m == 0 {
		return sign + strconv.Itoa(h)
	}
	return fmt.Sprintf("%s%d:%02d", sign, h, m)
}

// difference formats how far a clock is ahead of (+) or behind (-) the
// local one: "+15h", "-2h 30m", "Same time".
func (f formatter) difference(seconds int) string {
	if seconds == 0 {
		return f.l.T("diff.same")
	}
	sign := "+"
	if seconds < 0 {
		sign, seconds = "-", -seconds
	}
	h, m := seconds/3600, seconds%3600/60
	if m == 0 {
		return f.l.T("diff.hours", "hours", sign+strconv.Itoa(h))
	}
	return f.l.T("diff.hoursMinutes", "hours", sign+strconv.Itoa(h), "minutes", m)
}

// relativeDay is the number of calendar days between the dates of local and
// t, each in its own zone: 1 when it is already tomorrow at t.
func relativeDay(t, local time.Time) int {
	y1, m1, d1 := local.Date()
	y2, m2, d2 := t.Date()
	a := time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)
	b := time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}

// night reports whether it is night at t (20:00 to 6:00).
func night(t time.Time) bool { return t.Hour() >= 20 || t.Hour() < 6 }

// Accessory symbols.
const (
	symbolDay      = "☀"
	symbolNight    = "☾"
	symbolFavorite = "★"
)

// accessory is the day/night symbol plus "Tomorrow" or "Yesterday" when the
// date differs from the local one.
func (f formatter) accessory(t, local time.Time, favorite bool) string {
	s := symbolDay
	if night(t) {
		s = symbolNight
	}
	switch relativeDay(t, local) {
	case 1:
		s += " " + f.l.T("relative.tomorrow")
	case -1:
		s += " " + f.l.T("relative.yesterday")
	}
	if favorite {
		s = symbolFavorite + " " + s
	}
	return s
}

// zoneAbbr returns the abbreviation of t's zone when it is a name ("CEST")
// rather than a number ("+08"), else "".
func zoneAbbr(t time.Time) string {
	name, _ := t.Zone()
	if name == "" || name[0] == '+' || name[0] == '-' {
		return ""
	}
	return name
}

// detail is the detail panel of a clock showing t.
func (f formatter) detail(name, country, zone string, t time.Time) *plugin.Detail {
	d := &plugin.Detail{
		Markdown: "# " + f.clock(t, true) + "\n\n" + name + " · " + f.longDate(t),
	}
	add := func(label, value string) {
		if value != "" {
			d.Metadata = append(d.Metadata, plugin.Metadata{Label: label, Value: value})
		}
	}
	add(f.l.T("meta.time"), f.clock(t, true))
	add(f.l.T("meta.date"), f.longDate(t))
	add(f.l.T("meta.country"), country)
	add(f.l.T("meta.zone"), zone)
	add(f.l.T("meta.offset"), f.offset(t))
	add(f.l.T("meta.abbreviation"), zoneAbbr(t))
	dst := f.l.T("dst.no")
	if t.IsDST() {
		dst = f.l.T("dst.yes")
	}
	add(f.l.T("meta.dst"), dst)
	add(f.l.T("meta.nextChange"), f.nextChange(t))
	return d
}

// nextChange describes the next change of t's UTC offset (a daylight saving
// time transition) at the wall time it happens, e.g. "Sunday, November 1,
// 2026 02:00 → UTC-5".
func (f formatter) nextChange(t time.Time) string {
	_, end := t.ZoneBounds()
	if end.IsZero() {
		return f.l.T("nextChange.none")
	}
	_, before := t.Zone()
	at := end.In(time.FixedZone("", before))
	return f.l.T("nextChange.value", "date", f.longDate(at), "time", f.clock(at, false), "offset", f.offset(end.In(t.Location())))
}

func (f formatter) weekday(d time.Weekday) string {
	switch d {
	case time.Monday:
		return f.l.T("weekday.monday")
	case time.Tuesday:
		return f.l.T("weekday.tuesday")
	case time.Wednesday:
		return f.l.T("weekday.wednesday")
	case time.Thursday:
		return f.l.T("weekday.thursday")
	case time.Friday:
		return f.l.T("weekday.friday")
	case time.Saturday:
		return f.l.T("weekday.saturday")
	default:
		return f.l.T("weekday.sunday")
	}
}

func (f formatter) shortMonth(m time.Month) string {
	switch m {
	case time.February:
		return f.l.T("month.short.feb")
	case time.March:
		return f.l.T("month.short.mar")
	case time.April:
		return f.l.T("month.short.apr")
	case time.May:
		return f.l.T("month.short.may")
	case time.June:
		return f.l.T("month.short.jun")
	case time.July:
		return f.l.T("month.short.jul")
	case time.August:
		return f.l.T("month.short.aug")
	case time.September:
		return f.l.T("month.short.sep")
	case time.October:
		return f.l.T("month.short.oct")
	case time.November:
		return f.l.T("month.short.nov")
	case time.December:
		return f.l.T("month.short.dec")
	default:
		return f.l.T("month.short.jan")
	}
}

func (f formatter) longMonth(m time.Month) string {
	switch m {
	case time.February:
		return f.l.T("month.long.feb")
	case time.March:
		return f.l.T("month.long.mar")
	case time.April:
		return f.l.T("month.long.apr")
	case time.May:
		return f.l.T("month.long.may")
	case time.June:
		return f.l.T("month.long.jun")
	case time.July:
		return f.l.T("month.long.jul")
	case time.August:
		return f.l.T("month.long.aug")
	case time.September:
		return f.l.T("month.long.sep")
	case time.October:
		return f.l.T("month.long.oct")
	case time.November:
		return f.l.T("month.long.nov")
	case time.December:
		return f.l.T("month.long.dec")
	default:
		return f.l.T("month.long.jan")
	}
}
