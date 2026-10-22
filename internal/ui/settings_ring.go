package ui

import (
	"context"
	"slices"
	"strconv"
	"strings"

	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/config"
	"github.com/eziq1/golauncher/internal/darwin/tcc"
	"github.com/eziq1/golauncher/internal/ring"
)

// PageRing is the Action Ring's settings page (docs/action-ring.md §4).
const PageRing = "ring"

// ringSettingsState is the Action Ring page's state: the dialog adding a
// fixed action, and the names of the fixed actions found for the page.
// UI thread only, except titles and results (Settings.asyncMu).
type ringSettingsState struct {
	adding    bool
	query     string
	want      string // the query the results are wanted for (asyncMu)
	results   []ringChoice
	exception string // the app being added to the exceptions
	confirm   bool   // "Restore Defaults" asks first

	titles map[string]ringChoice // the fixed actions' names, by key
}

// ringChoice is an action that can be a fixed one.
type ringChoice struct {
	Key, Title, Subtitle, Icon string
}

// openRingPage reads the names of the fixed actions in the background
// (finding a plugin's item asks the plugin) and, on macOS, the permission.
func (s *Settings) openRingPage() {
	if _, ok := s.permissionsHost(); ok {
		s.checkPermissions()
	}
	s.refreshRingTitles()
}

// refreshRingTitles finds the fixed actions again for their names.
func (s *Settings) refreshRingTitles() {
	fixed := s.env.Config.Get().Ring.Fixed
	s.async(func() {
		got := map[string]ringChoice{}
		for _, a := range fixed {
			if ring.IsOwn(a.Key) {
				continue
			}
			if e, ok := s.env.Engine.RingEntry(context.Background(), a.Key, a.Title); ok {
				got[a.Key] = ringChoice{Key: a.Key, Title: e.Title, Subtitle: e.Subtitle, Icon: e.Icon.Name}
			}
		}
		s.asyncMu.Lock()
		s.ring.titles = got
		s.asyncMu.Unlock()
	})
}

// ringChoiceOf describes a fixed action as the page shows it.
func (s *Settings) ringChoiceOf(a config.RingAction) ringChoice {
	if title, icon, ok := RingOwn(s.loc)(a.Key); ok {
		return ringChoice{Key: a.Key, Title: title, Subtitle: s.loc.T("settings.ring.ownAction"), Icon: icon}
	}
	s.asyncMu.Lock()
	c, ok := s.ring.titles[a.Key]
	s.asyncMu.Unlock()
	if ok {
		return c
	}
	return ringChoice{Key: a.Key, Title: cmpOr(a.Title, a.Key), Icon: a.Icon}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ringPage is the Action Ring's page: on and off, how long to hold, the
// fixed actions, the context actions, the exceptions, and what the ring
// does with the mouse.
func (s *Settings) ringPage(c *mui.Context, cfg config.Config) {
	t := c.Theme()
	rc := cfg.Ring
	setRing := func(fn func(*config.RingConfig)) { s.update(func(c *config.Config) { fn(&c.Ring) }) }
	s.heading(c, s.loc.T("settings.nav.ring"))
	mui.Text(c, s.loc.T("settings.ring.intro")).TextColor(t.TextMuted)
	s.ringPermission(c)

	s.toggle(c, s.loc.T("settings.ring.enabled"), s.loc.T("settings.ring.enabled.desc"), rc.Enabled, func(v bool) {
		setRing(func(r *config.RingConfig) { r.Enabled = v })
	})
	key := "ring/hold"
	field := mui.Field(c.Key(key), s.loc.T("settings.ring.hold"), func() {
		b := s.buf(key, strconv.Itoa(rc.EffectiveHoldMS()))
		if mui.TextInput(c, b).Width(160).Label(s.loc.T("settings.ring.hold")).Changed() {
			n, err := strconv.Atoi(strings.TrimSpace(*b))
			if err != nil || n < config.MinRingHoldMS || n > config.MaxRingHoldMS {
				s.errs[key] = s.loc.T("settings.ring.hold.invalid", "min", config.MinRingHoldMS, "max", config.MaxRingHoldMS)
				return
			}
			delete(s.errs, key)
			setRing(func(r *config.RingConfig) { r.HoldMS = n })
		}
	})
	field.Description(s.loc.T("settings.ring.hold.desc"))
	if msg := s.errs[key]; msg != "" {
		field.Error(msg)
	}

	s.ringFixed(c, rc, setRing)

	s.subheading(c, s.loc.T("settings.ring.context"))
	s.toggle(c, s.loc.T("settings.ring.contextOn"), s.loc.T("settings.ring.contextOn.desc"), rc.Context, func(v bool) {
		setRing(func(r *config.RingConfig) { r.Context = v })
	})

	s.ringExceptions(c, rc, setRing)

	s.subheading(c, s.loc.T("settings.ring.privacy"))
	mui.Text(c, s.loc.T("settings.ring.privacy.text")).FontSize(12).TextColor(t.TextMuted)
	s.noticeView(c)
}

// ringPermission tells, on macOS, that the ring needs the Accessibility
// permission while it is missing, with a button to its System Settings
// page. Nothing is asked at startup.
func (s *Settings) ringPermission(c *mui.Context) {
	h, ok := s.permissionsHost()
	if !ok {
		return
	}
	s.asyncMu.Lock()
	st, checked := s.perms.status[tcc.Accessibility], s.perms.checked
	s.asyncMu.Unlock()
	if !checked || st == tcc.Granted {
		return
	}
	t := c.Theme()
	mui.Column(c.Key("ring:permission")).Gap(8).Padding(12).Radius(8).Border(1, t.Warning).Children(func() {
		mui.Text(c, s.loc.T("settings.ring.permission")).TextColor(t.Warning)
		if button(c.Key("ring:openPermission"), s.loc.T("settings.ring.permission.open")) {
			if err := h.OpenPermissionSettings(tcc.Accessibility); err != nil {
				s.setNotice(err.Error(), true)
			}
		}
	})
}

// ringFixed lists the fixed actions, in the order of their number keys,
// with ↑, ↓ and Remove; "Add Action" and "Restore Defaults" above.
func (s *Settings) ringFixed(c *mui.Context, rc config.RingConfig, setRing func(func(*config.RingConfig))) {
	t := c.Theme()
	st := &s.ring
	list := rc.Fixed
	save := func(fn func(out []config.RingAction) []config.RingAction) {
		setRing(func(r *config.RingConfig) { r.Fixed = fn(slices.Clone(list)) })
	}
	s.subheading(c, s.loc.T("settings.ring.fixed"))
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.Text(c, s.loc.T("settings.ring.fixed.intro")).FontSize(12).TextColor(t.TextMuted).Grow(1).Shrink(1)
		if mui.Button(c.Key("ring:add"), s.loc.T("settings.ring.add")).Disabled(len(list) >= config.MaxRingFixed).Clicked() {
			st.adding, st.query = true, ""
			s.searchRingChoices("")
		}
		if mui.Button(c.Key("ring:restore"), s.loc.T("settings.ring.restore")).Clicked() {
			st.confirm = true
		}
	})
	if len(list) == 0 {
		mui.Text(c, s.loc.T("settings.ring.fixed.none")).FontSize(12).TextColor(t.TextMuted)
	}
	for i, a := range list {
		ch := s.ringChoiceOf(a)
		mui.Row(c.Key("ring:fixed:"+a.Key)).Gap(8).Padding(8, 0).AlignItems(mui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			mui.Text(c, strconv.Itoa(i+1)).FontSize(11).TextColor(t.TextMuted).Padding(0, 5).Radius(4).Background(t.Text.Alpha(0.08))
			mui.Icon(c, builtinIcon(ch.Icon)).FontSize(16)
			mui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
				mui.Text(c, ch.Title).Shrink(1)
				if ch.Subtitle != "" {
					mui.Text(c, ch.Subtitle).FontSize(12).TextColor(t.TextMuted)
				}
			})
			if mui.Button(c, "↑").Label(s.loc.T("settings.ring.upOf", "name", ch.Title)).Disabled(i == 0).Clicked() {
				save(func(out []config.RingAction) []config.RingAction { out[i-1], out[i] = out[i], out[i-1]; return out })
			}
			if mui.Button(c, "↓").Label(s.loc.T("settings.ring.downOf", "name", ch.Title)).Disabled(i == len(list)-1).Clicked() {
				save(func(out []config.RingAction) []config.RingAction { out[i+1], out[i] = out[i], out[i+1]; return out })
			}
			if mui.Button(c, s.loc.T("settings.ring.remove")).Label(s.loc.T("settings.ring.removeOf", "name", ch.Title)).Clicked() {
				save(func(out []config.RingAction) []config.RingAction { return slices.Delete(out, i, i+1) })
			}
		})
	}
	if st.adding {
		mui.Modal(c.Key("ring:dialog"), &st.adding, func() { s.ringAddDialog(c, list, save) })
	}
	if st.confirm {
		mui.Modal(c.Key("ring:confirm"), &st.confirm, func() {
			mui.Column(c).Gap(12).Padding(20).Width(420).Children(func() {
				mui.Text(c, s.loc.T("settings.ring.restore.title")).Bold().Level(2)
				mui.Text(c, s.loc.T("settings.ring.restore.message"))
				mui.Row(c).Gap(8).Children(func() {
					mui.Spacer(c)
					if button(c, s.loc.T("settings.ring.cancel")) {
						st.confirm = false
					}
					if mui.PrimaryButton(c, s.loc.T("settings.ring.restore.confirm")).Clicked() {
						st.confirm = false
						setRing(func(r *config.RingConfig) { r.Fixed = config.DefaultRingFixed() })
						s.refreshRingTitles()
					}
				})
			})
		})
	}
}

// ringAddDialog searches the commands and actions of the launcher (as the
// root search finds them) and the ring's own actions, and adds the one
// clicked.
func (s *Settings) ringAddDialog(c *mui.Context, list []config.RingAction, save func(func([]config.RingAction) []config.RingAction)) {
	t := c.Theme()
	st := &s.ring
	mui.Column(c).Gap(12).Padding(20).Width(560).Children(func() {
		mui.Text(c, s.loc.T("settings.ring.add.title")).FontSize(16).Bold().Level(2)
		if mui.TextInput(c.Key("ring:query"), &st.query).Placeholder(s.loc.T("settings.ring.add.placeholder")).
			Label(s.loc.T("settings.ring.add.placeholder")).AutoFocus().Changed() {
			s.searchRingChoices(st.query)
		}
		s.asyncMu.Lock()
		results := st.results
		s.asyncMu.Unlock()
		mui.Scroll(c.Key("ring:results")).MaxHeight(320).Children(func() {
			mui.Column(c).Gap(2).Children(func() {
				for _, r := range results {
					taken := slices.ContainsFunc(list, func(a config.RingAction) bool { return a.Key == r.Key })
					row := mui.Row(c.Key("ring:result:"+r.Key)).Gap(8).Padding(6, 8).Radius(6).AlignItems(mui.Center).Label(r.Title)
					if !taken {
						row.Cursor(mui.CursorPointer)
						if row.Hovered() {
							row.Background(t.SurfaceHover)
						}
						if row.Clicked() {
							a := config.RingAction{Key: r.Key, Title: r.Title, Icon: r.Icon}
							if ring.IsOwn(r.Key) {
								a.Title, a.Icon = "", ""
							}
							st.adding = false
							save(func(out []config.RingAction) []config.RingAction { return append(out, a) })
							s.refreshRingTitles()
						}
					}
					row.Children(func() {
						mui.Icon(c, builtinIcon(r.Icon)).FontSize(16)
						mui.Column(c).Grow(1).Shrink(1).Children(func() {
							mui.Text(c, r.Title).Shrink(1)
							if r.Subtitle != "" {
								mui.Text(c, r.Subtitle).FontSize(12).TextColor(t.TextMuted)
							}
						})
						if taken {
							mui.Badge(c, s.loc.T("settings.ring.add.taken"))
						}
					})
				}
			})
		})
		mui.Row(c).Gap(8).Children(func() {
			mui.Spacer(c)
			if button(c, s.loc.T("settings.ring.cancel")) {
				st.adding = false
			}
		})
	})
}

// searchRingChoices finds the actions for query in the background: the
// ring's own (all of them while nothing is typed), then the root search's
// commands and items.
func (s *Settings) searchRingChoices(query string) {
	own := RingOwn(s.loc)
	s.asyncMu.Lock()
	s.ring.want = query
	s.asyncMu.Unlock()
	s.async(func() {
		var out []ringChoice
		q := strings.ToLower(strings.TrimSpace(query))
		for _, key := range []string{config.RingLauncher, config.RingAskAI, config.RingScreenshot} {
			title, icon, _ := own(key)
			if q == "" || strings.Contains(strings.ToLower(title), q) {
				out = append(out, ringChoice{Key: key, Title: title, Subtitle: s.loc.T("settings.ring.ownAction"), Icon: icon})
			}
		}
		if q != "" {
			entries, err := s.env.Engine.Search(context.Background(), query)
			if err == nil {
				for _, e := range entries[:min(len(entries), 30)] {
					out = append(out, ringChoice{Key: e.Key, Title: e.Title, Subtitle: e.Subtitle, Icon: e.Icon.Name})
				}
			}
		}
		s.asyncMu.Lock()
		if s.ring.want == query {
			s.ring.results = out
		}
		s.asyncMu.Unlock()
	})
}

// ringExceptions lists the apps where the middle button is left alone,
// each with Remove, and a field adding one.
func (s *Settings) ringExceptions(c *mui.Context, rc config.RingConfig, setRing func(func(*config.RingConfig))) {
	t := c.Theme()
	st := &s.ring
	s.subheading(c, s.loc.T("settings.ring.exceptions"))
	mui.Text(c, s.loc.T("settings.ring.exceptions.desc")).FontSize(12).TextColor(t.TextMuted)
	add := func() {
		name := strings.TrimSpace(st.exception)
		if name == "" || ring.Excluded(rc.Exceptions, name) {
			st.exception = ""
			return
		}
		st.exception = ""
		setRing(func(r *config.RingConfig) { r.Exceptions = append(slices.Clone(r.Exceptions), name) })
	}
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		mui.TextInput(c.Key("ring:exception"), &st.exception).Placeholder(s.loc.T("settings.ring.exceptions.placeholder")).
			Label(s.loc.T("settings.ring.exceptions.placeholder")).Grow(1).OnSubmit(add)
		if button(c.Key("ring:exception:add"), s.loc.T("settings.ring.exceptions.add")) {
			add()
		}
	})
	if len(rc.Exceptions) == 0 {
		mui.Text(c, s.loc.T("settings.ring.exceptions.none")).FontSize(12).TextColor(t.TextMuted)
	}
	for i, name := range rc.Exceptions {
		mui.Row(c.Key("ring:exc:"+strconv.Itoa(i)+":"+name)).Gap(8).Padding(4, 0).AlignItems(mui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			mui.Text(c, name).Grow(1).Shrink(1)
			if mui.Button(c, s.loc.T("settings.ring.remove")).Label(s.loc.T("settings.ring.removeOf", "name", name)).Clicked() {
				setRing(func(r *config.RingConfig) { r.Exceptions = slices.Delete(slices.Clone(r.Exceptions), i, i+1) })
			}
		})
	}
}
