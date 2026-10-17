//go:build darwin

package shell

import (
	"log/slog"
	"os/exec"

	"github.com/eziq1/golauncher/internal/darwin/tcc"
	"github.com/eziq1/golauncher/internal/ui"
)

// The settings' Permissions page on macOS (ui.PermissionsHost).

var _ ui.PermissionsHost = settingsHost{}

func (settingsHost) PermissionStatus(id tcc.ID) tcc.Status { return tcc.Check(id) }

func (settingsHost) RequestPermission(id tcc.ID) {
	switch id {
	case tcc.Accessibility:
		tcc.RequestAccessibility()
	case tcc.AutomationFinder:
		tcc.RequestAutomation()
	}
}

func (settingsHost) OpenPermissionSettings(id tcc.ID) error {
	if id == tcc.Accessibility {
		// The list in System Settings shows golauncher (or its terminal)
		// only once it asked; asking also shows macOS's own prompt, once.
		tcc.RequestAccessibility()
	}
	return tcc.OpenSettings(id)
}

func (settingsHost) PermissionHolder() string { return tcc.Terminal() }

// onboardingSupported reports whether the first-run permissions hint
// applies: on macOS.
func onboardingSupported() bool { return true }

// scriptNotify shows a notification through AppleScript, for when MyGo
// cannot (golauncher not running from a .app bundle, which macOS's
// notification center needs): it then shows under Script Editor's name and
// clicking it does nothing. It reports whether the notification was sent.
func scriptNotify(title, body string, log *slog.Logger) bool {
	cmd := exec.Command("/usr/bin/osascript",
		"-e", "on run argv",
		"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
		"-e", "end run", title, body)
	if err := cmd.Start(); err != nil {
		log.Warn("notification", "err", err)
		return false
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Warn("notification", "err", err)
		}
	}()
	return true
}
