package clock

import (
	"slices"
	"testing"
	"time"

	"github.com/eziq1/golauncher/pkg/plugin"
)

func load(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestClockFormat(t *testing.T) {
	e := newEnv(t)
	tm := time.Date(2026, 1, 2, 0, 5, 9, 0, time.UTC)
	pm := time.Date(2026, 1, 2, 13, 5, 9, 0, time.UTC)
	noon := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	f24 := formatter{l: e.h.I18n(), hour24: true}
	f12 := formatter{l: e.h.I18n()}
	tests := []struct {
		f       formatter
		t       time.Time
		seconds bool
		want    string
	}{
		{f24, tm, true, "00:05:09"},
		{f24, tm, false, "00:05"},
		{f24, pm, true, "13:05:09"},
		{f12, tm, true, "12:05:09 AM"},
		{f12, pm, false, "1:05 PM"},
		{f12, noon, false, "12:00 PM"},
	}
	for _, tt := range tests {
		if got := tt.f.clock(tt.t, tt.seconds); got != tt.want {
			t.Errorf("clock(%v, hour24=%v, %v) = %q, want %q", tt.t, tt.f.hour24, tt.seconds, got, tt.want)
		}
	}
	if got := f24.date(tm); got != "Friday, Jan 2" {
		t.Errorf("date = %q", got)
	}
	if got := f24.longDate(tm); got != "Friday, January 2, 2026" {
		t.Errorf("long date = %q", got)
	}
}

func TestOffsetsAndDifferences(t *testing.T) {
	e := newEnv(t)
	f := formatter{l: e.h.I18n(), hour24: true}
	at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		zone   string
		offset string
		clock  string
	}{
		{"Asia/Kolkata", "UTC+5:30", "15:30"},
		{"Asia/Kathmandu", "UTC+5:45", "15:45"},
		{"America/St_Johns", "UTC-2:30", "07:30"}, // NDT
		{"Asia/Tehran", "UTC+3:30", "13:30"},
		{"UTC", "UTC", "10:00"},
		{"America/New_York", "UTC-4", "06:00"},
	}
	for _, tt := range tests {
		ct := at.In(load(t, tt.zone))
		if got := f.offset(ct); got != tt.offset {
			t.Errorf("%s: offset %q, want %q", tt.zone, got, tt.offset)
		}
		if got := f.clock(ct, false); got != tt.clock {
			t.Errorf("%s: clock %q, want %q", tt.zone, got, tt.clock)
		}
	}
	diffs := map[int]string{
		0:                 "Same time",
		8 * 3600:          "+8h",
		-4 * 3600:         "-4h",
		5*3600 + 45*60:    "+5h 45m",
		-(9*3600 + 30*60): "-9h 30m",
		24 * 3600:         "+24h",
	}
	for d, want := range diffs {
		if got := f.difference(d); got != want {
			t.Errorf("difference(%d) = %q, want %q", d, got, want)
		}
	}
}

// TestDST checks New York around the switch to daylight saving time on
// 2026-03-08 at 2:00 (EST, UTC-5 -> EDT, UTC-4).
func TestDST(t *testing.T) {
	e := newEnv(t)
	f := formatter{l: e.h.I18n(), hour24: true}
	ny := load(t, "America/New_York")
	before := time.Date(2026, 3, 8, 6, 59, 59, 0, time.UTC).In(ny)
	after := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC).In(ny)
	if got := f.clock(before, true) + " " + f.offset(before); got != "01:59:59 UTC-5" {
		t.Errorf("before = %q", got)
	}
	if got := f.clock(after, true) + " " + f.offset(after); got != "03:00:00 UTC-4" {
		t.Errorf("after = %q", got)
	}
	if before.IsDST() || !after.IsDST() {
		t.Error("IsDST wrong")
	}
	if got := f.nextChange(before); got != "Sunday, March 8, 2026 02:00 → UTC-4" {
		t.Errorf("next change before = %q", got)
	}
	if got := f.nextChange(after); got != "Sunday, November 1, 2026 02:00 → UTC-5" {
		t.Errorf("next change after = %q", got)
	}
	if got := f.nextChange(time.Date(2026, 3, 8, 0, 0, 0, 0, load(t, "Asia/Shanghai"))); got != "None" {
		t.Errorf("next change in Shanghai = %q", got)
	}

	// The world clock follows: New York is 13h behind Beijing before the
	// switch and 12h after it.
	e.clock.set(time.Date(2026, 3, 8, 6, 30, 0, 0, load(t, "Asia/Shanghai")))
	if got := e.root(t, "time new york")[0].Subtitle; got != "United States · Saturday, Mar 7 · UTC-5 · -13h" {
		t.Errorf("before switch: %q", got)
	}
	e.clock.set(time.Date(2026, 3, 9, 6, 30, 0, 0, load(t, "Asia/Shanghai")))
	if got := e.root(t, "time new york")[0].Subtitle; got != "United States · Sunday, Mar 8 · UTC-4 · -12h" {
		t.Errorf("after switch: %q", got)
	}
}

func metadata(d *plugin.Detail, label string) string {
	i := slices.IndexFunc(d.Metadata, func(m plugin.Metadata) bool { return m.Label == label })
	if i < 0 {
		return ""
	}
	return d.Metadata[i].Value
}

func TestDetail(t *testing.T) {
	e := newEnv(t)
	it := e.root(t, "time new york")[0]
	d := it.Detail
	if d == nil {
		t.Fatal("no detail")
	}
	want := map[string]string{
		"Time":                 "06:00:00",
		"Date":                 "Thursday, October 15, 2026",
		"Country":              "United States",
		"Time zone":            "America/New_York",
		"UTC offset":           "UTC-4",
		"Abbreviation":         "EDT",
		"Daylight saving time": "Yes",
		"Next change":          "Sunday, November 1, 2026 02:00 → UTC-5",
	}
	for label, v := range want {
		if got := metadata(d, label); got != v {
			t.Errorf("%s = %q, want %q", label, got, v)
		}
	}
	if got := metadata(e.root(t, "time kathmandu")[0].Detail, "Abbreviation"); got != "" {
		t.Errorf("numeric abbreviation shown: %q", got)
	}
}

// TestDateLine checks Tomorrow and Yesterday across the date line: at 10:30
// UTC it is already the next day in Kiritimati (UTC+14) and still the
// previous day in Pago Pago (UTC-11).
func TestDateLine(t *testing.T) {
	e := newEnv(t)
	e.clock.set(time.Date(2026, 10, 15, 10, 30, 0, 0, time.UTC))
	acc := func(q string) string {
		t.Helper()
		return e.root(t, "time "+q)[0].Accessory
	}
	if got := acc("kiritimati"); got != "☾ Tomorrow" { // 00:30 tomorrow
		t.Errorf("Kiritimati: %q", got)
	}
	if got := acc("apia"); got != "☾" { // 23:30, UTC+13
		t.Errorf("Apia: %q", got)
	}
	if got := acc("pago pago"); got != "☾ Yesterday" {
		t.Errorf("Pago Pago: %q", got)
	}
	if got := acc("london"); got != "☀" {
		t.Errorf("London: %q", got)
	}
	if got := e.root(t, "time pago pago")[0].Subtitle; got != "American Samoa · Wednesday, Oct 14 · UTC-11 · -11h" {
		t.Errorf("Pago Pago subtitle: %q", got)
	}
	// Seen from Pago Pago, Apia is a day and an hour ahead.
	e.clock.set(time.Date(2026, 10, 15, 10, 30, 0, 0, load(t, "Pacific/Pago_Pago")))
	it := e.root(t, "time apia")[0]
	if it.Subtitle != "Samoa · Friday, Oct 16 · UTC+13 · +24h" || it.Accessory != "☀ Tomorrow" {
		t.Errorf("Apia from Pago Pago: %q %q", it.Subtitle, it.Accessory)
	}
}

func TestRelativeDay(t *testing.T) {
	utc := time.Date(2026, 12, 31, 23, 0, 0, 0, time.UTC)
	if got := relativeDay(utc.In(load(t, "Asia/Tokyo")), utc); got != 1 {
		t.Errorf("new year in Tokyo: %d", got)
	}
	if got := relativeDay(utc, utc.In(load(t, "Asia/Tokyo"))); got != -1 {
		t.Errorf("from Tokyo: %d", got)
	}
	if got := relativeDay(utc, utc); got != 0 {
		t.Errorf("same: %d", got)
	}
}

func TestShowSecondsAndHour24(t *testing.T) {
	e := newEnv(t)
	e.setPref(prefShowSeconds, false)
	e.setPref(prefHour24, false)
	if got := e.root(t, "time beijing")[0].Title; got != "Beijing  6:00 PM" {
		t.Errorf("title = %q", got)
	}
}
