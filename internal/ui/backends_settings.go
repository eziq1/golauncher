package ui

import (
	"context"
	"slices"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/plugins/ai/backend"
)

// backendsState is the Backends section of the AI plugin's page (R84,
// docs/ai.md §22). Its fields are guarded by Settings.asyncMu: the
// discovery and the checks of typed paths run in the background, since they
// run the programs' version commands.
type backendsState struct {
	requested bool // the first discovery was started
	loading   bool
	reports   []backend.Report
	// custom marks the backends whose "Custom path…" is chosen (the text
	// field shows); probing those whose typed path is being checked, and
	// probeErr what is wrong with it.
	custom   map[string]bool
	probing  map[string]bool
	probeErr map[string]string
	copied   string // the backend whose path was copied last
	// save are typed programs found usable, by backend ID, which the next
	// frame stores (the configuration is changed on the UI thread).
	save map[string]string
}

// loadBackends asks the AI plugin for its backends' installations;
// refresh probes them again.
func (s *Settings) loadBackends(refresh bool) {
	r := s.env.AIBackends
	if r == nil {
		return
	}
	s.asyncMu.Lock()
	st := &s.backends
	st.requested, st.loading = true, true
	s.asyncMu.Unlock()
	s.async(func() {
		reports := r.BackendReports(context.Background(), refresh)
		s.asyncMu.Lock()
		s.backends.reports, s.backends.loading = reports, false
		s.asyncMu.Unlock()
	})
}

// backendsSection lists each backend: its status, the program in use, the
// other installations found, and the "Program" choice; "Re-detect" looks
// again.
func (s *Settings) backendsSection(c *mui.Context) {
	if s.env.AIBackends == nil {
		return
	}
	t := c.Theme()
	s.asyncMu.Lock()
	if !s.backends.requested {
		s.asyncMu.Unlock()
		s.loadBackends(false)
		s.asyncMu.Lock()
	}
	loading := s.backends.loading
	reports := slices.Clone(s.backends.reports)
	save := s.backends.save
	s.backends.save = nil
	s.asyncMu.Unlock()
	for id, path := range save {
		s.setPref(aiPluginID, backend.ProgramPref(id), path)
	}
	if len(save) > 0 {
		s.loadBackends(false)
	}

	s.subheading(c, s.loc.T("settings.backends.heading"))
	mui.Row(c).Gap(12).AlignItems(mui.Center).Children(func() {
		mui.Text(c, s.loc.T("settings.backends.intro")).FontSize(12).TextColor(t.TextMuted).Grow(1).Shrink(1)
		if loading {
			mui.Spinner(c).Size(16, 16).Label(s.loc.T("settings.backends.detecting"))
		}
		if mui.Button(c.Key("backends:redetect"), s.loc.T("settings.backends.redetect")).Disabled(loading).Clicked() {
			s.loadBackends(true)
		}
	})
	if loading && len(reports) == 0 {
		mui.Text(c, s.loc.T("settings.backends.detecting")).FontSize(12).TextColor(t.TextMuted)
	}
	for _, r := range reports {
		mui.Column(c.Key("backend:"+r.ID)).Gap(4).Padding(10, 0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			s.backendRow(c, r)
		})
	}
}

// backendRow is one backend of the Backends section.
func (s *Settings) backendRow(c *mui.Context, r backend.Report) {
	t := c.Theme()
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.Text(c, r.Name).Bold().Level(3)
		color := t.TextMuted
		switch r.Status {
		case backend.StatusAvailable:
			color = t.Success
		case backend.StatusNotInstalled:
		default:
			color = t.Danger
		}
		mui.Text(c, s.backendStatus(r)).FontSize(12).TextColor(color)
	})
	if r.Message != "" {
		mui.Text(c, r.Message).FontSize(12).TextColor(t.TextMuted).Selectable()
	}
	if r.ConfiguredMissing {
		mui.Text(c, s.loc.T("settings.backends.missing", "path", r.Configured)).FontSize(12).TextColor(t.Danger).Selectable()
	}
	if in, ok := r.InUseInstall(); ok {
		mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
			mui.Text(c, s.loc.T("settings.backends.inUse")).FontSize(12).TextColor(t.TextMuted)
			mui.Text(c, in.Path).FontSize(12).Selectable().Padding(4, 6).Radius(6).Background(t.Text.Alpha(0.06)).Grow(1).Shrink(1)
			s.asyncMu.Lock()
			copied := s.backends.copied == r.ID
			s.asyncMu.Unlock()
			label := s.loc.T("settings.backends.copy")
			if copied {
				label = s.loc.T("settings.backends.copied")
			}
			if mui.Button(c.Key("backend-copy:"+r.ID), label).Clicked() && s.env.Services.Clipboard.WriteText(in.Path) == nil {
				s.asyncMu.Lock()
				s.backends.copied = r.ID
				s.asyncMu.Unlock()
			}
		})
		mui.Text(c, s.inUseDetail(r, in)).FontSize(12).TextColor(t.TextMuted)
	}
	var others []backend.Install
	for i, in := range r.Installs {
		if i != r.InUse {
			others = append(others, in)
		}
	}
	if len(others) > 0 {
		mui.Text(c, s.loc.T("settings.backends.others")).FontSize(12).Bold().TextColor(t.TextMuted)
		for _, in := range others {
			mui.Text(c, in.Path+" — "+s.loc.T("settings.backends.other.line", "detail", s.otherDetail(r, in), "source", s.sourceText(in.Source))).
				FontSize(12).TextColor(t.TextMuted).Selectable()
		}
	}
	s.programField(c, r)
}

// backendStatus words a backend's status.
func (s *Settings) backendStatus(r backend.Report) string {
	switch r.Status {
	case backend.StatusAvailable:
		return s.loc.T("settings.backends.status.available")
	case backend.StatusNotInstalled:
		return s.loc.T("settings.backends.status.notInstalled")
	case backend.StatusNotSignedIn:
		return s.loc.T("settings.backends.status.notSignedIn")
	case backend.StatusTooOld:
		return s.loc.T("settings.backends.status.tooOld", "min", r.MinVersion)
	}
	return s.loc.T("settings.backends.status.failed")
}

// sourceText words how an installation was found.
func (s *Settings) sourceText(src backend.Source) string {
	switch src {
	case backend.SourcePath:
		return s.loc.T("settings.backends.source.path")
	case backend.SourceInstallDir:
		return s.loc.T("settings.backends.source.installDir")
	case backend.SourceApp:
		return s.loc.T("settings.backends.source.app")
	case backend.SourceNpmVendor:
		return s.loc.T("settings.backends.source.npmVendor")
	}
	return s.loc.T("settings.backends.source.custom")
}

// inUseDetail is the line under the program in use: its version, how it
// was found and why it is used.
func (s *Settings) inUseDetail(r backend.Report, in backend.Install) string {
	version := in.Version
	if version == "" {
		version = "?"
	}
	if r.InUse != r.Auto || r.Configured != "" && !r.ConfiguredMissing {
		return s.loc.T("settings.backends.inUse.chosen", "version", version, "source", s.sourceText(in.Source))
	}
	reason := s.loc.T("settings.backends.reason.only")
	switch r.AutoReason {
	case backend.ReasonHighest:
		reason = s.loc.T("settings.backends.reason.highestVersion")
	case backend.ReasonFirst:
		reason = s.loc.T("settings.backends.reason.firstFound")
	}
	return s.loc.T("settings.backends.inUse.auto", "version", version, "source", s.sourceText(in.Source), "reason", reason)
}

// otherDetail says why another installation is not used.
func (s *Settings) otherDetail(r backend.Report, in backend.Install) string {
	switch {
	case in.Launcher:
		return s.loc.T("settings.backends.other.launcher")
	case in.TooOld:
		return s.loc.T("settings.backends.other.tooOld", "version", in.Version)
	case in.VersionError != "":
		return s.loc.T("settings.backends.other.failed", "err", in.VersionError)
	case in.Shadowed:
		return s.loc.T("settings.backends.other.shadowed", "version", in.Version)
	}
	return s.loc.T("settings.backends.other.version", "version", in.Version)
}

// programField is the "Program" choice of a backend: automatic, one of the
// usable installations, or a custom path, which is checked before it is
// saved.
func (s *Settings) programField(c *mui.Context, r backend.Report) {
	t := c.Theme()
	cfg := s.env.Config.Get()
	prefName := backend.ProgramPref(r.ID)
	current := strings.TrimSpace(decodePref[string](cfg.Plugin(aiPluginID).Preferences[prefName]))
	type choice struct{ title, path string }
	choices := []choice{{s.loc.T("settings.backends.program.auto"), ""}}
	for _, in := range r.Installs {
		if in.Usable() {
			choices = append(choices, choice{s.loc.T("settings.backends.program.install", "path", in.Path, "version", in.Version), in.Path})
		}
	}
	customTitle := s.loc.T("settings.backends.program.custom")
	titles := make([]string, 0, len(choices)+1)
	for _, ch := range choices {
		titles = append(titles, ch.title)
	}
	titles = append(titles, customTitle)

	s.asyncMu.Lock()
	custom := s.backends.custom[r.ID]
	probing := s.backends.probing[r.ID]
	probeErr := s.backends.probeErr[r.ID]
	s.asyncMu.Unlock()
	chosen := customTitle
	if !custom {
		for _, ch := range choices {
			if strings.EqualFold(ch.path, current) {
				chosen = ch.title
			}
		}
	}
	key := "backend-program:" + r.ID
	field := mui.Field(c.Key(key), s.loc.T("settings.backends.program"), func() {
		if mui.Select(c, &chosen, titles).Changed() {
			i := slices.Index(titles, chosen)
			switch {
			case i < 0:
			case i == len(choices): // a custom path: typed below
				s.setBackends(func(st *backendsState) { st.custom[r.ID] = true; delete(st.probeErr, r.ID) })
			default:
				s.setBackends(func(st *backendsState) { delete(st.custom, r.ID); delete(st.probeErr, r.ID) })
				s.setPref(aiPluginID, prefName, choices[i].path)
				s.loadBackends(false)
			}
		}
	})
	field.Description(s.loc.T("settings.backends.program.desc", "backend", r.Name))
	if chosen != customTitle {
		return
	}
	buf := s.buf("backend-path/"+r.ID, current)
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.TextInput(c.Key("backend-path:"+r.ID), buf).Label(s.loc.T("settings.backends.custom.label")).Placeholder(s.loc.T("settings.backends.custom.label")).Grow(1).Shrink(1)
		if probing {
			mui.Spinner(c).Size(16, 16).Label(s.loc.T("settings.backends.custom.checking"))
		}
		if mui.Button(c.Key("backend-use:"+r.ID), s.loc.T("settings.backends.custom.use")).Disabled(probing).Clicked() {
			s.useCustomProgram(r, strings.TrimSpace(*buf))
		}
	})
	if probeErr != "" {
		mui.Text(c, probeErr).FontSize(12).TextColor(t.Danger).Selectable()
	}
}

// setBackends changes the section's state under asyncMu.
func (s *Settings) setBackends(fn func(*backendsState)) {
	s.asyncMu.Lock()
	defer s.asyncMu.Unlock()
	st := &s.backends
	if st.custom == nil {
		st.custom, st.probing, st.probeErr = map[string]bool{}, map[string]bool{}, map[string]string{}
	}
	fn(st)
}

// useCustomProgram checks a typed program in the background and saves it
// when it is usable; otherwise it says what is wrong.
func (s *Settings) useCustomProgram(r backend.Report, path string) {
	rep := s.env.AIBackends
	s.setBackends(func(st *backendsState) { st.probing[r.ID] = true; delete(st.probeErr, r.ID) })
	s.async(func() {
		in := rep.ProbeProgram(context.Background(), r.ID, path)
		msg := ""
		switch {
		case in.Usable():
		case in.VersionError == backend.ProbeNotFound:
			msg = s.loc.T("settings.backends.custom.notFound")
		case in.VersionError == backend.ProbeNotProgram:
			msg = s.loc.T("settings.backends.custom.notProgram")
		case in.TooOld:
			msg = s.loc.T("settings.backends.custom.tooOld", "version", in.Version, "backend", r.Name, "min", r.MinVersion)
		default:
			msg = s.loc.T("settings.backends.custom.failed", "err", in.VersionError)
		}
		s.setBackends(func(st *backendsState) {
			delete(st.probing, r.ID)
			if msg != "" {
				st.probeErr[r.ID] = msg
				return
			}
			delete(st.custom, r.ID)
			if st.save == nil {
				st.save = map[string]string{}
			}
			st.save[r.ID] = in.Path
		})
	})
}
