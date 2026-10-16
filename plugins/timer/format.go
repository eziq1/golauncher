package timer

import (
	"fmt"
	"strings"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

// formatter formats durations and times for the UI language.
type formatter struct {
	l      plugin.Localizer
	hour24 bool
}

// duration is a length such as "25 min", "1 h 30 min" or "1 min 30 s".
func (f formatter) duration(d time.Duration) string {
	s := int64((d + time.Second/2) / time.Second)
	if s <= 0 {
		return f.l.T("dur.s", "n", 0)
	}
	var parts []string
	if n := s / 86400; n > 0 {
		parts = append(parts, f.l.T("dur.d", "n", n))
	}
	if n := s / 3600 % 24; n > 0 {
		parts = append(parts, f.l.T("dur.h", "n", n))
	}
	if n := s / 60 % 60; n > 0 {
		parts = append(parts, f.l.T("dur.m", "n", n))
	}
	if n := s % 60; n > 0 {
		parts = append(parts, f.l.T("dur.s", "n", n))
	}
	return strings.Join(parts, " ")
}

// approx is a duration rounded up to whole minutes from one minute on
// ("in 5 h 30 min").
func (f formatter) approx(d time.Duration) string {
	if d >= time.Minute {
		d = (d + time.Minute - 1).Truncate(time.Minute)
	}
	return f.duration(d)
}

// clock is the time left as shown in a list: "24:13" or "1:05:03". Seconds
// are rounded up, so a 25-minute timer starts at 25:00 and shows 00:01 in
// its last second.
func clock(d time.Duration) string {
	s := int64((max(d, 0) + time.Second - 1) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// timeOfDay is "15:04", or "3:04 PM" with the 12-hour clock.
func (f formatter) timeOfDay(t time.Time) string {
	if f.hour24 {
		return t.Format("15:04")
	}
	if t.Hour() >= 12 {
		return f.l.T("time.pm", "time", t.Format("3:04"))
	}
	return f.l.T("time.am", "time", t.Format("3:04"))
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
	}
	return f.l.T("weekday.sunday")
}

// when says when t is, seen from now: "at 15:42", "tomorrow at 09:00",
// "yesterday at 09:00", "Monday at 09:00" (within a week), else with the
// date.
func (f formatter) when(t, now time.Time) string {
	t = t.In(now.Location())
	tm := f.timeOfDay(t)
	days := dayNumber(t) - dayNumber(now)
	switch {
	case days == 0:
		return f.l.T("when.today", "time", tm)
	case days == 1:
		return f.l.T("when.tomorrow", "time", tm)
	case days == -1:
		return f.l.T("when.yesterday", "time", tm)
	case days > 1 && days < 7:
		return f.l.T("when.weekday", "weekday", f.weekday(t.Weekday()), "time", tm)
	case t.Year() != now.Year():
		return f.l.T("when.dateYear", "year", t.Year(), "month", int(t.Month()), "day", t.Day(), "time", tm)
	}
	return f.l.T("when.date", "month", int(t.Month()), "day", t.Day(), "time", tm)
}

// stamp is a date and time for the detail pane: "10/15 15:42".
func (f formatter) stamp(t, now time.Time) string {
	t = t.In(now.Location())
	if t.Year() != now.Year() {
		return f.l.T("stamp.year", "year", t.Year(), "month", int(t.Month()), "day", t.Day(), "time", f.timeOfDay(t))
	}
	return f.l.T("stamp", "month", int(t.Month()), "day", t.Day(), "time", f.timeOfDay(t))
}

// dayNumber counts calendar days, for comparing dates in t's location.
func dayNumber(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// name is what a timer is called: its label, or "25 min timer" /
// "Reminder" without one.
func (f formatter) name(e entry) string {
	if e.Label != "" {
		return e.Label
	}
	if e.Kind == kindReminder {
		return f.l.T("name.reminder")
	}
	return f.l.T("name.timer", "duration", f.duration(e.total()))
}

// notice is the notification of a timer that is due.
func (p *Plugin) notice(e entry) plugin.Toast {
	f := p.formatter()
	now := p.h.Now()
	t := plugin.Toast{Style: plugin.ToastInfo, Silent: !p.h.Preferences().Bool(prefSound)}
	switch {
	case e.Kind == kindReminder && e.Label != "":
		t.Title = f.l.T("notify.reminder", "label", e.Label)
	case e.Kind == kindReminder:
		t.Title = f.l.T("notify.reminderNoLabel")
	case e.Label != "":
		t.Title = f.l.T("notify.timer", "label", e.Label)
	default:
		t.Title = f.l.T("notify.timerNoLabel")
	}
	switch {
	case e.Missed:
		t.Message = f.l.T("notify.missed", "when", f.when(e.End, now))
	case e.Kind == kindReminder:
		t.Message = f.l.T("notify.reminderBody", "time", f.timeOfDay(e.End.In(now.Location())))
	default:
		t.Message = f.l.T("notify.timerBody", "duration", f.duration(e.total()), "time", f.timeOfDay(e.End.In(now.Location())))
	}
	return t
}

func (p *Plugin) formatter() formatter {
	return formatter{l: p.h.I18n(), hour24: p.h.Preferences().Bool(prefHour24)}
}
