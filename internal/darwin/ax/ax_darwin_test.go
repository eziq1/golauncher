package ax

import (
	"errors"
	"fmt"
	"testing"
)

func TestMissing(t *testing.T) {
	if !Missing(errNoValue) || !Missing(fmt.Errorf("x: %w", errAttributeUnsupported)) || Missing(ErrNotTrusted) || Missing(Error(-25200)) {
		t.Error("Missing")
	}
	if check(0) != nil || !errors.Is(check(-25211), ErrNotTrusted) || check(-25212) != errNoValue {
		t.Error("check")
	}
}

// TestSystemWide reads which app has the focus (read-only); without the
// Accessibility permission it must say so.
func TestSystemWide(t *testing.T) {
	sys, err := SystemWide()
	if !Trusted() {
		if !errors.Is(err, ErrNotTrusted) {
			t.Fatalf("untrusted SystemWide = %v", err)
		}
		t.Skip("no Accessibility permission")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer sys.Release()
	app, err := sys.Element("AXFocusedApplication")
	if err != nil {
		t.Skip("no focused application:", err)
	}
	defer app.Release()
	title, _ := app.String("AXTitle")
	pid, err := app.PID()
	t.Logf("focused app %q pid %d %v", title, pid, err)
}
