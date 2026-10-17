package shell

import (
	"errors"
	"fmt"
	"testing"

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
