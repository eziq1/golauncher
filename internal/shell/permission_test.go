package shell

import (
	"errors"
	"fmt"
	"testing"

	"github.com/eziq1/golauncher/internal/core"
	"github.com/eziq1/golauncher/internal/darwin/ax"
	"github.com/eziq1/golauncher/internal/winapi/clip"
	wincontext "github.com/eziq1/golauncher/internal/winapi/context"
)

func TestPermissionHints(t *testing.T) {
	for err, want := range map[error]hintKey{
		ax.ErrNotTrusted:                        hintAccessibility,
		fmt.Errorf("x: %w", clip.ErrNotTrusted): hintAccessibility,
		wincontext.ErrNotAuthorized:             hintAutomation,
		errors.New("other"):                     "",
	} {
		if got := hintFor(err); got != want {
			t.Errorf("hintFor(%v) = %q", err, got)
		}
	}
	var h hints
	if !h.once(hintAutomation) || h.once(hintAutomation) || !h.once(hintAccessibility) || h.once("") {
		t.Error("once")
	}
}

// TestPasteError: a paste refused for lack of Accessibility is a paste left
// to the user (core.ErrNotPasted, keeping the cause); other errors stay.
func TestPasteError(t *testing.T) {
	for _, cause := range []error{clip.ErrNotTrusted, ax.ErrNotTrusted, fmt.Errorf("x: %w", clip.ErrNotTrusted)} {
		err := pasteError(cause)
		if !errors.Is(err, core.ErrNotPasted) || !errors.Is(err, cause) || hintFor(err) != hintAccessibility {
			t.Errorf("pasteError(%v) = %v", cause, err)
		}
	}
	for _, err := range []error{nil, clip.ErrNotForeground, errNoTarget} {
		if got := pasteError(err); got != err {
			t.Errorf("pasteError(%v) = %v", err, got)
		}
	}
}
