package update

import "time"

// Mode is the user's choice of automatic updates (config "updates.mode").
type Mode string

const (
	ModeAuto   Mode = "auto"   // download and install automatically (default)
	ModeNotify Mode = "notify" // only tell the user about a new version
	ModeOff    Mode = "off"    // no automatic checks
)

// Check intervals (config "updates.frequency").
const (
	Daily  = 24 * time.Hour
	Weekly = 7 * Daily
)

// Settings are the user's update settings.
type Settings struct {
	Mode     Mode
	Interval time.Duration
}

// ParseSettings turns the configuration's strings into Settings; unknown or
// empty values take the defaults (auto, daily).
func ParseSettings(mode, frequency string) Settings {
	s := Settings{Mode: ModeAuto, Interval: Daily}
	switch Mode(mode) {
	case ModeNotify, ModeOff:
		s.Mode = Mode(mode)
	}
	if frequency == "weekly" {
		s.Interval = Weekly
	}
	return s
}

// Timing of the automatic checks and installs.
var (
	// StartupDelay is how long after starting the first check waits.
	StartupDelay = 30 * time.Second
	// MinRecheck: no check at startup when the last one is more recent
	// (e.g. golauncher just restarted into an update).
	MinRecheck = time.Hour
	// RetryAfter is how soon a failed check is retried (at most the
	// interval).
	RetryAfter = time.Hour
	// IdleGrace is how long after the user last used the launcher or the
	// settings an update may restart golauncher.
	IdleGrace = 3 * time.Minute
	// TickEvery is how often the service looks at what is due.
	TickEvery = time.Minute
)

// NextCheck returns when the next automatic check is due, for a service
// started at started: StartupDelay after the start (unless the last check
// is more recent than MinRecheck), then an interval after the last
// successful check; RetryAfter after a failed one; not before a rate limit
// ends.
func NextCheck(st State, s Settings, started, now time.Time) time.Time {
	interval := s.Interval
	if interval <= 0 {
		interval = Daily
	}
	startup := started.Add(StartupDelay)
	due := startup
	// A last check in the future means the clock was set back: ignore it.
	if last := st.LastCheck; !last.IsZero() && !last.After(now) {
		due = last.Add(interval)
		if startup.Sub(last) >= MinRecheck && startup.Before(due) {
			due = startup // check at every start, but not right after a restart
		}
	}
	if st.LastError != "" && st.LastAttempt.After(st.LastCheck) {
		if retry := st.LastAttempt.Add(min(RetryAfter, interval)); retry.After(due) {
			due = retry
		}
	}
	if st.LimitedUntil.After(due) {
		due = st.LimitedUntil
	}
	if due.Before(startup) {
		due = startup
	}
	return due
}

// Activity is what the user is doing, for SafeMoment.
type Activity struct {
	LauncherVisible bool
	SettingsVisible bool
	Tasks           int       // active background tasks
	AIBusy          bool      // an AI answer is being written
	LastUsed        time.Time // when the launcher or the settings last showed or hid
}

// Reasons SafeMoment gives for waiting.
const (
	BusyLauncher = "launcher"
	BusySettings = "settings"
	BusyTasks    = "tasks"
	BusyAI       = "ai"
	BusyRecent   = "recent"
)

// SafeMoment returns why golauncher should not restart into an update now,
// or "" when it may: the launcher and the settings are hidden, no
// background task runs, no AI answer is being written, and the user has
// not used golauncher for IdleGrace. When the user asked to restart (user),
// only the tasks and the AI answer count.
func SafeMoment(a Activity, now time.Time, user bool) string {
	switch {
	case a.Tasks > 0:
		return BusyTasks
	case a.AIBusy:
		return BusyAI
	case user:
		return ""
	case a.LauncherVisible:
		return BusyLauncher
	case a.SettingsVisible:
		return BusySettings
	case now.Sub(a.LastUsed) < IdleGrace:
		return BusyRecent
	}
	return ""
}
