package shell

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/darwin/ax"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	wincontext "github.com/eziq1/golauncher/internal/winapi/context"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Permissions on macOS (docs/macos-port.md §4.4): reading the selected text
// needs Accessibility, reading Finder needs Automation. When a show runs
// into a missing one, the launcher says so in a toast, once per run, and
// for Accessibility macOS is asked to show its own prompt. The settings'
// Permissions page (permissions_darwin.go, ui.PagePermissions) shows them
// all; the first run on macOS points to it once.

// hintKey names a permission hint.
type hintKey string

const (
	hintAccessibility hintKey = "accessibility"
	hintAutomation    hintKey = "automation"
)

// hintFor returns the hint for err, or "" when err is not about a missing
// permission.
func hintFor(err error) hintKey {
	switch {
	case errors.Is(err, ax.ErrNotTrusted), errors.Is(err, clip.ErrNotTrusted):
		return hintAccessibility
	case errors.Is(err, wincontext.ErrNotAuthorized):
		return hintAutomation
	}
	return ""
}

// hints remembers the hints shown in this run.
type hints struct {
	mu    sync.Mutex
	shown map[hintKey]bool
	// onboarding is the marker file of the first-run hint while that
	// hint is still to show (main thread only), else "".
	onboarding string
}

// once reports whether hint k is to be shown now: the first time only.
func (h *hints) once(k hintKey) bool {
	if k == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.shown[k] {
		return false
	}
	if h.shown == nil {
		h.shown = map[hintKey]bool{}
	}
	h.shown[k] = true
	return true
}

// permissionHint tells the user about the permission err lacks, once per
// run, in a toast over the launcher. Main thread only.
func (s *Shell) permissionHint(err error) bool {
	k := hintFor(err)
	if !s.hints.once(k) {
		return k != ""
	}
	s.log.Warn("permission missing", "err", err)
	if k == hintAccessibility {
		ax.RequestTrust() // macOS's prompt that opens the settings
	}
	t := plugin.Toast{Style: plugin.ToastFailure}
	switch k {
	case hintAccessibility:
		t.Title, t.Message = s.loc.T("toast.permission.accessibility"), s.loc.T("toast.permission.accessibility.message")
	case hintAutomation:
		t.Title, t.Message = s.loc.T("toast.permission.automation"), s.loc.T("toast.permission.automation.message")
	}
	s.env.Controller.ShowToast(t)
	return true
}

// watchPermissions routes the permission problems of context detection to
// permissionHint.
func (s *Shell) watchPermissions() {
	wincontext.OnProblem(func(err error) { s.post(func() { s.permissionHint(err) }) })
}

// pasteError maps the error of pasting into the previous app: without the
// Accessibility permission (macOS) the content is on the clipboard but no
// ⌘V can be sent, which core handles as a paste left to the user.
func pasteError(err error) error {
	if errors.Is(err, clip.ErrNotTrusted) || errors.Is(err, ax.ErrNotTrusted) {
		return fmt.Errorf("%w: %w", core.ErrNotPasted, err)
	}
	return err
}

// onboardingMarker is the file, next to the plugin data, whose presence
// means that the first-run permissions hint was shown.
const onboardingMarker = "permissions-hint-shown"

// prepareOnboarding arranges the first-run permissions hint for the next
// show of the launcher, on macOS, once ever, and only while Accessibility
// is missing. Main thread only.
func (s *Shell) prepareOnboarding() {
	if !onboardingSupported() || ax.Trusted() {
		return
	}
	marker := filepath.Join(filepath.Dir(s.env.Services.DataRoot), onboardingMarker)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		return
	}
	s.hints.onboarding = marker
}

// onboardingHint shows the first-run permissions hint if it is pending,
// and records that it was shown. Main thread only, after Launcher.Shown.
func (s *Shell) onboardingHint() {
	marker := s.hints.onboarding
	if marker == "" {
		return
	}
	s.hints.onboarding = ""
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		s.log.Warn("permissions hint marker", "err", err)
	}
	s.env.Controller.ShowToast(plugin.Toast{Style: plugin.ToastInfo,
		Title: s.loc.T("toast.permission.onboarding"), Message: s.loc.T("toast.permission.onboarding.message")})
}
