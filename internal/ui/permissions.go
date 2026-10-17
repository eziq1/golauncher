package ui

import (
	mui "github.com/egoist/mygo/ui"

	"github.com/eziq1/golauncher/internal/darwin/tcc"
)

// PagePermissions is the macOS privacy permissions page (docs/macos-port.md
// §4.4, §10). It shows only with a SettingsHost that is a PermissionsHost.
const PagePermissions = "permissions"

// PermissionsHost is implemented by a SettingsHost on macOS, where
// golauncher needs privacy permissions: the settings then have a
// Permissions page. PermissionStatus and RequestPermission are called off
// the UI thread.
type PermissionsHost interface {
	// PermissionStatus returns the status of permission id without
	// showing any prompt.
	PermissionStatus(id tcc.ID) tcc.Status
	// RequestPermission asks macOS for permission id, which may show its
	// prompt, and returns once it was answered.
	RequestPermission(id tcc.ID)
	// OpenPermissionSettings opens the System Settings page of id.
	OpenPermissionSettings(id tcc.ID) error
	// PermissionHolder names the terminal app that holds golauncher's
	// permissions because golauncher was started from it, or "".
	PermissionHolder() string
}

// permissionsState is the Permissions page; guarded by Settings.asyncMu.
type permissionsState struct {
	status     map[tcc.ID]tcc.Status
	checked    bool // the statuses were read at least once
	checking   bool
	again      bool // another check was asked for while one ran
	requesting map[tcc.ID]bool
}

// permissionsHost returns the host as a PermissionsHost, if it is one.
func (s *Settings) permissionsHost() (PermissionsHost, bool) {
	h, ok := s.host.(PermissionsHost)
	return h, ok
}

// RefreshPermissions reads the permission statuses again when the
// Permissions page shows, as when the settings window gets the focus
// back from System Settings. UI thread only.
func (s *Settings) RefreshPermissions() {
	if s.page == PagePermissions {
		s.checkPermissions()
	}
}

// checkPermissions reads the statuses of all permissions in the background.
func (s *Settings) checkPermissions() {
	h, ok := s.permissionsHost()
	if !ok {
		return
	}
	s.asyncMu.Lock()
	defer s.asyncMu.Unlock()
	if s.perms.checking {
		s.perms.again = true
		return
	}
	s.perms.checking = true
	s.async(func() {
		for {
			got := make(map[tcc.ID]tcc.Status, len(tcc.All))
			for _, id := range tcc.All {
				got[id] = h.PermissionStatus(id)
			}
			s.asyncMu.Lock()
			s.perms.status, s.perms.checked = got, true
			again := s.perms.again
			s.perms.again = false
			if !again {
				s.perms.checking = false
			}
			s.asyncMu.Unlock()
			if !again {
				return
			}
		}
	})
}

// requestPermission asks macOS for permission id in the background, then
// reads the statuses again.
func (s *Settings) requestPermission(id tcc.ID) {
	h, ok := s.permissionsHost()
	if !ok {
		return
	}
	s.asyncMu.Lock()
	if s.perms.requesting[id] {
		s.asyncMu.Unlock()
		return
	}
	if s.perms.requesting == nil {
		s.perms.requesting = map[tcc.ID]bool{}
	}
	s.perms.requesting[id] = true
	s.asyncMu.Unlock()
	s.async(func() {
		h.RequestPermission(id)
		s.asyncMu.Lock()
		delete(s.perms.requesting, id)
		s.asyncMu.Unlock()
		s.checkPermissions()
	})
}

// permissionText returns the label and description of permission id.
func (s *Settings) permissionText(id tcc.ID) (label, desc string) {
	switch id {
	case tcc.Accessibility:
		return s.loc.T("settings.permissions.accessibility"), s.loc.T("settings.permissions.accessibility.desc")
	case tcc.AutomationFinder:
		return s.loc.T("settings.permissions.automation"), s.loc.T("settings.permissions.automation.desc")
	case tcc.FullDiskAccess:
		return s.loc.T("settings.permissions.fullDiskAccess"), s.loc.T("settings.permissions.fullDiskAccess.desc")
	}
	return string(id), ""
}

// permissionStatusText returns how status is shown, and its color.
func (s *Settings) permissionStatusText(c *mui.Context, id tcc.ID, st tcc.Status, checked bool) (string, mui.Color) {
	t := c.Theme()
	if !checked {
		return s.loc.T("settings.permissions.checking"), t.TextMuted
	}
	switch st {
	case tcc.Granted:
		return s.loc.T("settings.permissions.granted"), t.Success
	case tcc.Denied:
		if tcc.Optional(id) {
			return s.loc.T("settings.permissions.notGrantedOptional"), t.TextMuted
		}
		return s.loc.T("settings.permissions.notGranted"), t.Danger
	case tcc.NotDetermined:
		return s.loc.T("settings.permissions.notDetermined"), t.Warning
	}
	return s.loc.T("settings.permissions.unknown"), t.TextMuted
}

// permissionsPage lists the permissions with their status and a way to
// grant each.
func (s *Settings) permissionsPage(c *mui.Context) {
	h, ok := s.permissionsHost()
	if !ok {
		return
	}
	t := c.Theme()
	s.heading(c, s.loc.T("settings.nav.permissions"))
	mui.Text(c, s.loc.T("settings.permissions.intro")).TextColor(t.TextMuted)
	if term := h.PermissionHolder(); term != "" {
		mui.Text(c, s.loc.T("settings.permissions.terminalNamed", "terminal", term)).FontSize(12).TextColor(t.Warning)
	} else {
		mui.Text(c, s.loc.T("settings.permissions.terminal")).FontSize(12).TextColor(t.TextMuted)
	}
	s.asyncMu.Lock()
	status, checked := s.perms.status, s.perms.checked
	requesting := map[tcc.ID]bool{}
	for id, v := range s.perms.requesting {
		requesting[id] = v
	}
	s.asyncMu.Unlock()
	for _, id := range tcc.All {
		label, desc := s.permissionText(id)
		if tcc.Optional(id) {
			label = s.loc.T("settings.permissions.optional", "name", label)
		}
		st := status[id]
		text, color := s.permissionStatusText(c, id, st, checked)
		row := c.Key("permission:" + string(id))
		mui.Column(row).Gap(6).Padding(10, 0).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			mui.Row(c).Gap(16).AlignItems(mui.Center).Children(func() {
				mui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
					mui.Text(c, label)
					mui.Text(c, desc).FontSize(12).TextColor(t.TextMuted)
				})
				mui.Text(c, text).FontSize(12).TextColor(color)
			})
			mui.Row(c).Gap(8).Wrap().AlignItems(mui.Center).Children(func() {
				if checked && st != tcc.Granted && (id == tcc.Accessibility || st == tcc.NotDetermined) {
					if mui.Button(c.Key("request:"+string(id)), s.loc.T("settings.permissions.request")).Disabled(requesting[id]).Clicked() {
						s.requestPermission(id)
					}
				}
				if button(c.Key("open:"+string(id)), s.loc.T("settings.permissions.open")) {
					if err := h.OpenPermissionSettings(id); err != nil {
						s.setNotice(err.Error(), true)
					}
				}
			})
		})
	}
	mui.Row(c).Gap(8).AlignItems(mui.Center).Children(func() {
		s.asyncMu.Lock()
		checking := s.perms.checking
		s.asyncMu.Unlock()
		if mui.Button(c, s.loc.T("settings.permissions.recheck")).Disabled(checking).Clicked() {
			s.checkPermissions()
		}
		mui.Text(c, s.loc.T("settings.permissions.restart")).FontSize(12).TextColor(t.TextMuted).Shrink(1)
	})
	s.noticeView(c)
}
