package ui

import (
	"context"
	"slices"
	"time"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/install"
	"github.com/eziq1/golauncher/internal/update"
)

// Updater is the update service as the settings see it (update.Service).
type Updater interface {
	Status() update.Status
	Check(ctx context.Context) (update.Status, error)
	InstallNow(ctx context.Context) error
	Skip(version string)
	OnChange(fn func())
}

// updatesState is the Updates section of the About page.
type updatesState struct {
	u        Updater
	checking bool   // a check the user asked for runs
	message  string // its answer
	failed   bool
}

// SetUpdater shows the update settings and status of u on the About page
// (none without one).
func (s *Settings) SetUpdater(u Updater) {
	s.updates.u = u
	u.OnChange(func() {
		s.asyncMu.Lock()
		svc := s.services
		s.asyncMu.Unlock()
		svc.Invalidate()
	})
}

// updatesSection is the Updates section of the About page.
func (s *Settings) updatesSection(c *mui.Context, cfg config.Config) {
	u := s.updates.u
	if u == nil {
		return
	}
	t := c.Theme()
	st := u.Status()
	s.subheading(c, s.loc.T("settings.updates.title"))
	s.asyncMu.Lock()
	checking, message, failed := s.updates.checking, s.updates.message, s.updates.failed
	s.asyncMu.Unlock()

	status, statusErr := s.updateStatus(st, checking)
	color := t.TextMuted
	if statusErr {
		color = t.Danger
	}
	mui.Text(c, status).TextColor(color)
	if message != "" {
		color := t.TextMuted
		if failed {
			color = t.Danger
		}
		mui.Text(c, message).FontSize(12).TextColor(color)
	}
	mui.Row(c).Gap(8).Wrap().AlignItems(mui.Center).Children(func() {
		if mui.Button(c, s.loc.T("settings.updates.check")).Disabled(checking || st.Phase == update.PhaseChecking).Clicked() {
			s.checkUpdates(cfg)
		}
		if st.Available && st.Disabled == "" {
			switch st.Phase {
			case update.PhaseReady:
				if button(c, s.loc.T("settings.updates.restart")) {
					s.installUpdate()
				}
			case update.PhaseIdle:
				if cfg.Updates.EffectiveMode() != config.UpdatesAuto || st.Skipped || st.Blocked || st.Error != "" {
					if button(c, s.loc.T("settings.updates.install")) {
						s.installUpdate()
					}
				}
			}
			if !st.Skipped && st.Phase != update.PhaseInstalling && button(c, s.loc.T("settings.updates.skip")) {
				u.Skip(st.Latest)
			}
		}
		page := st.Page
		if page == "" {
			page = update.ReleasesURL
		}
		mui.Link(c, s.loc.T("settings.updates.notes"), page)
	})

	if st.Disabled != "" {
		note := s.loc.T("settings.updates.disabledPortable")
		switch st.Disabled {
		case update.DisabledDev:
			note = s.loc.T("settings.updates.disabledDev")
		case update.DisabledEnv:
			note = s.loc.T("settings.updates.disabledEnv")
		case update.DisabledWinget:
			note = s.loc.T("settings.updates.disabledWinget", "command", install.WingetUpgrade)
		case update.DisabledHomebrew:
			note = s.loc.T("settings.updates.disabledHomebrew", "command", install.HomebrewUpgrade)
		}
		mui.Text(c, note).FontSize(12).TextColor(t.TextMuted)
		return
	}
	modes := []string{config.UpdatesAuto, config.UpdatesNotify, config.UpdatesOff}
	modeLabels := []string{s.loc.T("settings.updates.modeAuto"), s.loc.T("settings.updates.modeNotify"), s.loc.T("settings.updates.modeOff")}
	s.setting(c, s.loc.T("settings.updates.mode"), s.loc.T("settings.updates.mode.desc"), func() {
		chosen := modeLabels[slices.Index(modes, cfg.Updates.EffectiveMode())]
		if mui.Select(c, &chosen, modeLabels).Label(s.loc.T("settings.updates.mode")).Changed() {
			v := modes[max(slices.Index(modeLabels, chosen), 0)]
			s.update(func(c *config.Config) { c.Updates.Mode = v })
		}
	})
	if cfg.Updates.EffectiveMode() != config.UpdatesOff {
		freqs := []string{config.UpdatesDaily, config.UpdatesWeekly}
		freqLabels := []string{s.loc.T("settings.updates.daily"), s.loc.T("settings.updates.weekly")}
		s.setting(c, s.loc.T("settings.updates.frequency"), "", func() {
			chosen := freqLabels[slices.Index(freqs, cfg.Updates.EffectiveFrequency())]
			if mui.Select(c, &chosen, freqLabels).Label(s.loc.T("settings.updates.frequency")).Changed() {
				v := freqs[max(slices.Index(freqLabels, chosen), 0)]
				s.update(func(c *config.Config) { c.Updates.Frequency = v })
			}
		})
	}
	mui.Text(c, s.loc.T("settings.updates.channel")).FontSize(12).TextColor(t.TextMuted)
}

// updateStatus describes the status in a sentence, and whether it reports
// a failure.
func (s *Settings) updateStatus(st update.Status, checking bool) (string, bool) {
	last := s.loc.T("settings.updates.never")
	if !st.LastCheck.IsZero() {
		last = st.LastCheck.Local().Format(time.DateTime[:len(time.DateTime)-3])
	}
	switch {
	case checking || st.Phase == update.PhaseChecking:
		return s.loc.T("settings.updates.checking"), false
	case st.Phase == update.PhaseDownloading:
		pct := 0
		if st.Progress > 0 {
			pct = int(st.Progress * 100)
		}
		return s.loc.T("settings.updates.downloading", "version", st.Latest, "percent", pct), false
	case st.Phase == update.PhaseInstalling:
		return s.loc.T("settings.updates.installing", "version", st.Latest), false
	case st.Phase == update.PhaseReady:
		if st.Waiting == update.BusyTasks || st.Waiting == update.BusyAI {
			return s.loc.T("settings.updates.readyBusy", "version", st.Latest), false
		}
		return s.loc.T("settings.updates.ready", "version", st.Latest), false
	case st.Error != "":
		return s.loc.T("settings.updates.error", "err", st.Error), true
	case st.Available && st.Blocked:
		return s.loc.T("settings.updates.blocked", "version", st.Latest), false
	case st.Available && st.Skipped:
		return s.loc.T("settings.updates.skipped", "version", st.Latest), false
	case st.Available:
		return s.loc.T("settings.updates.available", "version", st.Latest), false
	}
	return s.loc.T("settings.updates.upToDate", "time", last), false
}

// checkUpdates checks for updates as the user asked.
func (s *Settings) checkUpdates(cfg config.Config) {
	u := s.updates.u
	s.asyncMu.Lock()
	s.updates.checking, s.updates.message, s.updates.failed = true, "", false
	s.asyncMu.Unlock()
	mode := update.Mode(cfg.Updates.EffectiveMode())
	s.async(func() {
		st, err := u.Check(context.Background())
		// The status line says what was found; the message adds what
		// happens next, or why the check failed.
		_, msg, failed := update.Summary(s.loc, st, err, mode)
		if !failed && !st.Available {
			msg = ""
		}
		s.asyncMu.Lock()
		s.updates.checking, s.updates.message, s.updates.failed = false, msg, failed
		s.asyncMu.Unlock()
	})
}

// installUpdate downloads the update if needed and restarts into it.
func (s *Settings) installUpdate() {
	u := s.updates.u
	s.asyncMu.Lock()
	s.updates.message, s.updates.failed = "", false
	s.asyncMu.Unlock()
	s.async(func() {
		err := u.InstallNow(context.Background())
		s.asyncMu.Lock()
		if err != nil {
			s.updates.message, s.updates.failed = err.Error(), true
		}
		s.asyncMu.Unlock()
	})
}
