package update_test

import (
	"testing"
	"time"

	"github.com/eziq1/golauncher/internal/update"
)

var t0 = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

func TestParseSettings(t *testing.T) {
	for _, tc := range []struct {
		mode, freq string
		want       update.Settings
	}{
		{"", "", update.Settings{Mode: update.ModeAuto, Interval: update.Daily}},
		{"notify", "weekly", update.Settings{Mode: update.ModeNotify, Interval: update.Weekly}},
		{"off", "daily", update.Settings{Mode: update.ModeOff, Interval: update.Daily}},
		{"bogus", "hourly", update.Settings{Mode: update.ModeAuto, Interval: update.Daily}},
	} {
		if got := update.ParseSettings(tc.mode, tc.freq); got != tc.want {
			t.Errorf("%q/%q = %+v", tc.mode, tc.freq, got)
		}
	}
}

func TestNextCheck(t *testing.T) {
	daily := update.Settings{Mode: update.ModeAuto, Interval: update.Daily}
	weekly := update.Settings{Mode: update.ModeAuto, Interval: update.Weekly}
	startup := t0.Add(update.StartupDelay)
	for _, tc := range []struct {
		name string
		st   update.State
		s    update.Settings
		now  time.Time
		want time.Time
	}{
		{"first start", update.State{}, daily, t0, startup},
		{"start, checked yesterday", update.State{LastCheck: t0.Add(-20 * time.Hour)}, daily, t0, startup},
		{"start, checked a week ago (weekly)", update.State{LastCheck: t0.Add(-8 * update.Daily)}, weekly, t0, startup},
		{"start right after a restart", update.State{LastCheck: t0.Add(-10 * time.Minute)}, daily, t0, t0.Add(-10 * time.Minute).Add(update.Daily)},
		{"checked during this run", update.State{LastCheck: t0.Add(time.Hour)}, daily, t0.Add(2 * time.Hour), t0.Add(time.Hour + update.Daily)},
		{"weekly during this run", update.State{LastCheck: t0.Add(time.Hour)}, weekly, t0.Add(2 * time.Hour), t0.Add(time.Hour + update.Weekly)},
		{"running for days", update.State{LastCheck: t0.Add(3 * update.Daily)}, daily, t0.Add(3*update.Daily + time.Hour), t0.Add(4 * update.Daily)},
		{"failed: retry in an hour", update.State{LastCheck: t0.Add(time.Hour), LastAttempt: t0.Add(update.Daily + time.Hour), LastError: "x"}, daily, t0.Add(update.Daily + time.Hour), t0.Add(update.Daily + 2*time.Hour)},
		{"rate limited", update.State{LimitedUntil: t0.Add(3 * time.Hour)}, daily, t0, t0.Add(3 * time.Hour)},
		{"clock set back", update.State{LastCheck: t0.Add(30 * update.Daily)}, daily, t0, startup},
	} {
		if got := update.NextCheck(tc.st, tc.s, t0, tc.now); !got.Equal(tc.want) {
			t.Errorf("%s: NextCheck = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSafeMoment(t *testing.T) {
	idle := update.Activity{LastUsed: t0.Add(-time.Hour)}
	for _, tc := range []struct {
		name string
		a    update.Activity
		user bool
		want string
	}{
		{"idle", idle, false, ""},
		{"launcher shows", update.Activity{LauncherVisible: true, LastUsed: idle.LastUsed}, false, update.BusyLauncher},
		{"settings show", update.Activity{SettingsVisible: true, LastUsed: idle.LastUsed}, false, update.BusySettings},
		{"a task runs", update.Activity{Tasks: 1, LastUsed: idle.LastUsed}, false, update.BusyTasks},
		{"AI answers", update.Activity{AIBusy: true, LastUsed: idle.LastUsed}, false, update.BusyAI},
		{"just used", update.Activity{LastUsed: t0.Add(-time.Minute)}, false, update.BusyRecent},
		{"used a while ago", update.Activity{LastUsed: t0.Add(-update.IdleGrace)}, false, ""},
		{"user, settings show", update.Activity{SettingsVisible: true, LastUsed: t0}, true, ""},
		{"user, a task runs", update.Activity{Tasks: 2, SettingsVisible: true}, true, update.BusyTasks},
		{"user, AI answers", update.Activity{AIBusy: true}, true, update.BusyAI},
	} {
		if got := update.SafeMoment(tc.a, t0, tc.user); got != tc.want {
			t.Errorf("%s: SafeMoment = %q, want %q", tc.name, got, tc.want)
		}
	}
}
