package ui

// The Advanced page: data folder, config file, log level, and settings
// import/export.

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
)

// ExportFileName is the file name suggested for exported settings.
const ExportFileName = "golauncher-settings.json"

// exportSettings asks for a file and writes the configuration to it.
func (s *Settings) exportSettings() {
	s.setNotice("", false)
	s.host.ChooseExportFile(ExportFileName, func(path string, err error) {
		if err == nil && path == "" {
			return // canceled
		}
		if err == nil {
			var data []byte
			if data, err = config.Export(s.env.Config.Get()); err == nil {
				err = os.WriteFile(path, data, 0o644)
			}
		}
		if err != nil {
			s.setNotice(s.loc.T("settings.advanced.exportFailed", "err", err.Error()), true)
			return
		}
		s.setNotice(s.loc.T("settings.advanced.exported", "path", path), false)
	})
}

// importSettings asks for a file exported before and replaces the whole
// configuration with it.
func (s *Settings) importSettings() {
	s.setNotice("", false)
	s.host.ChooseImportFile(func(path string, err error) {
		if err == nil && path == "" {
			return // canceled
		}
		var cfg config.Config
		if err == nil {
			var data []byte
			if data, err = os.ReadFile(path); err == nil {
				cfg, err = config.Import(data)
			}
		}
		if err == nil {
			// A file from an older version may hold passwords.
			err = s.env.MoveSecrets(&cfg)
		}
		if err == nil {
			err = s.env.Config.Update(func(c *config.Config) { *c = cfg })
		}
		if err != nil {
			s.setNotice(s.loc.T("settings.advanced.importFailed", "err", err.Error()), true)
			return
		}
		s.saveErr = ""
		clear(s.bufs)
		clear(s.errs)
		clear(s.stored)
		s.setNotice(s.loc.T("settings.advanced.imported", "path", path), false)
	})
}

func (s *Settings) advancedPage(c *mui.Context, cfg config.Config) {
	s.heading(c, s.loc.T("settings.nav.advanced"))
	dataDir := filepath.Dir(s.env.Services.DataRoot)
	s.setting(c, s.loc.T("settings.advanced.dataDir"), dataDir, func() {
		if button(c, s.loc.T("settings.advanced.open")) {
			s.open(dataDir)
		}
	})
	if path := s.env.Config.Path(); path != "" {
		s.setting(c, s.loc.T("settings.advanced.configFile"), path, func() {
			if button(c, s.loc.T("settings.advanced.openConfig")) {
				s.open(path)
			}
		})
	}
	levels := []string{"debug", "info", "warn", "error"}
	s.setting(c, s.loc.T("settings.advanced.logLevel"), "", func() {
		i := slices.Index(levels, cmp.Or(cfg.LogLevel, "info"))
		if mui.Segmented(c, &i, s.loc.T("settings.log.debug"), s.loc.T("settings.log.info"), s.loc.T("settings.log.warn"), s.loc.T("settings.log.error")).
			Label(s.loc.T("settings.advanced.logLevel")).Changed() {
			s.update(func(c *config.Config) { c.LogLevel = levels[i] })
		}
	})
	s.setting(c, s.loc.T("settings.advanced.clearRanking"), s.loc.T("settings.advanced.clearRanking.desc"), func() {
		if button(c, s.loc.T("settings.advanced.clear")) {
			if err := s.env.Services.DB.ClearUsage(); err != nil {
				s.setNotice(err.Error(), true)
			} else {
				s.setNotice(s.loc.T("settings.advanced.cleared"), false)
			}
		}
	})
	s.setting(c, s.loc.T("settings.advanced.export"), s.loc.T("settings.advanced.export.desc"), func() {
		if button(c, s.loc.T("settings.advanced.exportButton")) {
			s.exportSettings()
		}
	})
	s.setting(c, s.loc.T("settings.advanced.import"), s.loc.T("settings.advanced.import.desc"), func() {
		if button(c, s.loc.T("settings.advanced.importButton")) {
			s.importSettings()
		}
	})
	s.noticeView(c)
}

func (s *Settings) open(path string) {
	if err := s.host.OpenPath(path); err != nil {
		s.setNotice(err.Error(), true)
	}
}
