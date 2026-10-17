package shell

import (
	"errors"
	"sync"

	"github.com/eziq1/golauncher/internal/darwin/ax"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	wincontext "github.com/eziq1/golauncher/internal/winapi/context"
	"github.com/eziq1/golauncher/pkg/plugin"
)

// Permissions on macOS (docs/macos-port.md §4.4): reading the selected text
// needs Accessibility, reading Finder needs Automation. When a show runs
// into a missing one, the launcher says so in a toast, once per run, and
// for Accessibility macOS is asked to show its own prompt.

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
